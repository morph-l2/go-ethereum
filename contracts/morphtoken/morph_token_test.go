package morphtoken

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPackData(t *testing.T) {
	_, err := PacketData()
	require.NoError(t, err)
}
