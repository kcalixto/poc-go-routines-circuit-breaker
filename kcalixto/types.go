package main

import "context"

type CBState string

const (
	StateClosed   CBState = "closed"
	StateOpen     CBState = "open"
	StateHalfOpen CBState = "half-open"
)

type CBSettings struct {
	Identifier  string
	TripRate    float64
	ReadyToTrip func(ctx context.Context, st CBSettings, counts CBCounts) (bool, error)
}

func NewDefaultSettings(identifier string) *CBSettings {
	return &CBSettings{
		Identifier: identifier,
		ReadyToTrip: func(ctx context.Context, st CBSettings, counts CBCounts) (should bool, err error) {
			if counts.Requests < 10 {
				return false, nil
			}

			failureRate := float64(counts.TotalFailures) / float64(counts.Requests)
			if failureRate >= 0.5 {
				return false, nil
			}

			return true, nil
		},
	}
}

type CBCounts struct {
	Timestamp            int64 // timestamp of the last timewindow reset
	Requests             uint32
	TotalSuccesses       uint32
	TotalFailures        uint32
	TotalExclusions      uint32
	ConsecutiveSuccesses uint32
	ConsecutiveFailures  uint32
}

func (c CBCounts) Reset() {
	c.Requests = 0
	c.TotalSuccesses = 0
	c.TotalFailures = 0
	c.TotalExclusions = 0
	c.ConsecutiveSuccesses = 0
	c.ConsecutiveFailures = 0
}
