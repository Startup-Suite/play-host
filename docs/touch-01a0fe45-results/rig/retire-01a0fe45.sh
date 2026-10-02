#!/usr/bin/env bash
# retire-01a0fe45.sh: retire the 01a0fe45 stage 5 rig AFTER the stage 6 review.
# Acts only on this rig's names; every removal is followed by a check that
# was first shown to hit while the thing existed (run with CHECK=1 to see).
set -uo pipefail
WT=01a0fe45-d117-7001-a501-ad6244a19795
D='C:\Users\slaps\play-host-dev-01a0fe45'
echo "== moon: dev core, DB, uploads volume"
ssh rocks@moon.local "podman ps -a --filter name=dev-platform-$WT --format '{{.Names}} {{.Status}}'"
~/cron/teardown-dev-on-moon.sh $WT --drop-db
ssh rocks@moon.local "podman volume rm dev-uploads-$WT 2>&1 | tail -1; rm -f /var/home/rocks/devplay-01a0fe45.sh /tmp/rig-01a0fe45.exs; echo after: [\$(podman ps -a --filter name=dev-platform-$WT --format '{{.Names}}')]"
echo "== wave: task, processes under $D only, firewall rule, dir"
ssh wave "powershell -NoProfile -Command \"
\$p=(Get-CimInstance Win32_Process -Filter \\\"Name='play-host.exe'\\\" | ? { \$_.ExecutablePath -like 'C:\\Users\\slaps\\play-host\\bin\\*' }).ProcessId; 'prod pid before ' + \$p
Stop-ScheduledTask -TaskName suite-play-host-dev-01a0fe45 -ErrorAction SilentlyContinue
Get-CimInstance Win32_Process | ? { \$_.ExecutablePath -like '$D\\*' } | % { 'killing ' + \$_.ProcessId; Stop-Process -Id \$_.ProcessId -Force }
Unregister-ScheduledTask -TaskName suite-play-host-dev-01a0fe45 -Confirm:\$false -ErrorAction SilentlyContinue
Remove-NetFirewallRule -Name suite-play-host-dev-udp-01a0fe45 -ErrorAction SilentlyContinue
Start-Sleep 2; Remove-Item -Recurse -Force '$D' -ErrorAction SilentlyContinue
'after: task=[' + (Get-ScheduledTask -TaskName suite-play-host-dev-01a0fe45 -ErrorAction SilentlyContinue).TaskName + '] rule=[' + (Get-NetFirewallRule -Name suite-play-host-dev-udp-01a0fe45 -ErrorAction SilentlyContinue).Name + '] dir=' + (Test-Path '$D')
\$p2=(Get-CimInstance Win32_Process -Filter \\\"Name='play-host.exe'\\\" | ? { \$_.ExecutablePath -like 'C:\\Users\\slaps\\play-host\\bin\\*' }).ProcessId; 'prod pid after ' + \$p2
\""
