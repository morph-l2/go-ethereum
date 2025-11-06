package tracing

import (
	"bytes"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/common/hexutil"
	"github.com/morph-l2/go-ethereum/consensus"
	"github.com/morph-l2/go-ethereum/core"
	"github.com/morph-l2/go-ethereum/core/rawdb"
	"github.com/morph-l2/go-ethereum/core/state"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/core/vm"
	"github.com/morph-l2/go-ethereum/crypto/codehash"
	"github.com/morph-l2/go-ethereum/eth/tracers"
	"github.com/morph-l2/go-ethereum/eth/tracers/logger"
	_ "github.com/morph-l2/go-ethereum/eth/tracers/native"
	"github.com/morph-l2/go-ethereum/ethdb"
	"github.com/morph-l2/go-ethereum/log"
	"github.com/morph-l2/go-ethereum/metrics"
	"github.com/morph-l2/go-ethereum/params"
	"github.com/morph-l2/go-ethereum/rollup/fees"
	"github.com/morph-l2/go-ethereum/rollup/rcfg"
	"github.com/morph-l2/go-ethereum/rollup/sequencer"
	"github.com/morph-l2/go-ethereum/rollup/withdrawtrie"
)

var (
	getTxResultTimer             = metrics.NewRegisteredTimer("rollup/tracing/get_tx_result", nil)
	getTxResultApplyMessageTimer = metrics.NewRegisteredTimer("rollup/tracing/get_tx_result/apply_message", nil)
	getTxResultZkTrieBuildTimer  = metrics.NewRegisteredTimer("rollup/tracing/get_tx_result/zk_trie_build", nil)
	getTxResultTracerResultTimer = metrics.NewRegisteredTimer("rollup/tracing/get_tx_result/tracer_result", nil)
	feedTxToTracerTimer          = metrics.NewRegisteredTimer("rollup/tracing/feed_tx_to_tracer", nil)
	fillBlockTraceTimer          = metrics.NewRegisteredTimer("rollup/tracing/fill_block_trace", nil)
)

// TracerWrapper implements MprphTracerWrapper interface
type TracerWrapper struct{}

// NewTracerWrapper TracerWrapper creates a new TracerWrapper
func NewTracerWrapper() *TracerWrapper {
	return &TracerWrapper{}
}

// CreateTraceEnvAndGetBlockTrace wraps the whole block tracing logic for a block
func (tw *TracerWrapper) CreateTraceEnvAndGetBlockTrace(chainConfig *params.ChainConfig, chainContext core.ChainContext, engine consensus.Engine, chaindb ethdb.Database, statedb *state.StateDB, parent *types.Block, block *types.Block, commitAfterApply bool) (*types.BlockTrace, error) {
	traceEnv, err := CreateTraceEnv(chainConfig, chainContext, engine, chaindb, statedb, parent, block, commitAfterApply)
	if err != nil {
		return nil, err
	}

	return traceEnv.GetBlockTrace(block)
}

type TraceEnv struct {
	//logConfig        *vm.LogConfig
	logConfig        *logger.Config
	commitAfterApply bool
	chainConfig      *params.ChainConfig

	coinbase common.Address

	signer   types.Signer
	state    *state.StateDB
	blockCtx vm.BlockContext

	// The following Mutexes are used to protect against parallel read/write,
	// since txs are executed in parallel.
	pMu sync.Mutex // for `TraceEnv.StorageTrace.Proofs`
	sMu sync.Mutex // for `TraceEnv.state``
	cMu sync.Mutex // for `TraceEnv.Codes`

	*types.StorageTrace

	Codes           map[common.Hash]logger.CodeInfo
	TxStorageTraces []*types.StorageTrace
	// zktrie tracer is used for zktrie storage to build additional deletion proof
	ZkTrieTracer     map[string]state.ZktrieProofTracer
	ExecutionResults []*types.ExecutionResult

	// StartL1QueueIndex is the next L1 message queue index that this block can process.
	// Example: If the parent block included QueueIndex=9, then StartL1QueueIndex will
	// be 10.
	StartL1QueueIndex uint64
}

// Context is the same as Context in eth/tracers/tracers.go
type Context struct {
	BlockHash common.Hash
	TxIndex   int
	TxHash    common.Hash
}

// txTraceTask is the same as txTraceTask in eth/tracers/api.go
type txTraceTask struct {
	statedb *state.StateDB
	index   int
}

func CreateTraceEnvHelper(chainConfig *params.ChainConfig, logConfig *logger.Config, blockCtx vm.BlockContext, startL1QueueIndex uint64, coinbase common.Address, statedb *state.StateDB, rootBefore common.Hash, block *types.Block, commitAfterApply bool) *TraceEnv {
	return &TraceEnv{
		logConfig:        logConfig,
		commitAfterApply: commitAfterApply,
		chainConfig:      chainConfig,
		coinbase:         coinbase,
		signer:           types.MakeSigner(chainConfig, block.Number(), block.Time()),
		state:            statedb,
		blockCtx:         blockCtx,
		StorageTrace: &types.StorageTrace{
			RootBefore:    rootBefore,
			RootAfter:     block.Root(),
			Proofs:        make(map[string][]hexutil.Bytes),
			StorageProofs: make(map[string]map[string][]hexutil.Bytes),
		},
		Codes:             make(map[common.Hash]logger.CodeInfo),
		ZkTrieTracer:      make(map[string]state.ZktrieProofTracer),
		ExecutionResults:  make([]*types.ExecutionResult, block.Transactions().Len()),
		TxStorageTraces:   make([]*types.StorageTrace, block.Transactions().Len()),
		StartL1QueueIndex: startL1QueueIndex,
	}
}

func CreateTraceEnv(chainConfig *params.ChainConfig, chainContext core.ChainContext, engine consensus.Engine, chaindb ethdb.Database, statedb *state.StateDB, parent *types.Block, block *types.Block, commitAfterApply bool) (*TraceEnv, error) {
	var coinbase common.Address

	var err error
	if chainConfig.Morph.FeeVaultEnabled() {
		coinbase = *chainConfig.Morph.FeeVaultAddress
	} else {
		coinbase, err = engine.Author(block.Header())
		if err != nil {
			log.Warn("recover coinbase in CreateTraceEnv fail. using zero-address", "err", err, "blockNumber", block.Header().Number, "headerHash", block.Header().Hash())
		}
	}

	// Collect start queue index, we should always have this value for blocks
	// that have been executed.
	startL1QueueIndex := rawdb.ReadFirstQueueIndexNotInL2Block(chaindb, parent.Hash())
	if startL1QueueIndex == nil {
		log.Error("missing FirstQueueIndexNotInL2Block for block during trace call", "number", parent.NumberU64(), "hash", parent.Hash())
		return nil, fmt.Errorf("missing FirstQueueIndexNotInL2Block for block during trace call: hash=%v, parentHash=%vv", block.Hash(), parent.Hash())
	}
	env := CreateTraceEnvHelper(
		chainConfig,
		&logger.Config{
			DisableStorage:   true,
			DisableStack:     true,
			EnableMemory:     false,
			EnableReturnData: true,
		},
		core.NewEVMBlockContext(block.Header(), chainContext, chainConfig, nil),
		*startL1QueueIndex,
		coinbase,
		statedb,
		parent.Root(),
		block,
		commitAfterApply,
	)

	key := coinbase.String()
	if _, exist := env.Proofs[key]; !exist {
		proof, err := env.state.GetProof(coinbase)
		if err != nil {
			log.Error("Proof for coinbase not available", "coinbase", coinbase, "error", err)
			// but we still mark the proofs map with nil array
		}
		env.Proofs[key] = types.WrapProof(proof)
	}

	return env, nil
}

func (env *TraceEnv) GetBlockTrace(block *types.Block) (*types.BlockTrace, error) {
	// Execute all the transaction contained within the block concurrently
	var (
		txs   = block.Transactions()
		pend  = new(sync.WaitGroup)
		jobs  = make(chan *txTraceTask, len(txs))
		errCh = make(chan error, 1)
	)
	threads := runtime.NumCPU()
	if threads > len(txs) {
		threads = len(txs)
	}
	for th := 0; th < threads; th++ {
		pend.Add(1)
		go func() {
			defer func(t time.Time) {
				pend.Done()
				getTxResultTimer.Update(time.Since(t))
			}(time.Now())

			// Fetch and execute the next transaction trace tasks
			for task := range jobs {
				if err := env.getTxResult(task.statedb, task.index, block); err != nil {
					select {
					case errCh <- err:
					default:
					}
					log.Error(
						"failed to trace tx",
						"txHash", txs[task.index].Hash().String(),
						"blockHash", block.Hash().String(),
						"blockNumber", block.NumberU64(),
						"err", err,
					)
				}
			}
		}()
	}

	// Feed the transactions into the tracers and return
	var failed error
	common.WithTimer(feedTxToTracerTimer, func() {
		for i, tx := range txs {
			// Send the trace task over for execution
			jobs <- &txTraceTask{statedb: env.state.Copy(), index: i}

			// Generate the next state snapshot fast without tracing
			msg, _ := tx.AsMessage(env.signer, block.BaseFee())
			env.state.SetTxContext(tx.Hash(), i)
			vmenv := vm.NewEVM(env.blockCtx, core.NewEVMTxContext(msg), env.state, env.chainConfig, vm.Config{})
			l1DataFee, err := fees.CalculateL1DataFee(tx, env.state, env.chainConfig, block.Number())
			if err != nil {
				failed = err
				break
			}
			if _, err = core.ApplyMessage(vmenv, msg, new(core.GasPool).AddGas(msg.Gas()), l1DataFee); err != nil {
				failed = err
				break
			}
			if env.commitAfterApply {
				env.state.Finalise(vmenv.ChainConfig().IsEIP158(block.Number()))
			}
		}
	})
	close(jobs)
	pend.Wait()

	// after all tx has been traced, collect "deletion proof" for zktrie
	for _, tracer := range env.ZkTrieTracer {
		delProofs, err := tracer.GetDeletionProofs()
		if err != nil {
			log.Error("deletion proof failure", "error", err)
		} else {
			for _, proof := range delProofs {
				env.DeletionProofs = append(env.DeletionProofs, proof)
			}
		}
	}

	// build dummy per-tx deletion proof
	for _, txStorageTrace := range env.TxStorageTraces {
		if txStorageTrace != nil {
			txStorageTrace.DeletionProofs = env.DeletionProofs
		}
	}

	// If execution failed in between, abort
	select {
	case err := <-errCh:
		return nil, err
	default:
		if failed != nil {
			return nil, failed
		}
	}

	return env.fillBlockTrace(block)
}

func (env *TraceEnv) getTxResult(statedb *state.StateDB, index int, block *types.Block) error {
	tx := block.Transactions()[index]
	msg, _ := tx.AsMessage(env.signer, block.BaseFee())
	from, _ := types.Sender(env.signer, tx)
	to := tx.To()

	txctx := &Context{
		BlockHash: block.TxHash(),
		TxIndex:   index,
		TxHash:    tx.Hash(),
	}

	sender := &types.AccountWrapper{
		Address:          from,
		Nonce:            statedb.GetNonce(from),
		Balance:          (*hexutil.Big)(statedb.GetBalance(from)),
		KeccakCodeHash:   statedb.GetKeccakCodeHash(from),
		PoseidonCodeHash: statedb.GetPoseidonCodeHash(from),
		CodeSize:         statedb.GetCodeSize(from),
	}
	var receiver *types.AccountWrapper
	if to != nil {
		receiver = &types.AccountWrapper{
			Address:          *to,
			Nonce:            statedb.GetNonce(*to),
			Balance:          (*hexutil.Big)(statedb.GetBalance(*to)),
			KeccakCodeHash:   statedb.GetKeccakCodeHash(*to),
			PoseidonCodeHash: statedb.GetPoseidonCodeHash(*to),
			CodeSize:         statedb.GetCodeSize(*to),
		}
	}

	txContext := core.NewEVMTxContext(msg)
	tracerContext := tracers.Context{
		BlockHash: block.Hash(),
		TxIndex:   index,
		TxHash:    tx.Hash(),
	}
	callTracer, err := tracers.DefaultDirectory.New("callTracer", &tracerContext, nil, env.chainConfig) // warm up the tracer
	if err != nil {
		return fmt.Errorf("failed to create callTracer: %w", err)
	}

	applyMessageStart := time.Now()
	structLogger := logger.NewStructLogger(env.logConfig)
	tracer := NewMuxTracer(structLogger, *callTracer)

	tracingStateDB := state.NewHookedState(statedb, tracer.Hooks)

	// Run the transaction with tracing enabled.
	vmenv := vm.NewEVM(env.blockCtx, txContext, tracingStateDB, env.chainConfig, vm.Config{Tracer: tracer.Hooks, NoBaseFee: true})

	// Call Prepare to clear out the statedb access list
	statedb.SetTxContext(txctx.TxHash, txctx.TxIndex)

	receipt, err := core.ApplyTransactionWithEVM(msg, env.chainConfig, new(core.GasPool).AddGas(msg.Gas()), statedb, block.Number(), block.Hash(), tx, new(uint64), vmenv)
	if err != nil {
		getTxResultApplyMessageTimer.UpdateSince(applyMessageStart)
		return err
	}
	getTxResultApplyMessageTimer.UpdateSince(applyMessageStart)

	createdAcc := structLogger.CreatedAccount()
	var after []*types.AccountWrapper
	if to == nil {
		if createdAcc == nil {
			return errors.New("unexpected tx: address for created contract unavailable")
		}
		to = &createdAcc.Address
	}
	// collect affected account after tx being applied
	for _, acc := range []common.Address{from, *to, env.coinbase} {
		after = append(after, &types.AccountWrapper{
			Address:          acc,
			Nonce:            statedb.GetNonce(acc),
			Balance:          (*hexutil.Big)(statedb.GetBalance(acc)),
			KeccakCodeHash:   statedb.GetKeccakCodeHash(acc),
			PoseidonCodeHash: statedb.GetPoseidonCodeHash(acc),
			CodeSize:         statedb.GetCodeSize(acc),
		})
	}

	txStorageTrace := &types.StorageTrace{
		Proofs:        make(map[string][]hexutil.Bytes),
		StorageProofs: make(map[string]map[string][]hexutil.Bytes),
	}
	// still we have no state root for per tx, only set the head and tail
	if index == 0 {
		txStorageTrace.RootBefore = statedb.GetRootHash()
	}
	if index == len(block.Transactions())-1 {
		txStorageTrace.RootAfter = block.Root()
	}

	// merge bytecodes
	env.cMu.Lock()
	for codeHash, codeInfo := range structLogger.TracedBytecodes() {
		if codeHash != (common.Hash{}) {
			env.Codes[codeHash] = codeInfo
		}
	}
	env.cMu.Unlock()

	// merge required proof data
	proofAccounts := structLogger.UpdatedAccounts()
	proofAccounts[vmenv.FeeRecipient()] = struct{}{}
	// add from/to address if it does not exist
	if _, ok := proofAccounts[from]; !ok {
		proofAccounts[from] = struct{}{}
	}
	if _, ok := proofAccounts[*to]; !ok {
		proofAccounts[*to] = struct{}{}
	}
	for addr := range proofAccounts {
		addrStr := addr.String()

		env.pMu.Lock()
		checkedProof, existed := env.Proofs[addrStr]
		if existed {
			txStorageTrace.Proofs[addrStr] = checkedProof
		}
		env.pMu.Unlock()
		if existed {
			continue
		}
		proof, err := statedb.GetProof(addr)
		if err != nil {
			log.Error("Proof not available", "address", addrStr, "error", err)
			// but we still mark the proofs map with nil array
		}
		wrappedProof := types.WrapProof(proof)
		env.pMu.Lock()
		env.Proofs[addrStr] = wrappedProof
		txStorageTrace.Proofs[addrStr] = wrappedProof
		env.pMu.Unlock()
	}

	zkTrieBuildStart := time.Now()
	proofStorages := structLogger.UpdatedStorages()
	for addr, keys := range proofStorages {
		if _, existed := txStorageTrace.StorageProofs[addr.String()]; !existed {
			txStorageTrace.StorageProofs[addr.String()] = make(map[string][]hexutil.Bytes)
		}

		env.sMu.Lock()
		trie, err := statedb.GetStorageTrieForProof(addr)
		if err != nil {
			// but we still continue to next address
			log.Error("Storage trie not available", "error", err, "address", addr)
			env.sMu.Unlock()
			continue
		}
		zktrieTracer := statedb.NewProofTracer(trie)
		env.sMu.Unlock()

		for key := range keys {
			addrStr := addr.String()
			keyStr := key.String()
			value := statedb.GetState(addr, key)
			isDelete := bytes.Equal(value.Bytes(), common.Hash{}.Bytes())

			txm := txStorageTrace.StorageProofs[addrStr]
			env.sMu.Lock()
			m, existed := env.StorageProofs[addrStr]
			if !existed {
				m = make(map[string][]hexutil.Bytes)
				env.StorageProofs[addrStr] = m
			}
			if zktrieTracer.Available() && !env.ZkTrieTracer[addrStr].Available() {
				env.ZkTrieTracer[addrStr] = statedb.NewProofTracer(trie)
			}

			if proof, existed := m[keyStr]; existed {
				txm[keyStr] = proof
				// still need to touch tracer for deletion
				if isDelete && zktrieTracer.Available() {
					env.ZkTrieTracer[addrStr].MarkDeletion(key)
				}
				env.sMu.Unlock()
				continue
			}
			env.sMu.Unlock()

			var proof [][]byte
			var err error
			if zktrieTracer.Available() {
				proof, err = statedb.GetSecureTrieProof(zktrieTracer, key)
			} else {
				proof, err = statedb.GetSecureTrieProof(trie, key)
			}
			if err != nil {
				log.Error("Storage proof not available", "error", err, "address", addrStr, "key", keyStr)
				// but we still mark the proofs map with nil array
			}
			wrappedProof := types.WrapProof(proof)
			env.sMu.Lock()
			txm[keyStr] = wrappedProof
			m[keyStr] = wrappedProof
			if zktrieTracer.Available() {
				if isDelete {
					zktrieTracer.MarkDeletion(key)
				}
				env.ZkTrieTracer[addrStr].Merge(zktrieTracer)
			}
			env.sMu.Unlock()
		}
	}
	getTxResultZkTrieBuildTimer.UpdateSince(zkTrieBuildStart)

	tracerResultTimer := time.Now()
	callTrace, err := callTracer.GetResult()
	if err != nil {
		return fmt.Errorf("failed to get callTracer result: %w", err)
	}
	getTxResultTracerResultTimer.UpdateSince(tracerResultTimer)

	env.ExecutionResults[index] = &types.ExecutionResult{
		From:           sender,
		To:             receiver,
		AccountCreated: createdAcc,
		AccountsAfter:  after,
		L1DataFee:      (*hexutil.Big)(receipt.L1Fee),
		FeeTokenID:     receipt.FeeTokenID,
		FeeLimit:       (*hexutil.Big)(receipt.FeeLimit),
		FeeRate:        (*hexutil.Big)(receipt.FeeRate),
		TokenScale:     (*hexutil.Big)(receipt.TokenScale),
		Gas:            receipt.GasUsed,
		Failed:         receipt.Status == types.ReceiptStatusFailed,
		ReturnValue:    fmt.Sprintf("%x", receipt.ReturnValue),
		StructLogs:     logger.FormatLogs(structLogger.StructLogs()),
		CallTrace:      callTrace,
	}
	env.TxStorageTraces[index] = txStorageTrace

	return nil
}

// fillBlockTrace content after all the txs are finished running.
func (env *TraceEnv) fillBlockTrace(block *types.Block) (*types.BlockTrace, error) {
	defer func(t time.Time) {
		fillBlockTraceTimer.Update(time.Since(t))
	}(time.Now())

	statedb := env.state

	txs := make([]*types.TransactionData, block.Transactions().Len())
	for i, tx := range block.Transactions() {
		txs[i] = types.NewTransactionData(tx, block.NumberU64(), block.Time(), env.chainConfig)
	}

	intrinsicStorageProofs := map[common.Address][]common.Hash{
		rcfg.L2MessageQueueAddress: {rcfg.WithdrawTrieRootSlot},
		rcfg.L1GasPriceOracleAddress: {
			rcfg.L1BaseFeeSlot,
			rcfg.OverheadSlot,
			rcfg.ScalarSlot,
			rcfg.L1BlobBaseFeeSlot,
			rcfg.CommitScalarSlot,
			rcfg.BlobScalarSlot,
			rcfg.IsCurieSlot,
		},
		rcfg.SequencerAddress: {rcfg.SequencerSetVerifyHashSlot},
		// rcfg.L2ERC20RegistryAddress: {}, // TODO
	}

	for addr, storages := range intrinsicStorageProofs {
		if _, existed := env.Proofs[addr.String()]; !existed {
			if proof, err := statedb.GetProof(addr); err != nil {
				log.Error("Proof for intrinstic address not available", "error", err, "address", addr)
			} else {
				env.Proofs[addr.String()] = types.WrapProof(proof)
			}
		}

		if _, existed := env.StorageProofs[addr.String()]; !existed {
			env.StorageProofs[addr.String()] = make(map[string][]hexutil.Bytes)
		}

		for _, slot := range storages {
			if _, existed := env.StorageProofs[addr.String()][slot.String()]; !existed {
				if trie, err := statedb.GetStorageTrieForProof(addr); err != nil {
					log.Error("Storage proof for intrinstic address not available", "error", err, "address", addr)
				} else if proof, err := statedb.GetSecureTrieProof(trie, slot); err != nil {
					log.Error("Get storage proof for intrinstic address failed", "error", err, "address", addr, "slot", slot)
				} else {
					env.StorageProofs[addr.String()][slot.String()] = types.WrapProof(proof)
				}
			}
		}
	}

	var chainID uint64
	if env.chainConfig.ChainID != nil {
		chainID = env.chainConfig.ChainID.Uint64()
	}
	blockTrace := &types.BlockTrace{
		ChainID: chainID,
		Version: params.ArchiveVersion(params.CommitHash),
		Coinbase: &types.AccountWrapper{
			Address:          env.coinbase,
			Nonce:            statedb.GetNonce(env.coinbase),
			Balance:          (*hexutil.Big)(statedb.GetBalance(env.coinbase)),
			KeccakCodeHash:   statedb.GetKeccakCodeHash(env.coinbase),
			PoseidonCodeHash: statedb.GetPoseidonCodeHash(env.coinbase),
			CodeSize:         statedb.GetCodeSize(env.coinbase),
		},
		Header:            block.Header(),
		Bytecodes:         make([]*types.BytecodeTrace, 0, len(env.Codes)),
		StorageTrace:      env.StorageTrace,
		ExecutionResults:  env.ExecutionResults,
		TxStorageTraces:   env.TxStorageTraces,
		Transactions:      txs,
		StartL1QueueIndex: env.StartL1QueueIndex,
	}

	blockTrace.Bytecodes = append(blockTrace.Bytecodes, &types.BytecodeTrace{
		CodeSize:         0,
		KeccakCodeHash:   codehash.EmptyKeccakCodeHash,
		PoseidonCodeHash: codehash.EmptyPoseidonCodeHash,
		Code:             hexutil.Bytes{},
	})
	for _, codeInfo := range env.Codes {
		blockTrace.Bytecodes = append(blockTrace.Bytecodes, &types.BytecodeTrace{
			CodeSize:         codeInfo.CodeSize,
			KeccakCodeHash:   codeInfo.KeccakCodeHash,
			PoseidonCodeHash: codeInfo.PoseidonCodeHash,
			Code:             codeInfo.Code,
		})
	}

	blockTrace.WithdrawTrieRoot = withdrawtrie.ReadWTRSlot(rcfg.L2MessageQueueAddress, env.state)
	blockTrace.SequencerSetVerifyHash = sequencer.ReadVerifyHashSlot(rcfg.SequencerAddress, env.state)

	return blockTrace, nil
}
