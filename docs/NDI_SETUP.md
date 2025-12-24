# NDI Setup Guide

This guide explains how to set up native NDI support in go-video-capture.

## Prerequisites

### 1. Install NDI SDK

Download and install the NDI SDK from [ndi.video/tools](https://ndi.video/tools/):

**macOS:**
```bash
# Download "NDI SDK for Apple" from ndi.video
# Install the package - it installs to:
# /Library/NDI SDK for Apple/

# Verify installation:
ls "/Library/NDI SDK for Apple/lib/macOS/"
# Should show: libndi.dylib
```

**Linux:**
```bash
# Download NDI SDK for Linux from ndi.video
# Extract and install:
tar xzf NDI_SDK_Linux.tar.gz
sudo cp -r "NDI SDK for Linux/lib/x86_64-linux-gnu/"* /usr/lib/
sudo ldconfig

# Or for development, set library path:
export LD_LIBRARY_PATH="/path/to/NDI SDK for Linux/lib/x86_64-linux-gnu:$LD_LIBRARY_PATH"
```

**Windows:**
```powershell
# Download and install "NDI SDK" from ndi.video
# Default install location: C:\Program Files\NDI\NDI 5 SDK\
```

### 2. Install NDI Tools (Optional but Recommended)

Install NDI Tools to get test sources and monitoring utilities:

- **NDI Tools** - Includes NDI Test Patterns, NDI Studio Monitor
- Available for macOS, Windows, Linux

## Building go-video-capture with NDI

```bash
cd go-video-capture

# Build with CGO enabled (required for NDI)
CGO_ENABLED=1 go build -o capture ./cmd/capture

# Run with NDI config
./capture -config configs/ndi-native-example.yaml
```

### Build Tags (Optional)

To build without NDI support (if SDK not available):
```bash
go build -tags no_ndi -o capture ./cmd/capture
```

## Configuration

### Native NDI Input

```yaml
input:
  type: ndi
  device: "MY-COMPUTER (OBS)"  # NDI source name exactly as shown

buffer:
  duration: 30m
  segment_size: 2s
  path: "/var/capture/ndi"

encode:
  codec: h264
  preset: fast
  bitrate: 6000
```

### Discovering NDI Sources

Use the API to discover available NDI sources:

```bash
# List all NDI sources on the network
curl http://localhost:8081/api/v1/ndi/sources

# Response:
{
  "supported": true,
  "sources": [
    {"name": "MY-COMPUTER (OBS)", "address": "192.168.1.100:5961"},
    {"name": "NDI-CAMERA (Channel 1)", "address": "192.168.1.50:5960"}
  ]
}
```

### Check NDI Support

```bash
curl http://localhost:8081/api/v1/ndi/support

# Response:
{"supported": true}
```

## Example Configurations

### Single NDI Camera

```yaml
input:
  type: ndi
  device: "PTZ-CAMERA (Main)"
  resolution: "1920x1080"
  framerate: 30

buffer:
  duration: 30m
  segment_size: 2s
  path: "/var/capture/ptz"

encode:
  codec: h264
  preset: fast
  bitrate: 8000

api:
  port: 8081

platform:
  enabled: true
  url: "http://localhost:8080"
  agent_name: "PTZ Camera Capture"
```

### Multi-NDI Setup

```yaml
channels:
  - id: wide-angle
    input:
      type: ndi
      device: "CAMERA-1 (Wide)"
    buffer:
      duration: 30m
      segment_size: 2s
      path: "/var/capture/multi"
    encode:
      codec: h264
      preset: fast
      bitrate: 6000

  - id: tight-angle
    input:
      type: ndi
      device: "CAMERA-2 (Tight)"
    buffer:
      duration: 30m
      segment_size: 2s
      path: "/var/capture/multi"
    encode:
      codec: h264
      preset: fast
      bitrate: 6000

  - id: endzone
    input:
      type: ndi
      device: "CAMERA-3 (Endzone)"
    buffer:
      duration: 30m
      segment_size: 2s
      path: "/var/capture/multi"
    encode:
      codec: h264
      preset: fast
      bitrate: 6000

buffer:
  path: "/var/capture/multi"

api:
  port: 8081
```

## Troubleshooting

### "NDI SDK not available"

The NDI runtime library is not found. Check:

1. SDK is installed correctly
2. Library path is set:
   - macOS: `/Library/NDI SDK for Apple/lib/macOS/libndi.dylib`
   - Linux: `/usr/lib/libndi.so` or `LD_LIBRARY_PATH`
   - Windows: SDK bin directory in PATH

### "Source not found"

The NDI source name must match exactly, including parentheses:
- Correct: `MY-PC (OBS)`
- Wrong: `MY-PC` or `my-pc (obs)`

Use the discovery API to get exact names:
```bash
curl http://localhost:8081/api/v1/ndi/sources
```

### Poor Performance

1. **Use wired network** - NDI works best over gigabit Ethernet
2. **Check bandwidth** - Full HD NDI needs ~125 Mbps
3. **Use NDI|HX** for lower bandwidth (hardware-compressed)

### Firewall Issues

NDI uses mDNS for discovery (port 5353 UDP) and dynamic ports for video.

Open these ports:
- UDP 5353 (mDNS discovery)
- TCP/UDP 5960-5990 (NDI streams)

## Testing with NDI Test Patterns

1. Install NDI Tools
2. Launch "NDI Test Patterns" app
3. It creates a source like "MY-COMPUTER (Test Pattern 1)"
4. Use this name in your config to test

## Performance Tips

- **Resolution**: 1080p is the sweet spot for most use cases
- **Frame rate**: 30fps for sports, 60fps if needed
- **Bitrate**: 6-8 Mbps for good quality 1080p
- **Network**: Use dedicated VLAN for NDI traffic
- **Hardware encoding**: Use `nvenc` or `videotoolbox` if available
