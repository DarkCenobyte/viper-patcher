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

const filePairSeparator = "::"

type filePairList []patch.FilePair

func (pairs *filePairList) String() string {
	values := make([]string, len(*pairs))
	for index, pair := range *pairs {
		values[index] = pair.SourcePath + filePairSeparator + pair.TargetPath
	}
	return strings.Join(values, ", ")
}

func (pairs *filePairList) Set(value string) error {
	parts := strings.SplitN(value, filePairSeparator, 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return fmt.Errorf("file pair must use the form <source>%s<target>", filePairSeparator)
	}
	*pairs = append(*pairs, patch.FilePair{SourcePath: parts[0], TargetPath: parts[1]})
	return nil
}

// Run executes creator CLI mode and returns a process exit code.
func Run(ctx context.Context, arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("creator", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var pairs filePairList
	var level int
	var comment string
	var reverse bool
	var workDirectory string
	var workerBudget int
	var help bool
	var version bool
	flags.Var(&pairs, "file-pair", "source and target pair; repeat for each file")
	flags.IntVar(&level, "compression-level", 3, "zstd compression level")
	flags.StringVar(&comment, "comment", "Created with Viper-Patcher", "comment stored in the patch")
	flags.BoolVar(&reverse, "create-reverse", false, "include reverse differentials")
	flags.StringVar(&workDirectory, "work-directory", "", "parent directory for temporary creator data")
	flags.IntVar(&workerBudget, "workers", 0, "logical worker target; 0 selects the automatic system-aware default")
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
		fmt.Fprintf(stdout, "viper-patcher creator %s (%s, %s)\n", buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate)
		return 0
	}
	if len(pairs) == 0 {
		fmt.Fprintln(stderr, "Error: at least one --file-pair value is required.")
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
	reporter := newTerminalProgress(stderr)
	err := patch.Create(ctx, patch.CreateOptions{
		Files:            pairs,
		OutputPath:       output,
		CompressionLevel: level,
		Comment:          comment,
		CreateReverse:    reverse,
		WorkDirectory:    workDirectory,
		WorkerBudget:     workerBudget,
	}, reporter.Report)
	reporter.Finish()
	if err != nil && !patch.IsCommittedWarning(err) {
		fmt.Fprintf(stderr, "Patch creation failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Patch created: %s\n", output)
	if err != nil {
		fmt.Fprintf(stderr, "Warning: %v\n", err)
	}
	return 0
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, "Example:")
	fmt.Fprintln(writer, "  creator --headless --file-pair old/bin/game.exe::new/bin/game.exe --file-pair old/data/assets.bin::new/data/assets.bin --compression-level 12 --workers 4 --comment \"Version 1.1 update\" --create-reverse update.vipr")
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "Supported parameters and arguments:")
	fmt.Fprintln(writer, "  --file-pair <source>::<target> Required. Repeat once per source/target pair.")
	fmt.Fprintln(writer, "  [--compression-level <level>]  Default: 3.")
	fmt.Fprintln(writer, "  [--comment <text>]             Default: Created with Viper-Patcher.")
	fmt.Fprintln(writer, "  [--create-reverse]             Default: false.")
	fmt.Fprintln(writer, "  [--work-directory <directory>] Optional creator temporary-data parent.")
	fmt.Fprintln(writer, "  [--workers <count>]            0 (automatic) or 1..logical CPU count. Default: 0.")
	fmt.Fprintln(writer, "  [--headless]                   Force CLI mode.")
	fmt.Fprintln(writer, "  [--version]                    Show version information.")
	fmt.Fprintln(writer, "  [--help]                       Show this help.")
	fmt.Fprintln(writer, "  <output.vipr>                  Required final positional argument.")
}

type terminalProgress struct {
	writer     io.Writer
	lastFile   int
	lastStage  progress.Stage
	lineActive bool
}

func newTerminalProgress(writer io.Writer) *terminalProgress {
	return &terminalProgress{writer: writer}
}

func (reporter *terminalProgress) Report(event progress.Event) {
	if event.Stage == progress.StageCompleted {
		reporter.Finish()
		return
	}
	if event.FileIndex != reporter.lastFile || event.Stage != reporter.lastStage {
		reporter.Finish()
		if event.Path == "" {
			fmt.Fprintf(reporter.writer, "%s\n", event.Stage)
		} else {
			fmt.Fprintf(reporter.writer, "[%d/%d] %s: %s\n", event.FileIndex, event.FileCount, event.Stage, event.Path)
		}
		reporter.lastFile = event.FileIndex
		reporter.lastStage = event.Stage
	}
	if event.TotalBytes > 0 {
		percentage := float64(event.ProcessedBytes) * 100 / float64(event.TotalBytes)
		fmt.Fprintf(reporter.writer, "\r  Progress: %6.2f%% (%d/%d bytes, overall %6.2f%%)", percentage, event.ProcessedBytes, event.TotalBytes, event.Overall*100)
		reporter.lineActive = true
	}
}

func (reporter *terminalProgress) Finish() {
	if reporter.lineActive {
		fmt.Fprintln(reporter.writer)
		reporter.lineActive = false
	}
}
