# rollback-01a0ff61.ps1 -Stamp <stamp>: restore the prod play-host.exe saved by
# deploy-01a0ff61.ps1 as play-host.exe.bak-01a0ff61-<stamp>. Exe only.
param([Parameter(Mandatory)][string]$Stamp)
$ErrorActionPreference = 'Stop'
$P = 'C:\Users\slaps\play-host'; $X = "$P\bin\play-host.exe"; $T = 'suite-play-host'; $Bak = "$X.bak-01a0ff61-$Stamp"
function L($m) { "$(Get-Date -Format o) $m" }
function ProdPids { @(Get-CimInstance Win32_Process -Filter "Name='play-host.exe'" | ? { $_.ExecutablePath -eq $X }) }
$want = (Get-FileHash $Bak).Hash; L "rollback to $Bak sha256 $want"
Stop-ScheduledTask -TaskName $T
for ($i = 0; $i -lt 20 -and @(ProdPids).Count -gt 0; $i++) { Start-Sleep 1 }
ProdPids | % { Stop-Process -Id $_.ProcessId -Force }; Start-Sleep 2
Copy-Item -Force $Bak $X
Start-ScheduledTask -TaskName $T; Start-Sleep 10
ProdPids | % { L "prod pid $($_.ProcessId) image sha256 $((Get-FileHash $_.ExecutablePath).Hash) (want $want)" }
Get-Content "$P\logs\play-host.log" -Tail 4
