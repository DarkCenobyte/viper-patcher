package appmode

import (
	"context"
	"os"

	"github.com/DarkCenobyte/viper-patcher/internal/commandctx"
)

// CommandContext returns a context canceled by the parent, Ctrl+C, or SIGTERM.
// Signal handling is restored before cancellation, so a later signal retains its
// platform-default behavior while the first one allows normal cleanup to run.
func CommandContext(parent context.Context) (context.Context, context.CancelFunc) {
	return commandctx.New(parent)
}

func commandContext(parent context.Context, notifications <-chan os.Signal, stopNotifications func()) (context.Context, context.CancelFunc) {
	return commandctx.FromNotifications(parent, notifications, stopNotifications)
}
