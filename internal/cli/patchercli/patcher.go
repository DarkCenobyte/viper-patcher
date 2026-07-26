package patchercli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"runtime"

	"github.com/DarkCenobyte/viper-patcher/internal/buildinfo"
	"github.com/DarkCenobyte/viper-patcher/internal/patch"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

// Run executes patcher CLI mode and returns a process exit code.
func Run(ctx context.Context, arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("patcher", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var patchFile string
	var reverse bool
	var parallelism int
	var help bool
	var version bool
	flags.StringVar(&patchFile, "patch-file", "", "input .vipr patch")
	flags.BoolVar(&reverse, "reverse", false, "apply reverse differentials")
	flags.IntVar(&parallelism, "parallel", runtime.NumCPU(), "number of files prepared in parallel")
	flags.Bool("headless", false, "force command-line mode")
	flags.BoolVar(&help, "help", false, "show help")
	flags.BoolVar(&version, "version", false, "show version")

	if len(arguments) == 0 {
		printUsage(stderr)
		return 2
	}
	if err := flags.Parse(arguments); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n\n", err)
		printUsage(stderr)
		return 2
	}
	if help {
		printUsage(stdout)
		return 0
	}
	if version {
		fmt.Fprintf(stdout, "viper-patcher patcher %s (%s, %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate)
		return 0
	}
	if patchFile == "" || flags.NArg() != 1 || flags.Arg(0) == "" {
		fmt.Fprintln(stderr, "Error: --patch-file and exactly one target directory argument are required.")
		fmt.Fprintln(stderr)
		printUsage(stderr)
		return 2
	}
	if parallelism < 1 || parallelism > runtime.NumCPU() {
		fmt.Fprintf(stderr, "Error: --parallel must be between 1 and %d.\n", runtime.NumCPU())
		return 2
	}

	direction := patch.Forward
	if reverse {
		direction = patch.Reverse
	}
	reporter := newTerminalProgress(stderr)
	applyError := patch.ApplyWithOptions(ctx, patch.ApplyOptions{
		PatchPath:   patchFile,
		Root:        flags.Arg(0),
		Direction:   direction,
		Parallelism: parallelism,
	}, reporter.Report)
	reporter.Finish()
	if applyError != nil && !patch.IsCommittedWarning(applyError) {
		fmt.Fprintf(stderr, "Patch validation failed: %v\n", applyError)
		return 1
	}
	fmt.Fprintf(stdout, "%s patch applied successfully.\n", direction)
	if applyError != nil {
		fmt.Fprintf(stderr, "Warning: %v\n", applyError)
	}
	return 0
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Example:")
	fmt.Fprintln(writer, "  patcher --headless --patch-file update.vipr /path/to/application")
	fmt.Fprintln(writer, "  patcher --headless --patch-file update.vipr --reverse /path/to/application")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Supported parameters:")
	fmt.Fprintln(writer, "  --patch-file <file.vipr>       Required.")
	fmt.Fprintln(writer, "  <target-directory>            Required positional argument.")
	fmt.Fprintln(writer, "  [--reverse]                   Default: false.")
	fmt.Fprintln(writer, "  [--parallel <count>]          Parallel file preparation. Default: logical CPU count.")
	fmt.Fprintln(writer, "  [--headless]                  Force CLI mode.")
	fmt.Fprintln(writer, "  [--version]                   Show version information.")
	fmt.Fprintln(writer, "  [--help]                      Show this help.")
}

type terminalProgress struct {
	writer        io.Writer
	lastFile      int
	lastCompleted int
	lastStage     progress.Stage
	lineActive    bool
}

func newTerminalProgress(writer io.Writer) *terminalProgress {
	return &terminalProgress{writer: writer}
}

func (reporter *terminalProgress) Report(event progress.Event) {
	if event.Stage == progress.StageCompleted {
		reporter.Finish()
		return
	}
	if event.Stage == progress.StageFilePrepared {
		reporter.Finish()
		fmt.Fprintf(reporter.writer, "  Prepared: %s\n", event.Path)
		return
	}
	if event.Stage == progress.StageFileCompleted {
		reporter.Finish()
		if event.FileIndex != reporter.lastCompleted {
			fmt.Fprintf(reporter.writer, "  Committed: %s\n", event.Path)
			reporter.lastCompleted = event.FileIndex
		}
		return
	}
	if event.FileIndex != reporter.lastFile || event.Stage != reporter.lastStage {
		reporter.Finish()
		fmt.Fprintf(reporter.writer, "[%d/%d] Before: %s\n", event.FileIndex, event.FileCount, event.Path)
		reporter.lastFile = event.FileIndex
		reporter.lastStage = event.Stage
	}
	if event.TotalBytes > 0 {
		percentage := float64(event.ProcessedBytes) * 100 / float64(event.TotalBytes)
		label := "Applying"
		if event.Stage == progress.StageVerifying {
			label = "Verifying"
		}
		fmt.Fprintf(reporter.writer, "\r  %s: %6.2f%% (%d/%d bytes)", label, percentage, event.ProcessedBytes, event.TotalBytes)
		reporter.lineActive = true
	}
}

func (reporter *terminalProgress) Finish() {
	if reporter.lineActive {
		fmt.Fprintln(reporter.writer)
		reporter.lineActive = false
	}
}
