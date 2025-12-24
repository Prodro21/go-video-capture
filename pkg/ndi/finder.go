package ndi

/*
#include <stdlib.h>
#include <stdbool.h>
#include <stdint.h>

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

extern NDIlib_find_instance_t NDIlib_find_create_v2(const NDIlib_find_create_t* p_create_settings);
extern void NDIlib_find_destroy(NDIlib_find_instance_t p_instance);
extern bool NDIlib_find_wait_for_sources(NDIlib_find_instance_t p_instance, uint32_t timeout_in_ms);
extern const NDIlib_source_t* NDIlib_find_get_current_sources(NDIlib_find_instance_t p_instance, uint32_t* p_no_sources);
*/
import "C"
import (
	"errors"
	"time"
	"unsafe"
)

// FinderConfig configures NDI source discovery
type FinderConfig struct {
	ShowLocalSources bool   // Include sources on this machine
	Groups           string // NDI groups to search (comma-separated)
	ExtraIPs         string // Additional IPs to search (comma-separated)
}

// Finder discovers NDI sources on the network
type Finder struct {
	instance C.NDIlib_find_instance_t
}

// NewFinder creates a new NDI source finder
func NewFinder(config *FinderConfig) (*Finder, error) {
	if err := Initialize(); err != nil {
		return nil, err
	}

	var createSettings C.NDIlib_find_create_t
	createSettings.show_local_sources = C.bool(true)

	if config != nil {
		createSettings.show_local_sources = C.bool(config.ShowLocalSources)
		if config.Groups != "" {
			createSettings.p_groups = C.CString(config.Groups)
			defer C.free(unsafe.Pointer(createSettings.p_groups))
		}
		if config.ExtraIPs != "" {
			createSettings.p_extra_ips = C.CString(config.ExtraIPs)
			defer C.free(unsafe.Pointer(createSettings.p_extra_ips))
		}
	}

	instance := C.NDIlib_find_create_v2(&createSettings)
	if instance == nil {
		return nil, errors.New("failed to create NDI finder")
	}

	return &Finder{instance: instance}, nil
}

// Destroy releases the finder resources
func (f *Finder) Destroy() {
	if f.instance != nil {
		C.NDIlib_find_destroy(f.instance)
		f.instance = nil
	}
}

// WaitForSources waits for NDI sources to be discovered
func (f *Finder) WaitForSources(timeout time.Duration) ([]Source, error) {
	if f.instance == nil {
		return nil, errors.New("finder not initialized")
	}

	timeoutMs := uint32(timeout.Milliseconds())
	C.NDIlib_find_wait_for_sources(f.instance, C.uint32_t(timeoutMs))

	return f.GetCurrentSources(), nil
}

// GetCurrentSources returns all currently discovered sources
func (f *Finder) GetCurrentSources() []Source {
	if f.instance == nil {
		return nil
	}

	var numSources C.uint32_t
	cSources := C.NDIlib_find_get_current_sources(f.instance, &numSources)

	if numSources == 0 || cSources == nil {
		return nil
	}

	sources := make([]Source, int(numSources))
	sourceSlice := unsafe.Slice(cSources, int(numSources))

	for i := 0; i < int(numSources); i++ {
		sources[i] = Source{
			Name:    C.GoString(sourceSlice[i].p_ndi_name),
			Address: C.GoString(sourceSlice[i].p_url_address),
		}
	}

	return sources
}

// FindSourceByName searches for a source with the given name
func (f *Finder) FindSourceByName(name string, timeout time.Duration) (*Source, error) {
	sources, err := f.WaitForSources(timeout)
	if err != nil {
		return nil, err
	}

	for _, src := range sources {
		if src.Name == name {
			return &src, nil
		}
	}

	return nil, errors.New("source not found: " + name)
}
