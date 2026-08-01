// Package commandctx provides cancellation contexts for command-line entry points.
package commandctx

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// New returns a context canceled by the parent, Ctrl+C, or SIGTERM.
// Signal handling is restored before cancellation, so a later signal retains its
// platform-default behavior while the first one allows normal cleanup to run.
func New(parent context.Context) (context.Context, context.CancelFunc) {
	notifications := make(chan os.Signal, 1)
	signal.Notify(notifications, os.Interrupt, syscall.SIGTERM)
	return FromNotifications(parent, notifications, func() {
		signal.Stop(notifications)
	})
}

// FromNotifications builds a command context from an existing signal channel.
// It is primarily useful to keep platform entry points and tests deterministic.
func FromNotifications(parent context.Context, notifications <-chan os.Signal, stopNotifications func()) (context.Context, context.CancelFunc) {
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
