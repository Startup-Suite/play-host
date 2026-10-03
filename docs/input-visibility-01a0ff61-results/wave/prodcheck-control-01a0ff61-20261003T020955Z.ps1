# Positive control for prodcheck-01a0fe45.ps1 (task 01a0ff61): a scratch dir whose
# log's last session line is an `offer` (a live session). Never touches prod.
$D = 'C:\Users\slaps\play-host-01a0ff61-scratch\ctl'
New-Item -ItemType Directory -Force "$D\logs","$D\checkouts" | Out-Null
@('2026/10/03 00:00:00.000000 ready for play_session_start',
  '2026/10/03 00:00:01.000000 [ctl-01a0ff61] offer from peer p1',
  '2026/10/03 00:00:02.000000 [ctl-01a0ff61] peer p1: data channel input-events open, slots [src0:0]') | Set-Content "$D\logs\play-host.log"
& "$PSScriptRoot\prodcheck-01a0fe45.ps1" -Dir $D
Add-Content "$D\logs\play-host.log" '2026/10/03 00:00:03.000000 [ctl-01a0ff61] session ended: control'
& "$PSScriptRoot\prodcheck-01a0fe45.ps1" -Dir $D
