package l2staking

import (
	"testing"

	"github.com/morph-l2/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestPackData(t *testing.T) {
	_, err := PacketData(common.HexToAddress("0x01"))
	require.NoError(t, err)
}
