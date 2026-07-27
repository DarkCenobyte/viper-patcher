package progress

// Stage identifies one phase of a patch creation or application operation.
type Stage string

const (
	StagePreparing          Stage = "preparing"
	StageSnapshotting       Stage = "snapshotting"
	StageCompressingForward Stage = "compressing-forward"
	StageCompressingReverse Stage = "compressing-reverse"
	StageApplying           Stage = "applying"
	StageVerifying          Stage = "verifying"
	StageFilePrepared       Stage = "file-prepared"
	StageFileCompleted      Stage = "file-completed"
	StageCompleted          Stage = "completed"
)

// Event reports progress for one file in a multi-file operation.
type Event struct {
	FileIndex      int
	FileCount      int
	Path           string
	ProcessedBytes uint64
	TotalBytes     uint64
	Stage          Stage
	// Overall is the monotonic weighted progress of the complete operation in
	// the inclusive range 0..1.
	Overall float64
}

// Callback receives serialized progress events.
type Callback func(Event)

// Report invokes callback when it is not nil.
func Report(callback Callback, event Event) {
	if callback != nil {
		callback(event)
	}
}
