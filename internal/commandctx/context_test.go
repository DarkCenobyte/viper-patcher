package commandctx

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

type testSignal string

func (signal testSignal) Signal()        {}
func (signal testSignal) String() string { return string(signal) }

func TestFromNotificationsCancelsOnNotification(t *testing.T) {
	notifications := make(chan os.Signal, 1)
	stopped := make(chan struct{})
	ctx, stop := FromNotifications(context.Background(), notifications, func() {
		close(stopped)
	})
	defer stop()

	notifications <- testSignal("interrupt")
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("signal notification did not cancel the command context")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("context error = %v", ctx.Err())
	}
	select {
	case <-stopped:
	default:
		t.Fatal("signal notifications were not stopped before cancellation completed")
	}
}

func TestFromNotificationsStopIsIdempotent(t *testing.T) {
	stopCount := 0
	ctx, stop := FromNotifications(context.Background(), make(chan os.Signal), func() {
		stopCount++
	})
	stop()
	stop()
	if stopCount != 1 {
		t.Fatalf("stop notifications count = %d", stopCount)
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("context error = %v", ctx.Err())
	}
}

func TestFromNotificationsFollowsParentCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	ctx, stop := FromNotifications(parent, make(chan os.Signal), func() {
		close(stopped)
	})
	defer stop()

	cancelParent()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not cancel the command context")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("context error = %v", ctx.Err())
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not stop signal notifications")
	}
}
