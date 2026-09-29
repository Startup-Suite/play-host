# Redeploy the DEV play-host (task 01a0dbd6 stage 6). Never touches prod:
# prod's pid is logged before and after and must not change.
param([string]$Exe)
$D = "C:\Users\slaps\play-host-dev-01a0dbd6"
$log = "$D\logs\deploy-s6-01a0dbd6.log"
function L($m) { $l = "$(Get-Date -Format o) $m"; Add-Content $log $l; $l }
L "redeploy dev play-host 01a0dbd6 stage 6 from $Exe"
$prod = Get-CimInstance Win32_Process -Filter "Name='play-host.exe'" | Where-Object { $_.ExecutablePath -like "C:\Users\slaps\play-host\bin\*" }
L "prod pid before: $($prod.ProcessId)"
$dev = Get-CimInstance Win32_Process -Filter "Name='play-host.exe'" | Where-Object { $_.ExecutablePath -like "$D\bin\*" }
foreach ($p in $dev) { L "stopping dev pid $($p.ProcessId)"; Stop-Process -Id $p.ProcessId -Force }
Start-Sleep -Seconds 2
Copy-Item -Force $Exe "$D\bin\play-host.exe"
L "dev exe sha256 $((Get-FileHash "$D\bin\play-host.exe" -Algorithm SHA256).Hash)"
Start-ScheduledTask -TaskName "suite-play-host-dev-01a0dbd6"
Start-Sleep -Seconds 5
$prod2 = Get-CimInstance Win32_Process -Filter "Name='play-host.exe'" | Where-Object { $_.ExecutablePath -like "C:\Users\slaps\play-host\bin\*" }
$dev2 = Get-CimInstance Win32_Process -Filter "Name='play-host.exe'" | Where-Object { $_.ExecutablePath -like "$D\bin\*" }
L "prod pid after: $($prod2.ProcessId); dev pid: $($dev2.ProcessId)"
L "dev task: $((Get-ScheduledTask -TaskName suite-play-host-dev-01a0dbd6).State); prod task: $((Get-ScheduledTask -TaskName suite-play-host).State)"
Get-Content "$D\logs\play-host.log" -Tail 3
