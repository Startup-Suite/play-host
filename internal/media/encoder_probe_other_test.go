//go:build !windows

package media

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitializeEncoderFeedsPastFFmpegProbeBuffer(t *testing.T) {
	ffmpeg := filepath.Join(t.TempDir(), "ffmpeg")
	const script = `#!/bin/sh
bytes=$(wc -c | tr -d ' ')
[ "$bytes" -eq 49152 ] || exit 23
printf h264
`
	if err := os.WriteFile(ffmpeg, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := InitializeEncoder(ffmpeg, DefaultPreset(), EncoderVideoToolbox); err != nil {
		t.Fatal(err)
	}
}
