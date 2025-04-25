package rcfg

import (
	"math/big"

	"github.com/morph-l2/go-ethereum/common"
)

var (
	// L2MessageQueueAddress is the address of the L2MessageQueue
	// predeploy
	// see contracts/src/L2/predeploys/L2MessageQueue.sol
	L2MessageQueueAddress = common.HexToAddress("0x5300000000000000000000000000000000000001")
	WithdrawTrieRootSlot  = common.BigToHash(big.NewInt(33))

	// SequencerAddress is the address of the Sequencer
	// predeploy
	// set contracts/contracts/l2/staking/Sequencer.sol
	SequencerAddress           = common.HexToAddress("0x5300000000000000000000000000000000000017")
	SequencerSetVerifyHashSlot = common.BigToHash(big.NewInt(101))

	// MorphFeeVaultAddress is the address of the L2TxFeeVault
	// predeploy
	// see morph-l2/morph/contracts/contracts/l2/system/L2TxFeeVault.sol
	MorphFeeVaultAddress = common.HexToAddress("0x530000000000000000000000000000000000000a")

	// L1GasPriceOracleAddress is the address of the GasPriceOracle
	// predeploy
	// see morph-l2/morph/contracts/contracts/l2/system/GasPriceOracle.sol
	L1GasPriceOracleAddress = common.HexToAddress("0x530000000000000000000000000000000000000F")
	Precision               = new(big.Int).SetUint64(1e9)
	L1BaseFeeSlot           = common.BigToHash(big.NewInt(1))
	OverheadSlot            = common.BigToHash(big.NewInt(2))
	ScalarSlot              = common.BigToHash(big.NewInt(3))

	// MorphTokenAddress is the address of the morph token contract
	MorphTokenAddress         = common.HexToAddress("0x5300000000000000000000000000000000000013")
	InflationMintedEpochsSolt = common.BigToHash(big.NewInt(8))

	// L2StakingAddress is the address of the l2 staking contract
	L2StakingAddress    = common.HexToAddress("0x5300000000000000000000000000000000000015")
	RewardStartedSlot   = common.BigToHash(big.NewInt(1))
	RewardStartTimeSlot = common.BigToHash(big.NewInt(2))

	// SystemAddress is the address of the system
	SystemAddress = common.HexToAddress("0x5300000000000000000000000000000000000021")

	// New fields added in the Curie hard fork
	L1BlobBaseFeeSlot = common.BigToHash(big.NewInt(6))
	CommitScalarSlot  = common.BigToHash(big.NewInt(7))
	BlobScalarSlot    = common.BigToHash(big.NewInt(8))
	IsCurieSlot       = common.BigToHash(big.NewInt(9))

	InitialCommitScalar = big.NewInt(230759955285)
	InitialBlobScalar   = big.NewInt(417565260)
)
