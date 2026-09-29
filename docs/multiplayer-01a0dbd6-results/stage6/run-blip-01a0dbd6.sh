#!/usr/bin/env bash
# run-blip-01a0dbd6.sh <tag>: session + 3 live viewers + a deliberate socket drop.
set -uo pipefail
tag=$1
RV=~/sources/tmp/rig-01a0dbd6/review; S6=~/sources/tmp/rig-01a0dbd6/s6
C=dev-platform-01a0dbd6-76bb-7001-a322-08a81e3d7927
M=/var/home/rocks/tmp-01a0dbd6-s6/$tag
out=$($RV/sess-01a0dbd6.sh start 1) || { echo "session start failed" >&2; exit 3; }
sid=$(echo "$out" | python3 -c 'import sys,json; print(json.loads(sys.stdin.read().strip().splitlines()[-1])["session_id"])')
canvas=$(echo "$out" | python3 -c 'import sys,json; print(json.loads(sys.stdin.read().strip().splitlines()[-1])["canvas_id"])')
echo "tag=$tag session=$sid canvas=$canvas" >&2
ssh rocks@moon.local "rm -rf $M && mkdir -p $M/out"
scp -q $RV/rv-lib-01a0dbd6.mjs $S6/blip-drv-01a0dbd6.mjs rocks@moon.local:$M/
ssh rocks@moon.local "podman run -d --name blipdrv-01a0dbd6-$tag --network host -e RV_OUT=/out -v $M:/w -v $M/out:/out --security-opt label=disable -w /w docker.io/library/node:24-slim node blip-drv-01a0dbd6.mjs $canvas $sid" >/dev/null
for i in $(seq 1 120); do ssh rocks@moon.local "test -f $M/out/blip-ready" && break; sleep 2; done
ssh rocks@moon.local "podman exec $C bash -c 'cd /app/apps/platform && elixir --sname s6blip\$\$ --rpc-eval dev \"Code.eval_file(\\\"/tmp/blip-01a0dbd6.exs\\\")\"' 2>&1 | grep BLIP; date +%s%3N > $M/out/blip-go-done"
ssh rocks@moon.local "podman wait blipdrv-01a0dbd6-$tag >/dev/null; podman logs blipdrv-01a0dbd6-$tag 2>&1 | tail -20; podman rm blipdrv-01a0dbd6-$tag >/dev/null"
mkdir -p $S6/out-$tag; scp -q "rocks@moon.local:$M/out/*" $S6/out-$tag/
$RV/sess-01a0dbd6.sh report > $S6/out-$tag/final-status-01a0dbd6.txt 2>&1
echo "session=$sid" >&2
