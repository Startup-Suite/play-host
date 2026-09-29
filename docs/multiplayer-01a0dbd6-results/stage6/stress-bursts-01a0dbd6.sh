#!/usr/bin/env bash
# Stage 6 (01a0dbd6): heartbeat + large-message stress through the real suite
# client, from wave, on the STRESS runtime (never the play host's), while the
# long case-4 run is live. Burst A: gorilla's 4096 B buffer (fragmented);
# B: the new 64 KiB default; C: B plus core pushing back at 100/s.
set -uo pipefail
S6=~/sources/tmp/rig-01a0dbd6/s6; W='C:\Users\slaps\play-host-dev-01a0dbd6'
C=dev-platform-01a0dbd6-76bb-7001-a322-08a81e3d7927
run() { timeout 400 ssh wave "$W\\wsstress2-01a0dbd6.exe -runtime s6stress-01a0dbd6 -token-file $W\\secrets\\s6stress-token-01a0dbd6 -dur 240s -rate 40 -workers 6 -min 1000 -max 16000 -hb 1s $*" 2>&1 | tail -3; }
sleep 300
echo "== A 4096 $(date -u +%T)"; run -write-buffer 4096
sleep 60
echo "== B default $(date -u +%T)"; run
sleep 60
echo "== C default + core pushes $(date -u +%T)"
(timeout 300 ssh rocks@moon.local "podman exec $C bash -c 'cd /app/apps/platform && S6_SECS=230 S6_RATE=100 elixir --sname s6dl\$\$ --rpc-eval dev \"Code.eval_file(\\\"/tmp/download-01a0dbd6.exs\\\")\"' 2>&1 | grep S6_DOWN" &)
run
echo "== end $(date -u +%T)"
