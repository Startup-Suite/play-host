# READ-ONLY: is the PROD play-host (C:\Users\slaps\play-host) streaming?
# Busy = a Godot/ffmpeg child whose command line points at the prod checkouts,
# or a prod log whose last session line is not an end. -Dir points the same
# check at another instance (the positive control: the busy dev instance).
param([string]$Dir = 'C:\Users\slaps\play-host')
$p = @(Get-CimInstance Win32_Process | Where-Object { ($_.Name -like "Godot*" -or $_.Name -like "ffmpeg*") -and $_.CommandLine -like "*$Dir\checkouts*" })
$tail = Get-Content "$Dir\logs\play-host.log" -Tail 300 | Where-Object { $_ -notlike "ice WARNING*" }
$last = $tail | Where-Object { $_ -match "\] (status |session ended|offer|viewer)|ready for play_session_start" } | Select-Object -Last 1
$busy = ($p.Count -gt 0) -or ($last -and $last -notmatch "session ended|ready for play_session_start")
$udp = @(Get-NetUDPEndpoint -ErrorAction SilentlyContinue | ? { ($_.LocalPort -ge 40300 -and $_.LocalPort -le 40309) -or $_.LocalPort -in 40320,40330,40331 }).Count
"PROD_BUSY=$busy dir=$Dir children=$($p.Count) prod_udp_binds=$udp last=[$last]"
