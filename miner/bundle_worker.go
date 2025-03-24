package miner

import (
	"errors"
	"math/big"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core"
	"github.com/morph-l2/go-ethereum/core/state"
	"github.com/morph-l2/go-ethereum/core/types"
	"github.com/morph-l2/go-ethereum/log"
	"github.com/morph-l2/go-ethereum/params"
	"github.com/morph-l2/go-ethereum/rollup/fees"
)

var (
	errNonRevertingTxInBundleFailed = errors.New("non-reverting tx in bundle failed")
	errBundlePriceTooLow            = errors.New("bundle price too low")
)

// fillTransactions retrieves the pending bundles and transactions from the txpool and fills them
// into the given sealing block. The selection and ordering strategy can be extended in the future.
func (miner *Miner) fillTransactionsAndBundles(env *environment, l1Transactions types.Transactions, interrupt *int32) error {
	// TODO will remove after fix txpool perf issue
	if interrupt != nil {
		if signal := atomic.LoadInt32(interrupt); signal != commitInterruptNone {
			log.Warn("fill bundles interrupted by signal")
			return errFillBundleInterrupted
		}
	}

	bundles := miner.txpool.PendingBundles(env.header.Number.Uint64(), env.header.Time)
	// if no bundles, not necessary to fill transactions
	if len(bundles) == 0 {
		log.Warn("no bundles in bundle pool")
		return errFillBundleInterrupted
	}

	txs, _, err := miner.generateOrderedBundles(env, bundles)
	if err != nil {
		log.Error("fail to generate ordered bundles", "err", err)
		return errFillBundleInterrupted
	}

	if err = miner.commitBundles(env, txs, interrupt); err != nil {
		log.Error("fail to commit bundles", "err", err)
		return errFillBundleInterrupted
	}
	log.Info("fill bundles", "bundles_count", len(bundles))

	// fill mempool's transactions
	start := time.Now()
	err = miner.fillTransactions(env, l1Transactions, interrupt)
	if err != nil {
		return err
	}
	log.Debug("commitTxpoolTxsTimer", "duration", common.PrettyDuration(time.Since(start)), "hash", env.header.Hash())

	log.Info("fill bundles and transactions done", "total_txs_count", len(env.txs))
	return nil
}

func (miner *Miner) commitBundles(
	env *environment,
	txs types.Transactions,
	interrupt *int32,
) error {
	gasLimit := prepareGasPool()
	if env.gasPool == nil {
		env.gasPool = new(core.GasPool).AddGas(gasLimit.Gas())
	}

	for _, tx := range txs {
		if interrupt != nil {
			if signal := atomic.LoadInt32(interrupt); signal != commitInterruptNone {
				return errors.New("failed bundle commit due to payload timeout or resolve")
			}
		}
		// If we don't have enough gas for any further transactions then we're done.
		if env.gasPool.Gas() < params.TxGas {
			return errors.New("not enough gas for further transactions")
		}
		if tx == nil {
			return errors.New("unexpected nil transaction in bundle")
		}
		// Error may be ignored here. The error has already been checked
		// during transaction acceptance is the transaction pool.
		//
		// We use the eip155 signer regardless of the current hf.
		from, _ := types.Sender(env.signer, tx)
		// Check whether the tx is replay protected. If we're not in the EIP155 hf
		// phase, start ignoring the sender until we do.
		if tx.Protected() && !miner.chainConfig.IsEIP155(env.header.Number) {
			return errors.New("unexpected protected transaction in bundle")
		}
		// Start executing the transaction
		env.state.SetTxContext(tx.Hash(), env.tcount)

		_, err := miner.commitBundleTransaction(env, tx, env.header.Coinbase, env.UnRevertible.Contains(tx.Hash()))
		switch err {
		case core.ErrGasLimitReached:
			// Pop the current out-of-gas transaction without shifting in the next from the account
			log.Error("Unexpected gas limit exceeded for current block in the bundle", "sender", from)
			return signalToErr(commitInterruptBundleCommit)

		case core.ErrNonceTooLow:
			// New head notification data race between the transaction pool and miner, shift
			log.Error("Transaction with low nonce in the bundle", "sender", from, "nonce", tx.Nonce())
			return signalToErr(commitInterruptBundleCommit)

		case core.ErrNonceTooHigh:
			// Reorg notification data race between the transaction pool and miner, skip account =
			log.Error("Account with high nonce in the bundle", "sender", from, "nonce", tx.Nonce())
			return signalToErr(commitInterruptBundleCommit)

		case nil:
			// Everything ok, collect the logs and shift in the next transaction from the same account
			env.tcount++
			continue

		default:
			// Strange error, discard the transaction and get the next in line (note, the
			// nonce-too-high clause will prevent us from executing in vain).
			log.Error("Transaction failed in the bundle", "hash", tx.Hash(), "err", err)
			return signalToErr(commitInterruptBundleCommit)
		}
	}

	return nil
}

// generateOrderedBundles generates ordered txs from the given bundles.
// 1. sort bundles according to computed gas price when received.
// 2. simulate bundles based on the same state, resort.
// 3. merge resorted simulateBundles based on the iterative state.
func (miner *Miner) generateOrderedBundles(
	env *environment,
	bundles []*types.Bundle,
) (types.Transactions, *types.SimulatedBundle, error) {
	// sort bundles according to gas price computed when received
	slices.SortStableFunc(bundles, func(i, j *types.Bundle) int {
		priceI, priceJ := i.Price, j.Price
		return priceJ.Cmp(priceI)
	})

	// recompute bundle gas price based on the same state and current env
	simulatedBundles, err := miner.simulateBundles(env, bundles)
	if err != nil {
		log.Error("fail to simulate bundles base on the same state", "err", err)
		return nil, nil, err
	}

	// sort bundles according to fresh gas price
	slices.SortStableFunc(simulatedBundles, func(i, j *types.SimulatedBundle) int {
		priceI, priceJ := i.BundleGasPrice, j.BundleGasPrice
		return priceJ.Cmp(priceI)
	})

	// merge bundles based on iterative state
	includedTxs, mergedBundle, err := miner.mergeBundles(env, simulatedBundles)
	if err != nil {
		log.Error("fail to merge bundles", "err", err)
		return nil, nil, err
	}

	return includedTxs, mergedBundle, nil
}

func (miner *Miner) simulateBundles(env *environment, bundles []*types.Bundle) ([]*types.SimulatedBundle, error) {
	headerHash := env.header.Hash()
	simCache := miner.bundleCache.GetBundleCache(headerHash)
	simResult := make(map[common.Hash]*types.SimulatedBundle)

	var wg sync.WaitGroup
	var mu sync.Mutex
	for i, bundle := range bundles {
		if simmed, ok := simCache.GetSimulatedBundle(bundle.Hash()); ok {
			mu.Lock()
			simResult[bundle.Hash()] = simmed
			mu.Unlock()
			continue
		}

		wg.Add(1)
		go func(idx int, bundle *types.Bundle, state *state.StateDB) {
			defer wg.Done()
			gasPool := prepareGasPool()
			simmed, err := miner.simulateBundle(env, bundle, state, gasPool, 0, true, true)
			if err != nil {
				log.Trace("Error computing gas for a simulateBundle", "error", err)
				return
			}

			mu.Lock()
			defer mu.Unlock()
			simResult[bundle.Hash()] = simmed
		}(i, bundle, env.state.Copy())
	}

	wg.Wait()

	simulatedBundles := make([]*types.SimulatedBundle, 0)

	for _, bundle := range simResult {
		if bundle == nil {
			continue
		}

		simulatedBundles = append(simulatedBundles, bundle)
	}

	simCache.UpdateSimulatedBundles(simResult, bundles)

	return simulatedBundles, nil
}

// mergeBundles merges the given simulateBundle into the given environment.
// It returns the merged simulateBundle and the number of transactions that were merged.
func (miner *Miner) mergeBundles(
	env *environment,
	bundles []*types.SimulatedBundle,
) (types.Transactions, *types.SimulatedBundle, error) {
	currentState := env.state.Copy()
	gasPool := prepareGasPool()
	env.UnRevertible = mapset.NewSet[common.Hash]()

	includedTxs := types.Transactions{}
	mergedBundle := types.SimulatedBundle{
		BundleGasFees:  new(big.Int),
		BundleGasUsed:  0,
		BundleGasPrice: new(big.Int),
	}

	for _, bundle := range bundles {
		prevState := currentState.Copy()
		prevGasPool := new(core.GasPool).AddGas(gasPool.Gas())

		// the floor gas price is 99/100 what was simulated at the top of the block
		floorGasPrice := new(big.Int).Mul(bundle.BundleGasPrice, big.NewInt(99))
		floorGasPrice = floorGasPrice.Div(floorGasPrice, big.NewInt(100))

		simulatedBundle, err := miner.simulateBundle(env, bundle.OriginalBundle, currentState, gasPool, len(includedTxs), true, false)

		if err != nil && errors.Is(err, core.ErrGasLimitReached) {
			log.Error("failed to merge bundle, interrupt merge process", "err", err)
			break
		}
		if err != nil || simulatedBundle.BundleGasPrice.Cmp(floorGasPrice) <= 0 {
			currentState = prevState
			gasPool = prevGasPool

			log.Error("failed to merge bundle", "floorGasPrice", floorGasPrice, "err", err)
			continue
		}

		log.Info("included bundle",
			"gasUsed", simulatedBundle.BundleGasUsed,
			"gasPrice", simulatedBundle.BundleGasPrice,
			"txcount", len(simulatedBundle.OriginalBundle.Txs))

		includedTxs = append(includedTxs, bundle.OriginalBundle.Txs...)

		mergedBundle.BundleGasFees.Add(mergedBundle.BundleGasFees, simulatedBundle.BundleGasFees)
		mergedBundle.BundleGasUsed += simulatedBundle.BundleGasUsed

		for _, tx := range includedTxs {
			if !containsHash(bundle.OriginalBundle.RevertingTxHashes, tx.Hash()) {
				env.UnRevertible.Add(tx.Hash())
			}
		}
	}

	if len(includedTxs) == 0 {
		return nil, nil, errors.New("include no txs when merge bundles")
	}

	mergedBundle.BundleGasPrice.Div(mergedBundle.BundleGasFees, new(big.Int).SetUint64(mergedBundle.BundleGasUsed))

	return includedTxs, &mergedBundle, nil
}

// simulateBundle computes the gas price for a whole simulateBundle based on the same ctx
// named computeBundleGas in flashbots
func (miner *Miner) simulateBundle(
	env *environment, bundle *types.Bundle, state *state.StateDB, gasPool *core.GasPool, currentTxCount int,
	prune, pruneGasExceed bool,
) (*types.SimulatedBundle, error) {
	var (
		tempGasUsed   uint64
		bundleGasUsed uint64
		bundleGasFees = new(big.Int)
		// l1DataFees is the total l1DataFee of all zero tip txs in the bundle
		l1DataFees = new(big.Int)
	)

	for i, tx := range bundle.Txs {
		state.SetTxContext(tx.Hash(), i+currentTxCount)

		receipt, err := core.ApplyTransaction(miner.chainConfig, miner.chain, &env.header.Coinbase, env.gasPool, env.state, env.header, tx, &tempGasUsed, *miner.chain.GetVMConfig())
		if err != nil {
			log.Warn("fail to simulate bundle", "hash", bundle.Hash().String(), "err", err)

			if prune {
				if errors.Is(err, core.ErrGasLimitReached) && !pruneGasExceed {
					log.Warn("bundle gas limit exceed", "hash", bundle.Hash().String())
				} else {
					log.Warn("prune bundle", "hash", bundle.Hash().String(), "err", err)
					miner.txpool.PruneBundle(bundle.Hash())
				}
			}

			return nil, err
		}

		if receipt.Status == types.ReceiptStatusFailed && !containsHash(bundle.RevertingTxHashes, receipt.TxHash) {
			err = errNonRevertingTxInBundleFailed
			log.Warn("fail to simulate bundle", "hash", bundle.Hash().String(), "err", err)

			if prune {
				miner.txpool.PruneBundle(bundle.Hash())
				log.Warn("prune bundle", "hash", bundle.Hash().String())
			}

			return nil, err
		}
		if !miner.txpool.Has(tx.Hash()) {
			bundleGasUsed += receipt.GasUsed

			txGasUsed := new(big.Int).SetUint64(receipt.GasUsed)
			effectiveTip, er := tx.EffectiveGasTip(env.header.BaseFee)
			if er != nil {
				return nil, er
			}
			if env.header.BaseFee != nil {
				effectiveTip.Add(effectiveTip, env.header.BaseFee)
			}
			txGasFees := new(big.Int).Mul(txGasUsed, effectiveTip)
			bundleGasFees.Add(bundleGasFees, txGasFees)

			// if the tx is not from txpool, we need to calculate l1DataFee
			if tx.GasPrice().Cmp(big.NewInt(0)) == 0 && effectiveTip.Cmp(big.NewInt(0)) == 0 {
				l1DataFee, err := fees.CalculateL1DataFee(tx, state, miner.chainConfig, env.header.Number)
				if err != nil {
					return nil, err
				}
				l1DataFees.Add(l1DataFees, l1DataFee)
			}
		}
	}
	// if all txs in the bundle are from txpool, we accept the bundle without checking gas price
	bundleGasPrice := big.NewInt(0)
	if bundleGasUsed != 0 {
		if l1DataFees.Cmp(bundleGasFees) > 0 {
			return nil, errors.New("l1DataFees should not be greater than bundleGasFees")
		}

		bundleGasFees.Sub(bundleGasFees, l1DataFees)
		bundleGasPrice = new(big.Int).Div(bundleGasFees, new(big.Int).SetUint64(bundleGasUsed))
	}

	if bundleGasPrice.Cmp(big.NewInt(miner.config.Mev.MevBundleGasPriceFloor)) < 0 {
		err := errBundlePriceTooLow
		log.Warn("fail to simulate bundle", "hash", bundle.Hash().String(), "err", err)

		if prune {
			log.Warn("prune bundle", "hash", bundle.Hash().String())
			miner.txpool.PruneBundle(bundle.Hash())
		}

		return nil, err
	}

	return &types.SimulatedBundle{
		OriginalBundle: bundle,
		BundleGasFees:  bundleGasFees,
		BundleGasPrice: bundleGasPrice,
		BundleGasUsed:  bundleGasUsed,
	}, nil
}

func (miner *Miner) simulateGaslessBundle(env *environment, bundle *types.Bundle) (*types.SimulateGaslessBundleResp, error) {
	result := make([]types.GaslessTxSimResult, 0)

	txIdx := 0
	for _, tx := range bundle.Txs {
		env.state.SetTxContext(tx.Hash(), txIdx)

		var (
			snap = env.state.Snapshot()
			gp   = env.gasPool.Gas()
		)

		receipt, err := core.ApplyTransaction(miner.chainConfig, miner.chain, &env.header.Coinbase, env.gasPool, env.state, env.header, tx, &env.header.GasUsed, *miner.chain.GetVMConfig())
		if err != nil {
			env.state.RevertToSnapshot(snap)
			env.gasPool.SetGas(gp)
			log.Info("fail to simulate gasless bundle, skipped", "txHash", tx.Hash(), "err", err)
		} else {
			txIdx++
			result = append(result, types.GaslessTxSimResult{
				Hash:    tx.Hash(),
				GasUsed: receipt.GasUsed,
			})
		}
	}

	return &types.SimulateGaslessBundleResp{
		ValidResults:     result,
		BasedBlockNumber: env.header.Number.Int64(),
	}, nil
}

func containsHash(arr []common.Hash, match common.Hash) bool {
	for _, elem := range arr {
		if elem == match {
			return true
		}
	}
	return false
}
