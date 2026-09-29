# wave-sample-01a0dbd6.ps1 -Seconds N -Label L : READ-ONLY counters every 2 s.
# Adapter bytes (Get-NetAdapterStatistics), nvidia-smi memory/encoder/gpu,
# and the DEV play-host's UDP endpoint count (Get-NetUDPEndpoint). Binds
# nothing, starts nothing. Output: logs\sample-<L>-01a0dbd6.log in the dev dir.
param([int]$Seconds = 120, [string]$Label = "run")
$out = "C:\Users\slaps\play-host-dev-01a0dbd6\logs\sample-$Label-01a0dbd6.log"
$dev = Get-Process play-host -ErrorAction SilentlyContinue | Where-Object { $_.Path -like "*play-host-dev-01a0dbd6*" } | Select-Object -First 1
$end = (Get-Date).AddSeconds($Seconds)
"# dev pid $($dev.Id) label $Label" | Out-File -Encoding utf8 $out
while ((Get-Date) -lt $end) {
  $t = (Get-Date).ToUniversalTime().ToString("HH:mm:ss.fff")
  $a = Get-NetAdapterStatistics | Where-Object { $_.SentBytes -gt 0 } | ForEach-Object { "$($_.Name)=$($_.SentBytes)/$($_.ReceivedBytes)" }
  $g = (& nvidia-smi --query-gpu=memory.used,utilization.encoder,utilization.gpu --format=csv,noheader,nounits) -replace ' ', ''
  $u = @(Get-NetUDPEndpoint -OwningProcess $dev.Id -ErrorAction SilentlyContinue)
  $ports = ($u | ForEach-Object { "$($_.LocalAddress):$($_.LocalPort)" }) -join ','
  "$t gpu=$g udp=$($u.Count) [$ports] net=$($a -join ';')" | Out-File -Append -Encoding utf8 $out
  Start-Sleep -Milliseconds 2000
}
