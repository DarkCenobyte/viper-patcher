package patchercli

import (
	"context"
	"flag"
	"fmt"
	"io"

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
	var headless bool
	var help bool
	var version bool
	flags.StringVar(&patchFile, "patch-file", "", "input .vipr patch")
	flags.BoolVar(&reverse, "reverse", false, "apply reverse differentials")
	flags.BoolVar(&headless, "headless", false, "force command-line mode")
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
	_ = headless
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

	parsed, err := patch.Open(patchFile)
	if err != nil {
		fmt.Fprintf(stderr, "Patch validation failed: %v\n", err)
		return 1
	}
	direction := patch.Forward
	if reverse {
		direction = patch.Reverse
	}
	validation, err := patch.Inspect(flags.Arg(0), parsed)
	if err != nil {
		fmt.Fprintf(stderr, "File validation failed: %v\n", err)
		return 1
	}
	if !validation.Ready(direction) {
		fmt.Fprintf(stderr, "Cannot apply %s patch: %v\n", direction, validation.Error())
		return 1
	}

	reporter := newTerminalProgress(stderr)
	if err := patch.Apply(ctx, patchFile, flags.Arg(0), direction, reporter.Report); err != nil {
		reporter.Finish()
		fmt.Fprintf(stderr, "Patch operation failed: %v\n", err)
		return 1
	}
	reporter.Finish()
	fmt.Fprintf(stdout, "%s patch applied successfully.\n", direction)
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
	fmt.Fprintln(writer, "  [--headless]                  Force CLI mode.")
	fmt.Fprintln(writer, "  [--version]                   Show version information.")
	fmt.Fprintln(writer, "  [--help]                      Show this help.")
}

type terminalProgress struct {
	writer        io.Writer
	lastFile      int
	lastCompleted int
	lineActive    bool
}

func newTerminalProgress(writer io.Writer) *terminalProgress {
	return &terminalProgress{writer: writer}
}

func (reporter *terminalProgress) Report(event progress.Event) {
	if event.Stage == "completed" {
		reporter.Finish()
		return
	}
	if event.Stage == "file-completed" {
		reporter.Finish()
		if event.FileIndex != reporter.lastCompleted {
			fmt.Fprintf(reporter.writer, "  After:  %s\n", event.Path)
			reporter.lastCompleted = event.FileIndex
		}
		return
	}
	if event.FileIndex != reporter.lastFile {
		reporter.Finish()
		fmt.Fprintf(reporter.writer, "[%d/%d] Before: %s\n", event.FileIndex, event.FileCount, event.Path)
		reporter.lastFile = event.FileIndex
	}
	if event.TotalBytes > 0 {
		percentage := float64(event.ProcessedBytes) * 100 / float64(event.TotalBytes)
		fmt.Fprintf(reporter.writer, "\r  Applying: %6.2f%% (%d/%d bytes)", percentage, event.ProcessedBytes, event.TotalBytes)
		reporter.lineActive = true
	}
}

func (reporter *terminalProgress) Finish() {
	if reporter.lineActive {
		fmt.Fprintln(reporter.writer)
		reporter.lineActive = false
	}
}
