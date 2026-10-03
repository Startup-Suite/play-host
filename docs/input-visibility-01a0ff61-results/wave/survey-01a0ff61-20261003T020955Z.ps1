# READ-ONLY survey of wave prod play-host for task 01a0ff61.
$P = 'C:\Users\slaps\play-host'
"exe sha256 $((Get-FileHash "$P\bin\play-host.exe").Hash)"
"addon sha256 $((Get-FileHash "$P\addons\suite_play\suite_play.gd").Hash)"
Get-CimInstance Win32_Process -Filter "Name='play-host.exe'" | % { "play-host.exe pid $($_.ProcessId) $($_.ExecutablePath)" }
"task: $((Get-ScheduledTask -TaskName suite-play-host).State)"
$a = (Get-ScheduledTask -TaskName suite-play-host).Actions[0]; "task action: $($a.Execute) $($a.Arguments)"
$c = Get-Content "$P\config.json" -Raw | ConvertFrom-Json
"config keys: $(($c.PSObject.Properties.Name) -join ',')"
"suite_url: $($c.suite_url)"; "runtime_id: $($c.runtime_id)"
"--- log tail"; Get-Content "$P\logs\play-host.log" -Tail 8
ls "$P\bin" | % { "$($_.Name) $($_.Length) $($_.LastWriteTime)" }
ls C:\Users\slaps\play-host-dev* -Directory -ErrorAction SilentlyContinue | % { "devdir $($_.FullName)" }
