package capture

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all capture configuration
type Config struct {
	// Single-channel mode (backwards compatible)
	Input  InputConfig  `yaml:"input"`
	Buffer BufferConfig `yaml:"buffer"`
	Encode EncodeConfig `yaml:"encode"`

	// Multi-channel mode
	Channels []ChannelConfig `yaml:"channels"`

	// Shared configuration
	HLS      HLSConfig      `yaml:"hls"`
	API      APIConfig      `yaml:"api"`
	Platform PlatformConfig `yaml:"platform"`
	Session  SessionConfig  `yaml:"session"`
}

// IsMultiChannel returns true if multiple channels are configured
func (c *Config) IsMultiChannel() bool {
	return len(c.Channels) > 0
}

// InputConfig configures the video input source
type InputConfig struct {
	Type       string `yaml:"type"`       // decklink, ndi, v4l2, avfoundation, dshow, screen
	Device     string `yaml:"device"`     // Device identifier
	Resolution string `yaml:"resolution"` // 1920x1080, 3840x2160
	Framerate  int    `yaml:"framerate"`  // 30, 60
}

// BufferConfig configures the ring buffer
type BufferConfig struct {
	Duration    time.Duration `yaml:"duration"`     // How long to keep (30m)
	SegmentSize time.Duration `yaml:"segment_size"` // Segment duration (2s)
	Path        string        `yaml:"path"`         // Buffer storage path
	MaxSize     string        `yaml:"max_size"`     // Max storage size (8GB)
}

// EncodeConfig configures the encoder
type EncodeConfig struct {
	Type    string `yaml:"type"`    // software, nvenc, qsv, videotoolbox
	Codec   string `yaml:"codec"`   // h264, hevc
	Preset  string `yaml:"preset"`  // ultrafast, fast, medium
	Bitrate int    `yaml:"bitrate"` // Target bitrate in kbps
	GOP     int    `yaml:"gop"`     // Keyframe interval (frames)
}

// HLSConfig configures local HLS output
type HLSConfig struct {
	Enabled bool   `yaml:"enabled"`
	Path    string `yaml:"path"` // HLS output path
	Port    int    `yaml:"port"` // HTTP server port
}

// APIConfig configures the control API
type APIConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

// PlatformConfig configures optional platform integration
type PlatformConfig struct {
	Enabled bool   `yaml:"enabled"`
	URL     string `yaml:"url"`
	APIKey  string `yaml:"api_key"`
}

// SessionConfig holds runtime session info (set by operator-console)
type SessionConfig struct {
	SessionID string `yaml:"session_id"`
	ChannelID string `yaml:"channel_id"`
}

// LoadConfig loads configuration from a YAML file
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// Expand environment variables
	data = []byte(os.ExpandEnv(string(data)))

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Set defaults for single-channel mode
	if cfg.Buffer.Duration == 0 {
		cfg.Buffer.Duration = 30 * time.Minute
	}
	if cfg.Buffer.SegmentSize == 0 {
		cfg.Buffer.SegmentSize = 2 * time.Second
	}
	if cfg.Encode.Preset == "" {
		cfg.Encode.Preset = "fast"
	}
	if cfg.Encode.GOP == 0 {
		cfg.Encode.GOP = 60
	}
	if cfg.API.Port == 0 {
		cfg.API.Port = 8080
	}

	// Set defaults for multi-channel mode
	for i := range cfg.Channels {
		ch := &cfg.Channels[i]
		if ch.Buffer.Duration == 0 {
			ch.Buffer.Duration = cfg.Buffer.Duration
			if ch.Buffer.Duration == 0 {
				ch.Buffer.Duration = 30 * time.Minute
			}
		}
		if ch.Buffer.SegmentSize == 0 {
			ch.Buffer.SegmentSize = cfg.Buffer.SegmentSize
			if ch.Buffer.SegmentSize == 0 {
				ch.Buffer.SegmentSize = 2 * time.Second
			}
		}
		if ch.Encode.Preset == "" {
			ch.Encode.Preset = cfg.Encode.Preset
			if ch.Encode.Preset == "" {
				ch.Encode.Preset = "fast"
			}
		}
		if ch.Encode.GOP == 0 {
			ch.Encode.GOP = cfg.Encode.GOP
			if ch.Encode.GOP == 0 {
				ch.Encode.GOP = 60
			}
		}
	}

	return &cfg, nil
}
