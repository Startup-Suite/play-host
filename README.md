# play-host

Windows host that turns a Godot build into a WebRTC stream for Startup Suite's
`game_stream` canvas (task 01a0db5f). Go, `CGO_ENABLED=0`, cross-compiled:

    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -o play-host.exe ./cmd/play-host

Encoding is an `ffmpeg -c:v h264_nvenc` subprocess; WebRTC is pion v4.

Layout:

- `cmd/play-host` - the binary (stage 1: `play-host spike`).
- `internal/media` - ffmpeg/NVENC argument building, RTP and Annex-B forwarding.
- `internal/rtc` - pion peer: sendonly video, `input-state` + `input-events` channels.
- `internal/probe` - input-to-photon probe code (shared with the addon and the page).
- `internal/launch` - Job Object launcher; Stop kills only what the host started.
- `internal/spike` - stage 1 harness and the browser probe page.
- `addons/suite_play` - the Godot autoload (probe marker, frame export; input in stage 3).
- `spike/godot` - throwaway scene; `spike/driver` - CDP latency driver.
- `docs/spike-01a0db5f.md` - stage 1 findings.

Tests: `go test ./...` (linux) and `GOOS=windows go vet ./...`.
