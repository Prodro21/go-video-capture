package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Client is the video-platform API client
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// Config holds platform client configuration
type Config struct {
	URL    string
	APIKey string
}

// ClipMetadata represents clip metadata for upload
type ClipMetadata struct {
	SessionID       string                 `json:"session_id"`
	ChannelID       string                 `json:"channel_id"`
	PlayID          string                 `json:"play_id,omitempty"`
	Title           string                 `json:"title,omitempty"`
	StartTime       int64                  `json:"start_time"`
	EndTime         int64                  `json:"end_time"`
	DurationSeconds float64                `json:"duration_seconds"`
	FileSizeBytes   int64                  `json:"file_size_bytes,omitempty"`
	Tags            map[string]interface{} `json:"tags,omitempty"`
}

// UploadResult represents the result of a clip upload
type UploadResult struct {
	Status   string      `json:"status"`
	Clip     interface{} `json:"clip"`
	FileName string      `json:"file_name"`
	FileSize int64       `json:"file_size"`
	FilePath string      `json:"file_path"`
}

// SegmentNotification represents a segment ready notification for ghost clips
type SegmentNotification struct {
	PlayID     string `json:"play_id"`
	ChannelID  string `json:"channel_id"`
	SegmentURL string `json:"segment_url"`
	Sequence   int    `json:"sequence"`
	Timestamp  int64  `json:"timestamp"`
	IsFinal    bool   `json:"is_final"`
}

// New creates a new platform client
func New(cfg Config) *Client {
	return &Client{
		baseURL: cfg.URL,
		apiKey:  cfg.APIKey,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute, // Long timeout for large uploads
		},
	}
}

// IsConfigured returns true if the client is properly configured
func (c *Client) IsConfigured() bool {
	return c.baseURL != ""
}

// UploadClip uploads a clip file to the platform
func (c *Client) UploadClip(ctx context.Context, filePath string, metadata ClipMetadata) (*UploadResult, error) {
	if !c.IsConfigured() {
		return nil, fmt.Errorf("platform client not configured")
	}

	// Open the file
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	// Get file info for size
	fileInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}
	metadata.FileSizeBytes = fileInfo.Size()

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add metadata field
	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}
	if err := writer.WriteField("metadata", string(metadataJSON)); err != nil {
		return nil, fmt.Errorf("write metadata field: %w", err)
	}

	// Add file field
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		return nil, fmt.Errorf("copy file to form: %w", err)
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	// Create request
	url := fmt.Sprintf("%s/api/v1/clips/upload", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// Check status
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upload failed (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse response
	var result UploadResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &result, nil
}

// CheckHealth checks if the platform is accessible
func (c *Client) CheckHealth(ctx context.Context) error {
	if !c.IsConfigured() {
		return fmt.Errorf("platform client not configured")
	}

	url := fmt.Sprintf("%s/health", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("platform unhealthy (status %d)", resp.StatusCode)
	}

	return nil
}

// CheckUploadStatus checks if the upload endpoint is ready
func (c *Client) CheckUploadStatus(ctx context.Context) error {
	if !c.IsConfigured() {
		return fmt.Errorf("platform client not configured")
	}

	url := fmt.Sprintf("%s/api/v1/clips/upload/status", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upload not ready (status %d)", resp.StatusCode)
	}

	return nil
}

// NotifySegmentReady notifies the platform of a new segment during a ghost clip
func (c *Client) NotifySegmentReady(ctx context.Context, notification SegmentNotification) error {
	if !c.IsConfigured() {
		return nil // Silent skip if platform not configured
	}

	body, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/segments/notify", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("notification failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}
