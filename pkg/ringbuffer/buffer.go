package ringbuffer

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/video-system/go-video-capture/internal/ffmpeg"
)

// Config holds ring buffer configuration
type Config struct {
	Duration      time.Duration // How long to keep segments (e.g., 30m)
	SegmentSize   time.Duration // Duration of each segment (e.g., 2s)
	Path          string        // Storage path for segments
	RecordingPath string        // Path for full session recording (optional)
	ChannelID     string        // Channel identifier
}

// Buffer manages a ring buffer of CMAF segments
type Buffer struct {
	cfg    Config
	ffmpeg *ffmpeg.FFmpeg

	mu          sync.RWMutex
	segments    map[int]*Segment // sequence -> segment
	firstSeq    int
	lastSeq     int
	initSegment string // Path to init.mp4
	startTime   time.Time

	// Ghost-clipping state
	ghostMu      sync.RWMutex
	activeGhosts map[string]*GhostClip

	// Event callbacks
	onSegment      func(*Segment)
	onGhostSegment func(playID string, seg *Segment)

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
}

// Segment represents a single CMAF segment
type Segment struct {
	Sequence  int           `json:"sequence"`
	FilePath  string        `json:"file_path"`
	StartTime time.Time     `json:"start_time"`
	Duration  time.Duration `json:"duration"`
	SizeBytes int64         `json:"size_bytes"`
}

// GhostClip tracks an active ghost clip
type GhostClip struct {
	PlayID    string
	StartTime time.Time
	StartSeq  int   // First segment sequence
	Segments  []int // Sequence numbers included
}

// New creates a new ring buffer
func New(cfg Config, ff *ffmpeg.FFmpeg) (*Buffer, error) {
	// Ensure paths exist
	if err := os.MkdirAll(cfg.Path, 0755); err != nil {
		return nil, fmt.Errorf("create buffer path: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.Path, "clips"), 0755); err != nil {
		return nil, fmt.Errorf("create clips path: %w", err)
	}

	return &Buffer{
		cfg:          cfg,
		ffmpeg:       ff,
		segments:     make(map[int]*Segment),
		activeGhosts: make(map[string]*GhostClip),
		startTime:    time.Now(),
	}, nil
}

// OnSegment sets callback for new segments
func (b *Buffer) OnSegment(fn func(*Segment)) {
	b.onSegment = fn
}

// OnGhostSegment sets callback for ghost clip segments
func (b *Buffer) OnGhostSegment(fn func(playID string, seg *Segment)) {
	b.onGhostSegment = fn
}

// Start starts the buffer manager
func (b *Buffer) Start(ctx context.Context) error {
	b.ctx, b.cancel = context.WithCancel(ctx)
	b.startTime = time.Now()

	log.Printf("Ring buffer started (path: %s, duration: %v, segment: %v)",
		b.cfg.Path, b.cfg.Duration, b.cfg.SegmentSize)

	// Start cleanup goroutine
	go b.cleanupLoop()

	// Load any existing segments from disk
	if err := b.loadExistingSegments(); err != nil {
		log.Printf("Warning: failed to load existing segments: %v", err)
	}

	return nil
}

// Stop stops the buffer manager
func (b *Buffer) Stop() {
	if b.cancel != nil {
		b.cancel()
	}
	b.saveIndex()
	log.Println("Ring buffer stopped")
}

// AddSegment adds a new segment to the buffer
func (b *Buffer) AddSegment(seg *Segment) {
	b.mu.Lock()

	b.segments[seg.Sequence] = seg
	if b.firstSeq == 0 || seg.Sequence < b.firstSeq {
		b.firstSeq = seg.Sequence
	}
	if seg.Sequence > b.lastSeq {
		b.lastSeq = seg.Sequence
	}

	b.mu.Unlock()

	// Notify callbacks
	if b.onSegment != nil {
		b.onSegment(seg)
	}

	// Notify ghost clips
	b.notifyGhostClips(seg)

	// Periodically save index
	if seg.Sequence%10 == 0 {
		go b.saveIndex()
	}
}

// SetInitSegment sets the path to the init.mp4 segment
func (b *Buffer) SetInitSegment(path string) {
	b.mu.Lock()
	b.initSegment = path
	b.mu.Unlock()
}

// GetInitSegment returns the init segment path
func (b *Buffer) GetInitSegment() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.initSegment
}

// GetStatus returns the current buffer status
func (b *Buffer) GetStatus() BufferStatus {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var oldestTime, newestTime int64
	if b.firstSeq > 0 {
		if seg, ok := b.segments[b.firstSeq]; ok {
			oldestTime = seg.StartTime.UnixMilli()
		}
	}
	if b.lastSeq > 0 {
		if seg, ok := b.segments[b.lastSeq]; ok {
			newestTime = seg.StartTime.UnixMilli()
		}
	}

	maxSegments := int(b.cfg.Duration / b.cfg.SegmentSize)
	health := float64(len(b.segments)) / float64(maxSegments)
	if health > 1 {
		health = 1
	}

	return BufferStatus{
		Health:       health,
		OldestTime:   oldestTime,
		NewestTime:   newestTime,
		SegmentCount: len(b.segments),
		FirstSeq:     b.firstSeq,
		LastSeq:      b.lastSeq,
		InitSegment:  b.initSegment,
		ChannelID:    b.cfg.ChannelID,
	}
}

// GetSegment returns a segment by sequence number
func (b *Buffer) GetSegment(seq int) (*Segment, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	seg, ok := b.segments[seq]
	return seg, ok
}

// GetSegmentsInRange returns segments within a time range
func (b *Buffer) GetSegmentsInRange(startTime, endTime time.Time) []*Segment {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var result []*Segment
	for seq := b.firstSeq; seq <= b.lastSeq; seq++ {
		seg, ok := b.segments[seq]
		if !ok {
			continue
		}
		segEnd := seg.StartTime.Add(seg.Duration)
		if seg.StartTime.Before(endTime) && segEnd.After(startTime) {
			result = append(result, seg)
		}
	}
	return result
}

// GenerateClip extracts a clip from the buffer
func (b *Buffer) GenerateClip(ctx context.Context, startMs, endMs int64, playID string) (*ClipResult, error) {
	startTime := time.UnixMilli(startMs)
	endTime := time.UnixMilli(endMs)

	// Find segments covering the time range
	segments := b.GetSegmentsInRange(startTime, endTime)
	if len(segments) == 0 {
		return nil, fmt.Errorf("no segments found for time range %v - %v", startTime, endTime)
	}

	// Collect segment paths
	var segPaths []string
	for _, seg := range segments {
		segPaths = append(segPaths, seg.FilePath)
	}

	// Output path
	outputPath := filepath.Join(b.cfg.Path, "clips", fmt.Sprintf("%s.mp4", playID))

	// Calculate trim amounts
	firstSeg := segments[0]
	lastSeg := segments[len(segments)-1]
	trimStart := startTime.Sub(firstSeg.StartTime).Seconds()
	trimEnd := (lastSeg.StartTime.Add(lastSeg.Duration)).Sub(endTime).Seconds()

	// Concatenate segments
	if err := b.ffmpeg.ConcatSegments(ctx, b.initSegment, segPaths, outputPath); err != nil {
		return nil, fmt.Errorf("concat segments: %w", err)
	}

	// Trim if needed (more than 0.1 second off)
	if trimStart > 0.1 || trimEnd > 0.1 {
		tempPath := outputPath + ".temp.mp4"
		os.Rename(outputPath, tempPath)
		defer os.Remove(tempPath)

		duration := endTime.Sub(startTime).Seconds()
		if err := b.ffmpeg.TrimClip(ctx, tempPath, outputPath, trimStart, duration); err != nil {
			return nil, fmt.Errorf("trim clip: %w", err)
		}
	}

	// Get file info
	info, err := os.Stat(outputPath)
	if err != nil {
		return nil, fmt.Errorf("stat output: %w", err)
	}

	return &ClipResult{
		FilePath:      outputPath,
		Duration:      endTime.Sub(startTime).Seconds(),
		FileSizeBytes: info.Size(),
		SegmentCount:  len(segments),
	}, nil
}

// StartGhostClip begins ghost-clipping for a play
func (b *Buffer) StartGhostClip(playID string) error {
	b.ghostMu.Lock()
	defer b.ghostMu.Unlock()

	if _, exists := b.activeGhosts[playID]; exists {
		return fmt.Errorf("ghost clip already active: %s", playID)
	}

	b.mu.RLock()
	startSeq := b.lastSeq
	b.mu.RUnlock()

	b.activeGhosts[playID] = &GhostClip{
		PlayID:    playID,
		StartTime: time.Now(),
		StartSeq:  startSeq,
		Segments:  make([]int, 0),
	}

	log.Printf("Ghost clip started: %s (from seq %d)", playID, startSeq)
	return nil
}

// EndGhostClip ends ghost-clipping for a play and returns segment info
func (b *Buffer) EndGhostClip(playID string) (*GhostClipResult, error) {
	b.ghostMu.Lock()
	defer b.ghostMu.Unlock()

	ghost, exists := b.activeGhosts[playID]
	if !exists {
		return nil, fmt.Errorf("ghost clip not found: %s", playID)
	}

	result := &GhostClipResult{
		PlayID:       playID,
		StartTime:    ghost.StartTime,
		EndTime:      time.Now(),
		SegmentCount: len(ghost.Segments),
		Segments:     ghost.Segments,
	}

	log.Printf("Ghost clip ended: %s (segments: %d)", playID, len(ghost.Segments))
	delete(b.activeGhosts, playID)
	return result, nil
}

// GetActiveGhostClips returns list of active ghost clip IDs
func (b *Buffer) GetActiveGhostClips() []string {
	b.ghostMu.RLock()
	defer b.ghostMu.RUnlock()

	ids := make([]string, 0, len(b.activeGhosts))
	for id := range b.activeGhosts {
		ids = append(ids, id)
	}
	return ids
}

// notifyGhostClips adds segment to active ghost clips
func (b *Buffer) notifyGhostClips(seg *Segment) {
	b.ghostMu.Lock()
	defer b.ghostMu.Unlock()

	for playID, ghost := range b.activeGhosts {
		ghost.Segments = append(ghost.Segments, seg.Sequence)

		// Notify callback
		if b.onGhostSegment != nil {
			b.onGhostSegment(playID, seg)
		}
	}
}

// cleanupLoop removes old segments
func (b *Buffer) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			b.cleanup()
		}
	}
}

// cleanup removes segments older than duration
func (b *Buffer) cleanup() {
	b.mu.Lock()
	defer b.mu.Unlock()

	cutoff := time.Now().Add(-b.cfg.Duration)
	removed := 0

	// Find sequences to remove
	var toRemove []int
	for seq, seg := range b.segments {
		if seg.StartTime.Before(cutoff) {
			toRemove = append(toRemove, seq)
		}
	}

	// Remove segments
	for _, seq := range toRemove {
		seg := b.segments[seq]
		if err := os.Remove(seg.FilePath); err != nil && !os.IsNotExist(err) {
			log.Printf("Warning: failed to remove segment file: %v", err)
		}
		delete(b.segments, seq)
		removed++
	}

	// Update firstSeq
	if removed > 0 {
		for seq := b.firstSeq; seq <= b.lastSeq; seq++ {
			if _, ok := b.segments[seq]; ok {
				b.firstSeq = seq
				break
			}
		}
		log.Printf("Buffer cleanup: removed %d old segments (keeping %d)", removed, len(b.segments))
	}
}

// loadExistingSegments loads segments from disk on startup
func (b *Buffer) loadExistingSegments() error {
	// Look for init.mp4
	initPath := filepath.Join(b.cfg.Path, "init.mp4")
	if _, err := os.Stat(initPath); err == nil {
		b.initSegment = initPath
	}

	// Load index.json if exists
	indexPath := filepath.Join(b.cfg.Path, "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return nil // No index, that's fine
	}

	var index segmentIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return fmt.Errorf("parse index: %w", err)
	}

	// Validate and load segments
	for _, seg := range index.Segments {
		if _, err := os.Stat(seg.FilePath); err != nil {
			continue // Segment file doesn't exist
		}
		b.segments[seg.Sequence] = seg
		if b.firstSeq == 0 || seg.Sequence < b.firstSeq {
			b.firstSeq = seg.Sequence
		}
		if seg.Sequence > b.lastSeq {
			b.lastSeq = seg.Sequence
		}
	}

	log.Printf("Loaded %d existing segments from disk", len(b.segments))
	return nil
}

// saveIndex saves segment index to disk
func (b *Buffer) saveIndex() {
	b.mu.RLock()
	defer b.mu.RUnlock()

	// Collect segments
	segments := make([]*Segment, 0, len(b.segments))
	for _, seg := range b.segments {
		segments = append(segments, seg)
	}

	// Sort by sequence
	sort.Slice(segments, func(i, j int) bool {
		return segments[i].Sequence < segments[j].Sequence
	})

	index := segmentIndex{
		ChannelID:   b.cfg.ChannelID,
		InitSegment: b.initSegment,
		FirstSeq:    b.firstSeq,
		LastSeq:     b.lastSeq,
		UpdatedAt:   time.Now(),
		Segments:    segments,
	}

	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal index: %v", err)
		return
	}

	indexPath := filepath.Join(b.cfg.Path, "index.json")
	if err := os.WriteFile(indexPath, data, 0644); err != nil {
		log.Printf("Failed to save index: %v", err)
	}
}

// segmentIndex is the on-disk index format
type segmentIndex struct {
	ChannelID   string     `json:"channel_id"`
	InitSegment string     `json:"init_segment"`
	FirstSeq    int        `json:"first_seq"`
	LastSeq     int        `json:"last_seq"`
	UpdatedAt   time.Time  `json:"updated_at"`
	Segments    []*Segment `json:"segments"`
}

// BufferStatus represents the buffer status
type BufferStatus struct {
	Health       float64 `json:"health"`
	OldestTime   int64   `json:"oldest_time"`
	NewestTime   int64   `json:"newest_time"`
	SegmentCount int     `json:"segment_count"`
	FirstSeq     int     `json:"first_seq"`
	LastSeq      int     `json:"last_seq"`
	InitSegment  string  `json:"init_segment"`
	ChannelID    string  `json:"channel_id"`
}

// ClipResult represents clip generation result
type ClipResult struct {
	FilePath      string  `json:"file_path"`
	Duration      float64 `json:"duration"`
	FileSizeBytes int64   `json:"file_size_bytes"`
	SegmentCount  int     `json:"segment_count"`
}

// GhostClipResult represents the result of ending a ghost clip
type GhostClipResult struct {
	PlayID       string    `json:"play_id"`
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
	SegmentCount int       `json:"segment_count"`
	Segments     []int     `json:"segments"`
}
