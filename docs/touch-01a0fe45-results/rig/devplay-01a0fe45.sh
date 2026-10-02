#!/usr/bin/env bash
# devplay-01a0fe45.sh: (moon) recreate dev-platform-<wt> as a WITH-PLAY dev
# server: same mounts/env/port as spawn-dev-on-moon.sh, but run from
# apps/suite_release (ADR 0055 release host: platform + game_stream), whose
# assets.build writes the with-Play bundle; it is copied into
# apps/platform/priv/static/assets (the Endpoint's static dir; sync-excluded).
set -euo pipefail
[ "$(hostname -s)" = moon ] || { echo "moon only" >&2; exit 2; }
WT=01a0fe45-d117-7001-a501-ad6244a19795
C=dev-platform-$WT
DB=platform_dev_01a0fe45_d117_7001_a501_ad6244a19795
DEV_PG_PASS=$(grep DEV_PG_PASS ~/dev-from-hive/.env-dev-postgres | cut -d= -f2)
podman rm -f $C >/dev/null 2>&1 || true
taskset -c 0-5,8-13 podman run -d --name $C \
  --label dev-on-moon.port=4034 --label dev-on-moon.worktree=$WT \
  --network dev --cgroup-parent=background.slice \
  -v /var/home/rocks/dev-from-hive/$WT:/app:Z \
  -v dev-uploads-$WT:/data/platform/chat_uploads:Z \
  -e CHAT_ATTACHMENTS_ROOT=/data/platform/chat_uploads \
  -w /app/apps/suite_release \
  -e MIX_ENV=dev -e PGHOST=dev-postgres -e PGPORT=5432 -e PGUSER=postgres -e PGPASSWORD="$DEV_PG_PASS" \
  -e PLATFORM_DEV_DATABASE=$DB -e PHX_BIND_IP=0.0.0.0 -e PORT=4000 \
  -e GAME_STREAM_ENABLED=true \
  -p 192.168.1.200:4034:4000 \
  docker.io/elixir:1.19-otp-28 \
  bash -c '
    set -e
    mix local.hex --force >/dev/null
    mix local.rebar --force >/dev/null
    (cd ../platform && mix deps.get >/dev/null)
    mix deps.get
    mix deps.compile
    if ! command -v node >/dev/null 2>&1; then
      apt-get update >/dev/null && apt-get install -y --no-install-recommends nodejs npm >/dev/null
    fi
    (cd ../platform/assets && npm install --silent --no-audit --no-fund)
    mix assets.setup
    mix assets.build
    mkdir -p ../platform/priv/static/assets
    cp -a priv/static/assets/. ../platform/priv/static/assets/
    echo DEVPLAY_ASSETS_COPIED $(grep -c game_stream_touch ../platform/priv/static/assets/js/app.js)
    mix ecto.create
    mix run --no-start -e "Platform.Release.migrate(); IO.puts(:DEVPLAY_MIGRATED)"
    exec elixir --sname dev -S mix phx.server
  '
echo "started $C"
