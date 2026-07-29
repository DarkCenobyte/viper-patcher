package appmode

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// CommandContext returns a context canceled by the parent, Ctrl+C, or SIGTERM.
// Signal handling is restored before cancellation, so a later signal retains its
// platform-default behavior while the first one allows normal cleanup to run.
func CommandContext(parent context.Context) (context.Context, context.CancelFunc) {
	notifications := make(chan os.Signal, 1)
	signal.Notify(notifications, os.Interrupt, syscall.SIGTERM)
	return commandContext(parent, notifications, func() {
		signal.Stop(notifications)
	})
}

func commandContext(parent context.Context, notifications <-chan os.Signal, stopNotifications func()) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			stopNotifications()
			cancel()
		})
	}
	go func() {
		select {
		case <-notifications:
			stop()
		case <-ctx.Done():
			stop()
		}
	}()
	return ctx, stop
}
