# macOS play-host setup

Stage 1 makes the host configuration and encoder path portable. LaunchAgent installation and process-group cleanup are covered by the next stage; the browser smoke test is stage 3.

## Prerequisites

Install Godot 4, FFmpeg with `h264_videotoolbox` and `libx264`, Git, and an SSH client. Bare executable names are resolved with the service’s `PATH`; explicit paths and app bundles are supported. A Godot bundle such as `/Applications/Godot.app` resolves to `/Applications/Godot.app/Contents/MacOS/Godot`.

Confirm the encoders before enabling fallback:

```sh
ffmpeg -hide_banner -encoders | grep -E 'h264_videotoolbox|libx264'
```

## Configuration

```json
{
  "suite_url": "wss://suite.example/runtime/ws",
  "runtime_id": "play-host-rock",
  "token_file": "/Users/Shared/play-host/secrets/runtime-token",
  "godot": "/Applications/Godot.app",
  "rendering_driver": "",
  "ffmpeg": "ffmpeg",
  "encoder": "auto",
  "encoder_fallback": true,
  "git": "git",
  "ssh": "ssh",
  "known_hosts": "/Users/Shared/play-host/known_hosts",
  "mirrors_dir": "/Users/Shared/play-host/mirrors",
  "checkouts_dir": "/Users/Shared/play-host/checkouts",
  "addon_dir": "/Users/Shared/play-host/suite_play",
  "logs_dir": "/Users/Shared/play-host/logs",
  "host_ip": "",
  "udp_min": 40300,
  "udp_max": 40309,
  "godot_port": 40320,
  "rtp_port": 40330,
  "import_timeout_s": 1200,
  "keep_checkouts": 3,
  "heartbeat_s": 10,
  "launch_timeout_s": 90,
  "progress_every_ms": 5000,
  "repos": [
    {"match": "github.com/example/game", "url": "git@github.com:example/game.git"}
  ]
}
```

Encoder fields:

- `encoder`: `auto`, `nvenc`, `videotoolbox`, or `libx264`. `auto` resolves to NVENC on Windows, VideoToolbox on macOS, and libx264 on other hosts. A session may select an experiment arm with the additive `play_session_start.encoder.encoder` field; an omitted field remains `auto` for compatibility with current Suite and Wave traffic.
- `encoder_fallback`: when `true`, play-host performs a one-frame initialization of the chosen encoder when the stream starts. It tries libx264 only if that initialization fails. When `false` (the default), no software fallback or initialization probe is added; this preserves Wave’s Windows/NVENC path.
- Bitrate, frame rate, width, height, and GOP continue to come from Suite’s encoder preset. NVENC-only `preset` and `tune` values are ignored by VideoToolbox and mapped to fixed low-latency settings for libx264.

Rendering fields:

- `rendering_driver`: omitted or empty on macOS, allowing Godot to use its platform default. Set it only for a project-specific override. Windows continues to default to `vulkan`.

All filesystem values are native paths and remain individual argv elements; do not add shell quoting to JSON values.
