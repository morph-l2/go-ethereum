//go:build !placeholder

package circuitscapacitychecker

import (
	"github.com/scroll-tech/go-ethereum/core/types"
)

type CircuitsCapacityChecker struct{}

func NewCircuitsCapacityChecker() *CircuitsCapacityChecker {
	return &CircuitsCapacityChecker{}
}

func (ccc *CircuitsCapacityChecker) Reset() {
}

func (ccc *CircuitsCapacityChecker) ApplyTransaction(traces *types.BlockTrace) error {
	return nil
}
