#!/usr/bin/env python3
"""framemap-01a0dbd6.py <wave.pcapng> <moon.pcap> --at HH:MM:SS
For every frame wave sent near the first difference, where its first 24
bytes (header + mask + masked JSON head: unique) sit in the container's
stream. A shift that changes shows what was inserted, lost or reordered."""
import sys, importlib.util, struct
spec = importlib.util.spec_from_file_location('sd', '/var/home/rocks/tmp-01a0dbd6-s6/streamdiff-01a0dbd6.py')
sd = importlib.util.module_from_spec(spec); spec.loader.exec_module(sd)
wsf = sd.wsf
import datetime
a = sys.argv[1:]
at = a[a.index('--at') + 1]; del a[a.index('--at'):a.index('--at') + 2]
def pick(lst):
    day = datetime.datetime.fromtimestamp(lst[0][0], datetime.timezone.utc).date()
    t = datetime.datetime.combine(day, datetime.time.fromisoformat(at), datetime.timezone.utc).timestamp()
    return min(lst, key=lambda x: abs(x[0] - t))
W = pick(sd.streams([a[0]], 4033)); M = pick(sd.streams(a[1:], 4000))
w, m = W[2], M[2]
k = next(i for i in range(min(len(w), len(m))) if w[i] != m[i])
# walk wave's frames
pos = w.find(b'\r\n\r\n') + 4
starts = []
while pos + 2 <= len(w):
    n = w[pos + 1] & 127; h = 2
    if n == 126: n = struct.unpack('>H', w[pos + 2:pos + 4])[0]; h = 4
    elif n == 127: n = struct.unpack('>Q', w[pos + 2:pos + 10])[0]; h = 10
    h += 4
    starts.append((pos, h + n))
    pos += h + n
near = [s for s in starts if k - 40000 <= s[0] <= k + 60000]
for off, ln in near:
    probe = w[off:off + 24]
    found = []
    p = m.find(probe, max(0, off - 200000))
    while p >= 0 and len(found) < 3 and p < off + 200000:
        found.append(p); p = m.find(probe, p + 1)
    print(f'wave frame @{off} len {ln} ({wsf.ts_at(W[4], off)}) -> container {[(f, f - off) for f in found]}')
print('first difference', k)
