package capture

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/video-system/go-video-capture/internal/ffmpeg"
	"github.com/video-system/go-video-capture/pkg/api"
	"github.com/video-system/go-video-capture/pkg/platform"
	"github.com/video-system/go-video-capture/pkg/ringbuffer"
)

// Engine is the main capture engine
type Engine struct {
	cfg      *Config
	ffmpeg   *ffmpeg.FFmpeg
	buffer   *ringbuffer.Buffer
	writer   *ffmpeg.SegmentWriter
	api      *api.Server
	platform *platform.Client

	mu        sync.RWMutex
	isRunning bool
	sessionID string
	channelID string

	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a new capture engine
func New(cfg *Config) (*Engine, error) {
	// Initialize FFmpeg
	ff, err := ffmpeg.New()
	if err != nil {
		return nil, fmt.Errorf("init ffmpeg: %w", err)
	}

	// Verify FFmpeg version
	version, err := ff.Version(context.Background())
	if err != nil {
		return nil, fmt.Errorf("get ffmpeg version: %w", err)
	}
	log.Printf("FFmpeg: %s", version)

	// Create ring buffer
	bufferCfg := ringbuffer.Config{
		Duration:    cfg.Buffer.Duration,
		SegmentSize: cfg.Buffer.SegmentSize,
		Path:        cfg.Buffer.Path,
		ChannelID:   cfg.Session.ChannelID,
	}
	buffer, err := ringbuffer.New(bufferCfg, ff)
	if err != nil {
		return nil, fmt.Errorf("create ring buffer: %w", err)
	}

	// Create platform client if configured
	var platformClient *platform.Client
	if cfg.Platform.Enabled && cfg.Platform.URL != "" {
		platformClient = platform.New(platform.Config{
			URL:    cfg.Platform.URL,
			APIKey: cfg.Platform.APIKey,
		})
		log.Printf("Platform integration enabled: %s", cfg.Platform.URL)
	}

	engine := &Engine{
		cfg:       cfg,
		ffmpeg:    ff,
		buffer:    buffer,
		platform:  platformClient,
		sessionID: cfg.Session.SessionID,
		channelID: cfg.Session.ChannelID,
	}

	// Set up segment callback
	buffer.OnSegment(func(seg *ringbuffer.Segment) {
		log.Printf("Segment %d ready: %s (%.2f KB)",
			seg.Sequence, seg.FilePath, float64(seg.SizeBytes)/1024)
	})

	// Set up ghost segment callback
	buffer.OnGhostSegment(func(playID string, seg *ringbuffer.Segment) {
		log.Printf("Ghost segment for %s: seq=%d", playID, seg.Sequence)
		// TODO: Emit WebSocket event
	})

	// Create API server
	apiServer := api.NewServer(api.ServerConfig{
		Host:   cfg.API.Host,
		Port:   cfg.API.Port,
		Engine: engine,
	})
	engine.api = apiServer

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
	e.ctx, e.cancel = context.WithCancel(ctx)
	e.mu.Unlock()

	log.Printf("Starting capture engine (channel: %s)", e.channelID)

	// Start ring buffer
	if err := e.buffer.Start(e.ctx); err != nil {
		return fmt.Errorf("start buffer: %w", err)
	}

	// Start segment writer if input is configured
	if e.cfg.Input.Type != "" && e.cfg.Input.Device != "" {
		if err := e.startCapture(); err != nil {
			log.Printf("Warning: failed to start capture: %v", err)
			// Continue anyway - can be started later via API
		}
	}

	// Start API server in background
	go func() {
		if err := e.api.Start(); err != nil {
			log.Printf("API server error: %v", err)
		}
	}()

	// Wait for context cancellation
	<-e.ctx.Done()

	// Cleanup
	e.stopCapture()
	e.buffer.Stop()
	e.api.Stop()

	e.mu.Lock()
	e.isRunning = false
	e.mu.Unlock()

	return nil
}

// startCapture starts the FFmpeg segment writer
func (e *Engine) startCapture() error {
	cfg := e.cfg

	// Build input string based on type
	var input string
	var inputFormat string

	switch cfg.Input.Type {
	case "file":
		input = cfg.Input.Device
	case "screen":
		// macOS screen capture
		input = "0:none" // Screen 0, no audio
		inputFormat = "avfoundation"
	case "avfoundation":
		input = cfg.Input.Device
		inputFormat = "avfoundation"
	case "v4l2":
		input = cfg.Input.Device
		inputFormat = "v4l2"
	case "dshow":
		input = cfg.Input.Device
		inputFormat = "dshow"
	case "decklink":
		input = cfg.Input.Device
		inputFormat = "decklink"
	default:
		return fmt.Errorf("unknown input type: %s", cfg.Input.Type)
	}

	// Create segment writer
	e.writer = e.ffmpeg.NewSegmentWriter(ffmpeg.SegmentConfig{
		Input:           input,
		InputFormat:     inputFormat,
		Codec:           cfg.Encode.Codec,
		Preset:          cfg.Encode.Preset,
		Bitrate:         cfg.Encode.Bitrate,
		SegmentDuration: cfg.Buffer.SegmentSize.Seconds(),
		OutputDir:       cfg.Buffer.Path,
	})

	// Wire up segment callback
	e.writer.OnSegment(func(info ffmpeg.SegmentInfo) {
		e.buffer.AddSegment(&ringbuffer.Segment{
			Sequence:  info.Sequence,
			FilePath:  info.Path,
			StartTime: info.StartTime,
			Duration:  info.Duration,
			SizeBytes: info.Size,
		})
	})

	// Start writing segments
	if err := e.writer.Start(e.ctx); err != nil {
		return fmt.Errorf("start segment writer: %w", err)
	}

	// Set init segment path
	e.buffer.SetInitSegment(cfg.Buffer.Path + "/init.mp4")

	log.Printf("Capture started: %s -> %s", input, cfg.Buffer.Path)
	return nil
}

// stopCapture stops the FFmpeg segment writer
func (e *Engine) stopCapture() {
	if e.writer != nil {
		e.writer.Stop()
		e.writer = nil
	}
}

// Stop gracefully stops the engine
func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
}

// SetSession updates the session context
func (e *Engine) SetSession(sessionID, channelID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sessionID = sessionID
	e.channelID = channelID
	log.Printf("Session updated: session=%s channel=%s", sessionID, channelID)
}

// GetStatus returns the current capture status
func (e *Engine) GetStatus() interface{} {
	e.mu.RLock()
	defer e.mu.RUnlock()

	bufferStatus := e.buffer.GetStatus()

	return Status{
		IsRunning:    e.isRunning,
		IsCapturing:  e.writer != nil,
		SessionID:    e.sessionID,
		ChannelID:    e.channelID,
		BufferHealth: bufferStatus.Health,
		OldestTime:   bufferStatus.OldestTime,
		NewestTime:   bufferStatus.NewestTime,
		SegmentCount: bufferStatus.SegmentCount,
		InitSegment:  bufferStatus.InitSegment,
	}
}

// GenerateClip generates a clip from the ring buffer
func (e *Engine) GenerateClip(ctx context.Context, req interface{}) (interface{}, error) {
	clipReq, ok := req.(ClipRequest)
	if !ok {
		// Try to extract from map
		if m, ok := req.(map[string]interface{}); ok {
			clipReq = ClipRequest{
				StartTime: int64(m["start_time"].(float64)),
				EndTime:   int64(m["end_time"].(float64)),
				PlayID:    m["play_id"].(string),
			}
		} else {
			return nil, fmt.Errorf("invalid clip request type")
		}
	}

	result, err := e.buffer.GenerateClip(ctx, clipReq.StartTime, clipReq.EndTime, clipReq.PlayID)
	if err != nil {
		return nil, err
	}

	clipResult := ClipResult{
		FilePath:      result.FilePath,
		Duration:      result.Duration,
		FileSizeBytes: result.FileSizeBytes,
		SegmentCount:  result.SegmentCount,
	}

	// Upload to platform if configured
	if e.platform != nil && e.platform.IsConfigured() {
		go e.uploadClipToPlatform(ctx, result.FilePath, platform.ClipMetadata{
			SessionID:       e.sessionID,
			ChannelID:       e.channelID,
			PlayID:          clipReq.PlayID,
			StartTime:       clipReq.StartTime,
			EndTime:         clipReq.EndTime,
			DurationSeconds: result.Duration,
			FileSizeBytes:   result.FileSizeBytes,
		})
	}

	return clipResult, nil
}

// StartGhostClip starts ghost-clipping mode for a play
func (e *Engine) StartGhostClip(playID string) error {
	return e.buffer.StartGhostClip(playID)
}

// EndGhostClip ends ghost-clipping mode
func (e *Engine) EndGhostClip(playID string) error {
	_, err := e.buffer.EndGhostClip(playID)
	return err
}

// EndGhostClipAndGenerate ends ghost-clipping and generates the clip
func (e *Engine) EndGhostClipAndGenerate(ctx context.Context, playID string, tags map[string]interface{}) (interface{}, error) {
	// End ghost clip to get time range
	ghostResult, err := e.buffer.EndGhostClip(playID)
	if err != nil {
		return nil, err
	}

	// Generate clip from the time range
	startMs := ghostResult.StartTime.UnixMilli()
	endMs := ghostResult.EndTime.UnixMilli()

	clipResult, err := e.buffer.GenerateClip(ctx, startMs, endMs, playID)
	if err != nil {
		return nil, fmt.Errorf("generate clip: %w", err)
	}

	result := ClipResultWithTags{
		ClipResult: ClipResult{
			FilePath:      clipResult.FilePath,
			Duration:      clipResult.Duration,
			FileSizeBytes: clipResult.FileSizeBytes,
			SegmentCount:  clipResult.SegmentCount,
		},
		PlayID:    playID,
		StartTime: startMs,
		EndTime:   endMs,
		Tags:      tags,
		ChannelID: e.channelID,
		SessionID: e.sessionID,
	}

	// Upload to platform if configured
	if e.platform != nil && e.platform.IsConfigured() {
		go e.uploadClipToPlatform(ctx, clipResult.FilePath, platform.ClipMetadata{
			SessionID:       e.sessionID,
			ChannelID:       e.channelID,
			PlayID:          playID,
			StartTime:       startMs,
			EndTime:         endMs,
			DurationSeconds: clipResult.Duration,
			FileSizeBytes:   clipResult.FileSizeBytes,
			Tags:            tags,
		})
	}

	return result, nil
}

// uploadClipToPlatform uploads a clip to the video-platform in the background
func (e *Engine) uploadClipToPlatform(ctx context.Context, filePath string, metadata platform.ClipMetadata) {
	result, err := e.platform.UploadClip(ctx, filePath, metadata)
	if err != nil {
		log.Printf("Failed to upload clip to platform: %v", err)
		return
	}
	log.Printf("Clip uploaded to platform: %s (size: %d bytes)", result.FilePath, result.FileSize)
}

// GetBuffer returns the ring buffer (for advanced access)
func (e *Engine) GetBuffer() *ringbuffer.Buffer {
	return e.buffer
}

// Status represents the current engine status
type Status struct {
	IsRunning    bool    `json:"is_running"`
	IsCapturing  bool    `json:"is_capturing"`
	SessionID    string  `json:"session_id"`
	ChannelID    string  `json:"channel_id"`
	BufferHealth float64 `json:"buffer_health"`
	OldestTime   int64   `json:"oldest_time"`
	NewestTime   int64   `json:"newest_time"`
	SegmentCount int     `json:"segment_count"`
	InitSegment  string  `json:"init_segment"`
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
	SegmentCount  int     `json:"segment_count"`
}

// ClipResultWithTags includes clip result plus metadata
type ClipResultWithTags struct {
	ClipResult
	PlayID    string                 `json:"play_id"`
	StartTime int64                  `json:"start_time"`
	EndTime   int64                  `json:"end_time"`
	Tags      map[string]interface{} `json:"tags,omitempty"`
	ChannelID string                 `json:"channel_id"`
	SessionID string                 `json:"session_id"`
}
