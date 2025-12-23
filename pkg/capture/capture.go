package capture

// Status represents the current engine status (deprecated, use ChannelStatus)
type Status struct {
	IsRunning    bool    `json:"is_running"`
	IsCapturing  bool    `json:"is_capturing"`
	SessionID    string  `json:"session_id"`
	ChannelID    string  `json:"channel_id"`
	BufferHealth float64 `json:"buffer_health"`
	OldestTime   int64   `json:"oldest_time"`
	NewestTime   int64   `json:"newest_time"`
	SegmentCount int     `json:"segment_count"`
	InitSegment  string  `json:"init_segment"`
}

// ClipRequest represents a request to generate a clip
type ClipRequest struct {
	StartTime int64  `json:"start_time"` // Unix timestamp ms
	EndTime   int64  `json:"end_time"`   // Unix timestamp ms
	PlayID    string `json:"play_id,omitempty"`
}

// ClipResult represents the result of clip generation
type ClipResult struct {
	FilePath      string  `json:"file_path"`
	Duration      float64 `json:"duration"`
	FileSizeBytes int64   `json:"file_size_bytes"`
	SegmentCount  int     `json:"segment_count"`
}

// ClipResultWithTags includes clip result plus metadata
type ClipResultWithTags struct {
	ClipResult
	PlayID    string                 `json:"play_id"`
	StartTime int64                  `json:"start_time"`
	EndTime   int64                  `json:"end_time"`
	Tags      map[string]interface{} `json:"tags,omitempty"`
	ChannelID string                 `json:"channel_id"`
	SessionID string                 `json:"session_id"`
}
