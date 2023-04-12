package authclient

import (
	"context"
	"math/big"

	"github.com/scroll-tech/go-ethereum/eth/catalyst"
)

// AssembleL2Block assembles L2 Block used for L2 sequencer to propose a block in L2 consensus progress
func (ec *Client) AssembleL2Block(ctx context.Context, number *big.Int) (*catalyst.ExecutableL2Data, error) {
	var result catalyst.ExecutableL2Data
	err := ec.c.CallContext(ctx, &result, "engine_assembleL2Block", &catalyst.AssembleL2BlockParams{
		Number: number.Uint64(),
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
func (ec *Client) NewL2Block(ctx context.Context, executableL2Data *catalyst.ExecutableL2Data) error {
	return ec.c.CallContext(ctx, nil, "engine_newL2Block", executableL2Data)
}
