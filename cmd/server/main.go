package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kamini/latency-intelligence/internal/api"
	"github.com/kamini/latency-intelligence/internal/collector"
	"github.com/kamini/latency-intelligence/internal/config"
	"github.com/kamini/latency-intelligence/internal/regression"
	"github.com/kamini/latency-intelligence/internal/store"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("🚀 Starting Latency Intelligence Platform...")

	// — Config
	cfg := config.Load()

	// — Storage
	chStore, err := store.NewClickHouseStore(cfg)
	if err != nil {
		log.Fatalf("ClickHouse init failed: %v", err)
	}
	defer chStore.Close()

	// — Regression Detector
	detector := regression.NewDetector(chStore, cfg)

	// — OTLP gRPC Receiver (passive mode)
	otlpReceiver := collector.NewOTLPReceiver(chStore, cfg.GRPCPort)
	if err := otlpReceiver.Start(); err != nil {
		log.Fatalf("OTLP receiver failed to start: %v", err)
	}
	defer otlpReceiver.Stop()

	// — HTTP API
	handler := api.NewHandler(chStore, detector, cfg)
	router := api.NewRouter(handler)

	srv := &http.Server{
		Addr:         ":" + cfg.HTTPPort,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// — Start HTTP server
	go func() {
		log.Printf("🌐 HTTP API listening on :%s", cfg.HTTPPort)

		if err := srv.ListenAndServe(); err != nil &&
			err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	log.Println("✅ Latency Intelligence Platform ready")
	log.Printf("   REST API  → http://localhost:%s", cfg.HTTPPort)
	log.Printf("   OTLP gRPC → grpc://localhost:%s", cfg.GRPCPort)

	// — Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	<-quit

	log.Println("⏳ Shutting down gracefully...")

	ctx, cancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("HTTP shutdown error: %v", err)
	}

	log.Println("👋 Shutdown complete")
}
