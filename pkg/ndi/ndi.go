//go:build ndi

package ndi

/*
#cgo CFLAGS: -I${SRCDIR}/include
#cgo darwin LDFLAGS: -L/Library/NDI\ SDK\ for\ Apple/lib/macOS -lndi
#cgo linux LDFLAGS: -L/usr/lib -lndi
#cgo windows LDFLAGS: -L"C:/Program Files/NDI/NDI 5 SDK/Lib/x64" -lProcessing.NDI.Lib.x64
*/
import "C"

import (
	"context"
	"time"
)

// DiscoverSources discovers NDI sources on the network using the native SDK
func DiscoverSources(ctx context.Context) ([]Source, error) {
	finder, err := NewFinder(nil)
	if err != nil {
		return nil, err
	}
	defer finder.Destroy()

	// Wait for sources with timeout from context or default 5 seconds
	timeout := 5 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}

	return finder.WaitForSources(timeout)
}

// CheckSupport checks if NDI SDK is available
func CheckSupport(ctx context.Context) bool {
	return IsAvailable()
}
