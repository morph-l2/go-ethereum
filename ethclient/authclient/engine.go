package authclient

import (
	"context"
	"fmt"
	"github.com/scroll-tech/go-ethereum/common"
	"math/big"

	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/eth/catalyst"
)

// AssembleL2Block assembles L2 Block used for L2 sequencer to propose a block in L2 consensus progress
func (ec *Client) AssembleL2Block(ctx context.Context, number *big.Int, transactions types.Transactions) (*catalyst.ExecutableL2Data, error) {
	txs := make([][]byte, 0, len(transactions))
	for i, tx := range transactions {
		bz, err := tx.MarshalBinary()
		if err != nil {
			return nil, fmt.Errorf("failed to marshal tx, index: %d, error: %v", i, err)
		}
		txs = append(txs, bz)
	}
	var result catalyst.ExecutableL2Data
	err := ec.c.CallContext(ctx, &result, "engine_assembleL2Block", &catalyst.AssembleL2BlockParams{
		Number:       number.Uint64(),
		Transactions: txs,
	})
	return &result, err
}

// ValidateL2Block validates a L2 Block
func (ec *Client) ValidateL2Block(ctx context.Context, executableL2Data *catalyst.ExecutableL2Data) (bool, error) {
	var result catalyst.GenericResponse
	err := ec.c.CallContext(ctx, &result, "engine_validateL2Block", executableL2Data)
	return result.Success, err
}

// NewL2Block executes L2 Block, and set the block to chain
func (ec *Client) NewL2Block(ctx context.Context, executableL2Data *catalyst.ExecutableL2Data, batchHash *common.Hash) error {
	return ec.c.CallContext(ctx, nil, "engine_newL2Block", executableL2Data, batchHash)
}

// NewSafeL2Block executes a safe L2 Block, and set the block to chain
func (ec *Client) NewSafeL2Block(ctx context.Context, safeL2Data *catalyst.SafeL2Data) (*types.Header, error) {
	var header types.Header
	err := ec.c.CallContext(ctx, &header, "engine_newSafeL2Block", safeL2Data)
	return &header, err
}

// CommitBatch commit the batch, with the signatures
func (ec *Client) CommitBatch(ctx context.Context, batch *types.RollupBatch, signatures []types.BatchSignature) error {
	return ec.c.CallContext(ctx, nil, "engine_commitBatch", batch, signatures)
}

// AppendBlsSignature append a new bls signature to the batch
func (ec *Client) AppendBlsSignature(ctx context.Context, batchHash common.Hash, signature types.BatchSignature) error {
	return ec.c.CallContext(ctx, nil, "engine_appendBatchSignature", batchHash, signature)
}
