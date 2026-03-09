package circuitbreaker

import (
	"context"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/AcordoCertoBR/go-utils/pkg/valkeyr"
)

type CircuitBreakerInstance struct {
	currentTimeWindow time.Time
	st                *settings
	store             *valkeyCircuitBreakerStore
	State             *State // cached at structure level for quick access, but not guaranteed to be up-to-date
}

func NewCircuitBreakerInstance(valkeyClient *valkeyr.ValkeyR, st *settings) *CircuitBreakerInstance {
	return &CircuitBreakerInstance{
		st:    st,
		store: NewValkeyCBStore(valkeyClient, st.Name),
		State: defaultState(),
	}
}

// *
// * public operations
// *

// Name returns the name of the CircuitBreaker.
func (cb *CircuitBreakerInstance) Name() string {
	return cb.st.Name
}

// State returns the current state of the CircuitBreaker.
// This is meant for logging pourposes, there's no cache locking to guarantee this value
func (cb *CircuitBreakerInstance) FetchSharedState(ctx context.Context) (*State, error) {
	return cb.store.FetchSharedState(ctx)
}

// Counts returns internal counters
// This is meant for logging pourposes, there's no cache locking to guarantee this value
func (cb *CircuitBreakerInstance) FetchCounts(ctx context.Context) (*Counts, error) {
	return cb.store.FetchCounts(ctx)
}

// Execute runs the given request if the CircuitBreaker accepts it.
// Execute returns an error instantly if the CircuitBreaker rejects the request.
// Otherwise, Execute returns the result of the request.
// If a panic occurs in the request, the CircuitBreaker handles it as an error
// and causes the same panic again.
func (cb *CircuitBreakerInstance) Execute(ctx context.Context, req func() error) error {
	err := cb.beforeRequest(ctx)
	if err != nil {
		return err
	}

	defer func() {
		e := recover()
		if e != nil {
			cb.afterRequest(ctx, fmt.Errorf("%v", e))
			panic(e)
		}
	}()

	err = req()
	cb.afterRequest(ctx, err)
	return err
}

// *
// * private operations
// *

func (cb *CircuitBreakerInstance) beforeRequest(ctx context.Context) error {
	state, err := cb.currentState(ctx)
	if err != nil {
		return fmt.Errorf("error getting current state: %w", err)
	}

	fmt.Printf(
		`executing request
	"state": %s,
	"allowedReqs": %f,
	"expiry": %s,
	`, state.Value, state.AllowedReqs, state.Expiry.String(),
	)

	if state.IsOpen() {
		return ErrOpenState
	}

	rnd := rand.Float32() * 100
	if state.IsHalfOpen() && rnd > state.AllowedReqs {
		return ErrOpenState
	}

	cb.store.OnRequest(ctx)
	return nil
}

func (cb *CircuitBreakerInstance) afterRequest(ctx context.Context, err error) {
	if cb.st.IsSuccessful(err) {
		cb.store.OnSuccess(ctx)
	} else {
		cb.store.OnFailure(ctx)
	}
}

func (cb *CircuitBreakerInstance) currentState(ctx context.Context) (*State, error) {
	if cb.State == nil {
		return defaultState(), nil
	}
	fmt.Printf("cb.State.IsExpired: %t \n", cb.State.IsExpired())
	if cb.State.IsExpired() {
		err := cb.onExpired(ctx)
		if err != nil {
			return nil, fmt.Errorf("error handling state expiration: %w", err)
		}
	}
	return cb.State, nil
}

func (cb *CircuitBreakerInstance) onExpired(ctx context.Context) error {
	state, err := cb.store.FetchSharedState(ctx)
	if err != nil {
		return fmt.Errorf("error fetching shared state: %w", err)
	}

	acquired, err := cb.store.Lock(ctx, time.Second*5)
	if err != nil || !acquired {
		cb.State = state // update the state to the most recent one, even if it's expired, to avoid making decisions based on stale data
		return nil       // Another pod has already locked. Do nothing and let them handle the onExpired logic.
	}
	defer cb.store.Unlock(ctx)

	fmt.Printf("state expired, evaluating new state \n")
	counts, err := cb.store.FetchCounts(ctx)
	if err != nil {
		return fmt.Errorf("error fetching counts: %w", err)
	}

	if state == nil {
		return fmt.Errorf("state is nil")
	}
	if counts == nil {
		return fmt.Errorf("counts is nil")
	}

	d := cb.st.Evaluate(*state, *counts)
	newState := new(State)
	*newState = *state // copy the state to modify it without affecting the original one until we decide to switch to new generation

	switch d {
	case Increase:
		if !state.IsClosed() {
			if ar := state.AllowedReqs + cb.st.GrowthRate; ar >= 100 {
				newState.Close()
			} else {
				newState.HalfOpen(ar)
			}
		}
	case Decrease:
		if !state.IsOpen() {
			if ar := state.AllowedReqs - cb.st.GrowthRate; ar <= 0 {
				newState.Open()
			} else {
				newState.HalfOpen(ar)
			}
		}
	case Open:
		newState.Open()
	case Keep: // do nothing
	}

	// notifies that state will change
	if newState.Value != state.Value {
		cb.st.OnStateChange(cb.st.Name, *state, *newState, *counts)
	}

	// change the state effectively
	err = cb.toNewGeneration(ctx, newState)
	if err != nil {
		return fmt.Errorf("error evolving to new generation: %w", err)
	}

	return nil
}

func (cb *CircuitBreakerInstance) toNewGeneration(ctx context.Context, state *State) (err error) {
	cb.currentTimeWindow = time.Now()

	state.Expiry = time.Now().Add(cb.st.Interval)
	err = cb.store.ToNewGeneration(ctx, state)
	if err != nil {
		return fmt.Errorf("error writing new generation: %w", err)
	}

	err = cb.store.Clear(ctx)
	if err != nil {
		return fmt.Errorf("error clearing counts for new generation: %w", err)
	}

	cb.State = state

	return nil
}
