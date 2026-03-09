package main

import (
	"context"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
)

func main() {
	r := gin.Default()

	resultSuccessStatusFlag := true
	errorRate := 0.0

	r.GET("/hello", func(c *gin.Context) {
		if resultSuccessStatusFlag {
			r := rand.Float64()
			if r < errorRate {
				slog.Info("Simulating error response for /hello endpoint based on error rate", "error_rate", errorRate, "random_value", r)
				c.String(500, "Internal Server Error")
				return
			}

			slog.Info("Returning success response for /hello endpoint")
			c.String(200, "Hello, World!")
			return
		}

		slog.Info("Returning error response for /hello endpoint")
		c.String(500, "Internal Server Error")
	})
	r.POST("/toggle", func(c *gin.Context) {
		resultSuccessStatusFlag = !resultSuccessStatusFlag
		c.String(200, "Toggled result success status to %v", resultSuccessStatusFlag)
	})
	r.POST("/set", func(c *gin.Context) {
		var req struct {
			ErrorRate float64 `json:"error_rate"`
		}
		if err := c.BindJSON(&req); err != nil {
			c.String(400, "Invalid request body: %v", err)
			return
		}
		errorRate = req.ErrorRate
		c.String(200, "Set error rate to %f", errorRate)
	})
	r.GET("/status", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"result_success_status": resultSuccessStatusFlag,
			"error_rate":           errorRate,
		})
	})

	srv := &http.Server{
		Addr:    ":8181",
		Handler: r,
	}

	slog.Info("Starting server on :8181")
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
