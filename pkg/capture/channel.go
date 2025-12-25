package capture

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/video-system/go-video-capture/internal/ffmpeg"
	"github.com/video-system/go-video-capture/pkg/ndi"
	"github.com/video-system/go-video-capture/pkg/platform"
	"github.com/video-system/go-video-capture/pkg/ringbuffer"
)

// Channel represents a single video capture channel
// Each channel has its own FFmpeg process, ring buffer, and segment storage
type Channel struct {
	id       string
	cfg      ChannelConfig
	ffmpeg   *ffmpeg.FFmpeg
	buffer   *ringbuffer.Buffer
	writer   *ffmpeg.SegmentWriter
	platform *platform.Client

	// Native NDI capture (used when input type is "ndi")
	ndiCapture *ndi.Capture

	mu          sync.RWMutex
	isRunning   bool
	isCapturing bool
	sessionID   string
	basePath    string // Base path for segments (channel subdir added)

	ctx    context.Context
	cancel context.CancelFunc
}

// ChannelConfig holds per-channel configuration
type ChannelConfig struct {
	ID     string       `yaml:"id"`
	Input  InputConfig  `yaml:"input"`
	Buffer BufferConfig `yaml:"buffer"`
	Encode EncodeConfig `yaml:"encode"`
}

// NewChannel creates a new capture channel
func NewChannel(id string, cfg ChannelConfig, ff *ffmpeg.FFmpeg, platformClient *platform.Client, sessionID string, basePath string) (*Channel, error) {
	// Channel gets its own subdirectory
	channelPath := filepath.Join(basePath, id)

	// Create ring buffer for this channel
	bufferCfg := ringbuffer.Config{
		Duration:    cfg.Buffer.Duration,
		SegmentSize: cfg.Buffer.SegmentSize,
		Path:        channelPath,
		ChannelID:   id,
	}
	buffer, err := ringbuffer.New(bufferCfg, ff)
	if err != nil {
		return nil, fmt.Errorf("create ring buffer for channel %s: %w", id, err)
	}

	ch := &Channel{
		id:        id,
		cfg:       cfg,
		ffmpeg:    ff,
		buffer:    buffer,
		platform:  platformClient,
		sessionID: sessionID,
		basePath:  channelPath,
	}

	// Set up segment callback
	buffer.OnSegment(func(seg *ringbuffer.Segment) {
		log.Printf("[%s] Segment %d ready: %s (%.2f KB)",
			id, seg.Sequence, seg.FilePath, float64(seg.SizeBytes)/1024)
	})

	// Set up ghost segment callback - notify platform of each segment during ghost clip
	buffer.OnGhostSegment(func(playID string, seg *ringbuffer.Segment) {
		log.Printf("[%s] Ghost segment for %s: seq=%d", id, playID, seg.Sequence)

		// Notify platform of segment (non-blocking)
		if ch.platform != nil && ch.platform.IsConfigured() {
			go func() {
				notifyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				// Build segment URL (HLS path on this capture machine)
				segmentURL := fmt.Sprintf("/hls/%s/segment_%05d.m4s", id, seg.Sequence)

				if err := ch.platform.NotifySegmentReady(notifyCtx, platform.SegmentNotification{
					PlayID:     playID,
					ChannelID:  id,
					SegmentURL: segmentURL,
					Sequence:   seg.Sequence,
					Timestamp:  seg.StartTime.UnixMilli(),
					IsFinal:    false,
				}); err != nil {
					log.Printf("[%s] Failed to notify platform of segment: %v", id, err)
				}
			}()
		}
	})

	return ch, nil
}

// ID returns the channel identifier
func (ch *Channel) ID() string {
	return ch.id
}

// Start starts the channel capture
func (ch *Channel) Start(ctx context.Context) error {
	ch.mu.Lock()
	if ch.isRunning {
		ch.mu.Unlock()
		return fmt.Errorf("channel %s already running", ch.id)
	}
	ch.isRunning = true
	ch.ctx, ch.cancel = context.WithCancel(ctx)
	ch.mu.Unlock()

	log.Printf("[%s] Starting channel", ch.id)

	// Start ring buffer
	if err := ch.buffer.Start(ch.ctx); err != nil {
		return fmt.Errorf("start buffer: %w", err)
	}

	// Start capture if input is configured
	if ch.cfg.Input.Type != "" && ch.cfg.Input.Device != "" {
		if err := ch.startCapture(); err != nil {
			log.Printf("[%s] Warning: failed to start capture: %v", ch.id, err)
		}
	}

	return nil
}

// Stop stops the channel capture
func (ch *Channel) Stop() {
	ch.mu.Lock()
	defer ch.mu.Unlock()

	if ch.cancel != nil {
		ch.cancel()
	}
	ch.stopCapture()
	ch.buffer.Stop()
	ch.isRunning = false
	log.Printf("[%s] Channel stopped", ch.id)
}

// startCapture starts the FFmpeg segment writer or native NDI capture
func (ch *Channel) startCapture() error {
	cfg := ch.cfg

	// Handle NDI with native capture
	if cfg.Input.Type == "ndi" {
		return ch.startNDICapture()
	}

	// Build input string based on type
	var input string
	var inputFormat string

	switch cfg.Input.Type {
	case "file":
		input = cfg.Input.Device
	case "srt":
		// SRT input - Device should be full URL like srt://host:port
		input = cfg.Input.Device
		// No inputFormat needed - FFmpeg auto-detects from URL
	case "rtsp":
		// RTSP input - Device should be full URL like rtsp://host:port/path
		input = cfg.Input.Device
	case "rtmp":
		// RTMP input - Device should be full URL like rtmp://host:port/app/stream
		input = cfg.Input.Device
	case "screen":
		input = "0:none"
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
	ch.writer = ch.ffmpeg.NewSegmentWriter(ffmpeg.SegmentConfig{
		Input:           input,
		InputFormat:     inputFormat,
		Codec:           cfg.Encode.Codec,
		Preset:          cfg.Encode.Preset,
		Bitrate:         cfg.Encode.Bitrate,
		GOP:             cfg.Encode.GOP,
		BFrames:         cfg.Encode.BFrames,
		SegmentDuration: cfg.Buffer.SegmentSize.Seconds(),
		OutputDir:       ch.basePath,
	})

	// Wire up segment callback
	ch.writer.OnSegment(func(info ffmpeg.SegmentInfo) {
		ch.buffer.AddSegment(&ringbuffer.Segment{
			Sequence:  info.Sequence,
			FilePath:  info.Path,
			StartTime: info.StartTime,
			Duration:  info.Duration,
			SizeBytes: info.Size,
		})
	})

	// Start writing segments
	if err := ch.writer.Start(ch.ctx); err != nil {
		return fmt.Errorf("start segment writer: %w", err)
	}

	// Set init segment path
	ch.buffer.SetInitSegment(filepath.Join(ch.basePath, "init.mp4"))

	ch.mu.Lock()
	ch.isCapturing = true
	ch.mu.Unlock()

	log.Printf("[%s] Capture started: %s -> %s", ch.id, input, ch.basePath)
	return nil
}

// startNDICapture starts native NDI capture
func (ch *Channel) startNDICapture() error {
	cfg := ch.cfg

	// Check if NDI SDK is available
	if !ndi.IsAvailable() {
		return fmt.Errorf("NDI SDK not available - please install NDI SDK from https://ndi.video/tools/")
	}

	log.Printf("[%s] Starting native NDI capture: %s", ch.id, cfg.Input.Device)

	// Create NDI capture
	capture, err := ndi.NewCapture(ndi.CaptureConfig{
		SourceName:      cfg.Input.Device,
		OutputDir:       ch.basePath,
		SegmentDuration: cfg.Buffer.SegmentSize.Seconds(),
		Codec:           cfg.Encode.Codec,
		Preset:          cfg.Encode.Preset,
		Bitrate:         cfg.Encode.Bitrate,
	})
	if err != nil {
		return fmt.Errorf("create NDI capture: %w", err)
	}

	ch.ndiCapture = capture

	// Start capture
	if err := capture.Start(ch.ctx); err != nil {
		return fmt.Errorf("start NDI capture: %w", err)
	}

	// Set init segment path
	ch.buffer.SetInitSegment(filepath.Join(ch.basePath, "init.mp4"))

	ch.mu.Lock()
	ch.isCapturing = true
	ch.mu.Unlock()

	log.Printf("[%s] NDI capture started: %s -> %s", ch.id, cfg.Input.Device, ch.basePath)
	return nil
}

// stopCapture stops the FFmpeg segment writer or NDI capture
func (ch *Channel) stopCapture() {
	if ch.writer != nil {
		ch.writer.Stop()
		ch.writer = nil
	}
	if ch.ndiCapture != nil {
		ch.ndiCapture.Stop()
		ch.ndiCapture = nil
	}
	ch.isCapturing = false
}

// SetSession updates the session ID
func (ch *Channel) SetSession(sessionID string) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	ch.sessionID = sessionID
}

// GetStatus returns the channel status (implements api.ChannelInterface)
func (ch *Channel) GetStatus() interface{} {
	ch.mu.RLock()
	defer ch.mu.RUnlock()

	bufferStatus := ch.buffer.GetStatus()

	return ChannelStatus{
		ChannelID:    ch.id,
		IsRunning:    ch.isRunning,
		IsCapturing:  ch.isCapturing,
		SessionID:    ch.sessionID,
		BufferHealth: bufferStatus.Health,
		OldestTime:   bufferStatus.OldestTime,
		NewestTime:   bufferStatus.NewestTime,
		SegmentCount: bufferStatus.SegmentCount,
		InitSegment:  bufferStatus.InitSegment,
	}
}

// StartGhostClip starts ghost-clipping mode for a play
func (ch *Channel) StartGhostClip(playID string) error {
	return ch.buffer.StartGhostClip(playID)
}

// EndGhostClip ends ghost-clipping mode
func (ch *Channel) EndGhostClip(playID string) error {
	_, err := ch.buffer.EndGhostClip(playID)
	return err
}

// EndGhostClipAndGenerate ends ghost-clipping and generates the clip (implements api.ChannelInterface)
func (ch *Channel) EndGhostClipAndGenerate(ctx context.Context, playID string, tags map[string]interface{}) (interface{}, error) {
	ch.mu.RLock()
	sessionID := ch.sessionID
	ch.mu.RUnlock()

	// End ghost clip to get segment info
	ghostResult, err := ch.buffer.EndGhostClip(playID)
	if err != nil {
		return nil, err
	}

	// Send final segment notification to platform (IsFinal = true)
	if ch.platform != nil && ch.platform.IsConfigured() {
		go func() {
			notifyCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			// Use the last segment's sequence for the final notification
			lastSeq := 0
			if len(ghostResult.Segments) > 0 {
				lastSeq = ghostResult.Segments[len(ghostResult.Segments)-1]
			}

			if err := ch.platform.NotifySegmentReady(notifyCtx, platform.SegmentNotification{
				PlayID:     playID,
				ChannelID:  ch.id,
				SegmentURL: fmt.Sprintf("/hls/%s/segment_%05d.m4s", ch.id, lastSeq),
				Sequence:   lastSeq,
				Timestamp:  ghostResult.EndTime.UnixMilli(),
				IsFinal:    true,
			}); err != nil {
				log.Printf("[%s] Failed to send final segment notification: %v", ch.id, err)
			}
		}()
	}

	// Generate clip from the tracked segments
	clipResult, err := ch.buffer.GenerateClipFromSegments(ctx, ghostResult.Segments, playID)
	if err != nil {
		return nil, fmt.Errorf("generate clip: %w", err)
	}

	startMs := ghostResult.StartTime.UnixMilli()
	endMs := ghostResult.EndTime.UnixMilli()

	result := &ClipResultWithTags{
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
		ChannelID: ch.id,
		SessionID: sessionID,
	}

	// Upload to platform if configured
	if ch.platform != nil && ch.platform.IsConfigured() {
		uploadCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		go func() {
			defer cancel()
			ch.uploadClipToPlatform(uploadCtx, clipResult.FilePath, platform.ClipMetadata{
				SessionID:       sessionID,
				ChannelID:       ch.id,
				PlayID:          playID,
				StartTime:       startMs,
				EndTime:         endMs,
				DurationSeconds: clipResult.Duration,
				FileSizeBytes:   clipResult.FileSizeBytes,
				Tags:            tags,
			})
		}()
	}

	return result, nil
}

// GenerateClip generates a clip from the ring buffer by time range (implements api.ChannelInterface)
func (ch *Channel) GenerateClip(ctx context.Context, startTime, endTime int64, playID string) (interface{}, error) {
	ch.mu.RLock()
	sessionID := ch.sessionID
	ch.mu.RUnlock()

	result, err := ch.buffer.GenerateClip(ctx, startTime, endTime, playID)
	if err != nil {
		return nil, err
	}

	clipResult := &ClipResult{
		FilePath:      result.FilePath,
		Duration:      result.Duration,
		FileSizeBytes: result.FileSizeBytes,
		SegmentCount:  result.SegmentCount,
	}

	// Upload to platform if configured
	if ch.platform != nil && ch.platform.IsConfigured() {
		uploadCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		go func() {
			defer cancel()
			ch.uploadClipToPlatform(uploadCtx, result.FilePath, platform.ClipMetadata{
				SessionID:       sessionID,
				ChannelID:       ch.id,
				PlayID:          playID,
				StartTime:       startTime,
				EndTime:         endTime,
				DurationSeconds: result.Duration,
				FileSizeBytes:   result.FileSizeBytes,
			})
		}()
	}

	return clipResult, nil
}

// uploadClipToPlatform uploads a clip to the video-platform
func (ch *Channel) uploadClipToPlatform(ctx context.Context, filePath string, metadata platform.ClipMetadata) {
	result, err := ch.platform.UploadClip(ctx, filePath, metadata)
	if err != nil {
		log.Printf("[%s] Failed to upload clip to platform: %v", ch.id, err)
		return
	}
	log.Printf("[%s] Clip uploaded to platform: %s (size: %d bytes)", ch.id, result.FilePath, result.FileSize)
}

// GetHLSPlaylist generates a live HLS playlist
func (ch *Channel) GetHLSPlaylist() ([]byte, error) {
	status := ch.buffer.GetStatus()
	if status.SegmentCount == 0 {
		return nil, fmt.Errorf("no segments available")
	}

	segmentDuration := ch.cfg.Buffer.SegmentSize.Seconds()

	var playlist string
	playlist += "#EXTM3U\n"
	playlist += "#EXT-X-VERSION:7\n"
	playlist += fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", int(segmentDuration)+1)
	playlist += fmt.Sprintf("#EXT-X-MEDIA-SEQUENCE:%d\n", status.FirstSeq)
	playlist += "#EXT-X-MAP:URI=\"init.mp4\"\n"

	for seq := status.FirstSeq; seq <= status.LastSeq; seq++ {
		seg, ok := ch.buffer.GetSegment(seq)
		if !ok {
			continue
		}
		playlist += fmt.Sprintf("#EXTINF:%.3f,\n", seg.Duration.Seconds())
		playlist += fmt.Sprintf("segment_%05d.m4s\n", seg.Sequence)
	}

	return []byte(playlist), nil
}

// GetSegmentPath returns the path where segments are stored
func (ch *Channel) GetSegmentPath() string {
	return ch.basePath
}

// GetInitSegmentPath returns the path to the init segment
func (ch *Channel) GetInitSegmentPath() string {
	return ch.buffer.GetInitSegment()
}

// IsRecording returns true if the channel is actively capturing
func (ch *Channel) IsRecording() bool {
	ch.mu.RLock()
	defer ch.mu.RUnlock()
	return ch.isCapturing
}

// GetError returns the current error state, if any
func (ch *Channel) GetError() error {
	ch.mu.RLock()
	defer ch.mu.RUnlock()

	// Check if the segment writer has errors
	if ch.writer != nil {
		return ch.writer.GetError()
	}
	return nil
}

// ChannelStatus represents the status of a channel
type ChannelStatus struct {
	ChannelID    string  `json:"channel_id"`
	IsRunning    bool    `json:"is_running"`
	IsCapturing  bool    `json:"is_capturing"`
	SessionID    string  `json:"session_id"`
	BufferHealth float64 `json:"buffer_health"`
	OldestTime   int64   `json:"oldest_time"`
	NewestTime   int64   `json:"newest_time"`
	SegmentCount int     `json:"segment_count"`
	InitSegment  string  `json:"init_segment"`
}
