package patch

import (
	"errors"

	"github.com/DarkCenobyte/viper-patcher/internal/zstd"
)

type decoderPool struct {
	available chan *zstd.Decoder
	all       []*zstd.Decoder
}

func newDecoderPool(count int) (*decoderPool, error) {
	if count < 1 {
		count = 1
	}
	pool := &decoderPool{available: make(chan *zstd.Decoder, count), all: make([]*zstd.Decoder, 0, count)}
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

func (pool *decoderPool) acquire() *zstd.Decoder {
	return <-pool.available
}

func (pool *decoderPool) release(decoder *zstd.Decoder) {
	pool.available <- decoder
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
