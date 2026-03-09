package circuitbreaker

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/AcordoCertoBR/go-utils/pkg/valkeyr"
)

// Lua scripts for atomic operations
var (
	stateField                = "State"
	allowedReqsField          = "AllowedRequests"
	expiryField               = "Expiry"
	requestsField             = "Requests"
	consecutiveSuccessesField = "ConsecutiveSuccesses"
	consecutiveFailuresField  = "ConsecutiveFailures"
	totalSuccessesField       = "TotalSuccesses"
	totalFailuresField        = "TotalFailures"
	luaSuccess                = fmt.Sprintf(`
		redis.call('HINCRBY', KEYS[1], '%s', 1)
		redis.call('HINCRBY', KEYS[1], '%s', 1)
		redis.call('HSET', KEYS[1], '%s', 0)
		return 1
	`,
		totalSuccessesField,
		consecutiveSuccessesField,
		consecutiveFailuresField,
	)
	luaFailure = fmt.Sprintf(`
		redis.call('HINCRBY', KEYS[1], '%s', 1)
		redis.call('HINCRBY', KEYS[1], '%s', 1)
		redis.call('HSET', KEYS[1], '%s', 0)
		return 1
	`,
		totalFailuresField,
		consecutiveFailuresField,
		consecutiveSuccessesField,
	)
	lockKey = func(name string) string {
		return fmt.Sprintf("cb:%s:lock", name)
	}
)

type valkeyCircuitBreakerStore struct {
	client    *valkeyr.ValkeyR
	name      string
	countsKey string
	stateKey  string
}

func NewValkeyCBStore(client *valkeyr.ValkeyR, name string) *valkeyCircuitBreakerStore {
	return &valkeyCircuitBreakerStore{
		client:    client,
		name:      name,
		countsKey: fmt.Sprintf("cb:%s:counts", name),
		stateKey:  fmt.Sprintf("cb:%s:state", name),
	}
}

// onRequest atomic increment
func (v *valkeyCircuitBreakerStore) OnRequest(ctx context.Context) error {
	vClient, err := v.client.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get valkey client: %w", err)
	}

	cmd := vClient.B().
		Hincrby().
		Key(v.client.FormatKey(v.countsKey)).
		Field(requestsField).
		Increment(1).
		Build()

	return vClient.Do(ctx, cmd).Error()
}

func (v *valkeyCircuitBreakerStore) OnSuccess(ctx context.Context) error {
	vClient, err := v.client.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get valkey client: %w", err)
	}

	cmd := vClient.B().
		Eval().
		Script(luaSuccess).
		Numkeys(1).
		Key(v.client.FormatKey(v.countsKey)).
		Build()

	return vClient.Do(ctx, cmd).Error()
}

func (v *valkeyCircuitBreakerStore) OnFailure(ctx context.Context) error {
	vClient, err := v.client.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get valkey client: %w", err)
	}

	cmd := vClient.B().
		Eval().
		Script(luaFailure).
		Numkeys(1).
		Key(v.client.FormatKey(v.countsKey)).
		Build()

	return vClient.Do(ctx, cmd).Error()
}

// FetchCounts retrieves the current counts from Valkey
func (v *valkeyCircuitBreakerStore) FetchCounts(ctx context.Context) (*Counts, error) {
	res, err := v.client.GetAllHash(ctx, v.countsKey)
	if err != nil {
		return nil, err
	}

	counts := new(Counts)
	if val, ok := res[requestsField]; ok {
		parsed, err := strconv.ParseUint(val, 10, 32)
		if err == nil {
			counts.Requests = uint32(parsed)
		}
	}
	if val, ok := res[totalSuccessesField]; ok {
		parsed, err := strconv.ParseUint(val, 10, 32)
		if err == nil {
			counts.TotalSuccesses = uint32(parsed)
		}
	}
	if val, ok := res[totalFailuresField]; ok {
		parsed, err := strconv.ParseUint(val, 10, 32)
		if err == nil {
			counts.TotalFailures = uint32(parsed)
		}
	}
	if val, ok := res[consecutiveSuccessesField]; ok {
		parsed, err := strconv.ParseUint(val, 10, 32)
		if err == nil {
			counts.ConsecutiveSuccesses = uint32(parsed)
		}
	}
	if val, ok := res[consecutiveFailuresField]; ok {
		parsed, err := strconv.ParseUint(val, 10, 32)
		if err == nil {
			counts.ConsecutiveFailures = uint32(parsed)
		}
	}
	return counts, nil
}

// Clear resets the counts for a new generation
func (v *valkeyCircuitBreakerStore) Clear(ctx context.Context) error {
	vClient, err := v.client.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get valkey client: %w", err)
	}

	cmd := vClient.B().
		Hdel().
		Key(v.client.FormatKey(v.countsKey)).
		Field(requestsField, totalSuccessesField, totalFailuresField, consecutiveSuccessesField, consecutiveFailuresField).
		Build()

	return vClient.Do(ctx, cmd).Error()
}

// FetchSharedState retrieves the current state, allowed requests, and expiry timestamp.
// If the key doesn't exist yet, returns an error
func (v *valkeyCircuitBreakerStore) FetchSharedState(ctx context.Context) (*State, error) {
	res, err := v.client.GetAllHash(ctx, v.stateKey)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return defaultState(), nil // state not set yet, return default state
	}

	state := new(State)
	if val, ok := res[stateField]; ok {
		state.Value = val
	}
	if val, ok := res[allowedReqsField]; ok {
		parsed, err := strconv.ParseFloat(val, 32)
		if err == nil {
			state.AllowedReqs = float32(parsed)
		}
	}
	if val, ok := res[expiryField]; ok {
		parsed, err := strconv.ParseInt(val, 10, 64)
		if err == nil {
			state.Expiry = time.Unix(parsed, 0)
		}
	}

	return state, nil
}

// ToNewGeneration writes the updated state, allowed requests, and calculates the new expiry.
func (v *valkeyCircuitBreakerStore) ToNewGeneration(ctx context.Context, state *State) error {
	if state == nil {
		return fmt.Errorf("state cannot be nil")
	}
	vClient, err := v.client.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get valkey client: %w", err)
	}

	cmd := vClient.B().
		Hset().
		Key(v.client.FormatKey(v.stateKey)).
		FieldValue().
		FieldValue(stateField, state.Value).
		FieldValue(allowedReqsField, fmt.Sprintf("%.2f", state.AllowedReqs)).
		FieldValue(expiryField, strconv.FormatInt(state.Expiry.Unix(), 10)).
		Build()

	return vClient.Do(ctx, cmd).Error()
}

// Lock and Unlock for distributed locking to ensure only one instance evaluates the circuit breaker when it expires.
func (v *valkeyCircuitBreakerStore) Lock(ctx context.Context, ttl time.Duration) (bool, error) {
	vClient, err := v.client.GetClient()
	if err != nil {
		return false, fmt.Errorf("failed to get valkey client: %w", err)
	}

	cmd := vClient.B().
		Set().
		Key(v.client.FormatKey(lockKey(v.name))).
		Value("1").
		Nx().
		Px(ttl).
		Build()

	acquired, err := vClient.Do(ctx, cmd).AsBool()
	if err != nil {
		return false, fmt.Errorf("error acquiring lock: %w", err)
	}

	return acquired, nil
}

// Lock and Unlock for distributed locking to ensure only one instance evaluates the circuit breaker when it expires.
func (v *valkeyCircuitBreakerStore) Unlock(ctx context.Context) error {
	vClient, err := v.client.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get valkey client: %w", err)
	}

	cmd := vClient.B().
		Del().
		Key(v.client.FormatKey(lockKey(v.name))).
		Build()
	return vClient.Do(ctx, cmd).Error()
}
