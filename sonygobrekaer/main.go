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

	"github.com/gin-gonic/gin"
)

func main() {
	r := gin.Default()

	cb := newapicircuitbreaker()

	r.GET("/test", func(c *gin.Context) {
		fmt.Printf("\n\n\n\n\n\n\n\n\n\n\n")
		result, err := cb.Execute(simulatedAPICall)
		slog.Info("[]", "result", result, "error", err)
		if err != nil {
			c.String(http.StatusInternalServerError, "API call failed: %v", err)
			return
		}

		c.String(http.StatusOK, result)
	})

	srv := &http.Server{
		Addr:    ":8180",
		Handler: r,
	}

	slog.Info("Starting server on :8180")
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

func newapicircuitbreaker() *CircuitBreaker[string] {
	st := NewSettings()
	st.Name = "API Circuit Breaker"
	st.Interval = time.Second * 10
	st.GrowthRate = 20.0
	st.Evaluate = func(state State, counts *Counts) direction {
		// Rules
		// - minimum 15 samples to work with
		// - 95% failure rate to open the circuit
		// - reopen carefully with increase instead of switch to closed state

		if counts.Requests > 15 {
			failureRate := float64(counts.TotalFailures / counts.Requests)
			if failureRate >= 0.95 {
				return Open
			}
		}

		return Increase
	}

	cb := NewCircuitBreaker[string](st)
	slog.Info("Created new circuit breaker", "name", cb.Name())
	return cb
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
