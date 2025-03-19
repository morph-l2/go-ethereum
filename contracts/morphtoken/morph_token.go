package morphtoken

import (
	"fmt"
	"strings"
	"sync"

	"github.com/morph-l2/go-ethereum/accounts/abi"
)

const jsonData = `[{"inputs":[],"name":"mintInflations","outputs":[],"stateMutability":"nonpayable","type":"function"}]`

var (
	morphTokenABI *abi.ABI
	loadOnce      sync.Once
	loadErr       error
)

func Abi() (*abi.ABI, error) {
	loadOnce.Do(func() {
		tokenABI, err := abi.JSON(strings.NewReader(jsonData))
		if err != nil {
			loadErr = fmt.Errorf("failed to parse ABI: %w", err)
			return
		}
		morphTokenABI = &tokenABI
	})
	return morphTokenABI, loadErr
}

func PacketData() ([]byte, error) {
	a, err := Abi()
	if err != nil {
		return nil, fmt.Errorf("failed to get ABI: %w", err)
	}
	data, err := a.Pack("mintInflations")
	if err != nil {
		return nil, fmt.Errorf("failed to pack data: %w", err)
	}
	return data, nil
}
