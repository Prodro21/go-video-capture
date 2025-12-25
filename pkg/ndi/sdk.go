//go:build ndi

package ndi

/*
#cgo CFLAGS: -I${SRCDIR}/include
#cgo darwin LDFLAGS: -L/Library/NDI\ SDK\ for\ Apple/lib/macOS -lndi
#cgo linux LDFLAGS: -L/usr/lib -lndi
#cgo windows LDFLAGS: -L"C:/Program Files/NDI/NDI 5 SDK/Lib/x64" -lProcessing.NDI.Lib.x64

#include <stdlib.h>
#include <stdbool.h>
#include <stdint.h>

// NDI SDK types and functions
// These match the NDI SDK header definitions

typedef struct NDIlib_source_t {
    const char* p_ndi_name;
    const char* p_url_address;
} NDIlib_source_t;

typedef struct NDIlib_find_create_t {
    bool show_local_sources;
    const char* p_groups;
    const char* p_extra_ips;
} NDIlib_find_create_t;

typedef void* NDIlib_find_instance_t;
typedef void* NDIlib_recv_instance_t;

typedef struct NDIlib_recv_create_v3_t {
    NDIlib_source_t source_to_connect_to;
    int color_format;      // NDIlib_recv_color_format_e
    int bandwidth;         // NDIlib_recv_bandwidth_e
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

typedef enum NDIlib_FourCC_video_type_e {
    NDIlib_FourCC_type_UYVY = 0x59565955,  // YCbCr 4:2:2
    NDIlib_FourCC_type_BGRA = 0x41524742,  // BGRA
    NDIlib_FourCC_type_BGRX = 0x58524742,  // BGRX
    NDIlib_FourCC_type_RGBA = 0x41424752,  // RGBA
    NDIlib_FourCC_type_RGBX = 0x58424752,  // RGBX
    NDIlib_FourCC_type_NV12 = 0x3231564E,  // NV12 (Y plane + interleaved UV)
    NDIlib_FourCC_type_I420 = 0x30323449,  // I420 (Y + U + V planes)
    NDIlib_FourCC_type_P216 = 0x36313250,  // P216 (16-bit Y + 16-bit interleaved UV)
} NDIlib_FourCC_video_type_e;

typedef struct NDIlib_video_frame_v2_t {
    int xres;
    int yres;
    NDIlib_FourCC_video_type_e FourCC;
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

// External NDI SDK functions (linked at runtime)
extern bool NDIlib_initialize(void);
extern void NDIlib_destroy(void);
extern const char* NDIlib_version(void);

extern NDIlib_find_instance_t NDIlib_find_create_v2(const NDIlib_find_create_t* p_create_settings);
extern void NDIlib_find_destroy(NDIlib_find_instance_t p_instance);
extern bool NDIlib_find_wait_for_sources(NDIlib_find_instance_t p_instance, uint32_t timeout_in_ms);
extern const NDIlib_source_t* NDIlib_find_get_current_sources(NDIlib_find_instance_t p_instance, uint32_t* p_no_sources);

extern NDIlib_recv_instance_t NDIlib_recv_create_v3(const NDIlib_recv_create_v3_t* p_create_settings);
extern void NDIlib_recv_destroy(NDIlib_recv_instance_t p_instance);
extern void NDIlib_recv_connect(NDIlib_recv_instance_t p_instance, const NDIlib_source_t* p_src);
extern NDIlib_frame_type_e NDIlib_recv_capture_v2(NDIlib_recv_instance_t p_instance, NDIlib_video_frame_v2_t* p_video_data, NDIlib_audio_frame_v2_t* p_audio_data, void* p_metadata, uint32_t timeout_in_ms);
extern void NDIlib_recv_free_video_v2(NDIlib_recv_instance_t p_instance, const NDIlib_video_frame_v2_t* p_video_data);
extern void NDIlib_recv_free_audio_v2(NDIlib_recv_instance_t p_instance, const NDIlib_audio_frame_v2_t* p_audio_data);

// Helper to get SDK version safely
static inline const char* ndi_get_version() {
    return NDIlib_version();
}
*/
import "C"
import (
	"errors"
	"sync"
	"unsafe"
)

var (
	initOnce    sync.Once
	initialized bool
	initError   error
)

// Initialize initializes the NDI SDK. Must be called before any other NDI functions.
// Safe to call multiple times - will only initialize once.
func Initialize() error {
	initOnce.Do(func() {
		if C.NDIlib_initialize() {
			initialized = true
		} else {
			initError = errors.New("failed to initialize NDI SDK - ensure NDI runtime is installed")
		}
	})
	return initError
}

// Destroy cleans up the NDI SDK. Should be called when done using NDI.
func Destroy() {
	if initialized {
		C.NDIlib_destroy()
		initialized = false
	}
}

// Version returns the NDI SDK version string
func Version() string {
	if err := Initialize(); err != nil {
		return "unknown (not initialized)"
	}
	return C.GoString(C.ndi_get_version())
}

// IsAvailable checks if NDI SDK is available and can be initialized
func IsAvailable() bool {
	return Initialize() == nil
}

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
	FourCCUYVY = 0x59565955 // YCbCr 4:2:2
	FourCCBGRA = 0x41524742 // BGRA
	FourCCBGRX = 0x58524742 // BGRX
	FourCCRGBA = 0x41424752 // RGBA
	FourCCRGBX = 0x58424752 // RGBX
	FourCCNV12 = 0x3231564E // NV12
	FourCCI420 = 0x30323449 // I420
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

// cSource converts Go Source to C NDIlib_source_t
func (s *Source) cSource() C.NDIlib_source_t {
	return C.NDIlib_source_t{
		p_ndi_name:    C.CString(s.Name),
		p_url_address: C.CString(s.Address),
	}
}

// freeCSource frees memory allocated by cSource
func freeCSource(cs C.NDIlib_source_t) {
	C.free(unsafe.Pointer(cs.p_ndi_name))
	C.free(unsafe.Pointer(cs.p_url_address))
}
