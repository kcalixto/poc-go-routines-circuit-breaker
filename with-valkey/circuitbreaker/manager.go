package circuitbreaker

import (
	"time"

	"github.com/AcordoCertoBR/go-utils/pkg/valkeyr"
)

type CircuitBreakerManager struct {
	valkeyClient *valkeyr.ValkeyR
}

var (
	// state
	defaultState = func() *State {
		return &State{
			Value:       "closed",
			AllowedReqs: float32(100.0),
			Expiry:      time.Now(),
		}
	}

	// settings
	defaultName          = "unnamed"
	defaultInterval      = time.Minute * 5
	defaultEvaluate      = func(state State, counts Counts) Direction { return Keep }
	defaultOnStateChange = func(name string, from State, to State, counts Counts) {}
	defaultIsSuccessful  = func(err error) bool { return err == nil }
	defaultGrowthRate    = float32(5.0)
)

func NewCircuitBreakerManager(valkeyClient *valkeyr.ValkeyR) *CircuitBreakerManager {
	return &CircuitBreakerManager{
		valkeyClient: valkeyClient,
	}
}

func (cb *CircuitBreakerManager) NewInstace(st *settings) *CircuitBreakerInstance {
	return NewCircuitBreakerInstance(cb.valkeyClient, st)
}
