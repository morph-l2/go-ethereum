package l2staking

import (
	"fmt"
	"strings"
	"sync"

	"github.com/morph-l2/go-ethereum/accounts/abi"
	"github.com/morph-l2/go-ethereum/common"
)

const jsonData = `[{"inputs":[{"internalType":"address","name":"sequencerAddr","type":"address"}],"name":"recordBlocks","outputs":[],"stateMutability":"nonpayable","type":"function"}]`

var (
	l2StakingABI *abi.ABI
	loadOnce     sync.Once
	loadErr      error
)

func Abi() (*abi.ABI, error) {
	loadOnce.Do(func() {
		stakingABI, err := abi.JSON(strings.NewReader(jsonData))
		if err != nil {
			loadErr = fmt.Errorf("failed to parse ABI: %w", err)
			return
		}
		l2StakingABI = &stakingABI
	})
	return l2StakingABI, loadErr
}

func PacketData(addr common.Address) ([]byte, error) {
	a, err := Abi()
	if err != nil {
		return nil, fmt.Errorf("failed to get ABI: %w", err)
	}
	data, err := a.Pack("recordBlocks", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to pack data: %w", err)
	}
	return data, nil
}
