#!/usr/bin/env bash
# run-01a0fe45.sh <label> <scenario> <vp> <canvas>: prod check, driver, host log, analysis.
set -uo pipefail
label=$1; scen=$2; vp=$3; canvas=$4
R=~/sources/tmp/rig-01a0fe45; O=$R/out; W='C:\Users\slaps\play-host-dev-01a0fe45'
pc=$(ssh wave "powershell -NoProfile -ExecutionPolicy Bypass -File $W\\staging\\prodcheck-01a0fe45.ps1")
echo "prodcheck before: $pc"; case "$pc" in *PROD_BUSY=False*) ;; *) echo "ABORT: prod not idle"; exit 3;; esac
n0=$(ssh wave "(Get-Content $W\\logs\\play-host.log).Count" | tr -d '\r')
node $R/touch-01a0fe45.mjs --canvas $canvas --scenario $scen --vp $vp --label $label --out $O 2> $O/$label-driver-01a0fe45.log
echo "driver exit $?"
sleep 2
ssh wave "Get-Content $W\\logs\\play-host.log | Select-Object -Skip $n0" > $O/$label-hostlog-01a0fe45.log
pc2=$(ssh wave "powershell -NoProfile -ExecutionPolicy Bypass -File $W\\staging\\prodcheck-01a0fe45.ps1"); echo "prodcheck after: $pc2"
echo "$pc" > $O/$label-prodcheck-01a0fe45.log; echo "$pc2" >> $O/$label-prodcheck-01a0fe45.log
python3 $R/hostlines-01a0fe45.py $O/$label-01a0fe45.json $O/$label-hostlog-01a0fe45.log 180 > $O/$label-lines-01a0fe45.json
cat $O/$label-lines-01a0fe45.json | python3 -c 'import json,sys; d=json.load(sys.stdin); print("lines",d["total_lines"],"unassigned",d["unassigned"]); [print(" ",s["step"],s["st_down"],s["st_up"],s["st_cancel"],s["sd"],s["indices"],s.get("err_pct",""),s["drops"][:1]) for s in d["steps"]]'
grep -c ERROR $O/$label-driver-01a0fe45.log
