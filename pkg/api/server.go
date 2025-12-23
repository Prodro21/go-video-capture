package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// CaptureEngine interface for the capture engine
type CaptureEngine interface {
	GetStatus() interface{}
	SetSession(sessionID, channelID string)
	GenerateClip(ctx context.Context, req interface{}) (interface{}, error)
	StartGhostClip(playID string) error
	EndGhostClip(playID string) error
	EndGhostClipAndGenerate(ctx context.Context, playID string, tags map[string]interface{}) (interface{}, error)
}

// ServerConfig holds API server configuration
type ServerConfig struct {
	Host   string
	Port   int
	Engine CaptureEngine
}

// Server is the HTTP API server
type Server struct {
	cfg    ServerConfig
	server *http.Server
}

// NewServer creates a new API server
func NewServer(cfg ServerConfig) *Server {
	s := &Server{cfg: cfg}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/v1/status", s.handleStatus)
	mux.HandleFunc("/api/v1/config", s.handleConfig)
	mux.HandleFunc("/api/v1/mark/in", s.handleMarkIn)
	mux.HandleFunc("/api/v1/mark/out", s.handleMarkOut)
	mux.HandleFunc("/api/v1/clip", s.handleClip)
	mux.HandleFunc("/api/v1/clip/quick", s.handleQuickClip)
	mux.HandleFunc("/api/v1/buffer/status", s.handleBufferStatus)

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
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "healthy",
		"service": "go-video-capture",
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	status := s.cfg.Engine.GetStatus()
	json.NewEncoder(w).Encode(status)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
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

	s.cfg.Engine.SetSession(req.SessionID, req.ChannelID)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleMarkIn(w http.ResponseWriter, r *http.Request) {
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

	if err := s.cfg.Engine.StartGhostClip(req.PlayID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"play_id":   req.PlayID,
		"timestamp": time.Now().UnixMilli(),
	})
}

func (s *Server) handleMarkOut(w http.ResponseWriter, r *http.Request) {
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

	// Default to generating clip
	if req.GenerateClip || req.Tags != nil {
		// End ghost clip AND generate clip
		result, err := s.cfg.Engine.EndGhostClipAndGenerate(r.Context(), req.PlayID, req.Tags)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "ok",
			"play_id":   req.PlayID,
			"timestamp": time.Now().UnixMilli(),
			"clip":      result,
		})
		return
	}

	// Just end the ghost clip without generating
	if err := s.cfg.Engine.EndGhostClip(req.PlayID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"play_id":   req.PlayID,
		"timestamp": time.Now().UnixMilli(),
	})
}

func (s *Server) handleClip(w http.ResponseWriter, r *http.Request) {
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

	clipReq := map[string]interface{}{
		"start_time": float64(req.StartTime),
		"end_time":   float64(req.EndTime),
		"play_id":    req.PlayID,
	}

	result, err := s.cfg.Engine.GenerateClip(r.Context(), clipReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleQuickClip(w http.ResponseWriter, r *http.Request) {
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

	// Default to 15 seconds if not specified
	if req.DurationSeconds <= 0 {
		req.DurationSeconds = 15
	}

	// Calculate time range: now - duration to now
	endTime := time.Now().UnixMilli()
	startTime := endTime - int64(req.DurationSeconds*1000)

	// Generate clip request
	clipReq := map[string]interface{}{
		"start_time": float64(startTime),
		"end_time":   float64(endTime),
		"play_id":    req.PlayID,
	}

	result, err := s.cfg.Engine.GenerateClip(r.Context(), clipReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleBufferStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	status := s.cfg.Engine.GetStatus()
	json.NewEncoder(w).Encode(status)
}
