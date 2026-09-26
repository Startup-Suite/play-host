// Drives one latency measurement: headless Chrome (CDP) <-> play-host spike.
// usage: node run-probes.mjs --cdp 127.0.0.1:19223 --signal http://127.0.0.1:18431 \
//          --n 60 --label p4-ll --out results/p4-ll.json [--frame results/p4-ll.jpg]
// Node 22, no npm deps. The page script is internal/spike/web/probe.js.
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const arg = (k, d) => { const i = process.argv.indexOf('--' + k); return i > 0 ? process.argv[i + 1] : d; };
const cdp = arg('cdp', '127.0.0.1:19223'), signal = arg('signal', 'http://127.0.0.1:18431');
const n = +arg('n', 60), gap = +arg('gap', 120), label = arg('label', 'run'), out = arg('out'), frameOut = arg('frame');
const here = path.dirname(fileURLToPath(import.meta.url));
const probeJs = fs.readFileSync(path.join(here, '../../internal/spike/web/probe.js'), 'utf8');

const t = await (await fetch(`http://${cdp}/json/new?about:blank`, { method: 'PUT' })).json();
const ws = new WebSocket(t.webSocketDebuggerUrl);
let id = 0; const pend = new Map();
const send = (method, params = {}) => new Promise(r => { const i = ++id; pend.set(i, r); ws.send(JSON.stringify({ id: i, method, params })); });
ws.onmessage = m => { const d = JSON.parse(m.data); if (d.id && pend.has(d.id)) { pend.get(d.id)(d); pend.delete(d.id); } };
await new Promise(r => ws.onopen = r);
const ev = async (expression) => {
  const r = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true, timeout: 900000 });
  if (r.result?.exceptionDetails) throw new Error(JSON.stringify(r.result.exceptionDetails).slice(0, 600));
  return r.result?.result?.value;
};
try {
  await ev(probeJs);
  const offer = await (await fetch(signal + '/offer', { method: 'POST' })).json();
  if (!offer.sdp) throw new Error('no offer: ' + JSON.stringify(offer));
  const answer = await ev(`window.spike.start(${JSON.stringify(offer.sdp)})`);
  const a = await fetch(signal + '/answer', { method: 'POST', body: JSON.stringify({ ID: offer.id, SDP: answer }) });
  if (!a.ok) throw new Error('answer: ' + await a.text());
  const live = await ev('window.spike.waitLive(20000)');
  if (!live.ok) throw new Error('not live: ' + JSON.stringify(live));
  await new Promise(r => setTimeout(r, 1500)); // let the first keyframe and jitter buffer settle
  const res = await ev(`window.spike.runProbes(${n}, ${gap})`);
  const host = await (await fetch(signal + '/stats')).json();
  if (frameOut) {
    const url = await ev('window.spike.grabFrame()');
    fs.writeFileSync(frameOut, Buffer.from(url.split(',')[1], 'base64'));
  }
  const summary = { label, at: new Date().toISOString(), live, n: res.n, lost: res.lost, p50_ms: res.p50, p95_ms: res.p95, min_ms: res.min, max_ms: res.max,
    recv_p50_ms: res.recv_p50, recv_p95_ms: res.recv_p95, browser: res.stats, host: { ...host, host_probe_to_readback_ms: undefined },
    host_probe_to_readback: summarize(host.host_probe_to_readback_ms || []), samples: res.samples };
  if (out) fs.writeFileSync(out, JSON.stringify(summary, null, 1));
  const { samples, ...brief } = summary;
  console.log(JSON.stringify(brief, null, 1));
} finally {
  await fetch(`http://${cdp}/json/close/${t.id}`).catch(() => {});
  ws.close();
}

function summarize(a) {
  if (!a.length) return { n: 0 };
  const s = [...a].sort((x, y) => x - y), r = p => s[Math.max(0, Math.ceil(p * s.length) - 1)];
  return { n: s.length, p50_ms: r(0.5), p95_ms: r(0.95) };
}
