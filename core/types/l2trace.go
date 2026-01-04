package types

import (
	"encoding/json"
	"math/big"

	"github.com/holiman/uint256"
	"github.com/morph-l2/go-ethereum/common"
	"github.com/morph-l2/go-ethereum/common/hexutil"
	"github.com/morph-l2/go-ethereum/params"
)

// BlockTrace contains block execution traces and results required for rollers.
type BlockTrace struct {
	ChainID           uint64             `json:"chainID"`
	Coinbase          *AccountWrapper    `json:"coinbase"`
	Header            *Header            `json:"header"`
	Transactions      []*TransactionData `json:"transactions"`
	StorageTrace      *StorageTrace      `json:"storageTrace"`
	Bytecodes         []*BytecodeTrace   `json:"codes"`
	StartL1QueueIndex uint64             `json:"startL1QueueIndex"`
}

// BytecodeTrace stores all accessed bytecodes
type BytecodeTrace struct {
	CodeSize         uint64        `json:"codeSize"`
	KeccakCodeHash   common.Hash   `json:"keccakCodeHash"`
	PoseidonCodeHash common.Hash   `json:"hash"`
	Code             hexutil.Bytes `json:"code"`
}

// StorageTrace stores proofs of storage needed by storage circuit
type StorageTrace struct {
	// Root hash before block execution:
	RootBefore common.Hash `json:"rootBefore,omitempty"`
	// Root hash after block execution, is nil if execution has failed
	RootAfter common.Hash `json:"rootAfter,omitempty"`

	// All proofs BEFORE execution, for accounts which would be used in tracing
	Proofs map[string][]hexutil.Bytes `json:"proofs"`

	// All storage proofs BEFORE execution
	StorageProofs map[string]map[string][]hexutil.Bytes `json:"storageProofs,omitempty"`
}

// ExecutionResult groups all structured logs emitted by the EVM
// while replaying a transaction in debug mode as well as transaction
// execution status, the amount of gas used and the return value
type ExecutionResult struct {
	L1DataFee   *hexutil.Big `json:"l1DataFee,omitempty"`
	FeeTokenID  *uint16      `json:"feeTokenID,omitempty"`
	FeeRate     *hexutil.Big `json:"feeRate,omitempty"`
	TokenScale  *hexutil.Big `json:"tokenScale,omitempty"`
	FeeLimit    *hexutil.Big `json:"feeLimit,omitempty"`
	Gas         uint64       `json:"gas"`
	Failed      bool         `json:"failed"`
	ReturnValue string       `json:"returnValue"`
	// Sender's account state (before Tx)
	From *AccountWrapper `json:"from,omitempty"`
	// Receiver's account state (before Tx)
	To *AccountWrapper `json:"to,omitempty"`
	// AccountCreated record the account if the tx is "create"
	// (for creating inside a contract, we just handle CREATE op)
	AccountCreated *AccountWrapper `json:"accountCreated,omitempty"`

	// Record all accounts' state which would be affected AFTER tx executed
	// currently they are just `from` and `to` account
	AccountsAfter []*AccountWrapper `json:"accountAfter"`

	StructLogs []*StructLogRes `json:"structLogs"`
	CallTrace  json.RawMessage `json:"callTrace"`
}

// StructLogRes stores a structured log emitted by the EVM while replaying a
// transaction in debug mode
type StructLogRes struct {
	Pc            uint64            `json:"pc"`
	Op            string            `json:"op"`
	Gas           uint64            `json:"gas"`
	GasCost       uint64            `json:"gasCost"`
	Depth         int               `json:"depth"`
	Error         string            `json:"error,omitempty"`
	Stack         []string          `json:"stack,omitempty"`
	ReturnData    string            `json:"returnData,omitempty"`
	Memory        []string          `json:"memory,omitempty"`
	Storage       map[string]string `json:"storage,omitempty"`
	RefundCounter uint64            `json:"refund,omitempty"`
}

// NewStructLogResBasic Basic StructLogRes skeleton, Stack&Memory&Storage&ExtraData are separated from it for GC optimization;
// still need to fill in with Stack&Memory&Storage&ExtraData
func NewStructLogResBasic(pc uint64, op string, gas, gasCost uint64, depth int, refundCounter uint64, err error) *StructLogRes {
	logRes := &StructLogRes{
		Pc:            pc,
		Op:            op,
		Gas:           gas,
		GasCost:       gasCost,
		Depth:         depth,
		RefundCounter: refundCounter,
	}

	if err != nil {
		logRes.Error = err.Error()
	}
	return logRes
}

type AccountWrapper struct {
	Address          common.Address `json:"address"`
	Nonce            uint64         `json:"nonce"`
	Balance          *hexutil.Big   `json:"balance"`
	KeccakCodeHash   common.Hash    `json:"keccakCodeHash,omitempty"`
	PoseidonCodeHash common.Hash    `json:"poseidonCodeHash,omitempty"`
	CodeSize         uint64         `json:"codeSize"`
}

// StorageWrapper while key & value can also be retrieved from StructLogRes.Storage,
// we still stored in here for roller's processing convenience.
type StorageWrapper struct {
	Key   string `json:"key,omitempty"`
	Value string `json:"value,omitempty"`
}

type TransactionData struct {
	Type              uint8                      `json:"type"`
	Nonce             uint64                     `json:"nonce"`
	TxHash            string                     `json:"txHash"`
	Gas               uint64                     `json:"gas"`
	GasPrice          *hexutil.Big               `json:"gasPrice"`
	GasTipCap         *hexutil.Big               `json:"gasTipCap"`
	GasFeeCap         *hexutil.Big               `json:"gasFeeCap"`
	FeeTokenID        *uint16                    `json:"feeTokenID,omitempty"`
	FeeLimit          *hexutil.Big               `json:"feeLimit,omitempty"`
	From              common.Address             `json:"from"`
	To                *common.Address            `json:"to"`
	ChainId           *hexutil.Big               `json:"chainId"`
	Value             *hexutil.Big               `json:"value"`
	Data              string                     `json:"data"`
	IsCreate          bool                       `json:"isCreate"`
	AccessList        AccessList                 `json:"accessList"`
	AuthorizationList []SetCodeAuthorizationData `json:"authorizationList,omitempty"`
	V                 *hexutil.Big               `json:"v"`
	R                 *hexutil.Big               `json:"r"`
	S                 *hexutil.Big               `json:"s"`
}

type SetCodeAuthorizationData struct {
	ChainID uint256.Int    `json:"chainId" gencodec:"required"`
	Address common.Address `json:"address" gencodec:"required"`
	Nonce   uint64         `json:"nonce" gencodec:"required"`
	V       hexutil.Uint64 `json:"yParity" gencodec:"required"`
	R       hexutil.U256   `json:"r" gencodec:"required"`
	S       hexutil.U256   `json:"s" gencodec:"required"`
}

// NewTransactionData returns a transaction that will serialize to the trace
// representation, with the given location metadata set (if available).
func NewTransactionData(tx *Transaction, blockNumber uint64, blockTime uint64, config *params.ChainConfig) *TransactionData {
	signer := MakeSigner(config, big.NewInt(0).SetUint64(blockNumber), blockTime)
	from, _ := Sender(signer, tx)
	v, r, s := tx.RawSignatureValues()

	nonce := tx.Nonce()
	if tx.IsL1MessageTx() {
		nonce = tx.L1MessageQueueIndex()
	}

	result := &TransactionData{
		Type:              tx.Type(),
		TxHash:            tx.Hash().String(),
		Nonce:             nonce,
		ChainId:           (*hexutil.Big)(tx.ChainId()),
		From:              from,
		Gas:               tx.Gas(),
		GasPrice:          (*hexutil.Big)(tx.GasPrice()),
		GasTipCap:         (*hexutil.Big)(tx.GasTipCap()),
		GasFeeCap:         (*hexutil.Big)(tx.GasFeeCap()),
		To:                tx.To(),
		Value:             (*hexutil.Big)(tx.Value()),
		Data:              hexutil.Encode(tx.Data()),
		IsCreate:          tx.To() == nil,
		AccessList:        tx.AccessList(),
		AuthorizationList: convertToAuthorizationData(tx.SetCodeAuthorizations()),
		V:                 (*hexutil.Big)(v),
		R:                 (*hexutil.Big)(r),
		S:                 (*hexutil.Big)(s),
	}

	// Set FeeTokenID and FeeLimit for AltFeeTx
	if tx.Type() == AltFeeTxType {
		feeTokenID := tx.FeeTokenID()
		if feeTokenID != 0 {
			result.FeeTokenID = &feeTokenID
		}
		if feeLimit := tx.FeeLimit(); feeLimit != nil && feeLimit.Sign() > 0 {
			result.FeeLimit = (*hexutil.Big)(feeLimit)
		}
	}

	return result
}

func convertToAuthorizationData(auths []SetCodeAuthorization) []SetCodeAuthorizationData {
	result := make([]SetCodeAuthorizationData, len(auths))
	for i, auth := range auths {
		result[i] = SetCodeAuthorizationData{
			ChainID: auth.ChainID,
			Address: auth.Address,
			Nonce:   auth.Nonce,
			V:       hexutil.Uint64(auth.V),
			R:       hexutil.U256(auth.R),
			S:       hexutil.U256(auth.S),
		}
	}
	return result
}

// WrapProof turn the bytes array into proof type (array of hexutil.Bytes)
func WrapProof(proofBytes [][]byte) (wrappedProof []hexutil.Bytes) {
	wrappedProof = make([]hexutil.Bytes, len(proofBytes))
	for i, bt := range proofBytes {
		wrappedProof[i] = bt
	}
	return
}
