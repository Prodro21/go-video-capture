//go:build !ndi

package ndi

import (
	"context"
	"errors"
	"time"
)

var errNotAvailable = errors.New("NDI SDK not available - build with -tags ndi")

// Source represents an NDI source on the network
type Source struct {
	Name    string `json:"name"`
	Address string `json:"address,omitempty"`
}

// VideoFrame represents a decoded NDI video frame
type VideoFrame struct {
	Width       int
	Height      int
	FourCC      uint32
	FrameRateN  int
	FrameRateD  int
	Data        []byte
	LineStride  int
	Timecode    int64
	Timestamp   int64
	AspectRatio float32
}

// AudioFrame represents decoded NDI audio
type AudioFrame struct {
	SampleRate    int
	NumChannels   int
	NumSamples    int
	Data          []float32
	ChannelStride int
	Timecode      int64
	Timestamp     int64
}

// FrameType indicates the type of frame received
type FrameType int

const (
	FrameTypeNone         FrameType = 0
	FrameTypeVideo        FrameType = 1
	FrameTypeAudio        FrameType = 2
	FrameTypeMetadata     FrameType = 3
	FrameTypeError        FrameType = 4
	FrameTypeStatusChange FrameType = 100
)

// FourCC video format codes
const (
	FourCCUYVY = 0x59565955
	FourCCBGRA = 0x41524742
	FourCCBGRX = 0x58524742
	FourCCRGBA = 0x41424752
	FourCCRGBX = 0x58424752
	FourCCNV12 = 0x3231564E
	FourCCI420 = 0x30323449
)

// ColorFormat options for receiver
type ColorFormat int

const (
	ColorFormatBGRXBGRA ColorFormat = 0
	ColorFormatUYVYBGRA ColorFormat = 1
	ColorFormatRGBXRGBA ColorFormat = 2
	ColorFormatUYVYRGBA ColorFormat = 3
	ColorFormatFastest  ColorFormat = 100
	ColorFormatBest     ColorFormat = 101
)

// Bandwidth options for receiver
type Bandwidth int

const (
	BandwidthMetadataOnly Bandwidth = -10
	BandwidthAudioOnly    Bandwidth = 10
	BandwidthLowest       Bandwidth = 0
	BandwidthHighest      Bandwidth = 100
)

// Initialize is a no-op stub
func Initialize() error {
	return errNotAvailable
}

// Destroy is a no-op stub
func Destroy() {}

// Version returns unknown
func Version() string {
	return "not available"
}

// IsAvailable returns false when NDI is not built
func IsAvailable() bool {
	return false
}

// DiscoverSources returns an error when NDI is not available
func DiscoverSources(ctx context.Context) ([]Source, error) {
	return nil, errNotAvailable
}

// CheckSupport returns false when NDI is not built
func CheckSupport(ctx context.Context) bool {
	return false
}

// FinderConfig configures NDI source discovery
type FinderConfig struct {
	ShowLocalSources bool
	Groups           string
	ExtraIPs         string
}

// Finder stub
type Finder struct{}

// NewFinder returns an error when NDI is not available
func NewFinder(config *FinderConfig) (*Finder, error) {
	return nil, errNotAvailable
}

// Destroy is a no-op
func (f *Finder) Destroy() {}

// WaitForSources returns an error
func (f *Finder) WaitForSources(timeout time.Duration) ([]Source, error) {
	return nil, errNotAvailable
}

// GetCurrentSources returns nil
func (f *Finder) GetCurrentSources() []Source {
	return nil
}

// FindSourceByName returns an error
func (f *Finder) FindSourceByName(name string, timeout time.Duration) (*Source, error) {
	return nil, errNotAvailable
}

// ReceiverConfig configures NDI receiver
type ReceiverConfig struct {
	SourceName   string
	ColorFormat  ColorFormat
	Bandwidth    Bandwidth
	ReceiverName string
}

// ReceiverStats holds receiver statistics
type ReceiverStats struct {
	FramesReceived uint64
	FramesDropped  uint64
	LastFrameTime  time.Time
	Width          int
	Height         int
	FrameRateN     int
	FrameRateD     int
}

// Receiver stub
type Receiver struct{}

// NewReceiver returns an error when NDI is not available
func NewReceiver(config ReceiverConfig) (*Receiver, error) {
	return nil, errNotAvailable
}

// Destroy is a no-op
func (r *Receiver) Destroy() {}

// Source returns empty source
func (r *Receiver) Source() Source {
	return Source{}
}

// Stats returns empty stats
func (r *Receiver) Stats() ReceiverStats {
	return ReceiverStats{}
}

// CaptureVideo returns an error
func (r *Receiver) CaptureVideo(timeout time.Duration) (*VideoFrame, error) {
	return nil, errNotAvailable
}

// CaptureAudio returns an error
func (r *Receiver) CaptureAudio(timeout time.Duration) (*AudioFrame, error) {
	return nil, errNotAvailable
}

// Run returns an error
func (r *Receiver) Run(ctx context.Context, onVideo func(*VideoFrame), onAudio func(*AudioFrame)) error {
	return errNotAvailable
}

// LastError returns the stub error
func (r *Receiver) LastError() error {
	return errNotAvailable
}

// CaptureConfig configures NDI capture
type CaptureConfig struct {
	SourceName      string
	OutputDir       string
	SegmentDuration float64
	Codec           string
	Preset          string
	Bitrate         int
}

// SegmentInfo contains information about a completed segment
type SegmentInfo struct {
	Sequence  int
	Path      string
	StartTime time.Time
	Duration  time.Duration
	Size      int64
}

// Capture stub
type Capture struct{}

// NewCapture returns an error when NDI is not available
func NewCapture(config CaptureConfig) (*Capture, error) {
	return nil, errNotAvailable
}

// OnSegment is a no-op
func (c *Capture) OnSegment(fn func(SegmentInfo)) {}

// Start returns an error
func (c *Capture) Start(ctx context.Context) error {
	return errNotAvailable
}

// Stop is a no-op
func (c *Capture) Stop() {}

// IsRunning returns false
func (c *Capture) IsRunning() bool {
	return false
}

// Stats returns empty stats
func (c *Capture) Stats() ReceiverStats {
	return ReceiverStats{}
}

// LastError returns the stub error
func (c *Capture) LastError() error {
	return errNotAvailable
}

// FrameCount returns 0
func (c *Capture) FrameCount() uint64 {
	return 0
}
