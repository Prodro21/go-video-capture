package input

import "context"

// Input is the interface for video input sources
type Input interface {
	// Metadata
	Name() string
	Type() string
	Capabilities() Capabilities

	// Lifecycle
	Open(config Config) error
	Close() error

	// Capture
	ReadFrame(ctx context.Context) (*Frame, error)

	// Device discovery
	ListDevices() ([]Device, error)
}

// Capabilities describes what an input supports
type Capabilities struct {
	SupportsAudio    bool
	SupportsVideo    bool
	MaxWidth         int
	MaxHeight        int
	MaxFramerate     int
	SupportedFormats []PixelFormat
}

// Config holds input configuration
type Config struct {
	Device     string
	Width      int
	Height     int
	Framerate  int
	Format     PixelFormat
	BufferSize int
}

// Device represents a discovered input device
type Device struct {
	ID          string
	Name        string
	Type        string
	Description string
	Modes       []VideoMode
}

// VideoMode represents a supported video mode
type VideoMode struct {
	Width     int
	Height    int
	Framerate float64
	Format    PixelFormat
}

// Frame represents a captured video frame
type Frame struct {
	Data      []byte
	Width     int
	Height    int
	Format    PixelFormat
	Timestamp int64 // Unix nanoseconds
	Sequence  int64
}

// PixelFormat represents a video pixel format
type PixelFormat string

const (
	FormatYUV420P PixelFormat = "yuv420p"
	FormatYUYV    PixelFormat = "yuyv"
	FormatUYVY    PixelFormat = "uyvy"
	FormatNV12    PixelFormat = "nv12"
	FormatRGB24   PixelFormat = "rgb24"
	FormatBGRA    PixelFormat = "bgra"
)

// Registry holds registered input plugins
var Registry = make(map[string]func() Input)

// Register registers an input plugin
func Register(name string, factory func() Input) {
	Registry[name] = factory
}

// Get returns an input plugin by name
func Get(name string) (Input, bool) {
	factory, ok := Registry[name]
	if !ok {
		return nil, false
	}
	return factory(), true
}
