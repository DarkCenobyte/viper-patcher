package creatorcli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/DarkCenobyte/viper-patcher/internal/buildinfo"
	"github.com/DarkCenobyte/viper-patcher/internal/patch"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ", ") }
func (values *stringList) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("file path must not be empty")
	}
	*values = append(*values, value)
	return nil
}

// Run executes creator CLI mode and returns a process exit code.
func Run(ctx context.Context, arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("creator", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var sources stringList
	var targets stringList
	var level int
	var comment string
	var reverse bool
	var headless bool
	var help bool
	var version bool
	flags.Var(&sources, "source-files", "source file; repeat for each file")
	flags.Var(&targets, "target-files", "target file; repeat for each file")
	flags.IntVar(&level, "compression-level", 3, "zstd compression level")
	flags.StringVar(&comment, "comment", "Created with Viper-Patcher", "comment stored in the patch")
	flags.BoolVar(&reverse, "create-reverse", false, "include reverse differentials")
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
		fmt.Fprintf(stdout, "viper-patcher creator %s (%s, %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate)
		return 0
	}
	if len(sources) == 0 || len(targets) == 0 {
		fmt.Fprintln(stderr, "Error: --source-files and --target-files are required and must have values.")
		fmt.Fprintln(stderr)
		printUsage(stderr)
		return 2
	}
	if flags.NArg() == 0 {
		fmt.Fprintln(stderr, "Error: the output .vipr path is required as the final argument.")
		fmt.Fprintln(stderr)
		printUsage(stderr)
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintf(stderr, "Error: expected exactly one output .vipr path as the final argument, got %d positional arguments.\n\n", flags.NArg())
		printUsage(stderr)
		return 2
	}
	if strings.TrimSpace(flags.Arg(0)) == "" {
		fmt.Fprintln(stderr, "Error: the output .vipr path must not be empty.")
		fmt.Fprintln(stderr)
		printUsage(stderr)
		return 2
	}
	output := flags.Arg(0)
	if len(sources) != len(targets) {
		fmt.Fprintf(stderr, "Error: source-files contains %d file(s), but target-files contains %d file(s).\n\n", len(sources), len(targets))
		printUsage(stderr)
		return 2
	}

	reporter := newTerminalProgress(stderr)
	err := patch.Create(ctx, patch.CreateOptions{
		SourceFiles:      sources,
		TargetFiles:      targets,
		OutputPath:       output,
		CompressionLevel: level,
		Comment:          comment,
		CreateReverse:    reverse,
	}, reporter.Report)
	reporter.Finish()
	if err != nil {
		fmt.Fprintf(stderr, "Patch creation failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Patch created: %s\n", output)
	return 0
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Example:")
	fmt.Fprintln(writer, "  creator --headless --source-files old/bin/game.exe --target-files new/bin/game.exe --source-files old/data/assets.bin --target-files new/data/assets.bin --compression-level 12 --comment \"Version 1.1 update\" --create-reverse update.vipr")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Supported parameters and arguments:")
	fmt.Fprintln(writer, "  --source-files <file>          Required. Repeat once per source file.")
	fmt.Fprintln(writer, "  --target-files <file>          Required. Repeat once per target file, in matching order.")
	fmt.Fprintln(writer, "  [--compression-level <level>]  Default: 3.")
	fmt.Fprintln(writer, "  [--comment <text>]             Default: Created with Viper-Patcher.")
	fmt.Fprintln(writer, "  [--create-reverse]             Default: false.")
	fmt.Fprintln(writer, "  [--headless]                   Force CLI mode.")
	fmt.Fprintln(writer, "  [--version]                    Show version information.")
	fmt.Fprintln(writer, "  [--help]                       Show this help.")
	fmt.Fprintln(writer, "  <output.vipr>                  Required final positional argument.")
}

type terminalProgress struct {
	writer     io.Writer
	lastFile   int
	lastStage  string
	lineActive bool
}

func newTerminalProgress(writer io.Writer) *terminalProgress {
	return &terminalProgress{writer: writer}
}

func (reporter *terminalProgress) Report(event progress.Event) {
	if event.Stage == "completed" {
		reporter.Finish()
		return
	}
	if event.FileIndex != reporter.lastFile || event.Stage != reporter.lastStage {
		reporter.Finish()
		fmt.Fprintf(reporter.writer, "[%d/%d] %s: %s\n", event.FileIndex, event.FileCount, event.Stage, event.Path)
		reporter.lastFile = event.FileIndex
		reporter.lastStage = event.Stage
	}
	if event.TotalBytes > 0 {
		percentage := float64(event.ProcessedBytes) * 100 / float64(event.TotalBytes)
		fmt.Fprintf(reporter.writer, "\r  Progress: %6.2f%% (%d/%d bytes)", percentage, event.ProcessedBytes, event.TotalBytes)
		reporter.lineActive = true
	}
}

func (reporter *terminalProgress) Finish() {
	if reporter.lineActive {
		fmt.Fprintln(reporter.writer)
		reporter.lineActive = false
	}
}
