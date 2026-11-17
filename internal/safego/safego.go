// Package safego runs background work so that a panic is logged with its stack
// instead of taking the whole process down.
package safego

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"
)

// RestartDelay is the pause before Supervise restarts work that panicked.
var RestartDelay = 5 * time.Second

// Do runs fn once and recovers from a panic. It reports whether fn panicked.
func Do(log *slog.Logger, component string, fn func()) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			log.Error("panic recovered", "component", component, "panic", r, "stack", string(debug.Stack()))
		}
	}()
	fn()
	return false
}

// Supervise runs fn until it returns normally. When fn panics, the panic is
// logged and fn is started again after RestartDelay, unless ctx is done.
func Supervise(ctx context.Context, log *slog.Logger, component string, fn func(context.Context)) {
	for {
		if !Do(log, component, func() { fn(ctx) }) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(RestartDelay):
		}
	}
}
