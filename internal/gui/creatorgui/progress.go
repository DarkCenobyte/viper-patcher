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

func creatorOverallProgress(event progress.Event, includeReverse bool) float64 {
	if event.Stage == progress.StageCompleted {
		return 1
	}
	if event.FileCount <= 0 || event.FileIndex <= 0 {
		return 0
	}
	unitsPerFile := 2
	if includeReverse {
		unitsPerFile = 3
	}
	completedUnits := (event.FileIndex - 1) * unitsPerFile
	switch event.Stage {
	case progress.StageSnapshotting:
	case progress.StageCompressingForward:
		completedUnits++
	case progress.StageCompressingReverse:
		completedUnits += 2
	case progress.StageFilePrepared:
		completedUnits += unitsPerFile
	}
	fraction := 0.0
	if event.Stage != progress.StageFilePrepared && event.TotalBytes > 0 {
		fraction = float64(event.ProcessedBytes) / float64(event.TotalBytes)
	}
	value := (float64(completedUnits) + fraction) / float64(event.FileCount*unitsPerFile)
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
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
