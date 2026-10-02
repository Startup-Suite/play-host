# deploy-01a0fe45.ps1 -Sha <sha> [-NoTouch]: (re)deploy the DEV play-host
# instance for task 01a0fe45 stage 5 in C:\Users\slaps\play-host-dev-01a0fe45.
# Acts ONLY on processes whose ExecutablePath is under that dir. Reads prod
# files (known_hosts, survival-game deploy key) and never writes under
# C:\Users\slaps\play-host. -NoTouch writes features without game_stream_touch.
param([Parameter(Mandatory)][string]$Sha, [switch]$NoTouch)
$ErrorActionPreference = 'Stop'
$D = 'C:\Users\slaps\play-host-dev-01a0fe45'; $P = 'C:\Users\slaps\play-host'
$T = 'suite-play-host-dev-01a0fe45'; $FW = 'suite-play-host-dev-udp-01a0fe45'
foreach ($s in 'bin','addons\suite_play','secrets','logs','mirrors','checkouts') { New-Item -ItemType Directory -Force "$D\$s" | Out-Null }
$log = "$D\logs\deploy-01a0fe45.log"
function L($m) { $l = "$(Get-Date -Format o) $m"; Add-Content $log $l; $l }
L "deploy dev play-host 01a0fe45 sha $Sha notouch=$NoTouch"
$prod = (Get-CimInstance Win32_Process -Filter "Name='play-host.exe'" | ? { $_.ExecutablePath -like "$P\bin\*" }).ProcessId
L "prod pid before: $prod"
if (Get-ScheduledTask -TaskName $T -ErrorAction SilentlyContinue) { Stop-ScheduledTask -TaskName $T }
Get-CimInstance Win32_Process | ? { $_.ExecutablePath -like "$D\*" } | % { L "stopping dev pid $($_.ProcessId) $($_.ExecutablePath)"; Stop-Process -Id $_.ProcessId -Force }
Start-Sleep 2
Copy-Item -Force "$D\staging\play-host-$Sha.exe" "$D\bin\play-host.exe"
Copy-Item -Force "$D\staging\suite_play-$Sha.gd" "$D\addons\suite_play\suite_play.gd"
L "exe sha256 $((Get-FileHash "$D\bin\play-host.exe").Hash)"
L "addon sha256 $((Get-FileHash "$D\addons\suite_play\suite_play.gd").Hash)"
$acl = Get-Acl "$D\secrets\runtime-token"; L "token acl: $(($acl.Access | % { $_.IdentityReference }) -join ',')"
$cfg = [ordered]@{
  suite_url = 'ws://192.168.1.200:4034/runtime/ws'; runtime_id = 'play-host-wave-dev-01a0fe45'
  token_file = "$D\secrets\runtime-token"
  godot = 'C:\Users\slaps\AppData\Local\Microsoft\WinGet\Packages\GodotEngine.GodotEngine_Microsoft.Winget.Source_8wekyb3d8bbwe\Godot_v4.7.2-stable_win64_console.exe'
  ffmpeg = 'C:\Users\slaps\AppData\Local\Microsoft\WinGet\Packages\Gyan.FFmpeg_Microsoft.Winget.Source_8wekyb3d8bbwe\ffmpeg-9.0.2-full_build\bin\ffmpeg.exe'
  git = 'C:\Program Files\Git\cmd\git.exe'; ssh = 'C:\Program Files\Git\usr\bin\ssh.exe'
  known_hosts = "$P\secrets\known_hosts"
  mirrors_dir = "$D\mirrors"; checkouts_dir = "$D\checkouts"; addon_dir = "$D\addons\suite_play"; logs_dir = "$D\logs"
  host_ip = '192.168.1.107'; udp_min = 40310; udp_max = 40319; godot_port = 40321
  # rtp 40332/audio 40333: prod's audio RTP is ITS rtp_port+1 = 40331.
  rtp_port = 40332; audio_rtp_port = 40333
  import_timeout_s = 1200; launch_timeout_s = 90; keep_checkouts = 2; log_touch = $true
  repos = @(@{ match = 'github.com/ryanmilvenan/survival-game'; clone_url = 'git@github.com:ryanmilvenan/survival-game.git'; ssh_key = "$P\secrets\survival-game-deploy-ed25519" })
}
if ($NoTouch) { $cfg.features = @('game_stream_host','game_stream_multi') }
$cfg | ConvertTo-Json -Depth 5 | Set-Content -Encoding ascii "$D\config.json"
L "config written (features override: $NoTouch)"
if (-not (Get-ScheduledTask -TaskName $T -ErrorAction SilentlyContinue)) {
  $a = New-ScheduledTaskAction -Execute "$D\bin\play-host.exe" -Argument "serve -config $D\config.json" -WorkingDirectory $D
  $pr = New-ScheduledTaskPrincipal -UserId slaps -LogonType S4U -RunLevel Limited
  $st = New-ScheduledTaskSettingsSet -ExecutionTimeLimit 0 -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
  Register-ScheduledTask -TaskName $T -Action $a -Principal $pr -Settings $st | Out-Null
  L "task registered"
}
if (-not (Get-NetFirewallRule -Name $FW -ErrorAction SilentlyContinue)) {
  New-NetFirewallRule -Name $FW -DisplayName "Suite play host DEV WebRTC UDP 40310-40319 (task 01a0fe45)" -Direction Inbound -Protocol UDP -LocalPort 40310-40319 -Action Allow -Program "$D\bin\play-host.exe" | Out-Null
  L "firewall rule created"
}
Start-ScheduledTask -TaskName $T
Start-Sleep 6
L "dev task: $((Get-ScheduledTask -TaskName $T).State); prod task: $((Get-ScheduledTask -TaskName suite-play-host).State)"
Get-CimInstance Win32_Process -Filter "Name='play-host.exe'" | % { L "play-host.exe pid $($_.ProcessId) $($_.ExecutablePath)" }
$prod2 = (Get-CimInstance Win32_Process -Filter "Name='play-host.exe'" | ? { $_.ExecutablePath -like "$P\bin\*" }).ProcessId
L "prod pid after: $prod2 (unchanged: $($prod -eq $prod2))"
"--- dev play-host.log tail"; Get-Content "$D\logs\play-host.log" -Tail 6
