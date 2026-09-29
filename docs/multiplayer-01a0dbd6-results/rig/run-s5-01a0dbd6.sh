#!/usr/bin/env bash
# run-s5-01a0dbd6.sh <label> <players> <spectators> <relay 0|1> [mobile idx list|-] [n] [jitter ms]
# One measured run = one fresh session on the wave DEV play-host.
set -uo pipefail
label=$1; players=$2; specs=$3; relay=$4; mobile=${5:-}; [ "$mobile" = - ] && mobile=; n=${6:-50}; jitter=${7:-0}
D=~/sources/tmp/rig-01a0dbd6/s5; R=~/sources/tmp/rig-01a0dbd6; W='C:\Users\slaps\play-host-dev-01a0dbd6'
log=$D/run-$label-01a0dbd6.log
exec > >(sed -u -E 's/credential: \\"[^\\]*\\"/credential: <redacted>/g' | tee -a "$log") 2>&1
echo "== run $label players=$players spectators=$specs relay=$relay mobile=$mobile n=$n jitter=$jitter at $(date -u +%FT%TZ)"
pc=$(ssh wave "powershell -NoProfile -ExecutionPolicy Bypass -File $W\\prodcheck-01a0dbd6.ps1")
echo "prodcheck before: $pc"
case "$pc" in *PROD_BUSY=False*) ;; *) echo "ABORT: prod not idle"; exit 3;; esac
# Moon must be quiet (other agents' test runs load it): wait up to 20 min
# for a 10 s whole-host busy below 15 %, and record what we measured.
for q in $(seq 1 80); do
  mb=$(ssh rocks@moon.local 'read -r _ a b c d e f g h _ < /proc/stat; t1=$((a+b+c+d+e+f+g+h)); i1=$((d+e)); sleep 10; read -r _ a b c d e f g h _ < /proc/stat; t2=$((a+b+c+d+e+f+g+h)); i2=$((d+e)); echo $(( 1000*((t2-t1)-(i2-i1))/(t2-t1) ))')
  [ "${mb:-1000}" -lt 150 ] && break
  echo "moon busy ${mb} permille; waiting ($(ssh rocks@moon.local "podman ps --format '{{.Names}}' | grep core-test | tr '\n' ' '"))"
  sleep 5
done
echo "moon quiet check: ${mb} permille"
$R/s5rpc.sh stop >/dev/null
start=$($R/s5rpc.sh start S5_SHA=643edd99da14b4a71534c9339568bf7e7939de0f S5_RELAY=$relay)
echo "start: $(echo "$start" | sed -E 's/credential: \\"[^\\]*\\"/credential: <redacted>/')"
for i in $(seq 1 60); do st=$($R/s5rpc.sh report | grep -o 'state: \\"[a-z]*' | head -1); case "$st" in *connecting*|*live*) break;; "") echo "ABORT: report empty"; exit 6;; *ended*|*failed*) echo "session $st"; exit 4;; esac; sleep 3; done
echo "session state: $st"
cv=$($R/s5rpc.sh canvas); echo "canvas: $cv"
canvas=$(echo "$cv" | sed -E 's/.*"canvas_id":"([^"]+)".*/\1/')
session=$(echo "$cv" | sed -E 's/.*"session_id":"([^"]+)".*/\1/')
[[ "$canvas" =~ ^[0-9a-f-]{36}$ && "$session" =~ ^[0-9a-f-]{36}$ ]] || { echo "ABORT: no canvas/session"; $R/s5rpc.sh stop; exit 5; }
dur=$(( 150 + 25*(players+specs) ))
$D/moon-cpu-01a0dbd6.sh $dur $label &
cpid=$!
ssh wave "powershell -NoProfile -ExecutionPolicy Bypass -File $W\\wave-sample-01a0dbd6.ps1 -Seconds $dur -Label $label" &
wpid=$!
echo "driver start $(date -u +%T)"
node ~/sources/worktrees/play-host-01a0dbd6/spike/driver/run-probes-multi.mjs --canvas "$canvas" \
  --players $players --spectators $specs --n $n --label $label ${mobile:+--mobile $mobile} \
  --out $D/probes-$label-01a0dbd6.json --shot $D/frame-$label-01a0dbd6.jpg --max-seconds 200 --jitter-ms $jitter ${FORGE:+--forge-spectator} > $D/brief-$label-01a0dbd6.json
echo "driver exit $? at $(date -u +%T)"
pc2=$(ssh wave "powershell -NoProfile -ExecutionPolicy Bypass -File $W\\prodcheck-01a0dbd6.ps1")
echo "prodcheck after: $pc2"
echo "roster at end: $($R/s5rpc.sh report | grep -o 'roster.*' | cut -c1-600)"
$R/s5rpc.sh stop
sleep 2
$R/s5rpc.sh row S5_SESSION=$session > $D/row-$label-01a0dbd6.json
echo "row: $(cat $D/row-$label-01a0dbd6.json)"
wait $wpid; wait $cpid
for t in 1 2 3; do sleep 3; scp -q "wave:C:/Users/slaps/play-host-dev-01a0dbd6/logs/sample-$label-01a0dbd6.log" $D/ && break; done
ssh wave "Get-Content $W\\logs\\play-host.log | Select-String '$session' | Select-Object -ExpandProperty Line" > $D/hostlog-$label-01a0dbd6.log
echo "== done $label $(date -u +%FT%TZ)"
