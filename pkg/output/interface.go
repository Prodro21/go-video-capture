package output

import "context"

// Output is the interface for video output destinations
type Output interface {
	// Metadata
	Name() string
	Type() string

	// Lifecycle
	Open(config Config) error
	Close() error

	// Output
	WriteSegment(ctx context.Context, seg *Segment) error
	WriteInit(ctx context.Context, init *InitSegment) error
}

// Config holds output configuration
type Config struct {
	Path       string // Output path or URL
	Format     string // hls, dash, srt, rtmp, file
	SegmentDur float64
}

// Segment represents an encoded video segment
type Segment struct {
	Sequence  int
	Data      []byte
	StartTime int64   // Unix ms
	Duration  float64 // seconds
	IsInit    bool
}

// InitSegment represents the initialization segment
type InitSegment struct {
	Data       []byte
	Codec      string
	Width      int
	Height     int
	Framerate  int
}

// Registry holds registered output plugins
var Registry = make(map[string]func() Output)

// Register registers an output plugin
func Register(name string, factory func() Output) {
	Registry[name] = factory
}

// Get returns an output plugin by name
func Get(name string) (Output, bool) {
	factory, ok := Registry[name]
	if !ok {
		return nil, false
	}
	return factory(), true
}
