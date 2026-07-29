//go:build !windows

package appmode

import (
	"context"
	"testing"
	"time"
)

func TestV4PublicCommandContextAndPrepareGUI(t *testing.T) {
	PrepareGUI()
	ctx, stop := CommandContext(context.Background())
	stop()
	stop()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("CommandContext was not canceled by its stop function")
	}
}
