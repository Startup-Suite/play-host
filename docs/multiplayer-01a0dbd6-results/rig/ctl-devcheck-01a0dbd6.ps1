# READ-ONLY: is the PROD play-host (C:\Users\slaps\play-host) streaming?
# Busy = a Godot/ffmpeg child whose command line points at the prod dir, or
# a prod log tail whose last session line is not an end.
$p = Get-CimInstance Win32_Process | Where-Object { ($_.Name -like "Godot*" -or $_.Name -like "ffmpeg*") -and $_.CommandLine -like "*\play-host-dev-01a0dbd6\checkouts*" }
$tail = Get-Content C:\Users\slaps\play-host-dev-01a0dbd6\logs\play-host.log -Tail 200 | Where-Object { $_ -notlike "ice WARNING*" }
$last = $tail | Where-Object { $_ -match "\] (status |session ended|offer|viewer)|ready for play_session_start" } | Select-Object -Last 1
$busy = ($p.Count -gt 0) -or ($last -and $last -notmatch "session ended|ready for play_session_start")
"PROD_BUSY=$busy prod_children=$($p.Count) last=[$last]"
