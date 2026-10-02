#!/usr/bin/env python3
"""hostlines-01a0fe45.py <driver.json> <hostlog.txt> [offset_ms]: assigns the dev
host's `touch line` / drop lines to the driver's steps by UTC time and prints a
per-step table. offset_ms is wave minus hive (measured, ~180)."""
import json, sys, re, datetime as dt
run = json.load(open(sys.argv[1])); lines = open(sys.argv[2], encoding='utf-8', errors='replace').read().splitlines()
off = float(sys.argv[3]) / 1000 if len(sys.argv) > 3 else 0.18
pat = re.compile(r'^(\d{4}/\d\d/\d\d \d\d:\d\d:\d\d\.\d+) \[[0-9a-f-]+\] (touch line (\{.*\})|peer (\S+): dropped .*)$')
ev = []
for l in lines:
    m = pat.match(l.strip())
    if not m: continue
    t = dt.datetime.strptime(m.group(1), '%Y/%m/%d %H:%M:%S.%f').replace(tzinfo=dt.timezone.utc).timestamp() - off
    ev.append((t, json.loads(m.group(3)) if m.group(3) else {'drop': m.group(2)}))
iso = lambda s: dt.datetime.fromisoformat(s.replace('Z', '+00:00')).timestamp()
steps = run['steps']; out = []; used = set()
for k, s in enumerate(steps):
    a = iso(s['w0']) - 0.3
    b = iso(steps[k + 1]['w0']) - 0.3 if k + 1 < len(steps) else iso(s['w1']) + 12
    mine = [(i, e) for i, (t, e) in enumerate(ev) if a <= t < b and i not in used]
    for i, _ in mine: used.add(i)
    es = [e for _, e in mine]
    c = {'st_down': sum(1 for e in es if e.get('t') == 'st' and e.get('p')),
         'st_up': sum(1 for e in es if e.get('t') == 'st' and not e.get('p') and not e.get('c')),
         'st_cancel': sum(1 for e in es if e.get('t') == 'st' and e.get('c')),
         'sd': sum(1 for e in es if e.get('t') == 'sd'),
         'indices': sorted({e['i'] for e in es if 'i' in e}),
         'drops': [e['drop'] for e in es if 'drop' in e]}
    down = next((e for e in es if e.get('t') == 'st' and e.get('p')), None)
    if down and 'target' in s:
        c['down_xy'] = [round(down['x'], 4), round(down['y'], 4)]
        c['err_pct'] = [round(abs(down['x'] - s['target']['x']) * 100, 2), round(abs(down['y'] - s['target']['y']) * 100, 2)]
    out.append({'step': s['name'], **c})
print(json.dumps({'run': run['label'], 'unassigned': len(ev) - len(used), 'total_lines': len(ev), 'steps': out}, indent=1))
