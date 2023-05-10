package circuitscapacitychecker

import (
	"github.com/scroll-tech/go-ethereum/core/types"
)

type CircuitsCapacityChecker struct{}

func NewCircuitsCapacityChecker() *CircuitsCapacityChecker {
	return &CircuitsCapacityChecker{}
}

// TODO:
func (ccc *CircuitsCapacityChecker) Reset() {
	// panic if call fails?
}

// TODO:
func (ccc *CircuitsCapacityChecker) ApplyTransaction(logs []*types.Log) error {
	return nil
}
