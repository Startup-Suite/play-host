#!/usr/bin/env python3
"""wsframes-01a0dbd6.py <pcap|pcapng>... [--port 4000|4033] [--path /runtime/ws]

Stage 6 (task 01a0dbd6). Reassembles every TCP connection in the captures,
keeps the ones whose client request line asks for --path (the play host's
runtime socket), and parses the CLIENT -> SERVER byte stream as RFC 6455
frames with Bandit's reassembly rules: a text/binary frame while a
fragmented message is pending is an error (Bandit: "Received unexpected
binary frame (RFC6455 5.4)"), and so is a continuation with none pending.
Reports per connection: bytes, frames, messages, fragmented messages, TCP
sequence holes, and the first anomaly with the bytes around it.

Pure stdlib: pcap (LINUX_SLL2, EN10MB, RAW) and pcapng (EPB, any of those).
"""
import struct, sys, collections, datetime

def packets(path):
    with open(path, 'rb') as f:
        data = f.read()
    magic = data[:4]
    if magic in (b'\xd4\xc3\xb2\xa1', b'\x4d\x3c\xb2\xa1', b'\xa1\xb2\xc3\xd4', b'\xa1\xb2\x3c\x4d'):
        le = magic in (b'\xd4\xc3\xb2\xa1', b'\x4d\x3c\xb2\xa1')
        nano = magic in (b'\x4d\x3c\xb2\xa1', b'\xa1\xb2\x3c\x4d')
        e = '<' if le else '>'
        linktype = struct.unpack(e + 'I', data[20:24])[0]
        off = 24
        while off + 16 <= len(data):
            ts, tf, incl, orig = struct.unpack(e + 'IIII', data[off:off + 16])
            off += 16
            yield ts + tf / (1e9 if nano else 1e6), linktype, data[off:off + incl]
            off += incl
    elif magic == b'\x0a\x0d\x0d\x0a':
        off = 0
        links = []
        e = '<'
        tsres = []
        while off + 12 <= len(data):
            btype = struct.unpack(e + 'I', data[off:off + 4])[0]
            if btype == 0x0A0D0D0A:
                bom = data[off + 8:off + 12]
                e = '<' if bom == b'\x4d\x3c\x2b\x1a' else '>'
                links, tsres = [], []
            blen = struct.unpack(e + 'I', data[off + 4:off + 8])[0]
            body = data[off + 8:off + blen - 4]
            if btype == 1:  # IDB
                links.append(struct.unpack(e + 'H', body[0:2])[0])
                res = 6
                o = 8
                while o + 4 <= len(body):
                    code, ln = struct.unpack(e + 'HH', body[o:o + 4])
                    if code == 0:
                        break
                    if code == 9:
                        v = body[o + 4]
                        res = (v & 0x7f) if not v & 0x80 else None
                    o += 4 + ((ln + 3) & ~3)
                tsres.append(res if res is not None else 6)
            elif btype == 6:  # EPB
                iface, th, tl, cap, orig = struct.unpack(e + 'IIIII', body[0:20])
                ts = ((th << 32) | tl) / (10 ** tsres[iface])
                yield ts, links[iface], body[20:20 + cap]
            if blen == 0:
                break
            off += blen
    else:
        raise SystemExit(f'{path}: not a pcap/pcapng file')

def ip_payload(linktype, pkt):
    if linktype == 276:      # LINUX_SLL2
        proto = struct.unpack('>H', pkt[0:2])[0]; p = pkt[20:]
    elif linktype == 113:    # LINUX_SLL
        proto = struct.unpack('>H', pkt[14:16])[0]; p = pkt[16:]
    elif linktype == 1:      # Ethernet
        proto = struct.unpack('>H', pkt[12:14])[0]; p = pkt[14:]
        if proto == 0x8100:
            proto = struct.unpack('>H', p[2:4])[0]; p = p[4:]
    elif linktype in (101, 228):
        proto, p = 0x0800, pkt
    else:
        return None
    if proto != 0x0800 or len(p) < 20 or p[0] >> 4 != 4:
        return None
    ihl = (p[0] & 15) * 4
    tot = struct.unpack('>H', p[2:4])[0]
    if p[9] != 6:
        return None
    src, dst = p[12:16], p[16:20]
    t = p[ihl:tot if tot else None]
    sport, dport, seq = struct.unpack('>HHI', t[0:8])
    doff = (t[12] >> 4) * 4
    flags = t[13]
    return ('.'.join(map(str, src)), sport, '.'.join(map(str, dst)), dport, seq, flags, t[doff:])

class Dir:
    def __init__(self):
        self.isn = None; self.segs = {}; self.first_ts = {}
    def add(self, ts, seq, flags, payload):
        if flags & 0x02:  # SYN
            self.isn = (seq + 1) & 0xffffffff; return
        if not payload:
            return
        if self.isn is None:
            self.isn = seq
        rel = (seq - self.isn) & 0xffffffff
        if rel > 0x7fffffff:
            return
        if rel not in self.segs or len(payload) > len(self.segs[rel]):
            self.segs[rel] = payload
            self.first_ts.setdefault(rel, ts)
    def stream(self):
        out = bytearray(); holes = []; times = []
        pos = 0
        for rel in sorted(self.segs):
            p = self.segs[rel]
            if rel > pos:
                holes.append((pos, rel)); out += b'\0' * (rel - pos); pos = rel
            if rel + len(p) <= pos:
                continue
            cut = pos - rel
            times.append((pos, self.first_ts[rel]))
            out += p[cut:]; pos = rel + len(p)
        return bytes(out), holes, times

import bisect
def ts_at(times, off):
    if not hasattr(ts_at, 'cache') or ts_at.cache[0] is not times:
        ts_at.cache = (times, [o for o, _ in times])
    k = bisect.bisect_right(ts_at.cache[1], off) - 1
    best = times[k][1] if k >= 0 else None
    return datetime.datetime.fromtimestamp(best, datetime.timezone.utc).strftime('%H:%M:%S.%f')[:-3] if best else '?'

CHECK = True

def _sample(b):
    # where the text stops being the expected alphabet: first byte outside printable ASCII
    for k, c in enumerate(b):
        if c < 0x20 or c > 0x7e:
            return dict(first_bad_offset=k, around=b[max(0, k - 24):k + 40].hex(), head=b[:60].decode('latin1'))
    return dict(first_bad_offset=None, head=b[:60].decode('latin1'))

def parse_ws(buf, times, holes):
    i = buf.find(b'\r\n\r\n')
    if i < 0:
        return {'error': 'no http request'}
    pos = i + 4
    frames = msgs = frag_msgs = cont = 0
    pending = False; pending_start = None
    msgbuf = bytearray(); msg_start = None; bad_msgs = []
    first_bad = None; last_frames = collections.deque(maxlen=6)
    while pos + 2 <= len(buf):
        b0, b1 = buf[pos], buf[pos + 1]
        fin, rsv, op = b0 >> 7, (b0 >> 4) & 7, b0 & 15
        masked, n = b1 >> 7, b1 & 127
        h = 2
        if n == 126:
            if pos + 4 > len(buf): break
            n = struct.unpack('>H', buf[pos + 2:pos + 4])[0]; h = 4
        elif n == 127:
            if pos + 10 > len(buf): break
            n = struct.unpack('>Q', buf[pos + 2:pos + 10])[0]; h = 10
        if masked:
            h += 4
        in_hole = [x for x in holes if x[0] <= pos + h + n and x[1] >= pos]
        desc = dict(off=pos, at=ts_at(times, pos), fin=fin, rsv=rsv, op=op, masked=masked, len=n, hole=bool(in_hole))
        bad = None
        if rsv or not masked:
            bad = 'rsv bits set or unmasked client frame'
        elif op not in (0, 1, 2, 8, 9, 10):
            bad = f'unknown opcode {op}'
        elif op in (1, 2) and pending:
            bad = f'opcode {op} while a fragmented message (from {pending_start}) is pending'
        elif op == 0 and not pending:
            bad = 'continuation with no fragmented message pending'
        elif n > 16 << 20:
            bad = f'implausible length {n}'
        if bad:
            first_bad = dict(reason=bad, frame=desc, before=list(last_frames),
                             bytes_hex=buf[max(0, pos - 32):pos + 32].hex())
            break
        if pos + h + n > len(buf):
            desc['truncated_at_end'] = True
            last_frames.append(desc)
            break
        last_frames.append(desc)
        mkey = buf[pos + h - 4:pos + h] if masked else b'\0\0\0\0'
        if CHECK:
            raw = buf[pos + h:pos + h + n]
            key = (mkey * (n // 4 + 1))[:n]
            payload = (int.from_bytes(raw, 'big') ^ int.from_bytes(key, 'big')).to_bytes(n, 'big') if n else b''
        else:
            payload = b''
        if op in (1, 2):
            msgbuf = bytearray(payload); msg_start = desc
        elif op == 0:
            msgbuf += payload
        if CHECK and op in (0, 1) and fin:
            try:
                txt = bytes(msgbuf).decode('utf-8')
                import json as _j
                _j.loads(txt)
            except Exception as ex:
                bad_msgs.append(dict(start=msg_start, end=desc, err=str(ex)[:120], size=len(msgbuf),
                                     sample=_sample(bytes(msgbuf))))
        frames += 1
        if op < 8:
            if op in (1, 2):
                if not fin:
                    pending = True; pending_start = desc['at']; frag_msgs += 1
                else:
                    msgs += 1
            else:
                cont += 1
                if fin:
                    pending = False; msgs += 1
        pos += h + n
    return dict(bad_messages=bad_msgs, frames=frames, messages=msgs, fragmented=frag_msgs, continuations=cont,
                parsed_to=pos, stream_bytes=len(buf), pending_at_end=pending,
                last_frames=list(last_frames), first_anomaly=first_bad)

def main():
    args = sys.argv[1:]
    port = 4000; path = b'/runtime/ws'
    if '--port' in args:
        k = args.index('--port'); port = int(args[k + 1]); del args[k:k + 2]
    if '--path' in args:
        k = args.index('--path'); path = args[k + 1].encode(); del args[k:k + 2]
    conns = collections.defaultdict(Dir)
    for f in args:
        for ts, lt, pkt in packets(f):
            r = ip_payload(lt, pkt)
            if not r:
                continue
            s, sp, d, dp, seq, flags, pl = r
            if dp == port:
                conns[(s, sp, d, dp)].add(ts, seq, flags, pl)
    for key, d in sorted(conns.items(), key=lambda kv: min(kv[1].first_ts.values() or [0])):
        buf, holes, times = d.stream()
        line = buf.split(b'\r\n', 1)[0]
        if path not in line:
            continue
        r = parse_ws(buf, times, holes)
        start = ts_at(times, 0)
        print(f'== {key[0]}:{key[1]} -> {key[2]}:{key[3]} first byte {start}  request {line[:60]!r}...')
        print(f'   stream {r.get("stream_bytes")} B, TCP holes {holes[:5]}{"..." if len(holes) > 5 else ""}')
        print(f'   frames {r.get("frames")} messages {r.get("messages")} fragmented {r.get("fragmented")} continuations {r.get("continuations")} pending_at_end {r.get("pending_at_end")}')
        bm = r.get('bad_messages') or []
        print(f'   invalid text messages (not UTF-8 JSON after unmasking): {len(bm)}')
        for b in bm[:4]:
            print(f'     at {b["start"]["at"]} off {b["start"]["off"]} size {b["size"]} frame len {b["start"]["len"]}: {b["err"]}')
            print(f'       {b["sample"]}')
        if r.get('first_anomaly'):
            a = r['first_anomaly']
            print(f'   FIRST ANOMALY: {a["reason"]}')
            print(f'     frame {a["frame"]}')
            for b in a['before']:
                print(f'     before: {b}')
            print(f'     bytes around it: {a["bytes_hex"]}')
        else:
            print(f'   no anomaly; last frames: {r.get("last_frames")[-2:]}')

if __name__ == '__main__':
    main()
