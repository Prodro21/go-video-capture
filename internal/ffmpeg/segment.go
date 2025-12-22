package ffmpeg

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// SegmentWriter generates CMAF segments from a video source
type SegmentWriter struct {
	ffmpeg      *FFmpeg
	cfg         SegmentConfig
	cmd         *exec.Cmd
	outputPath  string
	onSegment   func(SegmentInfo)

	cancel context.CancelFunc
}

// SegmentConfig holds configuration for segment generation
type SegmentConfig struct {
	// Input source (file path, device, or URL)
	Input       string
	InputFormat string // Optional: force input format

	// Encoding settings
	Codec       string  // libx264, h264_nvenc, h264_videotoolbox
	Preset      string  // ultrafast, fast, medium
	Bitrate     int     // kbps (0 = use source bitrate)
	Width       int     // Output width (0 = source)
	Height      int     // Output height (0 = source)
	Framerate   int     // Output framerate (0 = source)

	// Segment settings
	SegmentDuration float64 // Seconds per segment (default: 2)
	GOP             int     // Keyframe interval in frames (0 = auto based on segment duration)

	// Output
	OutputDir   string // Directory for segments
}

// SegmentInfo describes a generated segment
type SegmentInfo struct {
	Sequence  int
	Path      string
	StartTime time.Time
	Duration  time.Duration
	Size      int64
}

// NewSegmentWriter creates a new segment writer
func (f *FFmpeg) NewSegmentWriter(cfg SegmentConfig) *SegmentWriter {
	// Set defaults
	if cfg.SegmentDuration == 0 {
		cfg.SegmentDuration = 2.0
	}
	if cfg.Codec == "" {
		cfg.Codec = "libx264"
	}
	if cfg.Preset == "" {
		cfg.Preset = "fast"
	}

	return &SegmentWriter{
		ffmpeg:     f,
		cfg:        cfg,
		outputPath: cfg.OutputDir,
	}
}

// OnSegment sets a callback for when segments are created
func (sw *SegmentWriter) OnSegment(fn func(SegmentInfo)) {
	sw.onSegment = fn
}

// Start begins generating segments
func (sw *SegmentWriter) Start(ctx context.Context) error {
	// Ensure output directory exists
	if err := os.MkdirAll(sw.outputPath, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	ctx, sw.cancel = context.WithCancel(ctx)

	args := sw.buildArgs()
	sw.cmd = exec.CommandContext(ctx, sw.ffmpeg.binaryPath, args...)

	// Capture stderr for progress monitoring
	stderr, err := sw.cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("get stderr pipe: %w", err)
	}

	if err := sw.cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	// Monitor output in background
	go sw.monitorOutput(bufio.NewScanner(stderr))

	// Watch for new segments
	go sw.watchSegments(ctx)

	return nil
}

// Stop stops segment generation
func (sw *SegmentWriter) Stop() error {
	if sw.cancel != nil {
		sw.cancel()
	}
	if sw.cmd != nil && sw.cmd.Process != nil {
		// Send SIGINT for graceful shutdown
		sw.cmd.Process.Signal(os.Interrupt)

		// Wait with timeout
		done := make(chan error, 1)
		go func() { done <- sw.cmd.Wait() }()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			sw.cmd.Process.Kill()
		}
	}
	return nil
}

// Wait waits for the segment writer to finish
func (sw *SegmentWriter) Wait() error {
	if sw.cmd == nil {
		return nil
	}
	return sw.cmd.Wait()
}

// buildArgs builds FFmpeg arguments for CMAF segment generation
func (sw *SegmentWriter) buildArgs() []string {
	cfg := sw.cfg
	args := []string{"-y"}

	// Input
	if cfg.InputFormat != "" {
		args = append(args, "-f", cfg.InputFormat)
	}
	args = append(args, "-i", cfg.Input)

	// Video encoding
	args = append(args, "-c:v", cfg.Codec)
	args = append(args, "-preset", cfg.Preset)

	if cfg.Bitrate > 0 {
		args = append(args, "-b:v", fmt.Sprintf("%dk", cfg.Bitrate))
	}

	// Calculate GOP based on framerate and segment duration
	gop := cfg.GOP
	if gop == 0 {
		framerate := cfg.Framerate
		if framerate == 0 {
			framerate = 30 // Default assumption
		}
		gop = int(float64(framerate) * cfg.SegmentDuration)
	}
	args = append(args, "-g", fmt.Sprintf("%d", gop))
	args = append(args, "-keyint_min", fmt.Sprintf("%d", gop))
	args = append(args, "-sc_threshold", "0")

	// Scaling if specified
	if cfg.Width > 0 && cfg.Height > 0 {
		args = append(args, "-vf", fmt.Sprintf("scale=%d:%d", cfg.Width, cfg.Height))
	}

	// Audio (copy or aac)
	args = append(args, "-c:a", "aac", "-b:a", "128k")

	// CMAF/fMP4 output via DASH muxer
	args = append(args,
		"-f", "dash",
		"-seg_duration", fmt.Sprintf("%.1f", cfg.SegmentDuration),
		"-init_seg_name", "init.mp4",
		"-media_seg_name", "segment_%05d.m4s",
		"-use_template", "1",
		"-use_timeline", "0",
		"-hls_playlist", "1",
		"-streaming", "1",
		"-ldash", "1",
		"-remove_at_exit", "0",
		filepath.Join(sw.outputPath, "manifest.mpd"),
	)

	return args
}

// monitorOutput parses FFmpeg stderr for progress
func (sw *SegmentWriter) monitorOutput(scanner *bufio.Scanner) {
	frameRegex := regexp.MustCompile(`frame=\s*(\d+)`)
	fpsRegex := regexp.MustCompile(`fps=\s*([\d.]+)`)
	timeRegex := regexp.MustCompile(`time=(\d+):(\d+):([\d.]+)`)

	for scanner.Scan() {
		line := scanner.Text()

		// Log errors
		if strings.Contains(line, "Error") || strings.Contains(line, "error") {
			log.Printf("FFmpeg: %s", line)
		}

		// Parse progress (optional - for monitoring)
		if frameRegex.MatchString(line) {
			// Could emit progress events here
			_ = fpsRegex
			_ = timeRegex
		}
	}
}

// watchSegments monitors for new segment files
func (sw *SegmentWriter) watchSegments(ctx context.Context) {
	if sw.onSegment == nil {
		return
	}

	seen := make(map[string]bool)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	startTime := time.Now()
	segmentDur := time.Duration(sw.cfg.SegmentDuration * float64(time.Second))

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			files, _ := filepath.Glob(filepath.Join(sw.outputPath, "segment_*.m4s"))
			for _, f := range files {
				if seen[f] {
					continue
				}

				// Check if file is complete (not being written)
				info, err := os.Stat(f)
				if err != nil || info.Size() == 0 {
					continue
				}

				// Parse sequence number from filename
				base := filepath.Base(f)
				var seq int
				fmt.Sscanf(base, "segment_%05d.m4s", &seq)

				seen[f] = true
				sw.onSegment(SegmentInfo{
					Sequence:  seq,
					Path:      f,
					StartTime: startTime.Add(time.Duration(seq) * segmentDur),
					Duration:  segmentDur,
					Size:      info.Size(),
				})
			}
		}
	}
}

// GenerateSegmentsFromFile generates segments from a video file (useful for testing)
func (f *FFmpeg) GenerateSegmentsFromFile(ctx context.Context, inputPath, outputDir string, segmentDur float64) error {
	sw := f.NewSegmentWriter(SegmentConfig{
		Input:           inputPath,
		Codec:           "libx264",
		Preset:          "fast",
		SegmentDuration: segmentDur,
		OutputDir:       outputDir,
	})

	if err := sw.Start(ctx); err != nil {
		return err
	}

	return sw.Wait()
}

// ConcatSegments concatenates multiple segments into a single MP4
func (f *FFmpeg) ConcatSegments(ctx context.Context, initPath string, segments []string, outputPath string) error {
	// Create temp concat file
	tmpFile, err := os.CreateTemp("", "concat_*.txt")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write segment list
	for _, seg := range segments {
		fmt.Fprintf(tmpFile, "file '%s'\n", seg)
	}
	tmpFile.Close()

	args := []string{
		"-y",
		"-f", "concat",
		"-safe", "0",
		"-i", tmpFile.Name(),
		"-c", "copy",
		outputPath,
	}

	cmd := exec.CommandContext(ctx, f.binaryPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg concat: %w\noutput: %s", err, output)
	}

	return nil
}

// TrimClip trims a video file
func (f *FFmpeg) TrimClip(ctx context.Context, inputPath, outputPath string, startSec, durationSec float64) error {
	args := []string{
		"-y",
		"-ss", fmt.Sprintf("%.3f", startSec),
		"-i", inputPath,
		"-t", fmt.Sprintf("%.3f", durationSec),
		"-c", "copy",
		outputPath,
	}

	cmd := exec.CommandContext(ctx, f.binaryPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg trim: %w\noutput: %s", err, output)
	}

	return nil
}

// GenerateThumbnail generates a thumbnail from a video
func (f *FFmpeg) GenerateThumbnail(ctx context.Context, inputPath, outputPath string, atSecond float64) error {
	args := []string{
		"-y",
		"-ss", fmt.Sprintf("%.3f", atSecond),
		"-i", inputPath,
		"-vframes", "1",
		"-vf", "scale=320:-1",
		outputPath,
	}

	cmd := exec.CommandContext(ctx, f.binaryPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg thumbnail: %w\noutput: %s", err, output)
	}

	return nil
}

// ListInputDevices lists available input devices (macOS AVFoundation, Linux v4l2)
func (f *FFmpeg) ListInputDevices(ctx context.Context) (string, error) {
	var args []string

	switch os := os.Getenv("GOOS"); os {
	case "darwin", "":
		args = []string{"-f", "avfoundation", "-list_devices", "true", "-i", ""}
	case "linux":
		args = []string{"-f", "v4l2", "-list_devices", "true", "-i", ""}
	case "windows":
		args = []string{"-f", "dshow", "-list_devices", "true", "-i", "dummy"}
	default:
		return "", fmt.Errorf("unsupported OS: %s", os)
	}

	cmd := exec.CommandContext(ctx, f.binaryPath, args...)
	output, _ := cmd.CombinedOutput() // This will "fail" but output device list
	return string(output), nil
}
