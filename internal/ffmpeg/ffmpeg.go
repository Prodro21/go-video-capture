package ffmpeg

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// FFmpeg wraps FFmpeg binary execution
type FFmpeg struct {
	binaryPath  string
	probePath   string
}

// New creates a new FFmpeg wrapper
func New() (*FFmpeg, error) {
	ffmpegPath, err := findBinary("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found: %w", err)
	}

	ffprobePath, err := findBinary("ffprobe")
	if err != nil {
		return nil, fmt.Errorf("ffprobe not found: %w", err)
	}

	return &FFmpeg{
		binaryPath: ffmpegPath,
		probePath:  ffprobePath,
	}, nil
}

// findBinary locates a binary in PATH or common locations
func findBinary(name string) (string, error) {
	// Try PATH first
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}

	// Common locations by OS
	var paths []string
	switch runtime.GOOS {
	case "darwin":
		paths = []string{
			"/opt/homebrew/bin/" + name,
			"/usr/local/bin/" + name,
		}
	case "linux":
		paths = []string{
			"/usr/bin/" + name,
			"/usr/local/bin/" + name,
		}
	case "windows":
		paths = []string{
			"C:\\ffmpeg\\bin\\" + name + ".exe",
			"C:\\Program Files\\ffmpeg\\bin\\" + name + ".exe",
		}
	}

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("%s not found in PATH or common locations", name)
}

// Version returns the FFmpeg version string
func (f *FFmpeg) Version(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, f.binaryPath, "-version")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(output), "\n")
	if len(lines) > 0 {
		return strings.TrimSpace(lines[0]), nil
	}
	return "", fmt.Errorf("no version output")
}

// Process represents a running FFmpeg process
type Process struct {
	cmd    *exec.Cmd
	stdin  *os.File
	stderr *bufio.Scanner
	done   chan error
	mu     sync.Mutex
}

// StartEncoder starts an FFmpeg encoding process for CMAF output
func (f *FFmpeg) StartEncoder(ctx context.Context, cfg EncoderConfig) (*Process, error) {
	args := buildEncoderArgs(cfg)

	cmd := exec.CommandContext(ctx, f.binaryPath, args...)

	// Get stdin for piping frames
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("get stdin pipe: %w", err)
	}

	// Capture stderr for progress
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("get stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}

	proc := &Process{
		cmd:    cmd,
		stdin:  stdin.(*os.File),
		stderr: bufio.NewScanner(stderrPipe),
		done:   make(chan error, 1),
	}

	// Wait for process in background
	go func() {
		proc.done <- cmd.Wait()
	}()

	return proc, nil
}

// Write writes raw frame data to FFmpeg stdin
func (p *Process) Write(data []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stdin.Write(data)
}

// Close closes stdin and waits for FFmpeg to finish
func (p *Process) Close() error {
	p.mu.Lock()
	p.stdin.Close()
	p.mu.Unlock()
	return <-p.done
}

// Kill forcefully terminates the process
func (p *Process) Kill() error {
	return p.cmd.Process.Kill()
}

// Done returns a channel that receives the exit error
func (p *Process) Done() <-chan error {
	return p.done
}

// EncoderConfig holds configuration for the encoder
type EncoderConfig struct {
	// Input
	InputFormat  string // rawvideo, pipe, etc.
	PixelFormat  string // yuv420p, nv12, etc.
	Width        int
	Height       int
	Framerate    int

	// Encoding
	Codec        string // libx264, h264_nvenc, h264_videotoolbox
	Preset       string // ultrafast, fast, medium
	Bitrate      int    // kbps
	GOP          int    // Keyframe interval in frames

	// Output
	OutputPath   string // Directory for segments
	SegmentDuration float64 // Segment duration in seconds
}

// buildEncoderArgs builds FFmpeg arguments for CMAF encoding
func buildEncoderArgs(cfg EncoderConfig) []string {
	args := []string{
		"-y", // Overwrite output

		// Input
		"-f", cfg.InputFormat,
		"-pix_fmt", cfg.PixelFormat,
		"-s", fmt.Sprintf("%dx%d", cfg.Width, cfg.Height),
		"-r", fmt.Sprintf("%d", cfg.Framerate),
		"-i", "pipe:0", // Read from stdin

		// Video encoding
		"-c:v", cfg.Codec,
		"-preset", cfg.Preset,
		"-b:v", fmt.Sprintf("%dk", cfg.Bitrate),
		"-g", fmt.Sprintf("%d", cfg.GOP),
		"-keyint_min", fmt.Sprintf("%d", cfg.GOP),
		"-sc_threshold", "0", // Disable scene change detection for consistent GOPs

		// CMAF/fMP4 output
		"-f", "dash",
		"-seg_duration", fmt.Sprintf("%.1f", cfg.SegmentDuration),
		"-init_seg_name", "init.mp4",
		"-media_seg_name", "segment_$Number%05d$.m4s",
		"-use_template", "1",
		"-use_timeline", "0",
		"-hls_playlist", "1", // Also generate HLS playlist

		// Output manifest
		filepath.Join(cfg.OutputPath, "manifest.mpd"),
	}

	return args
}

// ExtractClip extracts a clip from segments
func (f *FFmpeg) ExtractClip(ctx context.Context, cfg ClipConfig) error {
	// Create concat file
	concatPath := filepath.Join(cfg.TempDir, "concat.txt")
	concatFile, err := os.Create(concatPath)
	if err != nil {
		return fmt.Errorf("create concat file: %w", err)
	}

	for _, seg := range cfg.Segments {
		fmt.Fprintf(concatFile, "file '%s'\n", seg)
	}
	concatFile.Close()
	defer os.Remove(concatPath)

	args := []string{
		"-y",
		"-f", "concat",
		"-safe", "0",
		"-i", concatPath,
	}

	// Add trim if needed
	if cfg.TrimStart > 0 {
		args = append(args, "-ss", fmt.Sprintf("%.3f", cfg.TrimStart))
	}
	if cfg.TrimEnd > 0 {
		args = append(args, "-t", fmt.Sprintf("%.3f", cfg.Duration-cfg.TrimStart))
	}

	args = append(args,
		"-c", "copy", // No re-encode
		cfg.OutputPath,
	)

	cmd := exec.CommandContext(ctx, f.binaryPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg extract: %w\noutput: %s", err, output)
	}

	return nil
}

// ClipConfig holds configuration for clip extraction
type ClipConfig struct {
	Segments   []string // Paths to segment files in order
	OutputPath string   // Output MP4 path
	TempDir    string   // Temp directory for concat file
	TrimStart  float64  // Seconds to trim from start
	TrimEnd    float64  // Seconds to trim from end
	Duration   float64  // Total expected duration
}
