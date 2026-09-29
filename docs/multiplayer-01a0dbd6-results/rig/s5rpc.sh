#!/usr/bin/env bash
# s5rpc.sh <mode> [VAR=val ...]: evaluates stage5-01a0dbd6.exs in the dev BEAM.
set -euo pipefail
C=dev-platform-01a0dbd6-76bb-7001-a322-08a81e3d7927
mode=$1; shift
envs="System.put_env(\\\"S5_MODE\\\", \\\"$mode\\\");"
for kv in "$@"; do k=${kv%%=*}; v=${kv#*=}; envs="$envs System.put_env(\\\"$k\\\", \\\"$v\\\");"; done
scp -q ~/sources/tmp/rig-01a0dbd6/stage5-01a0dbd6.exs rocks@moon.local:/tmp/stage5-01a0dbd6.exs
ssh rocks@moon.local "podman cp /tmp/stage5-01a0dbd6.exs $C:/tmp/stage5-01a0dbd6.exs && podman exec $C bash -c 'cd /app/apps/platform && elixir --sname s5rpc\$\$ --rpc-eval dev \"$envs Code.eval_file(\\\"/tmp/stage5-01a0dbd6.exs\\\")\"'" 2>&1 | grep -E "S5_JSON|rror" | sed 's/^S5_JSON //'
