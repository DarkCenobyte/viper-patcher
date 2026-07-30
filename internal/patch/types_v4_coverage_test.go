package patch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DarkCenobyte/viper-patcher/internal/nativev4"
	"github.com/DarkCenobyte/viper-patcher/internal/patchformat"
	"github.com/DarkCenobyte/viper-patcher/internal/progress"
)

func TestV4ParseModes(t *testing.T) {
	verifyTests := map[string]VerifyMode{"": VerifyReferenced, " referenced ": VerifyReferenced, "STRICT": VerifyStrict, "output": VerifyOutput}
	for input, want := range verifyTests {
		got, err := ParseVerifyMode(input)
		if err != nil || got != want {
			t.Fatalf("ParseVerifyMode(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := ParseVerifyMode("none"); err == nil {
		t.Fatal("ParseVerifyMode accepted an unsupported mode")
	}

	durabilityTests := map[string]DurabilityMode{"": DurabilityBuffered, " BUFFERED ": DurabilityBuffered, "durable": DurabilityDurable}
	for input, want := range durabilityTests {
		got, err := ParseDurabilityMode(input)
		if err != nil || got != want {
			t.Fatalf("ParseDurabilityMode(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := ParseDurabilityMode("journaled"); err == nil {
		t.Fatal("ParseDurabilityMode accepted an unsupported mode")
	}

	profileTests := map[string]IOProfile{"": IOAuto, " AUTO ": IOAuto, "hdd": IOHDD, "SSD": IOSSD, "nvme": IONVMe}
	for input, want := range profileTests {
		got, err := ParseIOProfile(input)
		if err != nil || got != want {
			t.Fatalf("ParseIOProfile(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := ParseIOProfile("tape"); err == nil {
		t.Fatal("ParseIOProfile accepted an unsupported profile")
	}
}

func TestV4WindowSizeParsingAndAutomaticSelection(t *testing.T) {
	valid := map[string]uint32{
		"": 0, "auto": 0, " AUTO ": 0,
		"256K": 256 << 10, "512k": 512 << 10,
		"1M": 1 << 20, "2m": 2 << 20, "4M": 4 << 20, "8M": 8 << 20,
		"262144": 256 << 10,
	}
	for input, want := range valid {
		got, err := ParseWindowSize(input)
		if err != nil || got != want {
			t.Fatalf("ParseWindowSize(%q) = %d, %v; want %d", input, got, err, want)
		}
	}
	for _, input := range []string{"1K", "3M", "1024", "garbage", "4294967296"} {
		if _, err := ParseWindowSize(input); err == nil {
			t.Fatalf("ParseWindowSize(%q) unexpectedly succeeded", input)
		}
	}

	tests := []struct {
		size uint64
		want uint32
	}{
		{0, 256 << 10},
		{1<<20 - 1, 256 << 10},
		{1 << 20, 512 << 10},
		{16 << 20, 512 << 10},
		{16<<20 + 1, 1 << 20},
		{128 << 20, 1 << 20},
		{128<<20 + 1, 2 << 20},
		{1 << 30, 2 << 20},
		{1<<30 + 1, 4 << 20},
	}
	for _, test := range tests {
		if got := automaticWindowSize(test.size); got != test.want {
			t.Fatalf("automaticWindowSize(%d) = %d, want %d", test.size, got, test.want)
		}
	}
	if max64(1, 2) != 2 || max64(3, 2) != 3 {
		t.Fatal("max64 returned the wrong operand")
	}
}

func TestV4CommittedWarningsAndValidationResult(t *testing.T) {
	if warning := (*CommittedWarning)(nil); warning.Error() != "" || warning.Unwrap() != nil {
		t.Fatal("nil committed warning is not inert")
	}
	causeOne := errors.New("one")
	causeTwo := errors.New("two")
	warning := committedWarning("operation", nil, causeOne, causeTwo)
	if warning == nil || !IsCommittedWarning(warning) || !errors.Is(warning, causeOne) || !errors.Is(warning, causeTwo) {
		t.Fatalf("unexpected committed warning: %v", warning)
	}
	if !strings.Contains(warning.Error(), "operation committed with a cleanup warning") {
		t.Fatalf("warning text = %q", warning.Error())
	}
	if committedWarning("operation", nil) != nil || IsCommittedWarning(causeOne) {
		t.Fatal("committed warning classification is incorrect")
	}

	result := ValidationResult{State: StateForwardReady, CanApplyForward: true}
	if !result.Ready(Forward) || result.Ready(Reverse) || result.Ready(Direction("sideways")) {
		t.Fatal("Ready returned an invalid direction result")
	}
	if err := result.ErrorFor(Forward); err != nil {
		t.Fatal(err)
	}
	if err := result.ErrorFor(Reverse); err == nil || !strings.Contains(err.Error(), string(StateForwardReady)) {
		t.Fatalf("ErrorFor = %v", err)
	}
}

func TestV4ApplyModeNormalizationAndScheduling(t *testing.T) {
	verify, durability, profile, err := normalizeApplyModes("", "", "")
	if err != nil || verify != VerifyReferenced || durability != DurabilityBuffered || profile != IOAuto {
		t.Fatalf("normalizeApplyModes = %q, %q, %q, %v", verify, durability, profile, err)
	}
	for _, test := range []struct {
		verify     VerifyMode
		durability DurabilityMode
		profile    IOProfile
	}{
		{verify: "bad"},
		{durability: "bad"},
		{profile: "bad"},
	} {
		if _, _, _, err := normalizeApplyModes(test.verify, test.durability, test.profile); err == nil {
			t.Fatalf("normalizeApplyModes(%q,%q,%q) unexpectedly succeeded", test.verify, test.durability, test.profile)
		}
	}

	if nativeIOProfile(IOHDD) != nativev4.IOHDD || nativeIOProfile(IOSSD) != nativev4.IOSSD || nativeIOProfile(IONVMe) != nativev4.IONVMe || nativeIOProfile(IOAuto) != nativev4.IOAuto || nativeIOProfile("unknown") != nativev4.IOAuto {
		t.Fatal("native I/O profile mapping is incorrect")
	}

	tests := []struct {
		profile                IOProfile
		requested, files       int
		wantFiles, wantPerFile int
	}{
		{IOHDD, 16, 8, 1, 1},
		{IOSSD, 32, 1, 1, 8},
		{IONVMe, 8, 1, 1, 8},
		{IOAuto, 0, 1, 1, 1},
		{IOAuto, 2, 4, 2, 1},
		{IOAuto, 8, 3, 3, 2},
		{IOProfile("unknown"), 16, 2, 2, 2},
	}
	for _, test := range tests {
		files, perFile := applicationWorkers(test.profile, test.requested, test.files)
		if files != test.wantFiles || perFile != test.wantPerFile {
			t.Fatalf("applicationWorkers(%q,%d,%d) = %d,%d; want %d,%d", test.profile, test.requested, test.files, files, perFile, test.wantFiles, test.wantPerFile)
		}
	}
}

func TestV4DirectionGroupingAndClonePlanning(t *testing.T) {
	entry := patchformat.FileEntry{
		SourceSize: 10, TargetSize: 20,
		SourceDigest: patchformat.Digest{1}, TargetDigest: patchformat.Digest{2},
		SourceChunks: []patchformat.Digest{{3}}, TargetChunks: []patchformat.Digest{{4}},
		ForwardWindows: []patchformat.WindowDescriptor{{OutputSize: 20}},
		ReverseWindows: []patchformat.WindowDescriptor{{OutputSize: 10}},
	}
	input, output, inputRoot, outputRoot, inputChunks, outputChunks, windows := directionData(entry, Forward)
	if input != 10 || output != 20 || inputRoot != entry.SourceDigest || outputRoot != entry.TargetDigest || len(inputChunks) != 1 || len(outputChunks) != 1 || len(windows) != 1 || windows[0].OutputSize != 20 {
		t.Fatal("forward direction data is incorrect")
	}
	input, output, inputRoot, outputRoot, inputChunks, outputChunks, windows = directionData(entry, Reverse)
	if input != 20 || output != 10 || inputRoot != entry.TargetDigest || outputRoot != entry.SourceDigest || len(inputChunks) != 1 || len(outputChunks) != 1 || windows[0].OutputSize != 10 {
		t.Fatal("reverse direction data is incorrect")
	}

	if cloneWorthTrying(nil, 1, 2) || cloneWorthTrying(nil, 512<<10, 512<<10) {
		t.Fatal("clone was selected for an ineligible size")
	}
	cloneWindows := []patchformat.WindowDescriptor{
		{OutputSize: 9 << 20, Kind: patchformat.WindowSame},
		{OutputOffset: 9 << 20, OutputSize: 1 << 20, Kind: patchformat.WindowReplaceRaw},
	}
	if !cloneWorthTrying(cloneWindows, 10<<20, 10<<20) {
		t.Fatal("90 percent SAME coverage was not clone eligible")
	}
	cloneWindows[0].OutputSize--
	if cloneWorthTrying(cloneWindows, 10<<20, 10<<20) {
		t.Fatal("sub-90 percent SAME coverage was clone eligible")
	}

	windows = []patchformat.WindowDescriptor{
		{OutputOffset: 0, OutputSize: 4 << 20, Kind: patchformat.WindowSame},
		{OutputOffset: 4 << 20, OutputSize: 4 << 20, Kind: patchformat.WindowSame},
		{OutputOffset: 8 << 20, OutputSize: 2 << 20, Kind: patchformat.WindowZero},
	}
	groups := groupWindows(windows, 10<<20)
	if len(groups) != 2 || groups[0].first != 0 || groups[0].last != 2 || groups[0].size != 8<<20 || groups[1].first != 2 || groups[1].last != 3 || groups[1].size != 2<<20 {
		t.Fatalf("groups = %+v", groups)
	}
	if groupWindows(nil, 0) != nil || sumWindowBytes(windows) != 10<<20 {
		t.Fatal("window grouping totals are incorrect")
	}
	if applicationWorkCount(false, windows, 10<<20) != 2 ||
		applicationWorkCount(true, windows, 10<<20) != 1 ||
		applicationWorkCount(true, windows[:2], 8<<20) != 0 {
		t.Fatal("application work count is incorrect")
	}

	var events []progress.Event
	if err := applyClonedWindows(context.Background(), nil, nil, windows[:2], 8<<20, nil, 2, 0, 1, "same.bin", func(event progress.Event) { events = append(events, event) }); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].ProcessedBytes != 8<<20 {
		t.Fatalf("clone progress events = %+v", events)
	}
	if err := applyWindowGroups(context.Background(), nil, nil, nil, 0, 0, nil, nil, 1, 0, 1, "empty.bin", nil); err != nil {
		t.Fatal(err)
	}
	if err := applyWindowGroups(context.Background(), nil, nil, []patchformat.WindowDescriptor{{OutputSize: 1}}, 0, 1, nil, nil, 1, 0, 1, "bad.bin", nil); err == nil {
		t.Fatal("applyWindowGroups accepted a missing output digest")
	}

	var counter atomicCounter
	if counter.Add(2) != 2 || counter.Add(3) != 5 {
		t.Fatal("atomic counter is incorrect")
	}
}
