//go:build circuits_capacity_checker

package circuitscapacitychecker

/*
#cgo LDFLAGS: -L${SRCDIR}/lib/ -lm -ldl -lzkp -lzktrie -L${SRCDIR}/libzkp/ -Wl,-rpath=${SRCDIR}/libzkp
#include <stdlib.h>
#include "./libzkp/libzkp.h"
*/
import "C" //nolint:typecheck

import (
	"encoding/json"
	"unsafe"

	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/log"
)

type CircuitsCapacityChecker struct{}

func NewCircuitsCapacityChecker() *CircuitsCapacityChecker {
	C.new_circuit_capacity_checker()
	return &CircuitsCapacityChecker{}
}

func (ccc *CircuitsCapacityChecker) Reset() {
	C.reset_circuit_capacity_checker()
}

func (ccc *CircuitsCapacityChecker) ApplyTransaction(traces *types.BlockTrace) error {
	tracesByt, err := json.Marshal(traces)
	if err != nil {
		return ErrUnknown
	}

	tracesStr := C.CString(string(tracesByt))
	defer func() {
		C.free(unsafe.Pointer(tracesStr))
	}()

	log.Info("start to check circuits capacity")
	result := C.apply_tx(tracesStr)
	log.Info("check circuits capacity done")

	switch result {
	case 0:
		return nil
	case 1:
		return ErrBlockRowUsageOverflow
	case 2:
		return ErrTxRowUsageOverflow
	default:
		return ErrUnknown
	}
}
