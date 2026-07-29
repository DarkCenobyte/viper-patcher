//go:build ignore

package patch

import (
	"context"
	"errors"
	"fmt"
	"math/bits"

	"github.com/DarkCenobyte/viper-patcher/internal/zstd"
)

const zstdWindowBudgetUnit uint64 = 1 << 20

type applicationResources struct {
	zstdWindowBudget *zstdWindowBudget
}

var processApplicationResources = applicationResources{
	zstdWindowBudget: newZstdWindowBudget(processZstdWindowBudgetLimit()),
}

func processZstdWindowBudgetLimit() uint64 {
	maximumWindow := zstd.DecoderWindowLimit()
	if bits.UintSize == 32 {
		return maximumWindow
	}
	const target64 uint64 = 512 << 20
	if maximumWindow >= target64 {
		return maximumWindow
	}
	return target64
}

type zstdWindowBudget struct {
	*memoryBudget
}

func newZstdWindowBudget(limit uint64) *zstdWindowBudget {
	return &zstdWindowBudget{memoryBudget: newMemoryBudget(limit, zstdWindowBudgetUnit)}
}

func (budget *zstdWindowBudget) acquire(ctx context.Context, amount uint64) (func(), error) {
	if budget == nil {
		return func() {}, nil
	}
	required := zstdWindowBudgetUnits(amount)
	if required < 1 {
		required = 1
	}
	if required > budget.capacity {
		return nil, fmt.Errorf("zstd frame window requires %d bytes, exceeding the %d-byte decoder limit",
			amount, budget.limitBytes())
	}
	release, err := budget.reserve(ctx, required)
	if errors.Is(err, errMemoryBudgetExceeded) {
		return nil, fmt.Errorf("zstd frame window requires %d bytes, exceeding the %d-byte decoder limit",
			amount, budget.limitBytes())
	}
	return release, err
}

func zstdWindowBudgetUnits(amount uint64) int {
	return memoryBudgetUnits(amount, zstdWindowBudgetUnit)
}
