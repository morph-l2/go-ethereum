package circuitscapacitychecker

import (
	"errors"
)

var (
	ErrUnknown               = errors.New("unknown circuits capacity checker error")
	ErrTxRowUsageOverflow    = errors.New("tx row usage oveflow")
	ErrBlockRowUsageOverflow = errors.New("block row usage oveflow")
)
