package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
)

// ProbeResult holds video file information
type ProbeResult struct {
	Format  ProbeFormat   `json:"format"`
	Streams []ProbeStream `json:"streams"`
}

// ProbeFormat holds format-level information
type ProbeFormat struct {
	Filename       string `json:"filename"`
	FormatName     string `json:"format_name"`
	Duration       string `json:"duration"`
	Size           string `json:"size"`
	BitRate        string `json:"bit_rate"`
}

// ProbeStream holds stream-level information
type ProbeStream struct {
	Index         int    `json:"index"`
	CodecName     string `json:"codec_name"`
	CodecType     string `json:"codec_type"` // video, audio
	Width         int    `json:"width,omitempty"`
	Height        int    `json:"height,omitempty"`
	PixFmt        string `json:"pix_fmt,omitempty"`
	FrameRate     string `json:"r_frame_rate,omitempty"`
	AvgFrameRate  string `json:"avg_frame_rate,omitempty"`
	Duration      string `json:"duration,omitempty"`
	BitRate       string `json:"bit_rate,omitempty"`
	SampleRate    string `json:"sample_rate,omitempty"`
	Channels      int    `json:"channels,omitempty"`
}

// Probe analyzes a video file and returns metadata
func (f *FFmpeg) Probe(ctx context.Context, path string) (*ProbeResult, error) {
	args := []string{
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	}

	cmd := exec.CommandContext(ctx, f.probePath, args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	var result ProbeResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("parse ffprobe output: %w", err)
	}

	return &result, nil
}

// VideoInfo returns simplified video information
type VideoInfo struct {
	Width      int
	Height     int
	Duration   float64
	Framerate  float64
	Codec      string
	BitRate    int64
	PixelFmt   string
}

// GetVideoInfo returns simplified video information
func (f *FFmpeg) GetVideoInfo(ctx context.Context, path string) (*VideoInfo, error) {
	probe, err := f.Probe(ctx, path)
	if err != nil {
		return nil, err
	}

	info := &VideoInfo{}

	// Find video stream
	for _, stream := range probe.Streams {
		if stream.CodecType == "video" {
			info.Width = stream.Width
			info.Height = stream.Height
			info.Codec = stream.CodecName
			info.PixelFmt = stream.PixFmt

			// Parse framerate (format: "30/1" or "30000/1001")
			if stream.AvgFrameRate != "" {
				info.Framerate = parseFramerate(stream.AvgFrameRate)
			} else if stream.FrameRate != "" {
				info.Framerate = parseFramerate(stream.FrameRate)
			}

			// Parse bitrate
			if stream.BitRate != "" {
				info.BitRate, _ = strconv.ParseInt(stream.BitRate, 10, 64)
			}

			break
		}
	}

	// Parse duration from format
	if probe.Format.Duration != "" {
		info.Duration, _ = strconv.ParseFloat(probe.Format.Duration, 64)
	}

	// Fallback bitrate from format
	if info.BitRate == 0 && probe.Format.BitRate != "" {
		info.BitRate, _ = strconv.ParseInt(probe.Format.BitRate, 10, 64)
	}

	return info, nil
}

// parseFramerate parses a framerate string like "30/1" or "30000/1001"
func parseFramerate(s string) float64 {
	var num, den int
	if n, _ := fmt.Sscanf(s, "%d/%d", &num, &den); n == 2 && den != 0 {
		return float64(num) / float64(den)
	}
	// Try parsing as plain number
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return 0
}

// Resolution returns resolution string like "1920x1080"
func (v *VideoInfo) Resolution() string {
	return fmt.Sprintf("%dx%d", v.Width, v.Height)
}
