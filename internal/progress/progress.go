package progress

// Event reports progress for one file in a multi-file operation.
type Event struct {
	FileIndex      int
	FileCount      int
	Path           string
	ProcessedBytes uint64
	TotalBytes     uint64
	Stage          string
}

// Callback receives progress events. Implementations must be concurrency-safe.
type Callback func(Event)

// Report invokes callback when it is not nil.
func Report(callback Callback, event Event) {
	if callback != nil {
		callback(event)
	}
}
