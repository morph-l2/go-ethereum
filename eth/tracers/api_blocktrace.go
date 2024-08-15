package tracers

import (
	"context"
	"errors"

	"github.com/morph-l2/go-ethereum/consensus"
	"github.com/morph-l2/go-ethereum/core"
	"github.com/morph-l2/go-ethereum/core/state"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/log"
	"github.com/morph-l2/go-ethereum/params"
	"github.com/morph-l2/go-ethereum/rpc"
)

var errNoMorphTracerWrapper = errors.New("no MorphTracerWrapper")

type TraceBlock interface {
	GetBlockTraceByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash, config *TraceConfig) (trace *types.BlockTrace, err error)
	GetTxBlockTraceOnTopOfBlock(ctx context.Context, tx *types.Transaction, blockNrOrHash rpc.BlockNumberOrHash, config *TraceConfig) (*types.BlockTrace, error)
}

type morphTracerWrapper interface {
	CreateTraceEnvAndGetBlockTrace(*params.ChainConfig, core.ChainContext, consensus.Engine, ethdb.Database, *state.StateDB, *types.Block, *types.Block, bool) (*types.BlockTrace, error)
}

// GetBlockTraceByNumberOrHash replays the block and returns the structured BlockTrace by hash or number.
func (api *API) GetBlockTraceByNumberOrHash(ctx context.Context, blockNrOrHash rpc.BlockNumberOrHash, config *TraceConfig) (trace *types.BlockTrace, err error) {
	if api.morphTracerWrapper == nil {
		return nil, errNoMorphTracerWrapper
	}

	var block *types.Block
	if number, ok := blockNrOrHash.Number(); ok {
		block, err = api.blockByNumber(ctx, number)
	} else if hash, ok := blockNrOrHash.Hash(); ok {
		block, err = api.blockByHash(ctx, hash)
	} else {
		return nil, errors.New("invalid arguments; neither block number nor hash specified")
	}
	if err != nil {
		return nil, err
	}
	if block.NumberU64() == 0 {
		return nil, errors.New("genesis is not traceable")
	}

	return api.createTraceEnvAndGetBlockTrace(ctx, config, block)
}

func (api *API) GetTxBlockTraceOnTopOfBlock(ctx context.Context, tx *types.Transaction, blockNrOrHash rpc.BlockNumberOrHash, config *TraceConfig) (*types.BlockTrace, error) {
	if api.morphTracerWrapper == nil {
		return nil, errNoMorphTracerWrapper
	}

	// Try to retrieve the specified block
	var (
		err   error
		block *types.Block
	)
	if number, ok := blockNrOrHash.Number(); ok {
		block, err = api.blockByNumber(ctx, number)
	} else if hash, ok := blockNrOrHash.Hash(); ok {
		block, err = api.blockByHash(ctx, hash)
	} else {
		return nil, errors.New("invalid arguments; neither block number nor hash specified")
	}
	if err != nil {
		return nil, err
	}
	if block.NumberU64() == 0 {
		return nil, errors.New("genesis is not traceable")
	}

	block = types.NewBlockWithHeader(block.Header()).WithBody([]*types.Transaction{tx}, nil)

	return api.createTraceEnvAndGetBlockTrace(ctx, config, block)
}

// Make trace environment for current block, and then get the trace for the block.
func (api *API) createTraceEnvAndGetBlockTrace(ctx context.Context, config *TraceConfig, block *types.Block) (*types.BlockTrace, error) {
	if config == nil {
		config = &TraceConfig{
			LogConfig: &vm.LogConfig{
				DisableStorage:   true,
				DisableStack:     true,
				EnableMemory:     false,
				EnableReturnData: true,
			},
		}
	} else if config.Tracer != nil {
		config.Tracer = nil
		log.Warn("Tracer params is unsupported")
	}

	parent, err := api.blockByNumberAndHash(ctx, rpc.BlockNumber(block.NumberU64()-1), block.ParentHash())
	if err != nil {
		return nil, err
	}
	reexec := defaultTraceReexec
	if config != nil && config.Reexec != nil {
		reexec = *config.Reexec
	}
	statedb, err := api.backend.StateAtBlock(ctx, parent, reexec, nil, true, true)
	if err != nil {
		return nil, err
	}

	chaindb := api.backend.ChainDb()
	return api.morphTracerWrapper.CreateTraceEnvAndGetBlockTrace(api.backend.ChainConfig(), api.chainContext(ctx), api.backend.Engine(), chaindb, statedb, parent, block, true)
}
