// Package jobs runs the background tasks of the API: escalation, sensor
// monitoring, photo analysis, post-meal impact, weekly reports and the
// simulator. Each task is safe with several API instances: the work is
// claimed with row locks (SKIP LOCKED) or made idempotent by unique keys.
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Trigger wakes a job before its next tick.
type Trigger struct{ ch chan struct{} }

func NewTrigger() *Trigger { return &Trigger{ch: make(chan struct{}, 1)} }

// Wake asks the job to run now. It never blocks.
func (t *Trigger) Wake() {
	select {
	case t.ch <- struct{}{}:
	default:
	}
}

// Every runs fn at once and then every interval until ctx is cancelled. A
// panic is logged and the job keeps running, so a bug in one task never
// stops the API.
func Every(ctx context.Context, log *slog.Logger, name string, interval time.Duration, fn func(context.Context)) {
	EveryOrWake(ctx, log, name, interval, nil, fn)
}

// EveryOrWake is Every, but the trigger can also run fn before the next tick.
func EveryOrWake(ctx context.Context, log *slog.Logger, name string, interval time.Duration, trigger *Trigger, fn func(context.Context)) {
	run := func() {
		defer func() {
			if v := recover(); v != nil {
				log.Error("background job panicked", "job", name, "panic", fmt.Sprint(v))
			}
		}()
		fn(ctx)
	}
	var wake chan struct{}
	if trigger != nil {
		wake = trigger.ch
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	run()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-wake:
		}
		run()
	}
}
