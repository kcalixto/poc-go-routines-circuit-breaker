package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AcordoCertoBR/go-utils/pkg/valkeyr"
	"github.com/gin-gonic/gin"
	"github.com/kcalixto/poc-go-routines-circuit-breaker/with-valkey/circuitbreaker"
)

func main() {
	r := gin.Default()

	valkeyClient, err := valkeyr.New(
		valkeyr.WithAddress("localhost:6379"),
		valkeyr.WithDB(0),
		valkeyr.WithPrefix("circuitbreaker"),
		valkeyr.WithExpiration(time.Hour),
	)
	if err != nil {
		slog.Error("Failed to create ValkeyR client", "error", err)
		return
	}

	cbManager := circuitbreaker.NewCircuitBreakerManager(valkeyClient)
	handlers := NewHandlers(cbManager)

	r.GET("/test/:partner", handlers.SomeHandler)

	port := "8180"
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}
	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	slog.Info("Starting server on :" + port)
	// Start server in goroutine
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("listen error", "err", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit
	slog.Info("Shutdown signal received", "signal", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "err", err)
	} else {
		slog.Info("Server exited gracefully")
	}
}

type Handlers struct {
	cbManager *circuitbreaker.CircuitBreakerManager
	cbs       map[string]*circuitbreaker.CircuitBreakerInstance
}

func NewHandlers(cbManager *circuitbreaker.CircuitBreakerManager) *Handlers {
	return &Handlers{
		cbManager: cbManager,
		cbs:       make(map[string]*circuitbreaker.CircuitBreakerInstance),
	}
}

func (h *Handlers) SomeHandler(c *gin.Context) {
	fmt.Printf("\n\n\n\n\n\n\n\n\n\n\n")
	ctx := c.Request.Context()

	partner := c.Param("partner")
	if partner == "" {
		c.String(http.StatusBadRequest, "Partner parameter is required")
		return
	}

	cb := h.cbs[partner]
	if cb == nil {
		cb = h.newCircuitBreakerInstance(partner)
		h.cbs[partner] = cb
	}

	var retVal string
	err := cb.Execute(ctx, func() (err error) {
		retVal, err = simulatedAPICall()
		fmt.Printf("API call result: %s, error: %v\n", retVal, err)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if err == circuitbreaker.ErrOpenState {
			c.String(http.StatusServiceUnavailable, "Service is currently unavailable due to high failure rate. Please try again later.")
			return
		}
		c.String(http.StatusInternalServerError, "API call failed: %v", err)
		return
	}

	c.String(http.StatusOK, partner+"-"+retVal)
}

func (h *Handlers) newCircuitBreakerInstance(name string) *circuitbreaker.CircuitBreakerInstance {
	st := circuitbreaker.NewDefaultSettings()
	st.Name = name
	st.Interval = time.Second * 15
	st.GrowthRate = 20.0
	st.OnStateChange = func(name string, oldState, newState circuitbreaker.State, counts circuitbreaker.Counts) {
		slog.Info("Circuit breaker state changed", "name", name, "oldState", oldState.Value, "newState", newState.Value)
	}
	st.Evaluate = func(state circuitbreaker.State, counts circuitbreaker.Counts) circuitbreaker.Direction {
		// Rules
		// - minimum 15 samples to work with
		// - 95% failure rate to open the circuit
		// - reopen carefully with increase instead of switch to closed state

		fmt.Printf(`
	"requests": %d,
	"failures": %d
`, counts.Requests, counts.TotalFailures,
		)
		if counts.Requests > 15 {
			failureRate := float64(counts.TotalFailures / counts.Requests)
			if failureRate >= 0.95 {
				return circuitbreaker.Open
			}
		}

		return circuitbreaker.Increase
	}

	return h.cbManager.NewInstace(st)
}

// simulatedAPICall randomly fails or succeeds to simulate an external API call
func simulatedAPICall() (string, error) {
	client := http.Client{
		Timeout: time.Millisecond * 500, // Set a timeout for the request
	}

	req, err := http.NewRequest("GET", "http://localhost:8181/hello", nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return string(responseBody), nil
}
