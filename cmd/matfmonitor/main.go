package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joesiltberg/bowness/fedtls"
	"github.com/joesiltberg/matfmonitor/internal/checker"
	"github.com/joesiltberg/matfmonitor/internal/config"
	"github.com/joesiltberg/matfmonitor/internal/store"
	"github.com/joesiltberg/matfmonitor/internal/web"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	log.Printf("Starting matfmonitor...")
	log.Printf("Metadata URL: %s", cfg.MetadataURL)
	log.Printf("Listen address: %s", cfg.ListenAddress)

	// Initialize store
	dataStore, err := store.New(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer dataStore.Close()

	// Initialize metadata store
	metadataStore := fedtls.NewMetadataStore(
		cfg.MetadataURL,
		cfg.JWKSPath,
		cfg.CachePath,
	)

	// Initialize health checker and scheduler
	healthChecker := checker.NewRealChecker(cfg.TLSTimeout)
	scheduler := checker.NewScheduler(
		healthChecker,
		dataStore,
		metadataStore,
		cfg.MaxParallelChecks,
		cfg.ChecksPerMinute,
		cfg.MinCheckInterval,
	)

	// Initialize web handler
	webHandler, err := web.NewHandler(dataStore, metadataStore)
	if err != nil {
		log.Fatalf("Failed to initialize web handler: %v", err)
	}

	// Set up HTTP server
	server := &http.Server{
		Addr:         cfg.ListenAddress,
		Handler:      webHandler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start scheduler
	scheduler.Start()
	log.Printf("Health check scheduler started (max %d parallel, %d/min, interval %v)",
		cfg.MaxParallelChecks, cfg.ChecksPerMinute, cfg.MinCheckInterval)

	// Start HTTP server in goroutine
	go func() {
		log.Printf("Web server listening on %s", cfg.ListenAddress)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)
	sig := <-sigChan

	log.Printf("Received signal %v, shutting down...", sig)

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Stop accepting new requests
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	// Stop scheduler (waits for in-progress checks)
	scheduler.Stop()
	log.Printf("Scheduler stopped")

	// Stop metadata store
	metadataStore.Quit()
	log.Printf("Metadata store stopped")

	fmt.Println("Shutdown complete")
}
