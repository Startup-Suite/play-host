#!/usr/bin/env bash
# run-c4-01a0dbd6.sh <tag> <reps> [only]: start a force_relay session (prod
# idle check inside sess-01a0dbd6.sh), run the reviewer's case-4 driver ON MOON
# (node:24-slim, host network, Chrome 153 at 127.0.0.1:9223), copy results to
# s6/out-<tag>/. Stage 6 (01a0dbd6).
set -uo pipefail
tag=$1; reps=${2:-5}; only=${3:-}
RV=~/sources/tmp/rig-01a0dbd6/review; S6=~/sources/tmp/rig-01a0dbd6/s6
M=/var/home/rocks/tmp-01a0dbd6-s6/$tag
canvas=$($RV/sess-01a0dbd6.sh start 1) || { echo "session start failed" >&2; exit 3; }
sid=$(echo "$canvas" | python3 -c 'import sys,json; print(json.loads(sys.stdin.read().strip().splitlines()[-1])["session_id"])')
canvas=$(echo "$canvas" | python3 -c 'import sys,json; print(json.loads(sys.stdin.read().strip().splitlines()[-1])["canvas_id"])')
echo "tag=$tag canvas=$canvas session=$sid reps=$reps only=$only" >&2
ssh rocks@moon.local "mkdir -p $M/out"
scp -q $RV/case4-01a0dbd6.mjs $RV/rv-lib-01a0dbd6.mjs rocks@moon.local:$M/
ssh rocks@moon.local "podman run --rm --name c4drv-01a0dbd6-$tag --network host -e RV_OUT=/out -e C4_REPS=$reps -e C4_ONLY=$only -v $M:/w -v $M/out:/out --security-opt label=disable -w /w docker.io/library/node:24-slim node case4-01a0dbd6.mjs $canvas $sid" > $S6/c4-$tag-01a0dbd6.log 2>&1
rc=$?
mkdir -p $S6/out-$tag; scp -q "rocks@moon.local:$M/out/*" $S6/out-$tag/ 2>/dev/null
$RV/sess-01a0dbd6.sh report > $S6/out-$tag/final-status-01a0dbd6.txt 2>&1
$RV/sess-01a0dbd6.sh stop >/dev/null 2>&1
echo "rc=$rc session=$sid" >&2
