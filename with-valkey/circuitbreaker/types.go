package circuitbreaker

import (
	"errors"
	"fmt"
	"time"
)

type Counts struct {
	Requests             uint32
	TotalSuccesses       uint32
	TotalFailures        uint32
	ConsecutiveSuccesses uint32
	ConsecutiveFailures  uint32
}

type settings struct {
	// Circuit breaker name
	Name string
	// Circuit breaker time window duration - after this interval, the circuit breaker will evaluate the state and decide what to do
	Interval time.Duration
	// Evaluate function to determine the direction of state change based on the current state and counts
	Evaluate func(state State, counts Counts) Direction
	// What to do when cb state changes - this is triggered before the state effectively changes, that's why we have counts variable available
	OnStateChange func(name string, from State, to State, counts Counts)
	// IsSuccessful helps to determine if a response should be considered as an error or success
	IsSuccessful func(err error) bool
	// GrowRate is how much requests we're going to allow while in state HalfOpen
	GrowthRate float32
}

func NewDefaultSettings() *settings {
	return &settings{
		Name:          defaultName,
		Interval:      defaultInterval,
		Evaluate:      defaultEvaluate,
		OnStateChange: defaultOnStateChange,
		IsSuccessful:  defaultIsSuccessful,
		GrowthRate:    defaultGrowthRate,
	}
}

type Direction int

const (
	Increase Direction = iota
	Decrease
	Open
	Keep
)

func (d Direction) String() string {
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

type State struct {
	Value       string
	AllowedReqs float32
	Expiry      time.Time
}

func (s *State) IsOpen() bool {
	return s.Value == "open"
}

func (s *State) IsClosed() bool {
	return s.Value == "closed"
}

func (s *State) IsHalfOpen() bool {
	return s.Value == "half-open"
}

func (s *State) IsExpired() bool {
	return time.Now().After(s.Expiry)
}

func (s *State) Open() {
	s.Value = "open"
	s.AllowedReqs = 0
}

func (s *State) Close() {
	s.Value = "closed"
	s.AllowedReqs = 100
}

func (s *State) HalfOpen(allowedReqs ...float32) {
	s.Value = "half-open"
	s.AllowedReqs = 0
	if len(allowedReqs) > 0 {
		s.AllowedReqs = allowedReqs[0]
	}
}

var (
	ErrOpenState = errors.New("circuit breaker is open")
)
