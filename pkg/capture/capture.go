package capture

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/video-system/go-video-capture/pkg/api"
	"github.com/video-system/go-video-capture/pkg/ringbuffer"
)

// Engine is the main capture engine
type Engine struct {
	cfg       *Config
	buffer    *ringbuffer.Buffer
	apiServer *api.Server

	mu        sync.RWMutex
	isRunning bool
	sessionID string
	channelID string
}

// New creates a new capture engine
func New(cfg *Config) (*Engine, error) {
	// Create ring buffer
	bufferCfg := ringbuffer.Config{
		Duration:    cfg.Buffer.Duration,
		SegmentSize: cfg.Buffer.SegmentSize,
		Path:        cfg.Buffer.Path,
	}
	buffer, err := ringbuffer.New(bufferCfg)
	if err != nil {
		return nil, fmt.Errorf("create ring buffer: %w", err)
	}

	engine := &Engine{
		cfg:       cfg,
		buffer:    buffer,
		sessionID: cfg.Session.SessionID,
		channelID: cfg.Session.ChannelID,
	}

	// Create API server
	apiServer := api.NewServer(api.ServerConfig{
		Host:   cfg.API.Host,
		Port:   cfg.API.Port,
		Engine: engine,
	})
	engine.apiServer = apiServer

	return engine, nil
}

// Start starts the capture engine
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	if e.isRunning {
		e.mu.Unlock()
		return fmt.Errorf("capture already running")
	}
	e.isRunning = true
	e.mu.Unlock()

	log.Printf("Starting capture engine (channel: %s)", e.channelID)

	// Start API server in background
	go func() {
		if err := e.apiServer.Start(); err != nil {
			log.Printf("API server error: %v", err)
		}
	}()

	// Start ring buffer
	if err := e.buffer.Start(ctx); err != nil {
		return fmt.Errorf("start buffer: %w", err)
	}

	// TODO: Start input capture pipeline
	// TODO: Start encoder
	// TODO: Start HLS server if enabled

	// Wait for context cancellation
	<-ctx.Done()

	// Cleanup
	e.mu.Lock()
	e.isRunning = false
	e.mu.Unlock()

	e.apiServer.Stop()
	e.buffer.Stop()

	return nil
}

// SetSession updates the session context (called by operator-console)
func (e *Engine) SetSession(sessionID, channelID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sessionID = sessionID
	e.channelID = channelID
	log.Printf("Session updated: session=%s channel=%s", sessionID, channelID)
}

// GetStatus returns the current capture status
func (e *Engine) GetStatus() Status {
	e.mu.RLock()
	defer e.mu.RUnlock()

	bufferStatus := e.buffer.GetStatus()

	return Status{
		IsRunning:    e.isRunning,
		SessionID:    e.sessionID,
		ChannelID:    e.channelID,
		BufferHealth: bufferStatus.Health,
		OldestTime:   bufferStatus.OldestTime,
		NewestTime:   bufferStatus.NewestTime,
		SegmentCount: bufferStatus.SegmentCount,
	}
}

// GenerateClip generates a clip from the ring buffer
func (e *Engine) GenerateClip(ctx context.Context, req ClipRequest) (*ClipResult, error) {
	return e.buffer.GenerateClip(ctx, req.StartTime, req.EndTime, req.PlayID)
}

// StartGhostClip starts ghost-clipping mode for a play
func (e *Engine) StartGhostClip(playID string) error {
	return e.buffer.StartGhostClip(playID)
}

// EndGhostClip ends ghost-clipping mode
func (e *Engine) EndGhostClip(playID string) error {
	return e.buffer.EndGhostClip(playID)
}

// Status represents the current engine status
type Status struct {
	IsRunning    bool    `json:"is_running"`
	SessionID    string  `json:"session_id"`
	ChannelID    string  `json:"channel_id"`
	BufferHealth float64 `json:"buffer_health"`
	OldestTime   int64   `json:"oldest_time"`
	NewestTime   int64   `json:"newest_time"`
	SegmentCount int     `json:"segment_count"`
}

// ClipRequest represents a request to generate a clip
type ClipRequest struct {
	StartTime int64  `json:"start_time"` // Unix timestamp ms
	EndTime   int64  `json:"end_time"`   // Unix timestamp ms
	PlayID    string `json:"play_id,omitempty"`
}

// ClipResult represents the result of clip generation
type ClipResult struct {
	FilePath      string  `json:"file_path"`
	Duration      float64 `json:"duration"`
	FileSizeBytes int64   `json:"file_size_bytes"`
}
