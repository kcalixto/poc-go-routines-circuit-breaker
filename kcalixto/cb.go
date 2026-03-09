package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const DEFAULT_TIME_WINDOW = time.Minute * 5

// ****************
// * CIRCUIT BREAKER ORGANISM
// ****************

type CircuitBreakerManager struct {
	settings []CBSettings
	rc       *redis.Client
	cbs      []*CircuitBreaker
}

func NewCircuitBreakerManager(settings []CBSettings) (*CircuitBreakerManager, error) {
	rc := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       0,
	})

	cbs := make([]*CircuitBreaker, len(settings))
	for i, st := range settings {
		cbs[i] = NewCircuitBreaker(st)
	}

	return &CircuitBreakerManager{
		rc:       rc,
		settings: settings,
		cbs:      cbs,
	}, nil
}

func (cbm *CircuitBreakerManager) Print() {
	for _, cb := range cbm.cbs {
		fmt.Printf("Circuit Breaker: %s - %s\n", cb.settings.Identifier, cb.State())
	}
}

// ****************
// * CIRCUIT BREAKER CELL
// ****************

type CircuitBreaker struct {
	state      CBState
	generation uint64
	expiry     time.Time
	mutex      sync.Mutex
	settings   CBSettings
	counts     CBCounts
}

func NewCircuitBreaker(settings CBSettings) *CircuitBreaker {
	cb := &CircuitBreaker{
		settings: settings,
		counts:   CBCounts{},
	}

	cb.toNewGeneration(time.Now())

	return cb
}

func (cb *CircuitBreaker) toNewGeneration(now time.Time) {
	cb.generation++
	cb.counts.Reset()
	cb.expiry = now.Add(DEFAULT_TIME_WINDOW)
}

func (cb *CircuitBreaker) State() CBState {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	return cb.state
}

func (cb *CircuitBreaker) setState(newState CBState) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	cb.state = newState
}

func (cb *CircuitBreaker) IsCallPermited(ctx context.Context) (bool, error) {
	cb.mutex.Lock()
	defer cb.mutex.Unlock()

	return false, nil
}

func (cb *CircuitBreakerManager) OnStateChange(ctx context.Context, identifier string, newState string) error {
	return nil
}

// func (cb *CircuitBreaker) RateLimit(ctx context.Context, key string, limit, expirationInSeconds int) (isLimited bool, err error) {
// 	var (
// 		maxCacheHitRetries = 10000
// 		IS_LIMITED         = true
// 		NOT_LIMITED        = false
// 		expiration         = time.Second * time.Duration(expirationInSeconds)
// 		HAS_NO_EXPIRATION  = -1
// 	)
//
// 	isLimited = IS_LIMITED
//
// 	src := rand.NewSource(time.Now().UnixNano())
// 	r := rand.New(src)
// 	for range maxCacheHitRetries {
// 		err := cb.rc.Watch(ctx, func(tx *redis.Tx) error {
// 			// Try to get the key
// 			counter, err := tx.Get(ctx, key).Int()
// 			if err != nil {
// 				if err != redis.Nil {
// 					return err
// 				}
//
// 				// If the key doesn't exist, initialize it with the limit and duration
// 				pipe := tx.TxPipeline()
// 				pipe.Set(ctx, key, limit, expiration)
// 				counter = limit
//
// 				_, err := pipe.Exec(ctx)
// 				if err != nil {
// 					log.Print("error at initialization: ", err)
// 					return err
// 				}
// 			}
//
// 			if counter <= 0 {
// 				log.Printf("limit exceeded, waiting until expiration => %d", counter)
// 				currentExpiration := tx.TTL(ctx, key).Val()
// 				if currentExpiration > time.Second*60 || currentExpiration == time.Duration(HAS_NO_EXPIRATION) {
// 					tx.Del(ctx, key)
// 				}
// 				return nil
// 			}
//
// 			// Decrement the value of the key and check if it's negative (reached the limit)
// 			pipe := tx.TxPipeline()
// 			pipe.Decr(ctx, key)
// 			pipe.TTL(ctx, key)
//
// 			results, err := pipe.Exec(ctx)
// 			if err != nil {
// 				log.Print("error at limit update: ", err)
// 				return err
// 			}
//
// 			// Get the decremented value and the remaining time until expiration
// 			decremented := results[0].(*redis.IntCmd).Val()
// 			expiration := results[1].(*redis.DurationCmd).Val()
// 			log.Printf("decremented: %d, expiration: %v", decremented, expiration)
//
// 			if decremented < 0 {
// 				log.Print("limit exceeded")
// 				// Rate limit exceeded
// 				isLimited = IS_LIMITED
// 				return nil
// 			}
//
// 			// Rate limit not exceeded
// 			log.Print("limit not exceeded")
// 			isLimited = NOT_LIMITED
//
// 			return nil
// 		}, key)
//
// 		if err == nil {
// 			return isLimited, nil
// 		}
//
// 		if err == redis.TxFailedErr {
// 			sleepDuration := 1 + r.Intn(2)
// 			time.Sleep(time.Duration(sleepDuration) * time.Millisecond)
// 			continue
// 		}
//
// 		return isLimited, err
// 	}
//
// 	return isLimited, err
// }
