package creatorgui

import (
	"fmt"
	"strconv"

	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

func integerOptions(minimum, maximum int) []string {
	values := make([]string, 0, maximum-minimum+1)
	for value := minimum; value <= maximum; value++ {
		values = append(values, strconv.Itoa(value))
	}
	return values
}

func creatorProgressText(event progress.Event) string {
	switch event.Stage {
	case progress.StageSnapshotting:
		return fmt.Sprintf("[%d/%d] Snapshotting inputs: %s", event.FileIndex, event.FileCount, event.Path)
	case progress.StageCompressingForward:
		return fmt.Sprintf("[%d/%d] Creating forward differential: %s", event.FileIndex, event.FileCount, event.Path)
	case progress.StageCompressingReverse:
		return fmt.Sprintf("[%d/%d] Creating reverse differential: %s", event.FileIndex, event.FileCount, event.Path)
	case progress.StageFilePrepared:
		return fmt.Sprintf("[%d/%d] Differential prepared: %s", event.FileIndex, event.FileCount, event.Path)
	case progress.StageCompleted:
		return "Finalizing patch..."
	default:
		return string(event.Stage)
	}
}

func formatByteSize(value uint64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	divisor := uint64(unit)
	exponent := 0
	for quotient := value / unit; quotient >= unit && exponent < 5; quotient /= unit {
		divisor *= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(divisor), "KMGTPE"[exponent])
}
