// Touch driver for task 01a0fe45 stage 5: headless Chrome on moon (CDP via an
// ssh tunnel), one browser context per viewer, real CDP touch emulation:
// Emulation.setTouchEmulationEnabled + setDeviceMetricsOverride(mobile) +
// Input.dispatchTouchEvent. Chrome turns those into pointer events of
// pointerType "touch" plus its compat mouse events and click, which is the
// duplicate-activation risk under test.
//
// usage: node touch-01a0fe45.mjs --cdp 127.0.0.1:9245 --app http://192.168.1.200:4034 \
//          --canvas <uuid> --scenario <name> --out <dir> --label <label>
//
// Scenarios: probe (layout + one screenshot), taps (four controls + empty spot
// + drag + bar tap, in one viewport), rotate (portrait -> landscape mid-run),
// spectator (spectator taps/drags + forged touch, with a P1 positive control),
// keypad (desktop P2 keyboard + fake gamepad), notouch (host without the
// feature). Every step records its UTC wall-clock window so host log lines
// can be assigned to it afterwards.
import fs from 'node:fs';

const arg = (k, d) => { const i = process.argv.indexOf('--' + k); return i > 0 ? process.argv[i + 1] : d; };
const cdpHost = arg('cdp', '127.0.0.1:9245'), app = arg('app', 'http://192.168.1.200:4034');
const canvas = arg('canvas'), scenario = arg('scenario', 'probe'), outDir = arg('out', '.');
const label = arg('label', scenario), vpName = arg('vp', 'portrait');
if (!canvas) throw new Error('--canvas is required');
const sleep = ms => new Promise(r => setTimeout(r, ms));
const t0 = Date.now();
const log = (...a) => console.error(`[${((Date.now() - t0) / 1000).toFixed(1)}s]`, ...a);

const VP = {
  portrait: { width: 390, height: 844, deviceScaleFactor: 3, mobile: true, touch: true },
  landscape: { width: 844, height: 390, deviceScaleFactor: 3, mobile: true, touch: true },
  desktop: { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false, touch: true },
  desktop_mouse: { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false, touch: false },
};

// Survival Game @3c92194, measured on its 450x1000 frame (probe screenshot):
// normalised frame coordinates of the controls' centres.
const TARGETS = JSON.parse(fs.readFileSync(new URL('./targets-01a0fe45.json', import.meta.url)));

const INSTRUMENT = (pad) => `(() => {
  window.__r5 = { sends: [], channels: [], clicks: 0, pointer: [], compat: [] }
  const r5 = window.__r5
  const send = RTCDataChannel.prototype.send
  RTCDataChannel.prototype.send = function (data) {
    let m = null; try { m = JSON.parse(data) } catch (_) {}
    r5.sends.push({ label: this.label, t: m && m.t, ph: m && m.ph, id: m && m.id, x: m && m.x, y: m && m.y, at: Date.now() })
    return send.apply(this, arguments)
  }
  const PC = window.RTCPeerConnection
  window.RTCPeerConnection = function (...args) {
    const pc = new PC(...args)
    r5.pc = pc
    pc.addEventListener('datachannel', e => r5.channels.push(e.channel))
    return pc
  }
  window.RTCPeerConnection.prototype = PC.prototype
  Object.setPrototypeOf(window.RTCPeerConnection, PC)
  // What the page saw: pointer types on the well, and the compat mouse/click
  // the browser emits after a touch (the hook must swallow the click).
  for (const t of ['pointerdown', 'pointerup', 'pointercancel']) window.addEventListener(t, e => r5.pointer.push({ t, type: e.pointerType, at: Date.now() }), true)
  for (const t of ['mousedown', 'mouseup', 'click']) window.addEventListener(t, e => r5.compat.push({ t, at: Date.now(), target: e.target && e.target.tagName }), true)
  ${pad ? `
  // A fake standard-mapping pad, driven through window.__r5pad.
  const pad = { id: 'Fake Pad 01a0fe45 (STANDARD GAMEPAD)', index: 0, connected: true, mapping: 'standard', timestamp: 0,
    axes: [0, 0, 0, 0], buttons: Array.from({ length: 17 }, () => ({ pressed: false, touched: false, value: 0 })) }
  window.__r5pad = (i, down) => { pad.buttons[i] = { pressed: down, touched: down, value: down ? 1 : 0 }; pad.timestamp = performance.now() }
  navigator.getGamepads = () => [pad, null, null, null]
  setTimeout(() => window.dispatchEvent(Object.assign(new Event('gamepadconnected'), { gamepad: pad })), 500)` : ''}
})()`;

const HOOKFIND = `(() => {
  const hs = []
  for (const el of document.querySelectorAll('[phx-hook=GameStream]')) {
    let view = null
    if (window.liveSocket) liveSocket.owner(el, v => (view = v))
    const hook = view && el.phxPrivate && view.viewHooks[el.phxPrivate.hookId]
    if (hook) hs.push(hook)
  }
  window.__r5hooks = hs
  return hs.length
})()`;

const STATE = `(() => {
  const hs = window.__r5hooks || []
  const h = hs.find(h => h.pc) || hs[0]
  const el = h ? h.el : document.querySelector('[phx-hook=GameStream]')
  if (!el) return null
  const q = s => el.querySelector(s)
  const vis = s => { const x = q(s); return !!x && !x.hidden && x.getClientRects().length > 0 }
  const v = q('video'), well = q('[data-gs-well]')
  const vr = v ? v.getBoundingClientRect() : null
  let content = null
  if (v && v.videoWidth) {
    const s = Math.min(vr.width / v.videoWidth, vr.height / v.videoHeight)
    const w = v.videoWidth * s, hh = v.videoHeight * s
    content = { left: vr.left + (vr.width - w) / 2, top: vr.top + (vr.height - hh) / 2, width: w, height: hh }
  }
  return {
    state: el.dataset.state, frames: v ? v.getVideoPlaybackQuality().totalVideoFrames : 0,
    videoWidth: v && v.videoWidth, videoHeight: v && v.videoHeight,
    videoRect: vr && { left: vr.left, top: vr.top, width: vr.width, height: vr.height }, content,
    touchAttr: well ? well.getAttribute('data-gs-touch') : null,
    tall: well ? well.hasAttribute('data-gs-tall') : null,
    touchAction: well ? getComputedStyle(well).touchAction : null,
    join: vis('[data-gs-join]'), leave: vis('[data-gs-leave]'),
    livePanel: vis('[data-gs-panel=live]'),
    controller: (q('[data-gs-controller]') || { textContent: '' }).textContent.trim(),
    hint: (q('[data-gs-hint]') || { textContent: '' }).textContent.trim(),
    ring: !!q('[data-gs-ring]') && !q('[data-gs-ring]').hidden,
    player: h ? !!(h.router && h.router.player) : null, coarse: h ? h.coarse : null,
    touchOn: h ? h.touchOn : null, touchCfg: h ? h.touchCfg : null,
    sidebarOpen: !!document.querySelector('#canvas-sidebar:not([hidden]):not(.hidden)') && getComputedStyle(document.querySelector('#canvas-sidebar')).display !== 'none',
    scrollY: window.scrollY, scrollers: Array.from(document.querySelectorAll('main, [data-scroll], .overflow-y-auto')).map(x => x.scrollTop).filter(x => x > 0),
    zoom: window.visualViewport ? window.visualViewport.scale : null,
    sends: (window.__r5 || { sends: [] }).sends.length,
    matchCoarse: matchMedia('(pointer: coarse)').matches, maxTouchPoints: navigator.maxTouchPoints,
    pcState: h && h.pc ? h.pc.connectionState : null,
  }
})()`;

// ── minimal CDP client (flattened sessions) ────────────────────────────────
const ver = await (await fetch(`http://${cdpHost}/json/version`)).json();
const ws = new WebSocket(ver.webSocketDebuggerUrl.replace(/ws:\/\/[^/]+/, `ws://${cdpHost}`));
await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
let id = 0; const pend = new Map();
ws.onmessage = m => {
  const d = JSON.parse(m.data);
  if (d.id && pend.has(d.id)) { const { res, rej } = pend.get(d.id); pend.delete(d.id); d.error ? rej(new Error(JSON.stringify(d.error))) : res(d.result); }
};
const send = (method, params = {}, sessionId) => new Promise((res, rej) => { const i = ++id; pend.set(i, { res, rej }); ws.send(JSON.stringify({ id: i, method, params, sessionId })); });
const contexts = [];
const result = { label, scenario, canvas, app, chrome: ver.Browser, started_at: new Date().toISOString(), steps: [], viewers: {} };
fs.mkdirSync(outDir, { recursive: true });

async function setViewport(s, vp) {
  await s('Emulation.setDeviceMetricsOverride', { width: vp.width, height: vp.height, deviceScaleFactor: vp.deviceScaleFactor, mobile: vp.mobile,
    screenOrientation: vp.width > vp.height ? { type: 'landscapePrimary', angle: 90 } : { type: 'portraitPrimary', angle: 0 } });
  await s('Emulation.setTouchEmulationEnabled', { enabled: vp.touch, maxTouchPoints: vp.touch ? 5 : 1 });
}

async function viewer(name, user, vpKey, { pad = false } = {}) {
  const vp = VP[vpKey];
  const { browserContextId } = await send('Target.createBrowserContext', {});
  contexts.push(browserContextId);
  const { targetId } = await send('Target.createTarget', { url: 'about:blank', browserContextId });
  const { sessionId } = await send('Target.attachToTarget', { targetId, flatten: true });
  const s = (m, p) => send(m, p, sessionId);
  await s('Page.enable'); await s('Runtime.enable');
  await setViewport(s, vp);
  await s('Page.addScriptToEvaluateOnNewDocument', { source: INSTRUMENT(pad) });
  const ev = async expr => {
    const r = await s('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error(JSON.stringify(r.exceptionDetails).slice(0, 400));
    return r.result.value;
  };
  const v = { name, user, vpKey, vp, s, ev };
  v.state = async () => { await ev(HOOKFIND).catch(() => 0); return ev(STATE); };
  v.waitFor = async (pred, what, ms = 60000) => {
    const t = Date.now(); let st;
    while (Date.now() - t < ms) { st = await v.state().catch(() => null); if (st && pred(st)) return st; await sleep(300); }
    throw new Error(`${name}: timeout waiting for ${what}: ${JSON.stringify(st)}`);
  };
  v.click = sel => ev(`(() => { const h = (window.__r5hooks || []).find(h => h.pc) || (window.__r5hooks || [])[0]; const n = (h ? h.el : document).querySelector(${JSON.stringify(sel)}); if (!n) throw new Error('no ${sel}'); n.click(); return true })()`);
  v.point = async (nx, ny) => {
    const st = await v.state();
    const c = st.content;
    return { x: c.left + nx * c.width, y: c.top + ny * c.height, st };
  };
  v.touch = (type, pts) => s('Input.dispatchTouchEvent', { type, touchPoints: pts.map((p, i) => ({ x: p.x, y: p.y, id: p.id ?? i, radiusX: 1, radiusY: 1, force: 1 })) });
  v.tapAt = async (x, y, holdMs = 70) => { await v.touch('touchStart', [{ x, y }]); await sleep(holdMs); await v.touch('touchEnd', []); };
  v.tap = async (nx, ny) => { const p = await v.point(nx, ny); await v.tapAt(p.x, p.y); return p; };
  v.drag = async (nx0, ny0, nx1, ny1, moves = 10, ms = 300, hold = 30) => {
    const a = await v.point(nx0, ny0), b = await v.point(nx1, ny1);
    await v.touch('touchStart', [{ x: a.x, y: a.y }]);
    for (let k = 1; k <= moves; k++) { await sleep(ms / moves); await v.touch('touchMove', [{ x: a.x + (b.x - a.x) * k / moves, y: a.y + (b.y - a.y) * k / moves }]); }
    await sleep(hold); await v.touch('touchEnd', []);
    return { from: a, to: b };
  };
  v.key = async (code, key, vk) => {
    await s('Input.dispatchKeyEvent', { type: 'keyDown', code, key, windowsVirtualKeyCode: vk });
    await sleep(80);
    await s('Input.dispatchKeyEvent', { type: 'keyUp', code, key, windowsVirtualKeyCode: vk });
  };
  v.pad = async (i) => { await ev(`window.__r5pad(${i}, true)`); await sleep(150); await ev(`window.__r5pad(${i}, false)`); };
  v.shot = async (file, wellOnly = false) => {
    let clip;
    if (wellOnly) { const st = await v.state(); const c = st.content; clip = { x: c.left, y: c.top, width: c.width, height: c.height, scale: 1 }; }
    const { data } = await s('Page.captureScreenshot', { format: 'jpeg', quality: 85, ...(clip ? { clip } : {}), captureBeyondViewport: false });
    fs.writeFileSync(`${outDir}/${file}`, Buffer.from(data, 'base64'));
    return file;
  };
  v.sends = () => ev('window.__r5.sends');
  // Reads the decoded frame itself (drawImage of the <video>): button rows of
  // Survival Game's UI by colour (button fill (31,33,30) vs panel (38,43,37)),
  // so taps aim at where the controls ARE in this frame, whatever the scroll.
  v.frame = async (regions = []) => { await ev(HOOKFIND).catch(() => 0); return ev(`(() => {
    const hs = window.__r5hooks || []; const h = hs.find(h => h.pc) || hs[0]
    const vid = h.el.querySelector('video'); const W = vid.videoWidth, H = vid.videoHeight
    const c = document.createElement('canvas'); c.width = W; c.height = H
    const g = c.getContext('2d', { willReadFrequently: true }); g.drawImage(vid, 0, 0)
    const d = g.getImageData(0, 0, W, H).data
    const at = (x, y) => { const i = (Math.round(y) * W + Math.round(x)) * 4; return [d[i], d[i + 1], d[i + 2]] }
    // A row's kind is how many long runs of non-panel pixels cross it: a
    // button row is 2 or 3 runs (text inside a button stays inside its run);
    // a text line breaks into short runs; the settlement grid is 1 run.
    const panel = ([r, gg, b]) => Math.abs(r - 38) <= 3 && Math.abs(gg - 43) <= 3 && Math.abs(b - 38) <= 4
    const btnish = ([r, gg, b]) => r <= 36 && gg <= 38 && b <= 35
    const kindAt = y => {
      // A run ends at 2+ consecutive panel pixels (anti-aliased glyph edges
      // inside a button can hit the panel colour for a pixel or two).
      let runs = 0, len = 0, btnPx = 0, gap = 0
      const close = () => { if (len > 0.15 * W && btnPx > 0.4 * len) runs++; len = 0; btnPx = 0 }
      for (let x = Math.round(0.02 * W); x < Math.round(0.975 * W); x++) {
        const p = at(x, y)
        if (panel(p)) { gap++; if (gap === 2) close() }
        else { if (gap >= 2) { len = 0; btnPx = 0 } gap = 0; len++; if (btnish(p)) btnPx++ }
      }
      close()
      return runs === 2 || runs === 3 ? String(runs) : null
    }
    const rows = []
    let cur = null
    for (let y = 0; y < H; y++) {
      const kind = kindAt(y)
      if (kind && cur && cur.kind === kind && y === cur.y1 + 1) cur.y1 = y
      else { if (cur && cur.y1 - cur.y0 >= 0.02 * H) rows.push(cur); cur = kind ? { kind, y0: y, y1: y } : null }
    }
    if (cur && cur.y1 - cur.y0 >= 0.02 * H) rows.push(cur)
    const mean = ([x0, y0, x1, y1]) => { let s = [0, 0, 0], n = 0
      for (let y = y0 * H; y < y1 * H; y += 2) for (let x = x0 * W; x < x1 * W; x += 2) { const p = at(x, y); s[0] += p[0]; s[1] += p[1]; s[2] += p[2]; n++ }
      return s.map(v => Math.round(v / n)) }
    return { W, H, rows: rows.map(r => ({ kind: r.kind, y: +((r.y0 + r.y1) / 2 / H).toFixed(4), h: +((r.y1 - r.y0) / H).toFixed(4) })),
      regions: ${JSON.stringify(regions)}.map(rg => ({ rg, mean: mean(rg) })) }
  })()`); };
  v.forge = (msg) => ev(`(() => {
    const ch = window.__r5.channels.filter(c => c.label === 'input-events' && c.readyState === 'open').pop()
    if (!ch) return { sent: false, channels: window.__r5.channels.map(c => c.label + ':' + c.readyState) }
    ch.send(${JSON.stringify(JSON.stringify(msg))}); return { sent: true, at: Date.now() } })()`);
  v.open = async () => {
    await s('Page.navigate', { url: `${app}/dev/login?as=${user}` }); await sleep(1500);
    await s('Page.navigate', { url: `${app}/canvases/${canvas}` }); await sleep(1500);
    return v.waitFor(st => st.state === 'live' && st.frames > 3 && st.videoWidth > 0, 'live video');
  };
  v.join = async () => { await v.click('[data-gs-join]'); return v.waitFor(st => st.leave && st.player, 'joined a slot'); };
  result.viewers[name] = { user, vp: vpKey };
  return v;
}

async function step(name, fn) {
  const at0 = new Date().toISOString();
  let r, err = null;
  try { r = await fn(); } catch (e) { err = String(e); }
  const at1 = new Date().toISOString();
  result.steps.push({ name, ...(r && typeof r === 'object' ? r : { value: r }), w0: at0, w1: at1, ...(err ? { error: err } : {}) });
  log(name, err || JSON.stringify(r).slice(0, 300));
  return r;
}

// Where the controls are in the CURRENT frame: Pause/Reset on the first
// three-button row from the top, Add child / Food shortage on the two-button row.

// The part of the frame a person can actually touch right now: between the
// app's scroll container top and the bottom nav (measured, not assumed).
async function visible(v) {
  return v.ev(`(() => { const w = document.querySelector('[data-gs-well]'); let e = w.parentElement, sc = null
    while (e) { const cs = getComputedStyle(e); if (/(auto|scroll)/.test(cs.overflowY) && e.scrollHeight > e.clientHeight) { sc = e; break } e = e.parentElement }
    const v = w.querySelector('video'); const r = v.getBoundingClientRect()
    const s = Math.min(r.width / v.videoWidth, r.height / v.videoHeight); const ch = v.videoHeight * s, ct = r.top + (r.height - ch) / 2
    // Hit-test down the content's centre line: the touchable band is where
    // elementFromPoint lands inside the well (headers and the bottom nav are
    // on top of it elsewhere).
    const cx = r.left + r.width / 2; let top = null, nav = null
    for (let y = Math.max(0, ct); y < Math.min(innerHeight, ct + ch); y += 2) { const e = document.elementFromPoint(cx, y); const inW = !!e && w.contains(e)
      if (inW && top == null) top = y; if (!inW && top != null && nav == null) nav = y }
    if (top == null) top = ct + ch; if (nav == null) nav = Math.min(innerHeight, ct + ch)
    return { y0: Math.max(0, (top - ct) / ch), y1: Math.min(1, (nav - ct) / ch), scrollTop: sc ? sc.scrollTop : null, max: sc ? sc.scrollHeight - sc.clientHeight : null, sideX: Math.max(8, r.left / 2) } })()`);
}
// A real touch swipe beside the well scrolls the page (the well itself is
// touch-action:none). dy > 0 brings lower content up.
async function pageSwipe(v, dy) {
  const vis = await visible(v); const H = v.vp.height
  const y0 = dy > 0 ? H * 0.75 : H * 0.3, y1 = Math.max(10, Math.min(H - 10, y0 - dy))
  await v.touch('touchStart', [{ x: vis.sideX, y: y0 }]); for (let k = 1; k <= 10; k++) { await sleep(30); await v.touch('touchMove', [{ x: vis.sideX, y: y0 + (y1 - y0) * k / 10 }]); }
  await sleep(250); await v.touch('touchEnd', []); await sleep(800);
  return visible(v);
}
async function ensureVisible(v, ny) {
  let vis = await visible(v); const swipes = [];
  for (let k = 0; k < 4 && !(ny > vis.y0 + 0.02 && ny < vis.y1 - 0.02); k++) {
    const st = await v.state(); const ch = st.content.height;
    const dy = ny >= vis.y1 - 0.02 ? (ny - (vis.y1 - 0.08)) * ch : (ny - (vis.y0 + 0.08)) * ch;
    vis = await pageSwipe(v, dy); swipes.push({ dy: Math.round(dy), after: vis });
  }
  return { vis, swipes };
}
const located = { two: null };
async function locate(v) {
  const f = await v.frame();
  // Pause/Reset: the topmost three-button row, only when it is the frame's
  // first row (scrolled to the top). Add child / Food shortage: the LOWEST
  // two-button row (Mara/Ivo/Nell reads as two while Nell is disabled).
  const three = f.rows.length && f.rows[0].kind === '3' && f.rows[0].y < 0.25 ? f.rows[0] : null;
  const two = [...f.rows].reverse().find(r => r.kind === '2' && r.y > 0.3);
  const t = {};
  if (three) { t.pause = { x: 0.178, y: three.y }; t.reset = { x: 0.805, y: three.y }; }
  if (two) { t.add_child = { x: 0.256, y: two.y }; t.food_shortage = { x: 0.726, y: two.y }; }
  // Add child turns disabled once used (3 citizens), so its row no longer reads
  // as two buttons: Food shortage keeps the row last seen, unless a drag moved it.
  if (two) located.two = two.y; else if (located.two != null) { t.food_shortage = { x: 0.726, y: located.two, cached: true }; }
  return { t, rows: f.rows };
}
async function tapNamed(v, name) { const loc = await locate(v); const T = loc.t[name] || TARGETS[name]; const p = await v.tap(T.x, T.y); p.located = !!loc.t[name]; return p; }
const PAUSE_REGION = rowY => [0.06, rowY - 0.012, 0.30, rowY + 0.012];

async function tapStep(v, tag, i, t, gap) {
  return step(`${tag}:tap:${t}`, async () => {
    const loc = await locate(v);
    const T = loc.t[t];
    if (!T) throw new Error(`${t} not visible in the frame: rows ${JSON.stringify(loc.rows)}`);
    const ens = await ensureVisible(v, T.y);
    const before = t === 'pause' ? (await v.frame([PAUSE_REGION(T.y)])).regions[0].mean : null;
    const sends0 = (await v.sends()).length;
    const p = await v.tap(T.x, T.y);
    await sleep(gap);
    const sends = (await v.sends()).slice(sends0);
    const after = await v.state();
    const afterPause = t === 'pause' ? (await v.frame([PAUSE_REGION(T.y)])).regions[0].mean : null;
    return { target: T, rows: loc.rows, page_swipes: ens.swipes, client: { x: p.x, y: p.y }, sends: sends.map(x => `${x.t}:${x.ph}:${x.id}:${x.x?.toFixed?.(4)},${x.y?.toFixed?.(4)}`),
      ...(before ? { pause_button_mean_before: before, pause_button_mean_after: afterPause } : {}),
      hint: after.hint, sidebarOpen: after.sidebarOpen, scrollY: after.scrollY, zoom: after.zoom, ring: after.ring,
      shot: await v.shot(`${label}-${tag}-${i}-${t}-01a0fe45.jpg`, true) };
  });
}

async function dragStep(v, tag, name, y0, y1, gap, shotName, hold = 300) {
  const D = TARGETS.drag;
  return step(`${tag}:${name}`, async () => {
    located.two = null;
    const vis = await visible(v);
    const lo = vis.y0 + 0.03, hi = vis.y1 - 0.03;
    y0 = Math.min(hi, Math.max(lo, y0)); y1 = Math.min(hi, Math.max(lo, y1));
    const sends0 = (await v.sends()).length; const before = await v.state(); const f0 = await v.frame();
    const d = await v.drag(D.x, y0, D.x, y1, 10, 300, hold);
    await sleep(gap);
    const sends = (await v.sends()).slice(sends0); const after = await v.state(); const f1 = await v.frame();
    return { visible: vis, y0, y1, from: { x: d.from.x, y: d.from.y }, to: { x: d.to.x, y: d.to.y }, sends_by_ph: sends.reduce((m, x) => (m[x.ph] = (m[x.ph] || 0) + 1, m), {}), ids: [...new Set(sends.map(x => x.id))],
      rows_before: f0.rows, rows_after: f1.rows,
      scrollY_before: before.scrollY, scrollY_after: after.scrollY, ...(shotName ? { shot: await v.shot(`${label}-${tag}-${shotName}-01a0fe45.jpg`, true) } : {}) };
  });
}

// Pause and Reset sit at the top of the 450x1000 frame; Add child and Food
// shortage sit below it, so the sequence scrolls the game's ScrollContainer
// with real touch drags (each held still 300 ms before release, so Godot's
// drag inertia does not carry it on) between them. The drags are under test too.
async function controls(v, tag, { gap = 1000 } = {}) {
  const st = await v.state();
  await step(`${tag}:layout`, async () => ({ state: st, shot: await v.shot(`${label}-${tag}-0-before-01a0fe45.jpg`) }));
  await dragStep(v, tag, 'settle-top', 0.12, 0.8, 600);
  await dragStep(v, tag, 'settle-top2', 0.12, 0.8, 600, '0-top');
  await tapStep(v, tag, 1, 'pause', gap);
  await step(`${tag}:page-down`, async () => ({ ...(await ensureVisible(v, 0.9)) }));
  await dragStep(v, tag, 'drag-up', 0.8, 0.12, gap, '2-dragged');
  await dragStep(v, tag, 'drag-up2', 0.8, 0.12, gap, '2b-dragged');
  await tapStep(v, tag, 3, 'add_child', gap);
  await tapStep(v, tag, 4, 'food_shortage', gap);
  await step(`${tag}:page-up`, async () => ({ ...(await ensureVisible(v, 0.1)) }));
  await dragStep(v, tag, 'drag-down', 0.12, 0.8, gap);
  await dragStep(v, tag, 'drag-down2', 0.12, 0.8, gap, '5-dragged-back');
  await tapStep(v, tag, 6, 'reset', gap);
  await step(`${tag}:tap:empty`, async () => {
    const ens = await ensureVisible(v, TARGETS.empty.y);
    const sends0 = (await v.sends()).length;
    const p = await v.tap(TARGETS.empty.x, TARGETS.empty.y); await sleep(gap);
    return { page_swipes: ens.swipes, client: { x: p.x, y: p.y }, sends: (await v.sends()).slice(sends0).map(x => `${x.t}:${x.ph}`) };
  });
  await step(`${tag}:tap:bar`, async () => {
    const st = await v.state(); const c = st.content, r = st.videoRect;
    // A point in the bar: left/right of the content when pillarboxed, above/below when letterboxed.
    let x = null, y = null;
    if (c.left - r.left > 8) { x = r.left + (c.left - r.left) / 2; y = c.top + c.height / 2; }
    else if (c.top - r.top > 8) { x = c.left + c.width / 2; y = r.top + (c.top - r.top) / 2; }
    if (x == null) return { bar: 'none (content fills the element)', content: c, videoRect: r };
    const sends0 = (await v.sends()).length;
    await v.tapAt(x, y); await sleep(gap);
    return { client: { x, y }, content: c, videoRect: r, sends: (await v.sends()).slice(sends0).map(x => `${x.label}:${x.t}:${x.ph || ""}`) };
  });
  await step(`${tag}:full`, async () => ({ state: await v.state(), shot: await v.shot(`${label}-${tag}-9-after-01a0fe45.jpg`) }));
}

try {
  if (scenario === 'probe') {
    const v = await viewer('P1', 'jordan', vpName);
    const st = await v.open();
    await step('probe:live', async () => ({ state: st }));
    await step('probe:join', async () => ({ state: await v.join() }));
    await sleep(1500);
    await step('probe:state', async () => ({ state: await v.state(), shot: await v.shot(`${label}-probe-01a0fe45.jpg`), well: await v.shot(`${label}-probe-well-01a0fe45.jpg`, true) }));
  } else if (scenario === 'pix') {
    const v = await viewer('P1', 'jordan', vpName);
    await v.open(); await sleep(1500);
    await step('pix', async () => ({ px: await v.ev(`(() => { const hs = window.__r5hooks || []; const h = hs.find(h => h.pc) || hs[0]
      const vid = h.el.querySelector('video'); const W = vid.videoWidth, H = vid.videoHeight
      const c = document.createElement('canvas'); c.width = W; c.height = H; const g = c.getContext('2d'); g.drawImage(vid, 0, 0)
      const out = {}; for (const y of [0.137, 0.19, 0.25, 0.5, 0.67]) out[y] = [0.05, 0.178, 0.33, 0.49, 0.665, 0.94].map(x => Array.from(g.getImageData(Math.round(x * W), Math.round(y * H), 1, 1).data.slice(0, 3)).join(','))
      return { W, H, out } })()`) }));
  } else if (scenario === 'pagescroll') {
    const v = await viewer('P1', 'jordan', vpName);
    await v.open(); await step('join', async () => ({ state: await v.join() })); await sleep(1500);
    const anc = `(() => { const w = document.querySelector('[data-gs-well]'); const out = []; let e = w.parentElement
      while (e) { const cs = getComputedStyle(e); if (/(auto|scroll)/.test(cs.overflowY) && e.scrollHeight > e.clientHeight) out.push({ tag: e.tagName, id: e.id, cls: String(e.className).slice(0, 80), scrollTop: e.scrollTop, max: e.scrollHeight - e.clientHeight }); e = e.parentElement }
      const r = w.getBoundingClientRect(); const nav = document.querySelector('nav[class*=bottom], [data-bottom-nav], #bottom-nav')
      return { scrollers: out, well: { top: r.top, bottom: r.bottom, h: r.height }, vh: innerHeight, nav: nav ? nav.getBoundingClientRect().top : null, navVar: getComputedStyle(document.documentElement).getPropertyValue('--bottom-nav-height') } })()`;
    await step('before', async () => ({ info: await v.ev(anc) }));
    // A real touch swipe OUTSIDE the well (its left side), as a person scrolls the page.
    await step('side-swipe', async () => {
      const st = await v.state(); const x = Math.max(8, st.videoRect.left / 2), y0 = Math.min(st.videoRect.top + 150, VP[vpName].height - 80), y1 = Math.max(y0 - 250, 60);
      await v.touch('touchStart', [{ x, y: y0 }]); for (let k = 1; k <= 10; k++) { await sleep(30); await v.touch('touchMove', [{ x, y: y0 + (y1 - y0) * k / 10 }]); }
      await sleep(200); await v.touch('touchEnd', []); await sleep(1500);
      return { x, y0, y1, info: await v.ev(anc), shot: await v.shot(`${label}-pagescrolled-01a0fe45.jpg`) };
    });
  } else if (scenario === 'fullscreen') {
    const v = await viewer('P1', 'jordan', vpName);
    await v.open(); await step('join', async () => ({ state: await v.join() })); await sleep(1500);
    await step('enter-fullscreen', async () => {
      const b = await v.ev(`(() => { const hs = window.__r5hooks || []; const h = hs.find(h => h.pc) || hs[0]; const n = h.el.querySelector('[data-gs-fullscreen]'); if (!n) return null; n.scrollIntoView({ block: 'center' }); const r = n.getBoundingClientRect(); return { x: r.left + r.width / 2, y: r.top + r.height / 2, hidden: n.hidden || r.width === 0 } })()`);
      if (!b || b.hidden) throw new Error('no visible fullscreen button: ' + JSON.stringify(b));
      await sleep(400); await v.tapAt(b.x, b.y); await sleep(1500);
      return { button: b, fsEl: await v.ev('document.fullscreenElement ? document.fullscreenElement.tagName + "." + (document.fullscreenElement.getAttribute("data-gs-well") !== null ? "well" : document.fullscreenElement.className.slice(0, 40)) : null'), state: await v.state(), shot: await v.shot(`${label}-fullscreen-01a0fe45.jpg`) };
    });
    if (!process.argv.includes('--fs-only')) await controls(v, vpName + '-fs');
  } else if (scenario === 'scrollprobe') {
    const v = await viewer('P1', 'jordan', vpName);
    await v.open(); await step('join', async () => ({ state: await v.join() })); await sleep(1500);
    const D = TARGETS.drag;
    await step('drag-up', async () => { const d = await v.drag(D.x, D.y0, D.x, D.y1); await sleep(1500); return { d, well: await v.shot(`${label}-scrolled-well-01a0fe45.jpg`, true) }; });
    await step('drag-down', async () => { await v.drag(D.x, D.y1, D.x, D.y0); await sleep(1500); return { well: await v.shot(`${label}-unscrolled-well-01a0fe45.jpg`, true) }; });
  } else if (scenario === 'taps') {
    const v = await viewer('P1', 'jordan', vpName);
    await v.open(); await step('join', async () => ({ state: await v.join() })); await sleep(1500);
    await controls(v, vpName);
    result.page = { pointer: await v.ev('window.__r5.pointer.length'), pointer_types: await v.ev('[...new Set(window.__r5.pointer.map(p => p.type))]'), compat: await v.ev('window.__r5.compat.map(c => c.t + ":" + c.target)') };
  } else if (scenario === 'rotate') {
    const v = await viewer('P1', 'jordan', 'portrait');
    await v.open(); await step('join', async () => ({ state: await v.join() })); await sleep(1500);
    await step('portrait:tap:pause', async () => { const p = await tapNamed(v, 'pause'); await sleep(1000); return { client: p, shot: await v.shot(`${label}-rot-1-portrait-01a0fe45.jpg`) }; });
    await step('rotate', async () => { await setViewport(v.s, VP.landscape); await sleep(1500); return { state: await v.state() }; });
    await step('landscape:tap:pause', async () => { const p = await tapNamed(v, 'pause'); await sleep(1000); return { client: { x: p.x, y: p.y }, content: p.st.content, shot: await v.shot(`${label}-rot-2-landscape-01a0fe45.jpg`) }; });
  } else if (scenario === 'spectator') {
    const p1 = await viewer('P1', 'jordan', 'portrait');
    await p1.open(); await step('p1:join', async () => ({ state: await p1.join() }));
    const sp = await viewer('S', 'saru', 'portrait');
    await step('s:open', async () => ({ state: await sp.open() }));
    await sleep(1500);
    await step('s:taps-and-drag', async () => {
      const urls = [await sp.ev('location.href')]
      for (const t of ['pause', 'reset', 'empty']) { const T = TARGETS[t]; await sp.tap(T.x, T.y); await sleep(700); urls.push(await sp.ev('location.href')); await sp.shot(`${label}-spectator-after-${t}-01a0fe45.jpg`) }
      await sp.drag(TARGETS.drag.x, TARGETS.drag.y0, TARGETS.drag.x, TARGETS.drag.y1);
      await sleep(800);
      const st = await sp.state();
      return { urls, spectator_sends: (await sp.sends()).length, touchAttr: st.touchAttr, touchAction: st.touchAction, player: st.player, shot: await sp.shot(`${label}-spectator-01a0fe45.jpg`) };
    });
    await step('s:channels', async () => ({ ch: await sp.ev('window.__r5.channels.map(c => c.label + ":" + c.readyState)'), pc: await sp.ev('window.__r5.pc ? window.__r5.pc.connectionState : null'), state: await sp.state() }));
    await step('s:forge', async () => ({ forged: await sp.forge({ t: 'touch', src: 0, id: 0, ph: 'down', x: 0.5, y: 0.5 }), forged2: await sp.forge({ t: 'touch', src: 0, id: 0, ph: 'up', x: 0.5, y: 0.5, slot: 0 }) }));
    await step('p1:positive-control-tap', async () => { const p = await tapNamed(p1, 'pause'); await sleep(1000); return { client: { x: p.x, y: p.y }, p1_sends: (await p1.sends()).filter(x => x.t === 'touch').length }; });
    await step('p1:unpause', async () => { await tapNamed(p1, 'pause'); await sleep(1000); return {}; });
    await sleep(11000); // host drop log: at most one line per 10 s per peer
    result.spectator_sends_total = (await sp.sends()).length;
  } else if (scenario === 'keypad') {
    const p1 = await viewer('P1', 'jordan', 'portrait');
    await p1.open(); await step('p1:join', async () => ({ state: await p1.join() }));
    const d = await viewer('D', 'ryan', 'desktop_mouse', { pad: true });
    await d.open(); await step('d:join', async () => ({ state: await d.join() }));
    await step('d:capture', async () => { await d.click('[data-gs-capture]'); return { state: await d.waitFor(st => st.ring, 'capture ring') }; });
    for (const [n, code, key, vk] of [['space-pause', 'Space', ' ', 32], ['space-resume', 'Space', ' ', 32], ['c-add-child', 'KeyC', 'c', 67], ['f-shortage', 'KeyF', 'f', 70], ['r-reset', 'KeyR', 'r', 82]]) {
      await step(`d:key:${n}`, async () => { const s0 = (await d.sends()).length; await d.key(code, key, vk); await sleep(1000);
        return { sends: (await d.sends()).slice(s0).map(x => `${x.t}`), shot: await d.shot(`${label}-key-${n}-01a0fe45.jpg`, true) }; });
    }
    // standard mapping: 9 = Start (pause), 3 = Y (add child), 2 = X (food shortage), 8 = Back (reset)
    for (const [n, b] of [['start-pause', 9], ['start-resume', 9], ['y-add-child', 3], ['x-shortage', 2], ['back-reset', 8]]) {
      await step(`d:pad:${n}`, async () => { const s0 = (await d.sends()).length; await d.pad(b); await sleep(1000);
        return { sends: (await d.sends()).slice(s0).map(x => `${x.t}`), shot: await d.shot(`${label}-pad-${n}-01a0fe45.jpg`, true) }; });
    }
    await step('d:escape', async () => { await d.key('Escape', 'Escape', 27); await sleep(500); return { ring: (await d.state()).ring }; });
    result.d_sends_by_type = (await d.sends()).reduce((m, x) => (m[x.t] = (m[x.t] || 0) + 1, m), {});
    await step('p1:touch-still-works', async () => { const p = await tapNamed(p1, 'pause'); await sleep(1000); await tapNamed(p1, 'pause'); await sleep(1000); return { client: { x: p.x, y: p.y } }; });
  } else if (scenario === 'notouch') {
    const v = await viewer('P1', 'jordan', 'portrait');
    await v.open(); await step('join', async () => ({ state: await v.join() })); await sleep(1500);
    await step('notouch:state', async () => ({ state: await v.state(), shot: await v.shot(`${label}-notouch-01a0fe45.jpg`) }));
    await step('notouch:tap', async () => { const s0 = (await v.sends()).length; await tapNamed(v, 'pause'); await sleep(1000); return { sends: (await v.sends()).slice(s0).map(x => x.t) }; });
    await step('notouch:swipe', async () => {
      const before = await v.state();
      const st = before; const c = st.content;
      const x = c.left + c.width / 2, y0 = Math.min(c.top + c.height * 0.8, 800), y1 = Math.max(y0 - 300, 10);
      await v.touch('touchStart', [{ x, y: y0 }]);
      for (let k = 1; k <= 10; k++) { await sleep(30); await v.touch('touchMove', [{ x, y: y0 + (y1 - y0) * k / 10 }]); }
      await v.touch('touchEnd', []); await sleep(1200);
      const after = await v.state();
      return { scrollY_before: before.scrollY, scrollY_after: after.scrollY, scrollers_before: before.scrollers, scrollers_after: after.scrollers, sends: (await v.sends()).filter(x => x.t === 'touch').length, shot: await v.shot(`${label}-notouch-swiped-01a0fe45.jpg`) };
    });
  }
  result.ended_at = new Date().toISOString();
} catch (e) {
  result.error = String(e.stack || e);
  log('ERROR', result.error);
} finally {
  fs.writeFileSync(`${outDir}/${label}-01a0fe45.json`, JSON.stringify(result, null, 1));
  for (const c of contexts) await send('Target.disposeBrowserContext', { browserContextId: c }).catch(() => {});
  ws.close();
}
