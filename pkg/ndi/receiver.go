//go:build ndi

package ndi

/*
#include <stdlib.h>
#include <stdbool.h>
#include <stdint.h>
#include <string.h>

typedef struct NDIlib_source_t {
    const char* p_ndi_name;
    const char* p_url_address;
} NDIlib_source_t;

typedef void* NDIlib_recv_instance_t;

typedef struct NDIlib_recv_create_v3_t {
    NDIlib_source_t source_to_connect_to;
    int color_format;
    int bandwidth;
    bool allow_video_fields;
    const char* p_ndi_recv_name;
} NDIlib_recv_create_v3_t;

typedef enum NDIlib_frame_type_e {
    NDIlib_frame_type_none = 0,
    NDIlib_frame_type_video = 1,
    NDIlib_frame_type_audio = 2,
    NDIlib_frame_type_metadata = 3,
    NDIlib_frame_type_error = 4,
    NDIlib_frame_type_status_change = 100
} NDIlib_frame_type_e;

typedef struct NDIlib_video_frame_v2_t {
    int xres;
    int yres;
    int FourCC;
    int frame_rate_N;
    int frame_rate_D;
    float picture_aspect_ratio;
    int frame_format_type;
    int64_t timecode;
    uint8_t* p_data;
    int line_stride_in_bytes;
    const char* p_metadata;
    int64_t timestamp;
} NDIlib_video_frame_v2_t;

typedef struct NDIlib_audio_frame_v2_t {
    int sample_rate;
    int no_channels;
    int no_samples;
    int64_t timecode;
    float* p_data;
    int channel_stride_in_bytes;
    const char* p_metadata;
    int64_t timestamp;
} NDIlib_audio_frame_v2_t;

// Color format options
#define NDIlib_recv_color_format_BGRX_BGRA 0
#define NDIlib_recv_color_format_UYVY_BGRA 1
#define NDIlib_recv_color_format_RGBX_RGBA 2
#define NDIlib_recv_color_format_UYVY_RGBA 3
#define NDIlib_recv_color_format_fastest 100
#define NDIlib_recv_color_format_best 101

// Bandwidth options
#define NDIlib_recv_bandwidth_metadata_only -10
#define NDIlib_recv_bandwidth_audio_only 10
#define NDIlib_recv_bandwidth_lowest 0
#define NDIlib_recv_bandwidth_highest 100

extern NDIlib_recv_instance_t NDIlib_recv_create_v3(const NDIlib_recv_create_v3_t* p_create_settings);
extern void NDIlib_recv_destroy(NDIlib_recv_instance_t p_instance);
extern void NDIlib_recv_connect(NDIlib_recv_instance_t p_instance, const NDIlib_source_t* p_src);
extern NDIlib_frame_type_e NDIlib_recv_capture_v2(NDIlib_recv_instance_t p_instance, NDIlib_video_frame_v2_t* p_video_data, NDIlib_audio_frame_v2_t* p_audio_data, void* p_metadata, uint32_t timeout_in_ms);
extern void NDIlib_recv_free_video_v2(NDIlib_recv_instance_t p_instance, const NDIlib_video_frame_v2_t* p_video_data);
extern void NDIlib_recv_free_audio_v2(NDIlib_recv_instance_t p_instance, const NDIlib_audio_frame_v2_t* p_audio_data);

// Helper to copy video frame data
static inline void copy_video_data(uint8_t* dst, const NDIlib_video_frame_v2_t* frame) {
    int data_size = frame->line_stride_in_bytes * frame->yres;
    memcpy(dst, frame->p_data, data_size);
}

// Helper to copy audio data
static inline void copy_audio_data(float* dst, const NDIlib_audio_frame_v2_t* frame) {
    int data_size = frame->no_channels * frame->no_samples * sizeof(float);
    memcpy(dst, frame->p_data, data_size);
}
*/
import "C"
import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"
)

// ReceiverConfig configures NDI receiver
type ReceiverConfig struct {
	SourceName   string
	ColorFormat  ColorFormat
	Bandwidth    Bandwidth
	ReceiverName string
}

// Receiver receives video/audio from an NDI source
type Receiver struct {
	instance C.NDIlib_recv_instance_t
	source   Source
	config   ReceiverConfig

	mu       sync.RWMutex
	running  bool
	lastErr  error
	stats    ReceiverStats
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

// NewReceiver creates a new NDI receiver for the specified source
func NewReceiver(config ReceiverConfig) (*Receiver, error) {
	if err := Initialize(); err != nil {
		return nil, err
	}

	// First find the source
	finder, err := NewFinder(nil)
	if err != nil {
		return nil, fmt.Errorf("create finder: %w", err)
	}
	defer finder.Destroy()

	source, err := finder.FindSourceByName(config.SourceName, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("find source: %w", err)
	}

	// Set defaults
	if config.ColorFormat == 0 {
		config.ColorFormat = ColorFormatUYVYBGRA
	}
	if config.Bandwidth == 0 {
		config.Bandwidth = BandwidthHighest
	}
	if config.ReceiverName == "" {
		config.ReceiverName = "go-video-capture"
	}

	// Create receiver
	cRecvName := C.CString(config.ReceiverName)
	defer C.free(unsafe.Pointer(cRecvName))

	cSourceName := C.CString(source.Name)
	defer C.free(unsafe.Pointer(cSourceName))

	var cSourceAddr *C.char
	if source.Address != "" {
		cSourceAddr = C.CString(source.Address)
		defer C.free(unsafe.Pointer(cSourceAddr))
	}

	createSettings := C.NDIlib_recv_create_v3_t{
		source_to_connect_to: C.NDIlib_source_t{
			p_ndi_name:    cSourceName,
			p_url_address: cSourceAddr,
		},
		color_format:       C.int(config.ColorFormat),
		bandwidth:          C.int(config.Bandwidth),
		allow_video_fields: C.bool(true),
		p_ndi_recv_name:    cRecvName,
	}

	instance := C.NDIlib_recv_create_v3(&createSettings)
	if instance == nil {
		return nil, errors.New("failed to create NDI receiver")
	}

	return &Receiver{
		instance: instance,
		source:   *source,
		config:   config,
	}, nil
}

// Destroy releases receiver resources
func (r *Receiver) Destroy() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.instance != nil {
		C.NDIlib_recv_destroy(r.instance)
		r.instance = nil
	}
}

// Source returns the connected source
func (r *Receiver) Source() Source {
	return r.source
}

// Stats returns current receiver statistics
func (r *Receiver) Stats() ReceiverStats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.stats
}

// CaptureVideo captures a single video frame with timeout
func (r *Receiver) CaptureVideo(timeout time.Duration) (*VideoFrame, error) {
	if r.instance == nil {
		return nil, errors.New("receiver not initialized")
	}

	var cVideoFrame C.NDIlib_video_frame_v2_t
	timeoutMs := uint32(timeout.Milliseconds())

	frameType := C.NDIlib_recv_capture_v2(
		r.instance,
		&cVideoFrame,
		nil, // no audio
		nil, // no metadata
		C.uint32_t(timeoutMs),
	)

	switch FrameType(frameType) {
	case FrameTypeVideo:
		// Copy frame data to Go memory
		dataSize := int(cVideoFrame.line_stride_in_bytes) * int(cVideoFrame.yres)
		data := make([]byte, dataSize)
		C.copy_video_data((*C.uint8_t)(unsafe.Pointer(&data[0])), &cVideoFrame)

		frame := &VideoFrame{
			Width:       int(cVideoFrame.xres),
			Height:      int(cVideoFrame.yres),
			FourCC:      uint32(cVideoFrame.FourCC),
			FrameRateN:  int(cVideoFrame.frame_rate_N),
			FrameRateD:  int(cVideoFrame.frame_rate_D),
			Data:        data,
			LineStride:  int(cVideoFrame.line_stride_in_bytes),
			Timecode:    int64(cVideoFrame.timecode),
			Timestamp:   int64(cVideoFrame.timestamp),
			AspectRatio: float32(cVideoFrame.picture_aspect_ratio),
		}

		// Free the NDI frame
		C.NDIlib_recv_free_video_v2(r.instance, &cVideoFrame)

		// Update stats
		r.mu.Lock()
		r.stats.FramesReceived++
		r.stats.LastFrameTime = time.Now()
		r.stats.Width = frame.Width
		r.stats.Height = frame.Height
		r.stats.FrameRateN = frame.FrameRateN
		r.stats.FrameRateD = frame.FrameRateD
		r.mu.Unlock()

		return frame, nil

	case FrameTypeNone:
		return nil, nil // Timeout, no frame available

	case FrameTypeError:
		return nil, errors.New("NDI receive error")

	case FrameTypeStatusChange:
		return nil, nil // Status change, try again

	default:
		return nil, nil // Audio or metadata, try again
	}
}

// CaptureAudio captures a single audio frame with timeout
func (r *Receiver) CaptureAudio(timeout time.Duration) (*AudioFrame, error) {
	if r.instance == nil {
		return nil, errors.New("receiver not initialized")
	}

	var cAudioFrame C.NDIlib_audio_frame_v2_t
	timeoutMs := uint32(timeout.Milliseconds())

	frameType := C.NDIlib_recv_capture_v2(
		r.instance,
		nil, // no video
		&cAudioFrame,
		nil, // no metadata
		C.uint32_t(timeoutMs),
	)

	if FrameType(frameType) != FrameTypeAudio {
		return nil, nil
	}

	// Copy audio data to Go memory
	numSamples := int(cAudioFrame.no_samples) * int(cAudioFrame.no_channels)
	data := make([]float32, numSamples)
	C.copy_audio_data((*C.float)(unsafe.Pointer(&data[0])), &cAudioFrame)

	frame := &AudioFrame{
		SampleRate:    int(cAudioFrame.sample_rate),
		NumChannels:   int(cAudioFrame.no_channels),
		NumSamples:    int(cAudioFrame.no_samples),
		Data:          data,
		ChannelStride: int(cAudioFrame.channel_stride_in_bytes),
		Timecode:      int64(cAudioFrame.timecode),
		Timestamp:     int64(cAudioFrame.timestamp),
	}

	C.NDIlib_recv_free_audio_v2(r.instance, &cAudioFrame)

	return frame, nil
}

// Run starts the receiver loop, sending frames to the provided callback
func (r *Receiver) Run(ctx context.Context, onVideo func(*VideoFrame), onAudio func(*AudioFrame)) error {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return errors.New("receiver already running")
	}
	r.running = true
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Try to capture video
		if onVideo != nil {
			frame, err := r.CaptureVideo(100 * time.Millisecond)
			if err != nil {
				r.mu.Lock()
				r.lastErr = err
				r.mu.Unlock()
				continue
			}
			if frame != nil {
				onVideo(frame)
			}
		}

		// Try to capture audio
		if onAudio != nil {
			frame, err := r.CaptureAudio(10 * time.Millisecond)
			if err != nil {
				continue
			}
			if frame != nil {
				onAudio(frame)
			}
		}
	}
}

// LastError returns the last error encountered
func (r *Receiver) LastError() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastErr
}
