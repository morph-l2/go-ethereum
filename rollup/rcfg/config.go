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

	// New fields added in the Curie hard fork
	L1BlobBaseFeeSlot = common.BigToHash(big.NewInt(6))
	CommitScalarSlot  = common.BigToHash(big.NewInt(7))
	BlobScalarSlot    = common.BigToHash(big.NewInt(8))
	IsCurieSlot       = common.BigToHash(big.NewInt(9))

	InitialCommitScalar = big.NewInt(230759955285)
	InitialBlobScalar   = big.NewInt(417565260)

	L2TokenRegistryAddress = common.HexToAddress("0x5300000000000000000000000000000000000021")

	// TokenRegistrySlot is the storage slot for mapping(uint16 => TokenInfo)
	// TokenInfo struct layout:
	//   - tokenAddress: address (offset 0)
	//   - balanceSlot: bytes32 (offset 1)
	//   - isActive: bool (offset 2, byte 0)
	//   - decimals: uint8 (offset 2, byte 1)
	//   - scale: uint256 (offset 3)
	// Based on L2TokenRegistryStorageLayout: slot 151
	TokenRegistrySlot = common.BigToHash(big.NewInt(151))
	// TokenRegistrationSlot is the storage slot for mapping(address => uint16)
	// Based on L2TokenRegistryStorageLayout: slot 152
	TokenRegistrationSlot = common.BigToHash(big.NewInt(152))
	// PriceRatioSlot is the storage slot for mapping(uint16 => uint256)
	// Based on L2TokenRegistryStorageLayout: slot 153
	PriceRatioSlot = common.BigToHash(big.NewInt(153))
	// AllowListSlot is the storage slot for mapping(address => bool)
	// Based on L2TokenRegistryStorageLayout: slot 154
	AllowListSlot = common.BigToHash(big.NewInt(154))
	// AllowListEnabledSlot is the storage slot for bool allowListEnabled
	// Based on L2TokenRegistryStorageLayout: slot 155
	AllowListEnabledSlot = common.BigToHash(big.NewInt(155))
)
