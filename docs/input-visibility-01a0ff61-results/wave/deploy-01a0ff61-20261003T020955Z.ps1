# deploy-01a0ff61.ps1 -Sha <sha> -ExeHash <sha256> -Stamp <stamp>: swap the PROD
# play-host.exe (C:\Users\slaps\play-host\bin) for task 01a0ff61 stage 2.
# Adapted from deploy-01a0fe45.ps1 (which deployed the DEV instance). Ships the
# exe ONLY: the addon is not touched and its sha256 is recorded before and after.
# Never writes config.json. Acts only on play-host.exe processes whose
# ExecutablePath is exactly the prod bin exe. Aborts before touching prod if
# prodcheck reads busy. Exit 0 = swapped and confirmed; 3 = confirm failed (run
# rollback-01a0ff61.ps1 -Stamp <stamp>).
param([Parameter(Mandatory)][string]$Sha, [Parameter(Mandatory)][string]$ExeHash, [Parameter(Mandatory)][string]$Stamp)
$ErrorActionPreference = 'Stop'
$P = 'C:\Users\slaps\play-host'; $X = "$P\bin\play-host.exe"; $T = 'suite-play-host'
$Sc = 'C:\Users\slaps\play-host-01a0ff61-scratch'; $Staged = "$Sc\play-host-$Sha.exe"
$Bak = "$X.bak-01a0ff61-$Stamp"; $Addon = "$P\addons\suite_play\suite_play.gd"
function L($m) { "$(Get-Date -Format o) $m" }
function ProdPids { @(Get-CimInstance Win32_Process -Filter "Name='play-host.exe'" | ? { $_.ExecutablePath -eq $X }) }
L "deploy prod play-host 01a0ff61 sha $Sha stamp $Stamp"
# 1. idle check (read-only)
$pc = & "$Sc\prodcheck-01a0fe45.ps1"; L "pre: $pc"
if ($pc -notmatch '^PROD_BUSY=False ') { L "ABORT: prod busy, nothing touched"; exit 2 }
$stagedHash = (Get-FileHash $Staged).Hash; L "staged exe sha256 $stagedHash"
if ($stagedHash -ne $ExeHash.ToUpper()) { L "ABORT: staged hash != built hash, nothing touched"; exit 2 }
$addon0 = (Get-FileHash $Addon).Hash; L "addon sha256 before $addon0"
$old = (Get-FileHash $X).Hash; L "prod exe sha256 before $old"
ProdPids | % { L "prod pid before $($_.ProcessId) $($_.ExecutablePath)" }
# 2. backup
Copy-Item $X $Bak
$bh = (Get-FileHash $Bak).Hash; L "backup $Bak sha256 $bh"
if ($bh -ne $old) { L "ABORT: backup hash mismatch; prod exe untouched"; exit 2 }
# 3. swap
$t0 = Get-Date
Stop-ScheduledTask -TaskName $T
for ($i = 0; $i -lt 20 -and @(ProdPids).Count -gt 0; $i++) { Start-Sleep 1 }
ProdPids | % { L "still running after Stop-ScheduledTask: pid $($_.ProcessId); Stop-Process"; Stop-Process -Id $_.ProcessId -Force }
Start-Sleep 2
if (@(ProdPids).Count -gt 0) { L "ABORT: prod play-host.exe still running; restarting task on the old exe"; Start-ScheduledTask -TaskName $T; exit 3 }
L "prod play-host.exe processes remaining: 0"
Copy-Item -Force $Staged $X
$new = (Get-FileHash $X).Hash; L "prod exe sha256 after copy $new"
Start-ScheduledTask -TaskName $T
# 4. confirm
$ok = $true
$ready = $null; $ver = $null
for ($i = 0; $i -lt 45 -and -not $ready; $i++) {
  Start-Sleep 1
  $lines = Get-Content "$P\logs\play-host.log" -Tail 60
  $ver = $lines | ? { $_ -like "*play-host $Sha serve*" } | Select-Object -Last 1
  if ($ver) { $ready = $lines | ? { $_ -like "*ready for play_session_start*" } | Select-Object -Last 1 }
}
L "task state: $((Get-ScheduledTask -TaskName $T).State)"
$pp = @(ProdPids)
$pp | % { L "prod pid after $($_.ProcessId) $($_.ExecutablePath) image sha256 $((Get-FileHash $_.ExecutablePath).Hash)" }
if ($pp.Count -ne 1 -or (Get-FileHash $pp[0].ExecutablePath).Hash -ne $ExeHash.ToUpper()) { L "CONFIRM FAIL: running image hash"; $ok = $false }
if ($ver) { L "log: $ver" } else { L "CONFIRM FAIL: no 'play-host $Sha serve' line"; $ok = $false }
if ($ready) { L "log: $ready" } else { L "CONFIRM FAIL: no 'ready for play_session_start' line"; $ok = $false }
$pc2 = & "$Sc\prodcheck-01a0fe45.ps1"; L "post: $pc2"
if ($pc2 -notmatch '^PROD_BUSY=False ') { $ok = $false }
$addon1 = (Get-FileHash $Addon).Hash; L "addon sha256 after $addon1 (unchanged: $($addon0 -eq $addon1))"
if ($addon0 -ne $addon1) { $ok = $false }
"--- prod play-host.log tail"; Get-Content "$P\logs\play-host.log" -Tail 6
if ($ok) { L "DEPLOY_OK"; exit 0 } else { L "DEPLOY_CONFIRM_FAILED: run rollback-01a0ff61.ps1 -Stamp $Stamp"; exit 3 }
