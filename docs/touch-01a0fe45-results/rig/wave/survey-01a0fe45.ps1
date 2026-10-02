Get-CimInstance Win32_Process | ? { $_.Name -match 'play-host|Godot|ffmpeg' } | Select ProcessId,Name,ExecutablePath,CreationDate | Format-Table -Auto | Out-String -Width 250
Get-ScheduledTask | ? TaskName -like '*play*' | Select TaskName,State | Format-Table -Auto
Get-NetFirewallRule | ? { $_.DisplayName -like '*play*host*' -or $_.Name -like '*play-host*' } | Select Name,DisplayName,Enabled | Format-Table -Auto | Out-String -Width 200
Get-NetUDPEndpoint | ? { $_.LocalPort -ge 40300 -and $_.LocalPort -le 40340 } | Select LocalAddress,LocalPort,OwningProcess | Format-Table -Auto
Get-NetTCPConnection -ErrorAction SilentlyContinue | ? { $_.LocalPort -ge 40300 -and $_.LocalPort -le 40340 } | Select LocalAddress,LocalPort,State,OwningProcess | Format-Table -Auto
Get-ScheduledTask -TaskName suite-play-host | Select -Expand Actions | Format-List Execute,Arguments,WorkingDirectory
(Get-ScheduledTask -TaskName suite-play-host).Principal | Format-List
$j = Get-Content C:\Users\slaps\play-host\config.json -Raw | ConvertFrom-Json
foreach ($k in $j.PSObject.Properties) { if ($k.Name -match 'token' -and $k.Name -ne 'token_file') { "$($k.Name) = <redacted>" } elseif ($k.Name -eq 'repos') { "repos = " + ($k.Value | ConvertTo-Json -Compress) } else { "$($k.Name) = $($k.Value)" } }
"--- config-dev-01a0f84e"
$j = Get-Content C:\Users\slaps\play-host\config-dev-01a0f84e.json -Raw | ConvertFrom-Json
foreach ($k in $j.PSObject.Properties) { if ($k.Name -match 'token' -and $k.Name -ne 'token_file') { "$($k.Name) = <redacted>" } elseif ($k.Name -eq 'repos') { "repos = " + ($k.Value | ConvertTo-Json -Compress) } else { "$($k.Name) = $($k.Value)" } }
"--- prod log tail"
Get-Content C:\Users\slaps\play-host\logs\play-host.log -Tail 12
