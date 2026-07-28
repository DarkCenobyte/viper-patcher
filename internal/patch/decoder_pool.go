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

type decoderPool struct {
	available    chan *zstd.Decoder
	all          []*zstd.Decoder
	windowBudget *zstdWindowBudget
}

func newDecoderPool(count int, windowBudget *zstdWindowBudget) (*decoderPool, error) {
	if count < 1 {
		count = 1
	}
	pool := &decoderPool{
		available:    make(chan *zstd.Decoder, count),
		all:          make([]*zstd.Decoder, 0, count),
		windowBudget: windowBudget,
	}
	for range count {
		decoder, err := zstd.NewDecoder()
		if err != nil {
			_ = pool.Close()
			return nil, err
		}
		pool.all = append(pool.all, decoder)
		pool.available <- decoder
	}
	return pool, nil
}

func (pool *decoderPool) acquire(ctx context.Context, patch *os.File, offset, length uint64) (*decoderLease, func(), error) {
	if pool == nil {
		return nil, nil, fmt.Errorf("zstd decoder pool is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	segment, err := zstd.PrepareSegment(patch, offset, length)
	if err != nil {
		return nil, nil, err
	}
	windowSize, err := segment.WindowSize()
	if err != nil {
		_ = segment.Close()
		return nil, nil, err
	}
	releaseWindow, err := pool.windowBudget.acquire(ctx, windowSize)
	if err != nil {
		_ = segment.Close()
		return nil, nil, err
	}

	var decoder *zstd.Decoder
	select {
	case decoder = <-pool.available:
	case <-ctx.Done():
		releaseWindow()
		_ = segment.Close()
		return nil, nil, ctx.Err()
	}
	if err := decoder.SetWindowLimit(windowSize); err != nil {
		pool.available <- decoder
		releaseWindow()
		_ = segment.Close()
		return nil, nil, err
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			_ = segment.Close()
			pool.available <- decoder
			releaseWindow()
		})
	}
	return &decoderLease{decoder: decoder, segment: segment}, release, nil
}

func (pool *decoderPool) Close() error {
	if pool == nil {
		return nil
	}
	var closeErrors []error
	for _, decoder := range pool.all {
		closeErrors = append(closeErrors, decoder.Close())
	}
	pool.all = nil
	return errors.Join(closeErrors...)
}
