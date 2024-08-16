package sequencer

import (
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/rollup/rcfg"
)

// StateDB represents the StateDB interface
// required to get sequencerSetVerifyHash
type StateDB interface {
	GetState(common.Address, common.Hash) common.Hash
}

// ReadVerifyHashSlot reads SequencerSetVerifyHash slot in Sequencer predeploy
func ReadVerifyHashSlot(addr common.Address, state StateDB) common.Hash {
	return state.GetState(addr, rcfg.SequencerSetVerifyHashSlot)
}
