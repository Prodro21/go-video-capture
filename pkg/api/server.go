package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/video-system/go-video-capture/pkg/ndi"
)

// ChannelInterface defines operations available on a single channel
type ChannelInterface interface {
	ID() string
	GetStatus() interface{}
	SetSession(sessionID string)
	StartGhostClip(playID string) error
	EndGhostClip(playID string) error
	EndGhostClipAndGenerate(ctx context.Context, playID string, tags map[string]interface{}) (interface{}, error)
	GenerateClip(ctx context.Context, startTime, endTime int64, playID string) (interface{}, error)
	GetHLSPlaylist() ([]byte, error)
	GetSegmentPath() string
	GetInitSegmentPath() string
}

// ChannelManager defines operations for managing multiple channels
type ChannelManager interface {
	GetChannel(id string) (ChannelInterface, bool)
	GetDefaultChannel() (ChannelInterface, bool)
	ListChannels() []string
	GetAllStatuses() map[string]interface{}
	SetSession(sessionID string)
}

// ServerConfig holds API server configuration
type ServerConfig struct {
	Host    string
	Port    int
	Manager ChannelManager
}

// Server is the HTTP API server
type Server struct {
	cfg    ServerConfig
	server *http.Server
}

// corsMiddleware wraps a handler with CORS headers
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next(w, r)
	}
}

// NewServer creates a new API server
func NewServer(cfg ServerConfig) *Server {
	s := &Server{cfg: cfg}

	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/health", corsMiddleware(s.handleHealth))

	// List all channels
	mux.HandleFunc("/api/v1/channels", corsMiddleware(s.handleListChannels))

	// Channel-specific routes (must come before legacy routes for proper matching)
	mux.HandleFunc("/api/v1/channels/", corsMiddleware(s.handleChannelRoute))

	// HLS per-channel routes
	mux.HandleFunc("/hls/", corsMiddleware(s.handleHLS))

	// Legacy single-channel routes (backwards compatible)
	mux.HandleFunc("/api/v1/status", corsMiddleware(s.handleLegacyStatus))
	mux.HandleFunc("/api/v1/config", corsMiddleware(s.handleLegacyConfig))
	mux.HandleFunc("/api/v1/mark/in", corsMiddleware(s.handleLegacyMarkIn))
	mux.HandleFunc("/api/v1/mark/out", corsMiddleware(s.handleLegacyMarkOut))
	mux.HandleFunc("/api/v1/clip", corsMiddleware(s.handleLegacyClip))
	mux.HandleFunc("/api/v1/clip/quick", corsMiddleware(s.handleLegacyQuickClip))
	mux.HandleFunc("/api/v1/buffer/status", corsMiddleware(s.handleLegacyBufferStatus))

	// NDI discovery routes
	mux.HandleFunc("/api/v1/ndi/sources", corsMiddleware(s.handleNDISources))
	mux.HandleFunc("/api/v1/ndi/support", corsMiddleware(s.handleNDISupport))

	s.server = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler: mux,
	}

	return s
}

// Start starts the API server
func (s *Server) Start() error {
	log.Printf("API server starting on %s", s.server.Addr)
	return s.server.ListenAndServe()
}

// Stop stops the API server
func (s *Server) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.server.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	channels := s.cfg.Manager.ListChannels()
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":        "healthy",
		"service":       "go-video-capture",
		"channel_count": len(channels),
	})
}

// handleListChannels returns all channel IDs and their statuses
func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	channels := s.cfg.Manager.ListChannels()
	statuses := s.cfg.Manager.GetAllStatuses()

	json.NewEncoder(w).Encode(map[string]interface{}{
		"channels": channels,
		"statuses": statuses,
	})
}

// handleChannelRoute routes /api/v1/channels/{id}/... to the appropriate handler
func (s *Server) handleChannelRoute(w http.ResponseWriter, r *http.Request) {
	// Parse: /api/v1/channels/{id}/{action}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/channels/")
	parts := strings.SplitN(path, "/", 2)

	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "Channel ID required", http.StatusBadRequest)
		return
	}

	channelID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	// Get the channel
	ch, ok := s.cfg.Manager.GetChannel(channelID)
	if !ok {
		http.Error(w, fmt.Sprintf("Channel not found: %s", channelID), http.StatusNotFound)
		return
	}

	// Route to action
	switch {
	case action == "status" || action == "":
		s.handleChannelStatus(w, r, ch)
	case action == "mark/in":
		s.handleChannelMarkIn(w, r, ch)
	case action == "mark/out":
		s.handleChannelMarkOut(w, r, ch)
	case action == "clip":
		s.handleChannelClip(w, r, ch)
	case action == "clip/quick":
		s.handleChannelQuickClip(w, r, ch)
	case action == "buffer/status":
		s.handleChannelStatus(w, r, ch)
	default:
		http.Error(w, fmt.Sprintf("Unknown action: %s", action), http.StatusNotFound)
	}
}

func (s *Server) handleChannelStatus(w http.ResponseWriter, r *http.Request, ch ChannelInterface) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	json.NewEncoder(w).Encode(ch.GetStatus())
}

func (s *Server) handleChannelMarkIn(w http.ResponseWriter, r *http.Request, ch ChannelInterface) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		PlayID string `json:"play_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := ch.StartGhostClip(req.PlayID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "ok",
		"channel_id": ch.ID(),
		"play_id":    req.PlayID,
		"timestamp":  time.Now().UnixMilli(),
	})
}

func (s *Server) handleChannelMarkOut(w http.ResponseWriter, r *http.Request, ch ChannelInterface) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		PlayID       string                 `json:"play_id"`
		GenerateClip bool                   `json:"generate_clip"`
		Tags         map[string]interface{} `json:"tags,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.GenerateClip || req.Tags != nil {
		result, err := ch.EndGhostClipAndGenerate(r.Context(), req.PlayID, req.Tags)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":     "ok",
			"channel_id": ch.ID(),
			"play_id":    req.PlayID,
			"timestamp":  time.Now().UnixMilli(),
			"clip":       result,
		})
		return
	}

	if err := ch.EndGhostClip(req.PlayID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "ok",
		"channel_id": ch.ID(),
		"play_id":    req.PlayID,
		"timestamp":  time.Now().UnixMilli(),
	})
}

func (s *Server) handleChannelClip(w http.ResponseWriter, r *http.Request, ch ChannelInterface) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		StartTime int64  `json:"start_time"`
		EndTime   int64  `json:"end_time"`
		PlayID    string `json:"play_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	result, err := ch.GenerateClip(r.Context(), req.StartTime, req.EndTime, req.PlayID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleChannelQuickClip(w http.ResponseWriter, r *http.Request, ch ChannelInterface) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		DurationSeconds int    `json:"duration_seconds"`
		PlayID          string `json:"play_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.DurationSeconds <= 0 {
		req.DurationSeconds = 15
	}

	endTime := time.Now().UnixMilli()
	startTime := endTime - int64(req.DurationSeconds*1000)

	result, err := ch.GenerateClip(r.Context(), startTime, endTime, req.PlayID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(result)
}

// handleHLS routes HLS requests to the appropriate channel
// Supports: /hls/{channelID}/live.m3u8, /hls/{channelID}/init.mp4, /hls/{channelID}/segment_*.m4s
// Also supports legacy: /hls/live.m3u8 (uses default channel)
func (s *Server) handleHLS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/hls/")
	parts := strings.SplitN(path, "/", 2)

	var ch ChannelInterface
	var segName string

	if len(parts) == 1 {
		// Legacy: /hls/live.m3u8 or /hls/segment_00001.m4s
		var ok bool
		ch, ok = s.cfg.Manager.GetDefaultChannel()
		if !ok {
			http.Error(w, "No default channel available", http.StatusNotFound)
			return
		}
		segName = parts[0]
	} else {
		// Multi-channel: /hls/{channelID}/live.m3u8
		channelID := parts[0]
		var ok bool
		ch, ok = s.cfg.Manager.GetChannel(channelID)
		if !ok {
			http.Error(w, fmt.Sprintf("Channel not found: %s", channelID), http.StatusNotFound)
			return
		}
		segName = parts[1]
	}

	// Handle playlist
	if segName == "live.m3u8" {
		playlist, err := ch.GetHLSPlaylist()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write(playlist)
		return
	}

	// Handle segments
	if len(segName) == 0 || segName[0] == '/' || segName[0] == '.' {
		http.Error(w, "Invalid segment name", http.StatusBadRequest)
		return
	}

	var filePath string
	if segName == "init.mp4" {
		filePath = ch.GetInitSegmentPath()
	} else {
		filePath = ch.GetSegmentPath() + "/" + segName
	}

	contentType := "video/mp4"
	if strings.HasSuffix(segName, ".m4s") {
		contentType = "video/iso.segment"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	http.ServeFile(w, r, filePath)
}

// Legacy handlers - delegate to default channel

func (s *Server) handleLegacyStatus(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.cfg.Manager.GetDefaultChannel()
	if !ok {
		http.Error(w, "No channel available", http.StatusNotFound)
		return
	}
	s.handleChannelStatus(w, r, ch)
}

func (s *Server) handleLegacyConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
		ChannelID string `json:"channel_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.cfg.Manager.SetSession(req.SessionID)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleLegacyMarkIn(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.cfg.Manager.GetDefaultChannel()
	if !ok {
		http.Error(w, "No channel available", http.StatusNotFound)
		return
	}
	s.handleChannelMarkIn(w, r, ch)
}

func (s *Server) handleLegacyMarkOut(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.cfg.Manager.GetDefaultChannel()
	if !ok {
		http.Error(w, "No channel available", http.StatusNotFound)
		return
	}
	s.handleChannelMarkOut(w, r, ch)
}

func (s *Server) handleLegacyClip(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.cfg.Manager.GetDefaultChannel()
	if !ok {
		http.Error(w, "No channel available", http.StatusNotFound)
		return
	}
	s.handleChannelClip(w, r, ch)
}

func (s *Server) handleLegacyQuickClip(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.cfg.Manager.GetDefaultChannel()
	if !ok {
		http.Error(w, "No channel available", http.StatusNotFound)
		return
	}
	s.handleChannelQuickClip(w, r, ch)
}

func (s *Server) handleLegacyBufferStatus(w http.ResponseWriter, r *http.Request) {
	ch, ok := s.cfg.Manager.GetDefaultChannel()
	if !ok {
		http.Error(w, "No channel available", http.StatusNotFound)
		return
	}
	s.handleChannelStatus(w, r, ch)
}

// handleNDISources discovers NDI sources on the network
func (s *Server) handleNDISources(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if NDI is supported first
	if !ndi.CheckSupport(r.Context()) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"supported": false,
			"sources":   []ndi.Source{},
			"message":   "FFmpeg is not compiled with NDI support (libndi_newtek)",
		})
		return
	}

	// Discover NDI sources
	sources, err := ndi.DiscoverSources(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("NDI discovery failed: %v", err), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"supported": true,
		"sources":   sources,
	})
}

// handleNDISupport checks if NDI is supported by FFmpeg
func (s *Server) handleNDISupport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	supported := ndi.CheckSupport(r.Context())

	json.NewEncoder(w).Encode(map[string]interface{}{
		"supported": supported,
	})
}
