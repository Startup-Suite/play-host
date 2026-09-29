#!/usr/bin/env python3
"""Builds the stage 5 tables (01a0dbd6) from the run artefacts in this dir.

Per run <label>: probes-<label>-01a0dbd6.json (driver), row-<label>-01a0dbd6.json
(game_stream_sessions row), sample-<label>-01a0dbd6.log (wave read-only
counters), moon-cpu-<label>-01a0dbd6.log, hostlog-<label>-01a0dbd6.log.
Prints markdown tables and writes summary-01a0dbd6.json.
"""
import json, os, re, sys, statistics
from datetime import datetime, timezone

D = os.path.dirname(os.path.abspath(__file__))
labels = sys.argv[1:]


def pct(a, p):
    if not a:
        return None
    s = sorted(a)
    import math
    return s[max(0, math.ceil(p * len(s)) - 1)]


import random


def boot_ci(a, p, reps=2000, seed=1):
    """90 % bootstrap interval of the nearest-rank percentile p."""
    if len(a) < 5:
        return None
    rng = random.Random(seed)
    vals = sorted(pct([rng.choice(a) for _ in a], p) for _ in range(reps))
    return [round(vals[int(0.05 * reps)], 1), round(vals[int(0.95 * reps) - 1], 1)]


def hms(s):
    return datetime.strptime(s, "%H:%M:%S.%f" if "." in s else "%H:%M:%S")


def iso_hms(iso):
    return datetime.fromisoformat(iso.replace("Z", "+00:00")).strftime("%H:%M:%S.%f")


def wave_samples(label):
    p = f"{D}/sample-{label}-01a0dbd6.log"
    rows = []
    if not os.path.exists(p):
        return rows
    for line in open(p, encoding="utf-8-sig"):
        m = re.match(r"(\S+) gpu=(\d+),(\d+),(\d+) udp=(\d+) \[([^\]]*)\] net=(.*)", line.strip())
        if not m:
            continue
        net = {}
        for part in m.group(7).split(";"):
            k, v = part.rsplit("=", 1)
            tx, rx = v.split("/")
            net[k] = (int(tx), int(rx))
        rows.append({"t": hms(m.group(1)), "mem": int(m.group(2)), "enc": int(m.group(3)), "gpu": int(m.group(4)),
                     "udp": int(m.group(5)), "ports": m.group(6), "tx": net.get("Ethernet 2", (0, 0))[0]})
    return rows


def moon_cpu(label):
    p = f"{D}/moon-cpu-{label}-01a0dbd6.log"
    rows = []
    if not os.path.exists(p):
        return rows
    for line in open(p):
        m = re.match(r"(\S+) host_busy_permille=(\d+) chrome_millicores=(-?\d+)", line.strip())
        if m:
            rows.append({"t": hms(m.group(1)), "busy": int(m.group(2)) / 10.0, "chrome": int(m.group(3)) / 1000.0})
        else:
            m = re.match(r"(\S+) host_busy_permille=(\d+)", line.strip())
            if m:
                rows.append({"t": hms(m.group(1)), "busy": int(m.group(2)) / 10.0, "chrome": None})
    return rows


def window(rows, a, b):
    return [r for r in rows if a <= r["t"] <= b]


out = {}
for label in labels:
    pr = json.load(open(f"{D}/probes-{label}-01a0dbd6.json"))
    rowp = f"{D}/row-{label}-01a0dbd6.json"
    row = json.load(open(rowp)) if os.path.exists(rowp) and os.path.getsize(rowp) > 2 else None
    a = hms(iso_hms(pr["phases"]["all_open_at"]))
    b = hms(iso_hms(pr["phases"]["probe_end_at"]))
    ws = wave_samples(label)
    wwin = window(ws, a, b)
    cpu = window(moon_cpu(label), a, b)
    peers = pr["players"] + pr["spectators"]
    up = None
    if len(wwin) >= 2:
        dt = (wwin[-1]["t"] - wwin[0]["t"]).total_seconds()
        up = (wwin[-1]["tx"] - wwin[0]["tx"]) * 8 / dt / 1e6
    pre = [r for r in ws if r["t"] < a]
    rec = {
        "label": label, "players": pr["players"], "spectators": pr["spectators"], "timed_out": pr.get("timed_out", False),
        "probe_window": [a.strftime("%H:%M:%S"), b.strftime("%H:%M:%S")],
        "vram_mib": [min(r["mem"] for r in wwin), max(r["mem"] for r in wwin)] if wwin else None,
        "enc_util": [min(r["enc"] for r in wwin), max(r["enc"] for r in wwin)] if wwin else None,
        "gpu_util": [min(r["gpu"] for r in wwin), max(r["gpu"] for r in wwin)] if wwin else None,
        "udp_endpoints": [min(r["udp"] for r in wwin), max(r["udp"] for r in wwin)] if wwin else None,
        "udp_ports_last": wwin[-1]["ports"] if wwin else None,
        "wave_tx_mbps": round(up, 2) if up is not None else None,
        "wave_tx_mbps_per_peer": round(up / peers, 2) if up is not None else None,
        "moon_busy_pct_median": statistics.median([c["busy"] for c in cpu]) if cpu else None,
        "chrome_cores_median": statistics.median([c["chrome"] for c in cpu if c["chrome"] is not None]) if any(c["chrome"] is not None for c in cpu) else None,
        "row": row, "viewers": [],
    }
    for v in pr["viewers"]:
        ms = [s["ms"] for s in v["samples"]]
        slot_row = (row or {}).get("slot_stats", {}).get(str(v["slot"])) if v["role"] == "player" else None
        win = v.get("window") or {}
        d50 = d95 = None
        if slot_row and win.get("p50") is not None:
            d50 = round(abs(slot_row["rtt_p50_ms"] - win["p50"]), 3)
            d95 = round(abs(slot_row["rtt_p95_ms"] - win["p95"]), 3)
        rec["viewers"].append({
            "i": v["i"], "user": v["user"], "role": v["role"], "mobile": v["mobile"], "slot": v["slot"],
            "sample_slots": v["sample_slots"], "n": len(ms), "sent": v["sent"], "lost": v["lost"],
            "p50": round(pct(ms, 0.5), 1) if ms else None, "p95": round(pct(ms, 0.95), 1) if ms else None,
            "max": round(max(ms), 1) if ms else None,
            "p50_ci90": boot_ci(ms, 0.5), "p95_ci90": boot_ci(ms, 0.95),
            "fps_median": v["fps_median"], "path": v["path"], "ttff_ms": v["ttff_ms"],
            "frames": (v["inbound"] or {}).get("framesDecoded"), "dropped": (v["inbound"] or {}).get("framesDropped"),
            "jb_ms": round(1000 * v["inbound"]["jitterBufferDelay"] / v["inbound"]["jitterBufferEmittedCount"], 1) if (v["inbound"] or {}).get("jitterBufferEmittedCount") else None,
            "decode_ms": round(1000 * v["inbound"]["totalDecodeTime"] / v["inbound"]["framesDecoded"], 2) if (v["inbound"] or {}).get("framesDecoded") else None,
            "sends_total": v["sends_total"], "sends_by_type": v["sends_by_type"],
            "row_vs_window_ms": [d50, d95] if slot_row else None,
            "window": win,
        })
    pooled = [s["ms"] for v in pr["viewers"] if v["role"] == "player" for s in v["samples"]]
    rec["pooled"] = {"n": len(pooled), "p50": round(pct(pooled, 0.5), 1) if pooled else None,
                     "p95": round(pct(pooled, 0.95), 1) if pooled else None,
                     "p50_ci90": boot_ci(pooled, 0.5), "p95_ci90": boot_ci(pooled, 0.95),
                     "lost": sum(v["lost"] for v in pr["viewers"] if v["role"] == "player"),
                     "sent": sum(v["sent"] for v in pr["viewers"] if v["role"] == "player")}
    rec["forged"] = pr.get("forged")
    out[label] = rec

json.dump(out, open(f"{D}/summary-01a0dbd6.json", "w"), indent=1, default=str)

print("| run | viewer | role | slot | n | lost | p50 ms (90% CI) | p95 ms (90% CI) | max ms | fps (median) | path | TTFF ms | decode ms/frame | jitter buf ms |")
print("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|")
for label, r in out.items():
    for v in r["viewers"]:
        slot = f"P{v['slot'] + 1}" if v["slot"] is not None and v["role"] == "player" else "-"
        role = v["role"] + (" (375px)" if v["mobile"] else "")
        print(f"| {label} | {v['user']} | {role} | {slot} | {v['n']} | {v['lost']} | {v['p50']} {tuple(v['p50_ci90']) if v['p50_ci90'] else ''} | {v['p95']} {tuple(v['p95_ci90']) if v['p95_ci90'] else ''} | {v['max']} | {v['fps_median']} | {v['path']} | {v['ttff_ms']} | {v['decode_ms']} | {v['jb_ms']} |")
print()
print("| run | slots pooled | n | lost/sent | p50 (90% CI) | p95 (90% CI) |")
print("|---|---|---|---|---|---|")
for label, r in out.items():
    q = r["pooled"]
    print(f"| {label} | {r['players']} | {q['n']} | {q['lost']}/{q['sent']} | {q['p50']} {tuple(q['p50_ci90']) if q['p50_ci90'] else ''} | {q['p95']} {tuple(q['p95_ci90']) if q['p95_ci90'] else ''} |")
print()
print("| run | peers | VRAM MiB (min-max) | NVENC % | wave tx Mbps | per peer | UDP endpoints | moon busy % (median, 16 cpu) | Chrome cores (median) |")
print("|---|---|---|---|---|---|---|---|---|")
for label, r in out.items():
    print(f"| {label} | {r['players']}+{r['spectators']} | {r['vram_mib']} | {r['enc_util']} | {r['wave_tx_mbps']} | {r['wave_tx_mbps_per_peer']} | {r['udp_endpoints']} | {r['moon_busy_pct_median']} | {r['chrome_cores_median']} |")
print()
for label, r in out.items():
    row = r["row"] or {}
    print(label, "row peak", row.get("peak_players"), row.get("peak_spectators"), "worst", row.get("rtt_p50_ms"), row.get("rtt_p95_ms"),
          "row-vs-window", [(v["slot"], v["row_vs_window_ms"]) for v in r["viewers"] if v["role"] == "player"],
          "spectator sends", [(v["user"], v["sends_total"], v["sends_by_type"]) for v in r["viewers"] if v["role"] == "spectator"], "forged", r["forged"])
