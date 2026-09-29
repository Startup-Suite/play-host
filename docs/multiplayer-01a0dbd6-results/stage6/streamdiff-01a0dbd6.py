#!/usr/bin/env python3
"""streamdiff-01a0dbd6.py <wave.pcapng> <moon.pcap>... --at HH:MM:SS

Stage 6 (task 01a0dbd6). Takes the client -> server byte stream of the ONE
runtime-socket connection that starts nearest --at on each side (wave's NIC:
port 4033; the core container's netns behind rootlessport: port 4000), and
compares them byte for byte. For the first difference it reports where the
bytes the container received actually came from in what wave sent (a shift:
data from later or earlier in the same stream, or bytes wave never sent).
"""
import sys, datetime
sys.path.insert(0, '/var/home/rocks/tmp-01a0dbd6-s6')
from importlib import import_module
wf = import_module('wsframes-01a0dbd6'.replace('-', '_')) if False else None
import importlib.util
spec = importlib.util.spec_from_file_location('wsf', '/var/home/rocks/tmp-01a0dbd6-s6/wsframes-01a0dbd6.py')
wsf = importlib.util.module_from_spec(spec); spec.loader.exec_module(wsf)
import collections

def streams(files, port):
    conns = collections.defaultdict(wsf.Dir)
    for f in files:
        for ts, lt, pkt in wsf.packets(f):
            r = wsf.ip_payload(lt, pkt)
            if r and r[3] == port:
                s, sp, d, dp, seq, flags, pl = r
                conns[(s, sp, d, dp)].add(ts, seq, flags, pl)
    out = []
    for k, d in conns.items():
        if not d.first_ts:
            continue
        buf, holes, times = d.stream()
        if b'/runtime/ws' in buf.split(b'\r\n', 1)[0]:
            out.append((min(d.first_ts.values()), k, buf, holes, times))
    return out

def main():
    a = sys.argv[1:]
    at = a[a.index('--at') + 1]; del a[a.index('--at'):a.index('--at') + 2]
    wave, moon = [a[0]], a[1:]
    def pick(lst):
        day = datetime.datetime.fromtimestamp(lst[0][0], datetime.timezone.utc).date()
        t = datetime.datetime.combine(day, datetime.time.fromisoformat(at), datetime.timezone.utc).timestamp()
        return min(lst, key=lambda x: abs(x[0] - t))
    W = pick(streams(wave, 4033)); M = pick(streams(moon, 4000))
    w, m = W[2], M[2]
    print(f'wave {W[1]} {len(w)} B holes {W[3][:3]}; container {M[1]} {len(m)} B holes {M[3][:3]}')
    n = min(len(w), len(m))
    k = next((i for i in range(n) if w[i] != m[i]), None)
    if k is None:
        print(f'identical over the common {n} bytes'); return
    print(f'first difference at byte {k} (container side at {wsf.ts_at(M[4], k)}, wave sent it at {wsf.ts_at(W[4], k)})')
    # How far does the difference run, and where did the container's bytes come from?
    j = k
    while j < n and w[j] != m[j]:
        j += 1
    print(f'differs for {j - k} bytes, then agrees again at {j}' if j < n else f'differs to the end of the common part ({n - k} bytes)')
    probe = m[k:k + 64]
    hits = []
    start = 0
    while True:
        p = w.find(probe, start)
        if p < 0 or len(hits) > 5:
            break
        hits.append(p); start = p + 1
    print(f'the container\'s 64 bytes at {k} occur in wave\'s stream at: {[(h, h - k) for h in hits]} (offset, shift)')
    # where the wave's bytes at k went (were they delivered elsewhere?)
    probe2 = w[k:k + 64]
    hits2 = []
    start = 0
    while True:
        p = m.find(probe2, start)
        if p < 0 or len(hits2) > 5:
            break
        hits2.append(p); start = p + 1
    print(f'wave\'s 64 bytes at {k} occur in the container\'s stream at: {[(h, h - k) for h in hits2]}')
    # Characterise the block [k, j): a rotation, a shift, or foreign bytes?
    blk_w, blk_m = w[k:j], m[k:j]
    rot = None
    for r in range(1, 17):
        if blk_m == blk_w[-r:] + blk_w[:-r]:
            rot = f'right-rotation by {r} byte(s): the block\'s last {r} byte(s) arrived FIRST'
            break
        if blk_m == blk_w[r:] + blk_w[:r]:
            rot = f'left-rotation by {r} byte(s): the block\'s first {r} byte(s) arrived LAST'
            break
    same_multiset = sorted(blk_w) == sorted(blk_m)
    print(f'block [{k}, {j}) of {j - k} bytes: {rot or "not a small rotation"}; same bytes, reordered: {same_multiset}')
    print(f'wave  [{k - 16}:{k + 48}] {w[k - 16:k + 48].hex()}')
    print(f'cntnr [{k - 16}:{k + 48}] {m[k - 16:k + 48].hex()}')
    print(f'wave  [{j - 48}:{j + 16}] {w[j - 48:j + 16].hex()}')
    print(f'cntnr [{j - 48}:{j + 16}] {m[j - 48:j + 16].hex()}')
    # TCP segment boundaries near k and j on the container side
    offs = [o for o, _ in M[4]]
    import bisect
    b1 = offs[max(0, bisect.bisect_right(offs, k) - 2):bisect.bisect_right(offs, k) + 2]
    b2 = offs[max(0, bisect.bisect_right(offs, j) - 2):bisect.bisect_right(offs, j) + 2]
    print(f'container segment starts around k: {b1}; around j: {b2}')
    offs_w = [o for o, _ in W[4]]
    c1 = offs_w[max(0, bisect.bisect_right(offs_w, k) - 2):bisect.bisect_right(offs_w, k) + 2]
    c2 = offs_w[max(0, bisect.bisect_right(offs_w, j) - 2):bisect.bisect_right(offs_w, j) + 2]
    print(f'wave segment starts around k: {c1}; around j: {c2}')

if __name__ == '__main__':
    main()
