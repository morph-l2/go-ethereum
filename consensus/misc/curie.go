package misc

import (
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/core/state"
	"github.com/morph-l2/go-ethereum/log"
	"github.com/morph-l2/go-ethereum/rollup/rcfg"
)

// ApplyCurieHardFork modifies the state database according to the Curie hard-fork rules,
// updating the bytecode and storage of the L1GasPriceOracle contract.
func ApplyCurieHardFork(statedb *state.StateDB) {
	log.Info("Applying Curie hard fork")

	// initialize new storage slots
	statedb.SetState(rcfg.L1GasPriceOracleAddress, rcfg.IsCurieSlot, common.BytesToHash([]byte{1}))
	statedb.SetState(rcfg.L1GasPriceOracleAddress, rcfg.L1BlobBaseFeeSlot, common.BytesToHash([]byte{1}))
	statedb.SetState(rcfg.L1GasPriceOracleAddress, rcfg.CommitScalarSlot, common.BigToHash(rcfg.InitialCommitScalar))
	statedb.SetState(rcfg.L1GasPriceOracleAddress, rcfg.BlobScalarSlot, common.BigToHash(rcfg.InitialBlobScalar))
}
