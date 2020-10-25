package roadrunner

import (
	"context"
	"github.com/spiral/roadrunner/v2/util"
	"time"
)

const MB = 1024 * 1024

type supervisedPool struct {
	cfg    SupervisorConfig
	events *util.EventHandler
	pool   Pool
	stopCh chan struct{}
}

func newPoolWatcher(pool *StaticPool, events *util.EventHandler, cfg SupervisorConfig) *supervisedPool {
	return &supervisedPool{
		cfg:    cfg,
		events: events,
		pool:   pool,
		stopCh: make(chan struct{}),
	}
}

func (sp *supervisedPool) Start() {
	go func() {
		watchTout := time.NewTicker(sp.cfg.WatchTick)
		for {
			select {
			case <-sp.stopCh:
				watchTout.Stop()
				return
			// stop here
			case <-watchTout.C:
				sp.control()
			}
		}
	}()
}

func (sp *supervisedPool) Stop() {
	sp.stopCh <- struct{}{}
}

func (sp *supervisedPool) control() {
	now := time.Now()
	ctx := context.TODO()

	// THIS IS A COPY OF WORKERS
	workers := sp.pool.Workers()

	for i := 0; i < len(workers); i++ {
		if workers[i].State().Value() == StateInvalid {
			continue
		}

		s, err := WorkerProcessState(workers[i])
		if err != nil {
			// worker not longer valid for supervision
			continue
		}

		if sp.cfg.TTL != 0 && now.Sub(workers[i].Created()).Seconds() >= float64(sp.cfg.TTL) {
			err = sp.pool.RemoveWorker(ctx, workers[i])
			if err != nil {
				sp.events.Push(PoolEvent{Event: EventSupervisorError, Payload: err})
				return
			} else {
				sp.events.Push(PoolEvent{Event: EventTTL, Payload: workers[i]})
			}

			continue
		}

		if sp.cfg.MaxWorkerMemory != 0 && s.MemoryUsage >= sp.cfg.MaxWorkerMemory*MB {
			// TODO events
			//sp.pool.Events() <- PoolEvent{Payload: fmt.Errorf("max allowed memory reached (%vMB)", sp.maxWorkerMemory)}
			err = sp.pool.RemoveWorker(ctx, workers[i])
			if err != nil {
				sp.events.Push(PoolEvent{Event: EventSupervisorError, Payload: err})
				return
			} else {
				sp.events.Push(PoolEvent{Event: EventTTL, Payload: workers[i]})
			}

			continue
		}

		// firs we check maxWorker idle
		if sp.cfg.IdleTTL != 0 {
			// then check for the worker state
			if workers[i].State().Value() != StateReady {
				continue
			}

			/*
				Calculate idle time
				If worker in the StateReady, we read it LastUsed timestamp as UnixNano uint64
				2. For example maxWorkerIdle is equal to 5sec, then, if (time.Now - LastUsed) > maxWorkerIdle
				we are guessing that worker overlap idle time and has to be killed
			*/

			// get last used unix nano
			lu := workers[i].State().LastUsed()

			// convert last used to unixNano and sub time.now
			res := int64(lu) - now.UnixNano()

			// maxWorkerIdle more than diff between now and last used
			if sp.cfg.IdleTTL-res <= 0 {
				err = sp.pool.RemoveWorker(ctx, workers[i])
				if err != nil {
					sp.events.Push(PoolEvent{Event: EventSupervisorError, Payload: err})
					return
				} else {
					sp.events.Push(PoolEvent{Event: EventIdleTTL, Payload: workers[i]})
				}
			}
		}

		// the very last step is to calculate pool memory usage (except excluded workers)
		//totalUsedMemory += s.MemoryUsage
	}

	//// if current usage more than max allowed pool memory usage
	//if totalUsedMemory > sp.maxPoolMemory {
	//	sp.pool.Destroy(ctx)
	//}
}
