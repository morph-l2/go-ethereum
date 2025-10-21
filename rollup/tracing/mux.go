package tracing

import (
	"math/big"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/tracing"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/eth/tracers"
	"github.com/morph-l2/go-ethereum/eth/tracers/logger"
)

type muxTracer struct {
	subTracers   []tracers.Tracer
	structLogger *logger.StructLogger
}

func NewMuxTracer(structLogger *logger.StructLogger, subTracers ...tracers.Tracer) *tracers.Tracer {
	t := &muxTracer{
		subTracers:   subTracers,
		structLogger: structLogger,
	}
	return &tracers.Tracer{
		Hooks: &tracing.Hooks{
			OnTxStart:       t.OnTxStart,
			OnTxEnd:         t.OnTxEnd,
			OnEnter:         t.OnEnter,
			OnExit:          t.OnExit,
			OnOpcode:        t.OnOpcode,
			OnFault:         t.OnFault,
			OnGasChange:     t.OnGasChange,
			OnBalanceChange: t.OnBalanceChange,
			OnNonceChange:   t.OnNonceChange,
			OnCodeChange:    t.OnCodeChange,
			OnStorageChange: t.OnStorageChange,
			OnLog:           t.OnLog,
		},
		Stop: t.Stop,
	}
}

func (t *muxTracer) OnOpcode(pc uint64, op byte, gas, cost uint64, scope tracing.OpContext, rData []byte, depth int, err error) {
	for _, t := range t.subTracers {
		if t.OnOpcode != nil {
			t.OnOpcode(pc, op, gas, cost, scope, rData, depth, err)
		}
	}
	t.structLogger.OnOpcode(pc, op, gas, cost, scope, rData, depth, err)
}

func (t *muxTracer) OnFault(pc uint64, op byte, gas, cost uint64, scope tracing.OpContext, depth int, err error) {
	for _, t := range t.subTracers {
		if t.OnFault != nil {
			t.OnFault(pc, op, gas, cost, scope, depth, err)
		}
	}
}

func (t *muxTracer) OnGasChange(old, new uint64, reason tracing.GasChangeReason) {
	for _, t := range t.subTracers {
		if t.OnGasChange != nil {
			t.OnGasChange(old, new, reason)
		}
	}
}

func (t *muxTracer) OnEnter(depth int, typ byte, from common.Address, to common.Address, input []byte, gas uint64, value *big.Int) {
	for _, t := range t.subTracers {
		if t.OnEnter != nil {
			t.OnEnter(depth, typ, from, to, input, gas, value)
		}
	}
	t.structLogger.OnEnter(depth, typ, from, to, input, gas, value)
}

func (t *muxTracer) OnExit(depth int, output []byte, gasUsed uint64, err error, reverted bool) {
	for _, t := range t.subTracers {
		if t.OnExit != nil {
			t.OnExit(depth, output, gasUsed, err, reverted)
		}
	}
	t.structLogger.OnExit(depth, output, gasUsed, err, reverted)
}

func (t *muxTracer) OnTxStart(env *tracing.VMContext, tx *types.Transaction, from common.Address) {
	for _, t := range t.subTracers {
		if t.OnTxStart != nil {
			t.OnTxStart(env, tx, from)
		}
	}
	t.structLogger.OnTxStart(env, tx, from)
}

func (t *muxTracer) OnTxEnd(receipt *types.Receipt, err error) {
	for _, t := range t.subTracers {
		if t.OnTxEnd != nil {
			t.OnTxEnd(receipt, err)
		}
	}
	t.structLogger.OnTxEnd(receipt, err)
}

func (t *muxTracer) OnBalanceChange(a common.Address, prev, new *big.Int, reason tracing.BalanceChangeReason) {
	for _, t := range t.subTracers {
		if t.OnBalanceChange != nil {
			t.OnBalanceChange(a, prev, new, reason)
		}
	}
}

func (t *muxTracer) OnNonceChange(a common.Address, prev, new uint64) {
	for _, t := range t.subTracers {
		if t.OnNonceChange != nil {
			t.OnNonceChange(a, prev, new)
		}
	}
}

func (t *muxTracer) OnCodeChange(a common.Address, prevCodeHash common.Hash, prev []byte, codeHash common.Hash, code []byte) {
	for _, t := range t.subTracers {
		if t.OnCodeChange != nil {
			t.OnCodeChange(a, prevCodeHash, prev, codeHash, code)
		}
	}
}

func (t *muxTracer) OnStorageChange(a common.Address, k, prev, new common.Hash) {
	for _, t := range t.subTracers {
		if t.OnStorageChange != nil {
			t.OnStorageChange(a, k, prev, new)
		}
	}
}

func (t *muxTracer) OnLog(log *types.Log) {
	for _, t := range t.subTracers {
		if t.OnLog != nil {
			t.OnLog(log)
		}
	}
}

// Stop terminates execution of the tracer at the first opportune moment.
func (t *muxTracer) Stop(err error) {
	for _, t := range t.subTracers {
		t.Stop(err)
	}
}
