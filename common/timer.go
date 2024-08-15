package common

import "github.com/morph-l2/go-ethereum/metrics"

// WithTimer calculates the interval of f
func WithTimer(timer metrics.Timer, f func()) {
	if metrics.Enabled {
		timer.Time(f)
	} else {
		f()
	}
}
