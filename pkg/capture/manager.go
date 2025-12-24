package capture

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/video-system/go-video-capture/internal/ffmpeg"
	"github.com/video-system/go-video-capture/pkg/api"
	"github.com/video-system/go-video-capture/pkg/platform"
)

// Manager orchestrates multiple capture channels
type Manager struct {
	cfg      *Config
	ffmpeg   *ffmpeg.FFmpeg
	platform *platform.Client
	channels map[string]*Channel

	mu        sync.RWMutex
	sessionID string
	basePath  string

	ctx    context.Context
	cancel context.CancelFunc
}

// NewManager creates a new channel manager
func NewManager(cfg *Config) (*Manager, error) {
	// Initialize FFmpeg (shared across all channels)
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

	// Create platform client if configured (shared across all channels)
	var platformClient *platform.Client
	if cfg.Platform.Enabled && cfg.Platform.URL != "" {
		platformClient = platform.New(platform.Config{
			URL:    cfg.Platform.URL,
			APIKey: cfg.Platform.APIKey,
		})
		log.Printf("Platform integration enabled: %s", cfg.Platform.URL)
	}

	m := &Manager{
		cfg:       cfg,
		ffmpeg:    ff,
		platform:  platformClient,
		channels:  make(map[string]*Channel),
		sessionID: cfg.Session.SessionID,
		basePath:  cfg.Buffer.Path,
	}

	// Create channels based on config
	if len(cfg.Channels) > 0 {
		// Multi-channel mode
		for _, chCfg := range cfg.Channels {
			ch, err := NewChannel(chCfg.ID, chCfg, ff, platformClient, cfg.Session.SessionID, cfg.Buffer.Path)
			if err != nil {
				return nil, fmt.Errorf("create channel %s: %w", chCfg.ID, err)
			}
			m.channels[chCfg.ID] = ch
			log.Printf("Channel configured: %s", chCfg.ID)
		}
	} else {
		// Single-channel mode (backwards compatible)
		chCfg := ChannelConfig{
			ID:     cfg.Session.ChannelID,
			Input:  cfg.Input,
			Buffer: cfg.Buffer,
			Encode: cfg.Encode,
		}
		ch, err := NewChannel(chCfg.ID, chCfg, ff, platformClient, cfg.Session.SessionID, cfg.Buffer.Path)
		if err != nil {
			return nil, fmt.Errorf("create channel %s: %w", chCfg.ID, err)
		}
		m.channels[chCfg.ID] = ch
		log.Printf("Single channel mode: %s", chCfg.ID)
	}

	return m, nil
}

// Start starts all channels
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.mu.Unlock()

	log.Printf("Starting %d channel(s)", len(m.channels))

	// Start all channels
	for id, ch := range m.channels {
		if err := ch.Start(m.ctx); err != nil {
			log.Printf("Warning: failed to start channel %s: %v", id, err)
			// Continue with other channels
		}
	}

	return nil
}

// Stop stops all channels
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()

	for _, ch := range m.channels {
		ch.Stop()
	}
	log.Printf("All channels stopped")
}

// Wait blocks until context is cancelled
func (m *Manager) Wait() {
	<-m.ctx.Done()
}

// GetChannel returns a channel by ID (implements api.ChannelManager)
func (m *Manager) GetChannel(id string) (api.ChannelInterface, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ch, ok := m.channels[id]
	if !ok {
		return nil, false
	}
	return ch, true
}

// ListChannels returns all channel IDs
func (m *Manager) ListChannels() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.channels))
	for id := range m.channels {
		ids = append(ids, id)
	}
	return ids
}

// GetAllStatuses returns status for all channels (implements api.ChannelManager)
func (m *Manager) GetAllStatuses() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statuses := make(map[string]interface{})
	for id, ch := range m.channels {
		statuses[id] = ch.GetStatus()
	}
	return statuses
}

// SetSession updates the session ID for all channels
func (m *Manager) SetSession(sessionID string) {
	m.mu.Lock()
	m.sessionID = sessionID
	m.mu.Unlock()

	for _, ch := range m.channels {
		ch.SetSession(sessionID)
	}
	log.Printf("Session updated for all channels: %s", sessionID)
}

// GetDefaultChannel returns the first/only channel (for backwards compatibility)
// Implements api.ChannelManager
func (m *Manager) GetDefaultChannel() (api.ChannelInterface, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// If single channel mode, return that channel
	if len(m.channels) == 1 {
		for _, ch := range m.channels {
			return ch, true
		}
	}

	// If multi-channel mode with a configured default, return it
	if m.cfg.Session.ChannelID != "" {
		if ch, ok := m.channels[m.cfg.Session.ChannelID]; ok {
			return ch, true
		}
	}

	// Return first channel found
	for _, ch := range m.channels {
		return ch, true
	}

	return nil, false
}

// ChannelCount returns the number of configured channels
func (m *Manager) ChannelCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.channels)
}

// IsRecording returns true if any channel is currently recording
func (m *Manager) IsRecording() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, ch := range m.channels {
		if ch.IsRecording() {
			return true
		}
	}
	return false
}

// GetError returns the first error from any channel, or nil if no errors
func (m *Manager) GetError() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, ch := range m.channels {
		if err := ch.GetError(); err != nil {
			return err
		}
	}
	return nil
}
