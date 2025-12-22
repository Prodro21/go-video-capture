package encode

import (
	"context"

	"github.com/video-system/go-video-capture/pkg/input"
	"github.com/video-system/go-video-capture/pkg/output"
)

// Encoder is the interface for video encoders
type Encoder interface {
	// Metadata
	Name() string
	Type() string // software, nvenc, qsv, videotoolbox
	Capabilities() Capabilities

	// Lifecycle
	Open(config Config) error
	Close() error

	// Encoding
	Encode(ctx context.Context, frame *input.Frame) (*output.Segment, error)
	Flush(ctx context.Context) ([]*output.Segment, error)
	GetInitSegment() *output.InitSegment
}

// Capabilities describes what an encoder supports
type Capabilities struct {
	SupportedCodecs   []string // h264, hevc, av1
	SupportsHardware  bool
	MaxWidth          int
	MaxHeight         int
	SupportsBFrames   bool
	SupportsLookahead bool
}

// Config holds encoder configuration
type Config struct {
	Codec      string // h264, hevc
	Width      int
	Height     int
	Framerate  int
	Bitrate    int    // kbps
	Preset     string // ultrafast, fast, medium, slow
	Profile    string // baseline, main, high
	GOP        int    // Keyframe interval in frames
	BFrames    int
	SegmentDur float64 // Target segment duration in seconds
}

// Registry holds registered encoder plugins
var Registry = make(map[string]func() Encoder)

// Register registers an encoder plugin
func Register(name string, factory func() Encoder) {
	Registry[name] = factory
}

// Get returns an encoder plugin by name
func Get(name string) (Encoder, bool) {
	factory, ok := Registry[name]
	if !ok {
		return nil, false
	}
	return factory(), true
}
