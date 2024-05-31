package batch

import (
	"bytes"
	"encoding/binary"
	"math/big"
)

type BlockContext struct {
	Number    uint64   `json:"number"         gencodec:"required"`
	Timestamp uint64   `json:"timestamp"      gencodec:"required"`
	BaseFee   *big.Int `json:"baseFeePerGas"  rlp:"optional"`
	GasLimit  uint64   `json:"gasLimit"       gencodec:"required"`
	NumTxs    uint16   `json:"numTxs"`
	NumL1Txs  uint16   `json:"numL1Txs"`
}

func (wb *BlockContext) DecodeBlockContext(bc []byte) error {
	reader := bytes.NewReader(bc)
	bsBaseFee := make([]byte, 32)
	if err := binary.Read(reader, binary.BigEndian, &wb.Number); err != nil {
		return err
	}
	if err := binary.Read(reader, binary.BigEndian, &wb.Timestamp); err != nil {
		return err
	}
	if err := binary.Read(reader, binary.BigEndian, &bsBaseFee); err != nil {
		return err
	}
	wb.BaseFee = new(big.Int).SetBytes(bsBaseFee)
	if err := binary.Read(reader, binary.BigEndian, &wb.GasLimit); err != nil {
		return err
	}
	if err := binary.Read(reader, binary.BigEndian, &wb.NumTxs); err != nil {
		return err
	}
	if err := binary.Read(reader, binary.BigEndian, &wb.NumL1Txs); err != nil {
		return err
	}
	return nil
}

func BlockContextsFromChunks(chunks [][]byte) (bcs []BlockContext, err error) {
	if len(chunks) == 0 {
		return nil, nil
	}
	for _, chunk := range chunks {
		blockCount := chunk[0]
		rawChunk := chunk[1:]
		for i := 0; i < int(blockCount); i++ {
			bc := new(BlockContext)
			err = bc.DecodeBlockContext(rawChunk[i*60 : i*60+60])
			if err != nil {
				return nil, err
			}
			bcs = append(bcs, *bc)
		}
	}
	return bcs, nil
}
