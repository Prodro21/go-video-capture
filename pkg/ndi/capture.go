package ndi

import (
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"sync"
	"time"
)

// CaptureConfig configures NDI capture to FFmpeg pipeline
type CaptureConfig struct {
	SourceName      string  // NDI source name to capture
	OutputDir       string  // Directory for output segments
	SegmentDuration float64 // Segment duration in seconds
	Codec           string  // Output codec (h264, hevc)
	Preset          string  // Encoder preset
	Bitrate         int     // Target bitrate in kbps
}

// Capture handles NDI capture and encoding pipeline
type Capture struct {
	config   CaptureConfig
	receiver *Receiver

	mu         sync.RWMutex
	running    bool
	ffmpegCmd  *exec.Cmd
	ffmpegIn   io.WriteCloser
	ctx        context.Context
	cancel     context.CancelFunc
	onSegment  func(SegmentInfo)
	lastErr    error
	frameCount uint64
}

// SegmentInfo contains information about a completed segment
type SegmentInfo struct {
	Sequence  int
	Path      string
	StartTime time.Time
	Duration  time.Duration
	Size      int64
}

// NewCapture creates a new NDI capture pipeline
func NewCapture(config CaptureConfig) (*Capture, error) {
	// Create NDI receiver
	receiver, err := NewReceiver(ReceiverConfig{
		SourceName:  config.SourceName,
		ColorFormat: ColorFormatUYVYBGRA, // UYVY is efficient for encoding
		Bandwidth:   BandwidthHighest,
	})
	if err != nil {
		return nil, fmt.Errorf("create NDI receiver: %w", err)
	}

	// Set defaults
	if config.SegmentDuration == 0 {
		config.SegmentDuration = 2.0
	}
	if config.Codec == "" {
		config.Codec = "h264"
	}
	if config.Preset == "" {
		config.Preset = "fast"
	}
	if config.Bitrate == 0 {
		config.Bitrate = 6000
	}

	return &Capture{
		config:   config,
		receiver: receiver,
	}, nil
}

// OnSegment sets the callback for completed segments
func (c *Capture) OnSegment(fn func(SegmentInfo)) {
	c.onSegment = fn
}

// Start starts the capture pipeline
func (c *Capture) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return fmt.Errorf("capture already running")
	}
	c.running = true
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.mu.Unlock()

	// Get initial frame to determine resolution and framerate
	log.Printf("[NDI] Waiting for first frame from %s...", c.config.SourceName)
	var firstFrame *VideoFrame
	for i := 0; i < 50; i++ { // Try for 5 seconds
		frame, err := c.receiver.CaptureVideo(100 * time.Millisecond)
		if err != nil {
			return fmt.Errorf("capture first frame: %w", err)
		}
		if frame != nil {
			firstFrame = frame
			break
		}
	}
	if firstFrame == nil {
		return fmt.Errorf("timeout waiting for first frame from %s", c.config.SourceName)
	}

	log.Printf("[NDI] Source: %dx%d @ %d/%d fps, FourCC: 0x%08X",
		firstFrame.Width, firstFrame.Height,
		firstFrame.FrameRateN, firstFrame.FrameRateD,
		firstFrame.FourCC)

	// Start FFmpeg process
	if err := c.startFFmpeg(firstFrame); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	// Start capture loop
	go c.captureLoop()

	return nil
}

// startFFmpeg starts the FFmpeg encoding process
func (c *Capture) startFFmpeg(frame *VideoFrame) error {
	// Determine pixel format based on FourCC
	pixFmt := "uyvy422" // Default for UYVY
	switch frame.FourCC {
	case FourCCUYVY:
		pixFmt = "uyvy422"
	case FourCCBGRA:
		pixFmt = "bgra"
	case FourCCRGBA:
		pixFmt = "rgba"
	case FourCCNV12:
		pixFmt = "nv12"
	case FourCCI420:
		pixFmt = "yuv420p"
	}

	frameRate := fmt.Sprintf("%d/%d", frame.FrameRateN, frame.FrameRateD)
	resolution := fmt.Sprintf("%dx%d", frame.Width, frame.Height)

	// Build FFmpeg command
	args := []string{
		"-y",
		"-f", "rawvideo",
		"-pixel_format", pixFmt,
		"-video_size", resolution,
		"-framerate", frameRate,
		"-i", "pipe:0", // Read from stdin
	}

	// Add encoder settings
	switch c.config.Codec {
	case "hevc", "h265":
		args = append(args, "-c:v", "libx265")
	default:
		args = append(args, "-c:v", "libx264")
	}

	args = append(args,
		"-preset", c.config.Preset,
		"-b:v", fmt.Sprintf("%dk", c.config.Bitrate),
		"-g", fmt.Sprintf("%d", int(float64(frame.FrameRateN)/float64(frame.FrameRateD)*c.config.SegmentDuration)),
		"-keyint_min", fmt.Sprintf("%d", int(float64(frame.FrameRateN)/float64(frame.FrameRateD)*c.config.SegmentDuration)),
		"-sc_threshold", "0",
	)

	// fMP4 segment output
	args = append(args,
		"-f", "segment",
		"-segment_format", "mp4",
		"-segment_time", fmt.Sprintf("%.1f", c.config.SegmentDuration),
		"-segment_format_options", "movflags=+frag_keyframe+empty_moov+default_base_moof",
		"-reset_timestamps", "1",
		"-strftime", "0",
		fmt.Sprintf("%s/segment_%%05d.m4s", c.config.OutputDir),
	)

	// Also output init segment
	// Note: FFmpeg segment muxer with fMP4 creates init.mp4 automatically
	// when using frag_keyframe+empty_moov

	log.Printf("[NDI] FFmpeg args: %v", args)

	c.ffmpegCmd = exec.CommandContext(c.ctx, "ffmpeg", args...)

	// Get stdin pipe
	var err error
	c.ffmpegIn, err = c.ffmpegCmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("get stdin pipe: %w", err)
	}

	// Start FFmpeg
	if err := c.ffmpegCmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	log.Printf("[NDI] FFmpeg started (PID %d)", c.ffmpegCmd.Process.Pid)
	return nil
}

// captureLoop runs the main capture loop
func (c *Capture) captureLoop() {
	defer func() {
		c.mu.Lock()
		c.running = false
		c.mu.Unlock()

		if c.ffmpegIn != nil {
			c.ffmpegIn.Close()
		}
		if c.ffmpegCmd != nil {
			c.ffmpegCmd.Wait()
		}
		c.receiver.Destroy()
	}()

	for {
		select {
		case <-c.ctx.Done():
			log.Printf("[NDI] Capture stopped")
			return
		default:
		}

		// Capture video frame
		frame, err := c.receiver.CaptureVideo(100 * time.Millisecond)
		if err != nil {
			c.mu.Lock()
			c.lastErr = err
			c.mu.Unlock()
			log.Printf("[NDI] Capture error: %v", err)
			continue
		}
		if frame == nil {
			continue // Timeout, no frame
		}

		// Write frame data to FFmpeg
		_, err = c.ffmpegIn.Write(frame.Data)
		if err != nil {
			c.mu.Lock()
			c.lastErr = err
			c.mu.Unlock()
			log.Printf("[NDI] Write error: %v", err)
			return
		}

		c.mu.Lock()
		c.frameCount++
		c.mu.Unlock()
	}
}

// Stop stops the capture pipeline
func (c *Capture) Stop() {
	c.mu.Lock()
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Unlock()
}

// IsRunning returns true if capture is running
func (c *Capture) IsRunning() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.running
}

// Stats returns capture statistics
func (c *Capture) Stats() ReceiverStats {
	return c.receiver.Stats()
}

// LastError returns the last error
func (c *Capture) LastError() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastErr
}

// FrameCount returns total frames captured
func (c *Capture) FrameCount() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.frameCount
}
