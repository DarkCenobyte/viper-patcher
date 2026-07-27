package patch

import "testing"

func TestWorkerAllocation(t *testing.T) {
	tests := []struct {
		workers     int
		files       int
		wantFiles   int
		wantPerFile int
	}{
		{workers: 16, files: 1, wantFiles: 1, wantPerFile: 16},
		{workers: 16, files: 2, wantFiles: 2, wantPerFile: 8},
		{workers: 16, files: 4, wantFiles: 4, wantPerFile: 4},
		{workers: 16, files: 10, wantFiles: 8, wantPerFile: 2},
		{workers: 2, files: 2, wantFiles: 2, wantPerFile: 1},
		{workers: 4, files: 10, wantFiles: 4, wantPerFile: 1},
	}
	for _, test := range tests {
		fileWorkers, perFile := workerAllocation(test.workers, test.files)
		if fileWorkers != test.wantFiles || perFile != test.wantPerFile {
			t.Fatalf("workers=%d files=%d: got %d/%d, want %d/%d", test.workers, test.files, fileWorkers, perFile, test.wantFiles, test.wantPerFile)
		}
	}
}

func TestAdaptiveChunkWorkers(t *testing.T) {
	tests := []struct {
		budget int
		size   uint64
		want   int
	}{
		{budget: 16, size: 500 << 20, want: 8},
		{budget: 8, size: 500 << 20, want: 8},
		{budget: 4, size: 500 << 20, want: 4},
		{budget: 16, size: 16 << 20, want: 1},
		{budget: 16, size: 50 << 20, want: 2},
	}
	for _, test := range tests {
		if got := adaptiveChunkWorkers(test.budget, test.size); got != test.want {
			t.Fatalf("budget=%d size=%d: got %d, want %d", test.budget, test.size, got, test.want)
		}
	}
}
