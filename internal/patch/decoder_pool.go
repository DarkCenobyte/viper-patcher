package patch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/DarkCenobyte/viper-patcher/internal/zstd"
)

type decoderLease struct {
	decoder *zstd.Decoder
	segment *zstd.PreparedSegment
}

func (lease *decoderLease) DecompressToFile(ctx context.Context, output *os.File, expectedOutputSize uint64, callback zstd.ProgressFunc, outputCallback zstd.OutputFunc) error {
	if lease == nil || lease.decoder == nil || lease.segment == nil {
		return fmt.Errorf("zstd decoder lease is unavailable")
	}
	return lease.decoder.DecompressPreparedSegmentToFile(ctx, lease.segment, output, expectedOutputSize, callback, outputCallback)
}

func (lease *decoderLease) DecompressToWriter(ctx context.Context, writer io.Writer, expectedOutputSize uint64, callback zstd.ProgressFunc) error {
	if lease == nil || lease.decoder == nil || lease.segment == nil {
		return fmt.Errorf("zstd decoder lease is unavailable")
	}
	return lease.decoder.DecompressPreparedSegmentToWriter(ctx, lease.segment, writer, expectedOutputSize, callback)
}

type decoderSlot struct {
	decoder *zstd.Decoder
	input   *zstd.PreparedInput
}

type decoderPool struct {
	available    chan *decoderSlot
	all          []*decoderSlot
	inspection   *zstd.PreparedInput
	patch        *os.File
	windowBudget *zstdWindowBudget
}

func newDecoderPool(count int, windowBudget *zstdWindowBudget, patch *os.File) (*decoderPool, error) {
	if patch == nil {
		return nil, fmt.Errorf("zstd decoder pool patch file is required")
	}
	if count < 1 {
		count = 1
	}
	inspection, err := zstd.PrepareInput(patch)
	if err != nil {
		return nil, err
	}
	pool := &decoderPool{
		available:    make(chan *decoderSlot, count),
		all:          make([]*decoderSlot, 0, count),
		inspection:   inspection,
		patch:        patch,
		windowBudget: windowBudget,
	}
	for range count {
		decoder, err := zstd.NewDecoder()
		if err != nil {
			_ = pool.Close()
			return nil, err
		}
		input, err := zstd.PrepareInput(patch)
		if err != nil {
			_ = decoder.Close()
			_ = pool.Close()
			return nil, err
		}
		slot := &decoderSlot{decoder: decoder, input: input}
		pool.all = append(pool.all, slot)
		pool.available <- slot
	}
	return pool, nil
}

func (pool *decoderPool) acquire(ctx context.Context, patch *os.File, offset, length uint64) (*decoderLease, func(), error) {
	if pool == nil {
		return nil, nil, fmt.Errorf("zstd decoder pool is unavailable")
	}
	if patch == nil || patch != pool.patch {
		return nil, nil, fmt.Errorf("zstd decoder pool belongs to a different patch file")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	inspectionSegment, err := pool.inspection.Segment(offset, length)
	if err != nil {
		return nil, nil, err
	}
	windowSize, err := inspectionSegment.WindowSize()
	_ = inspectionSegment.Close()
	if err != nil {
		return nil, nil, err
	}
	releaseWindow, err := pool.windowBudget.acquire(ctx, windowSize)
	if err != nil {
		return nil, nil, err
	}

	var slot *decoderSlot
	select {
	case slot = <-pool.available:
	case <-ctx.Done():
		releaseWindow()
		return nil, nil, ctx.Err()
	}
	segment, err := slot.input.Segment(offset, length)
	if err != nil {
		pool.available <- slot
		releaseWindow()
		return nil, nil, err
	}
	if err := slot.decoder.SetWindowLimit(windowSize); err != nil {
		_ = segment.Close()
		pool.available <- slot
		releaseWindow()
		return nil, nil, err
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			_ = segment.Close()
			pool.available <- slot
			releaseWindow()
		})
	}
	return &decoderLease{decoder: slot.decoder, segment: segment}, release, nil
}

func (pool *decoderPool) Close() error {
	if pool == nil {
		return nil
	}
	var closeErrors []error
	for _, slot := range pool.all {
		closeErrors = append(closeErrors, slot.decoder.Close(), slot.input.Close())
	}
	closeErrors = append(closeErrors, pool.inspection.Close())
	pool.all = nil
	pool.inspection = nil
	pool.patch = nil
	return errors.Join(closeErrors...)
}
