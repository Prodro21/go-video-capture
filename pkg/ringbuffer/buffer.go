package ringbuffer

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Config holds ring buffer configuration
type Config struct {
	Duration    time.Duration // How long to keep segments
	SegmentSize time.Duration // Duration of each segment
	Path        string        // Storage path
}

// Buffer manages a ring buffer of CMAF segments
type Buffer struct {
	cfg Config

	mu           sync.RWMutex
	segments     map[int]*Segment // sequence -> segment
	firstSeq     int
	lastSeq      int
	initSegment  string // Path to init.mp4

	// Ghost-clipping state
	ghostMu      sync.RWMutex
	activeGhosts map[string]*GhostClip // playID -> ghost clip state

	// Channels for segment events
	segmentChan chan *Segment
}

// Segment represents a single CMAF segment
type Segment struct {
	Sequence  int
	FilePath  string
	StartTime time.Time
	Duration  time.Duration
	SizeBytes int64
}

// GhostClip tracks an active ghost clip
type GhostClip struct {
	PlayID    string
	StartTime time.Time
	Segments  []int // Sequence numbers included
}

// New creates a new ring buffer
func New(cfg Config) (*Buffer, error) {
	// Ensure path exists
	if err := os.MkdirAll(cfg.Path, 0755); err != nil {
		return nil, fmt.Errorf("create buffer path: %w", err)
	}

	return &Buffer{
		cfg:          cfg,
		segments:     make(map[int]*Segment),
		activeGhosts: make(map[string]*GhostClip),
		segmentChan:  make(chan *Segment, 100),
	}, nil
}

// Start starts the buffer manager
func (b *Buffer) Start(ctx context.Context) error {
	log.Printf("Ring buffer started (path: %s, duration: %v)", b.cfg.Path, b.cfg.Duration)

	// Start cleanup goroutine
	go b.cleanupLoop(ctx)

	return nil
}

// Stop stops the buffer manager
func (b *Buffer) Stop() {
	close(b.segmentChan)
	log.Println("Ring buffer stopped")
}

// AddSegment adds a new segment to the buffer
func (b *Buffer) AddSegment(seg *Segment) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.segments[seg.Sequence] = seg
	if b.firstSeq == 0 || seg.Sequence < b.firstSeq {
		b.firstSeq = seg.Sequence
	}
	if seg.Sequence > b.lastSeq {
		b.lastSeq = seg.Sequence
	}

	// Notify ghost clips
	b.notifyGhostClips(seg)

	// Non-blocking send to segment channel
	select {
	case b.segmentChan <- seg:
	default:
	}
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
	}
}

// GenerateClip extracts a clip from the buffer
func (b *Buffer) GenerateClip(ctx context.Context, startMs, endMs int64, playID string) (*ClipResult, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	startTime := time.UnixMilli(startMs)
	endTime := time.UnixMilli(endMs)

	// Find segments covering the time range
	var clipSegments []*Segment
	for seq := b.firstSeq; seq <= b.lastSeq; seq++ {
		seg, ok := b.segments[seq]
		if !ok {
			continue
		}
		segEnd := seg.StartTime.Add(seg.Duration)
		if seg.StartTime.Before(endTime) && segEnd.After(startTime) {
			clipSegments = append(clipSegments, seg)
		}
	}

	if len(clipSegments) == 0 {
		return nil, fmt.Errorf("no segments found for time range")
	}

	// TODO: Concatenate segments and trim edges
	// For now, return placeholder
	outputPath := filepath.Join(b.cfg.Path, "clips", fmt.Sprintf("%s.mp4", playID))

	return &ClipResult{
		FilePath:      outputPath,
		Duration:      endTime.Sub(startTime).Seconds(),
		FileSizeBytes: 0, // TODO: Calculate actual size
	}, nil
}

// StartGhostClip begins ghost-clipping for a play
func (b *Buffer) StartGhostClip(playID string) error {
	b.ghostMu.Lock()
	defer b.ghostMu.Unlock()

	if _, exists := b.activeGhosts[playID]; exists {
		return fmt.Errorf("ghost clip already active: %s", playID)
	}

	b.activeGhosts[playID] = &GhostClip{
		PlayID:    playID,
		StartTime: time.Now(),
		Segments:  make([]int, 0),
	}

	log.Printf("Ghost clip started: %s", playID)
	return nil
}

// EndGhostClip ends ghost-clipping for a play
func (b *Buffer) EndGhostClip(playID string) error {
	b.ghostMu.Lock()
	defer b.ghostMu.Unlock()

	ghost, exists := b.activeGhosts[playID]
	if !exists {
		return fmt.Errorf("ghost clip not found: %s", playID)
	}

	log.Printf("Ghost clip ended: %s (segments: %d)", playID, len(ghost.Segments))
	delete(b.activeGhosts, playID)
	return nil
}

// notifyGhostClips adds segment to active ghost clips
func (b *Buffer) notifyGhostClips(seg *Segment) {
	b.ghostMu.Lock()
	defer b.ghostMu.Unlock()

	for _, ghost := range b.activeGhosts {
		ghost.Segments = append(ghost.Segments, seg.Sequence)
		// TODO: Emit ClipSegmentReady event via WebSocket
	}
}

// cleanupLoop removes old segments
func (b *Buffer) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
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

	for seq, seg := range b.segments {
		if seg.StartTime.Before(cutoff) {
			// Remove file
			os.Remove(seg.FilePath)
			delete(b.segments, seq)
			removed++
		}
	}

	// Update firstSeq
	if removed > 0 {
		for seq := b.firstSeq; seq <= b.lastSeq; seq++ {
			if _, ok := b.segments[seq]; ok {
				b.firstSeq = seq
				break
			}
		}
		log.Printf("Buffer cleanup: removed %d old segments", removed)
	}
}

// BufferStatus represents the buffer status
type BufferStatus struct {
	Health       float64 `json:"health"`
	OldestTime   int64   `json:"oldest_time"`
	NewestTime   int64   `json:"newest_time"`
	SegmentCount int     `json:"segment_count"`
	FirstSeq     int     `json:"first_seq"`
	LastSeq      int     `json:"last_seq"`
}

// ClipResult represents clip generation result
type ClipResult struct {
	FilePath      string  `json:"file_path"`
	Duration      float64 `json:"duration"`
	FileSizeBytes int64   `json:"file_size_bytes"`
}
