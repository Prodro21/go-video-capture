package ffmpeg

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	ff, err := New()
	if err != nil {
		t.Skipf("FFmpeg not found: %v", err)
	}

	version, err := ff.Version(context.Background())
	if err != nil {
		t.Fatalf("Failed to get version: %v", err)
	}

	t.Logf("FFmpeg version: %s", version)
}

func TestListDevices(t *testing.T) {
	ff, err := New()
	if err != nil {
		t.Skipf("FFmpeg not found: %v", err)
	}

	output, err := ff.ListInputDevices(context.Background())
	if err != nil {
		t.Logf("List devices error (expected): %v", err)
	}
	t.Logf("Devices:\n%s", output)
}

func TestGenerateSegments(t *testing.T) {
	ff, err := New()
	if err != nil {
		t.Skipf("FFmpeg not found: %v", err)
	}

	// Skip if no test video available
	testVideo := os.Getenv("TEST_VIDEO")
	if testVideo == "" {
		t.Skip("Set TEST_VIDEO env var to test segment generation")
	}

	// Create temp output dir
	outputDir, err := os.MkdirTemp("", "segments_*")
	if err != nil {
		t.Fatalf("Create temp dir: %v", err)
	}
	defer os.RemoveAll(outputDir)

	// Generate segments
	sw := ff.NewSegmentWriter(SegmentConfig{
		Input:           testVideo,
		Codec:           "libx264",
		Preset:          "ultrafast",
		SegmentDuration: 2.0,
		OutputDir:       outputDir,
	})

	var segments []SegmentInfo
	sw.OnSegment(func(seg SegmentInfo) {
		t.Logf("Segment %d: %s (%.2f KB)", seg.Sequence, seg.Path, float64(seg.Size)/1024)
		segments = append(segments, seg)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := sw.Start(ctx); err != nil {
		t.Fatalf("Start segment writer: %v", err)
	}

	if err := sw.Wait(); err != nil {
		t.Logf("Segment writer finished: %v", err)
	}

	// Check output
	initPath := filepath.Join(outputDir, "init.mp4")
	if _, err := os.Stat(initPath); err != nil {
		t.Errorf("Init segment not found: %v", err)
	}

	if len(segments) == 0 {
		t.Error("No segments generated")
	}

	t.Logf("Generated %d segments", len(segments))
}

func TestProbe(t *testing.T) {
	ff, err := New()
	if err != nil {
		t.Skipf("FFmpeg not found: %v", err)
	}

	testVideo := os.Getenv("TEST_VIDEO")
	if testVideo == "" {
		t.Skip("Set TEST_VIDEO env var to test probe")
	}

	info, err := ff.GetVideoInfo(context.Background(), testVideo)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}

	t.Logf("Video info: %dx%d @ %.2f fps, codec=%s, duration=%.2fs",
		info.Width, info.Height, info.Framerate, info.Codec, info.Duration)
}
