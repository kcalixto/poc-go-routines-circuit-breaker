// source: https://github.com/sony/gobreaker/blob/master/v2/gobreaker.go
package main

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"
)

type direction int

const (
	Increase direction = iota
	Decrease
	Open
	Keep
)

func (d direction) String() string {
	switch d {
	case Increase:
		return "increase"
	case Decrease:
		return "decrease"
	case Keep:
		return "keep"
	case Open:
		return "open"
	default:
		return fmt.Sprintf("unknown direction: %d", d)
	}
}

// State is a type that represents a state of CircuitBreaker.
type State int

// These constants are states of CircuitBreaker.
const (
	StateClosed State = iota
	StateHalfOpen
	StateOpen
)

var (
	ErrOpenState = errors.New("circuit breaker is open")
)

// String implements stringer interface.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateHalfOpen:
		return "half-open"
	case StateOpen:
		return "open"
	default:
		return fmt.Sprintf("unknown state: %d", s)
	}
}

type settings struct {
	Name          string
	Interval      time.Duration
	Evaluate      func(state State, counts *Counts) direction // to change state from half-open to open or closed - triggered after cb expires and cb is in half-open state
	OnStateChange func(name string, from State, to State)
	IsSuccessful  func(err error) bool
	GrowthRate    float32
}

func NewSettings() settings {
	return settings{
		Name:          "unnamed",
		Interval:      defaultInterval,
		Evaluate:      defaultEvaluate,
		OnStateChange: nil,
		IsSuccessful:  defaultIsSuccessful,
		GrowthRate:    5.0,
	}
}

// CircuitBreaker is a state machine to prevent sending requests that are likely to fail.
type CircuitBreaker[T any] struct {
	name          string
	interval      time.Duration
	currentTime   int64
	readyToOpen   func(counts *Counts) bool
	evaluate      func(state State, counts *Counts) direction
	isSuccessful  func(err error) bool
	onStateChange func(name string, from State, to State)

	allowedRequests float32
	growthRate      float32

	mutex  sync.Mutex
	state  State
	counts *Counts
	expiry time.Time
}

// NewCircuitBreaker returns a new CircuitBreaker configured with the given Settings.
func NewCircuitBreaker[T any](st settings) *CircuitBreaker[T] {
	cb := &CircuitBreaker[T]{
		name:            st.Name,
		interval:        st.Interval,
		evaluate:        st.Evaluate,
		isSuccessful:    st.IsSuccessful,
		onStateChange:   st.OnStateChange,
		state:           StateClosed,
		counts:          newCounts(),
		growthRate:      st.GrowthRate,
		allowedRequests: 100.0,
	}

	cb.toNewGeneration()

	return cb
}

const defaultInterval = time.Duration(0) * time.Second
const defaultTimeout = time.Duration(60) * time.Second

func defaultEvaluate(state State, counts *Counts) direction {
	if counts.Requests <= 0 {
		return Increase
	}

	if counts.ConsecutiveFailures == 0 {
		return Increase
	}
	if counts.ConsecutiveSuccesses == 0 {
		return Decrease
	}
	if float32(counts.TotalFailures/counts.Requests) > 0.75 {
		return Open
	}
	if float32(counts.TotalSuccesses/counts.Requests) > 0.5 {
		return Increase
	}

	return Keep
}

func defaultIsSuccessful(err error) bool {
	return err == nil
}

// Name returns the name of the CircuitBreaker.
func (cb *CircuitBreaker[T]) Name() string {
	return cb.name
}

// State returns the current state of the CircuitBreaker.
func (cb *CircuitBreaker[T]) State() State {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	return cb.currentState()
}

// Counts returns internal counters
func (cb *CircuitBreaker[T]) Counts() Counts {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	return *cb.counts
}

// Execute runs the given request if the CircuitBreaker accepts it.
// Execute returns an error instantly if the CircuitBreaker rejects the request.
// Otherwise, Execute returns the result of the request.
// If a panic occurs in the request, the CircuitBreaker handles it as an error
// and causes the same panic again.
func (cb *CircuitBreaker[T]) Execute(req func() (T, error)) (T, error) {
	err := cb.beforeRequest()
	if err != nil {
		var t T
		return t, err
	}

	defer func() {
		e := recover()
		if e != nil {
			cb.afterRequest(fmt.Errorf("%v", e))
			panic(e)
		}
	}()

	result, err := req()
	cb.afterRequest(err)
	return result, err
}

func (cb *CircuitBreaker[T]) beforeRequest() error {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	state := cb.currentState()
	fmt.Printf("current state: %s \n", state)
	fmt.Printf("allowed requests: %f \n", cb.allowedRequests)
	if state == StateOpen {
		return ErrOpenState
	}

	rnd := rand.Float32() * 100
	if state == StateHalfOpen && rnd > cb.allowedRequests {
		return ErrOpenState
	}

	cb.counts.onRequest()
	return nil
}

func (cb *CircuitBreaker[T]) afterRequest(err error) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	if cb.isSuccessful(err) {
		cb.counts.onSuccess()
	} else {
		cb.counts.onFailure()
	}
}

func (cb *CircuitBreaker[T]) currentState() State {
	now := time.Now()
	fmt.Printf("expires in: %s\n", cb.expiry.Sub(now).String())

	if cb.expiry.Before(now) {
		cb.onExpired()
	}
	return cb.state
}

func (cb *CircuitBreaker[T]) onExpired() {
	fmt.Printf("circuit breaker expired - [evaluating] - counts: %+v \n", *cb.counts)
	d := cb.evaluate(cb.state, cb.counts)
	fmt.Printf("evaluated direction: %s, current allowed requests: %f \n", d.String(), cb.allowedRequests)

	newState := cb.state
	switch d {
	case Increase:
		if cb.state != StateClosed {
			cb.allowedRequests = min(cb.allowedRequests+cb.growthRate, 100)
			if cb.allowedRequests == 100 {
				newState = StateClosed
			} else {
				newState = StateHalfOpen
			}
		}
	case Decrease:
		if cb.state != StateOpen {
			cb.allowedRequests = max(cb.allowedRequests-cb.growthRate, 0)
			if cb.allowedRequests == 0 {
				newState = StateOpen
			} else {
				newState = StateHalfOpen
			}
		}
	case Open:
		cb.allowedRequests = 0
		newState = StateOpen
	case Keep: // do nothing
	}

	fmt.Printf("newState: %s, new allowed requests: %f \n", newState.String(), cb.allowedRequests)
	cb.setState(newState)
}

func (cb *CircuitBreaker[T]) setState(state State) {
	defer cb.toNewGeneration()

	if cb.state == state {
		return
	}

	prev := cb.state
	cb.state = state

	if cb.onStateChange != nil {
		cb.onStateChange(cb.name, prev, state)
	}
}

func (cb *CircuitBreaker[T]) toNewGeneration() {
	fmt.Printf("to new generation: %s \n", cb.state)
	cb.currentTime = time.Now().Unix()
	cb.expiry = time.Now().Add(cb.interval)
	cb.counts.clear()
}
