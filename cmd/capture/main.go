package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/video-system/go-video-capture/pkg/api"
	"github.com/video-system/go-video-capture/pkg/capture"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	flag.Parse()

	// Load configuration
	cfg, err := capture.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Create channel manager
	manager, err := capture.NewManager(cfg)
	if err != nil {
		log.Fatalf("Failed to create manager: %v", err)
	}

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Shutdown signal received...")
		cancel()
	}()

	// Start all channels
	if err := manager.Start(ctx); err != nil {
		log.Fatalf("Failed to start channels: %v", err)
	}

	// Create and start API server
	apiServer := api.NewServer(api.ServerConfig{
		Host:    cfg.API.Host,
		Port:    cfg.API.Port,
		Manager: manager,
	})

	go func() {
		if err := apiServer.Start(); err != nil {
			log.Printf("API server error: %v", err)
		}
	}()

	// Wait for shutdown
	manager.Wait()

	// Cleanup
	manager.Stop()
	apiServer.Stop()

	log.Println("Capture stopped")
}
