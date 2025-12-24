package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/video-system/go-video-capture/pkg/api"
	"github.com/video-system/go-video-capture/pkg/capture"
	"github.com/video-system/go-video-capture/pkg/ndi"
	"github.com/video-system/go-video-capture/pkg/platform"
)

const version = "1.0.0"

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

	// Initialize platform client for agent registration
	var platformClient *platform.Client
	var agentID string
	if cfg.Platform.Enabled && cfg.Platform.URL != "" {
		platformClient = platform.New(platform.Config{
			URL:    cfg.Platform.URL,
			APIKey: cfg.Platform.APIKey,
		})

		// Register agent with platform
		agentID, err = registerAgent(ctx, platformClient, cfg)
		if err != nil {
			log.Printf("Warning: Failed to register with platform: %v", err)
		} else {
			log.Printf("Registered with platform as agent: %s", agentID)
			// Start heartbeat loop
			go runHeartbeat(ctx, platformClient, agentID, cfg, manager)
		}
	}

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

// registerAgent registers this capture agent with the video platform
func registerAgent(ctx context.Context, client *platform.Client, cfg *capture.Config) (string, error) {
	hostname, _ := os.Hostname()

	// Generate agent ID if not specified
	agentID := cfg.Platform.AgentID
	if agentID == "" {
		agentID = fmt.Sprintf("agent-%s", hostname)
	}

	// Generate agent name if not specified
	agentName := cfg.Platform.AgentName
	if agentName == "" {
		agentName = fmt.Sprintf("Capture Agent (%s)", hostname)
	}

	// Determine API URL for this agent
	agentURL := fmt.Sprintf("http://%s:%d", hostname, cfg.API.Port)
	if cfg.API.Host != "" && cfg.API.Host != "0.0.0.0" {
		agentURL = fmt.Sprintf("http://%s:%d", cfg.API.Host, cfg.API.Port)
	}

	// Check NDI support dynamically
	ndiSupported := ndi.CheckSupport(ctx)

	// Build capabilities based on config and system detection
	capabilities := platform.AgentCapabilities{
		CanCaptureSRT:   true, // Supported via FFmpeg
		CanCaptureRTSP:  true,
		CanCaptureRTMP:  true,
		CanCaptureNDI:   ndiSupported,
		CanCaptureUSB:   true,
		SupportedCodecs: []string{"h264", "hevc"},
		MaxResolution:   "3840x2160",
		MaxBitrate:      50000,
	}

	req := platform.RegisterAgentRequest{
		ID:           agentID,
		Name:         agentName,
		URL:          agentURL,
		ChannelID:    cfg.Session.ChannelID,
		Capabilities: capabilities,
		Version:      version,
		Hostname:     hostname,
	}

	agent, err := client.RegisterAgent(ctx, req)
	if err != nil {
		return "", err
	}

	return agent.ID, nil
}

// runHeartbeat runs a periodic heartbeat to keep the platform updated
func runHeartbeat(ctx context.Context, client *platform.Client, agentID string, cfg *capture.Config, manager *capture.Manager) {
	interval := 10 * time.Second
	if cfg.Platform.HeartbeatSecs > 0 {
		interval = time.Duration(cfg.Platform.HeartbeatSecs) * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Send final offline heartbeat
			offlineCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = client.Heartbeat(offlineCtx, agentID, platform.AgentHeartbeatRequest{
				Status:    platform.AgentStatusOffline,
				ChannelID: cfg.Session.ChannelID,
			})
			cancel()
			return
		case <-ticker.C:
			// Determine current status based on manager state
			status := platform.AgentStatusOnline
			var errorMsg string

			// Check if any channels are recording
			if manager.IsRecording() {
				status = platform.AgentStatusRecording
			}

			// Check for errors
			if err := manager.GetError(); err != nil {
				status = platform.AgentStatusError
				errorMsg = err.Error()
			}

			req := platform.AgentHeartbeatRequest{
				Status:       status,
				SessionID:    cfg.Session.SessionID,
				ChannelID:    cfg.Session.ChannelID,
				ErrorMessage: errorMsg,
			}

			_, err := client.Heartbeat(ctx, agentID, req)
			if err != nil {
				log.Printf("Heartbeat failed: %v", err)
			}
		}
	}
}
