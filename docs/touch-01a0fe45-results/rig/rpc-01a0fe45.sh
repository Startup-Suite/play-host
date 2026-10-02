#!/usr/bin/env bash
# rpc-01a0fe45.sh <mode> [VAR=val ...]: evaluates rig-01a0fe45.exs in the dev BEAM.
set -euo pipefail
C=dev-platform-01a0fe45-d117-7001-a501-ad6244a19795
mode=$1; shift
envs="System.put_env(\\\"R5_MODE\\\", \\\"$mode\\\");"
for kv in "$@"; do k=${kv%%=*}; v=${kv#*=}; envs="$envs System.put_env(\\\"$k\\\", \\\"$v\\\");"; done
scp -q ~/sources/tmp/rig-01a0fe45/rig-01a0fe45.exs rocks@moon.local:/tmp/rig-01a0fe45.exs
ssh rocks@moon.local "podman cp /tmp/rig-01a0fe45.exs $C:/tmp/rig-01a0fe45.exs && podman exec $C bash -c 'cd /app/apps/suite_release && elixir --sname r5rpc\$\$ --rpc-eval dev \"$envs Code.eval_file(\\\"/tmp/rig-01a0fe45.exs\\\")\"'" 2>&1 | grep -E "R5_JSON|rror" | sed 's/^R5_JSON //'
