//go:build !circuit_capacity_checker

package circuitcapacitychecker

import (
	"bytes"
	"math/rand"
	"unsafe"

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/core/types"
)

type CircuitCapacityChecker struct {
	ID        uint64
	countdown int
	nextError *error

	skipHash  string
	skipError error
}

// NewCircuitCapacityChecker creates a new CircuitCapacityChecker
func NewCircuitCapacityChecker(lightMode bool) *CircuitCapacityChecker {
	ccc := &CircuitCapacityChecker{ID: rand.Uint64()}
	ccc.SetLightMode(lightMode)
	return ccc
}

// Reset resets a ccc, but need to do nothing in mock_ccc.
func (ccc *CircuitCapacityChecker) Reset() {
}

// ApplyTransaction appends a tx's wrapped BlockTrace into the ccc, and return the accumulated RowConsumption.
// Will only return a dummy value in mock_ccc.
func (ccc *CircuitCapacityChecker) ApplyTransaction(traces *types.BlockTrace) (*types.RowConsumption, error) {
	if ccc.nextError != nil {
		ccc.countdown--
		if ccc.countdown == 0 {
			err := *ccc.nextError
			ccc.nextError = nil
			return nil, err
		}
	}
	if ccc.skipError != nil {
		if traces.Transactions[0].TxHash == ccc.skipHash {
			return nil, ccc.skipError
		}
	}
	return &types.RowConsumption{types.SubCircuitRowUsage{
		Name:      "mock",
		RowNumber: 1,
	}}, nil
}

func (ccc *CircuitCapacityChecker) ApplyTransactionRustTrace(rustTrace unsafe.Pointer) (*types.RowConsumption, error) {
	return ccc.ApplyTransaction(goTraces[rustTrace])
}

// ApplyBlock gets a block's RowConsumption.
// Will only return a dummy value in mock_ccc.
func (ccc *CircuitCapacityChecker) ApplyBlock(traces *types.BlockTrace) (*types.RowConsumption, error) {
	return &types.RowConsumption{types.SubCircuitRowUsage{
		Name:      "mock",
		RowNumber: 2,
	}}, nil
}

// CheckTxNum compares whether the tx_count in ccc match the expected.
// Will alway return true in mock_ccc.
func (ccc *CircuitCapacityChecker) CheckTxNum(expected int) (bool, uint64, error) {
	return true, uint64(expected), nil
}

// SetLightMode sets to ccc light mode
func (ccc *CircuitCapacityChecker) SetLightMode(lightMode bool) error {
	return nil
}

// ScheduleError schedules an error for a tx (see `ApplyTransaction`), only used in tests.
func (ccc *CircuitCapacityChecker) ScheduleError(cnt int, err error) {
	ccc.countdown = cnt
	ccc.nextError = &err
}

// Skip forced CCC to return always an error for a given txn
func (ccc *CircuitCapacityChecker) Skip(txnHash common.Hash, err error) {
	ccc.skipHash = txnHash.String()
	ccc.skipError = err
}

var goTraces = make(map[unsafe.Pointer]*types.BlockTrace)

func MakeRustTrace(trace *types.BlockTrace, buffer *bytes.Buffer) unsafe.Pointer {
	rustTrace := new(struct{})
	goTraces[unsafe.Pointer(rustTrace)] = trace
	return unsafe.Pointer(rustTrace)
}

func FreeRustTrace(ptr unsafe.Pointer) {
}
