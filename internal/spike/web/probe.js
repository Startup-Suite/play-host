// Browser side of the task 01a0db5f latency probe. Loaded by index.html, or
// injected over CDP by spike/driver/run-probes.mjs. Mirrors internal/probe.
(() => {
  const CELLS = 4, CELL_PX = 16, MAX_SEQ = 4095;
  const check = s => ((s & MAX_SEQ) ^ (s >> 4) ^ (s >> 8) ^ 0xA) & 0xF;
  const decode = bits => {
    let v = 0;
    bits.forEach((b, i) => { if (b) v |= 1 << (15 - i); });
    const seq = v >> 4;
    return seq !== 0 && (v & 0xF) === check(seq) ? seq : 0;
  };
  const pct = (a, p) => {
    if (!a.length) return null;
    const s = [...a].sort((x, y) => x - y);
    return s[Math.max(0, Math.ceil(p * s.length) - 1)];
  };
  const S = { pc: null, video: null, events: null, pending: new Map(), samples: [], lost: 0, frames: 0, lastSeq: 0, seq: 0 };
  const cvs = document.createElement('canvas');
  cvs.width = cvs.height = CELLS * CELL_PX;
  const ctx = cvs.getContext('2d', { willReadFrequently: true });

  function onFrame(now, meta) {
    S.frames++;
    ctx.drawImage(S.video, 0, 0, cvs.width, cvs.height, 0, 0, cvs.width, cvs.height);
    const d = ctx.getImageData(0, 0, cvs.width, cvs.height).data;
    const bits = [];
    for (let i = 0; i < CELLS * CELLS; i++) {
      const x = (i % CELLS) * CELL_PX + CELL_PX / 2, y = Math.floor(i / CELLS) * CELL_PX + CELL_PX / 2;
      const o = (y * cvs.width + x) * 4;
      bits.push((0.299 * d[o] + 0.587 * d[o + 1] + 0.114 * d[o + 2]) >= 128);
    }
    const seq = decode(bits);
    if (seq && S.pending.has(seq)) {
      const p = S.pending.get(seq);
      S.pending.delete(seq);
      S.samples.push({ seq, rtt: now - p.t0, recv: meta.receiveTime ? meta.receiveTime - p.t0 : null,
        pres: meta.presentationTime - p.t0, w: meta.width, h: meta.height });
      p.resolve(true);
    }
    S.video.requestVideoFrameCallback(onFrame);
  }

  async function start(offerSdp) {
    const pc = new RTCPeerConnection({ iceServers: [] });
    S.pc = pc;
    const video = document.createElement('video');
    video.muted = true; video.autoplay = true; video.playsInline = true;
    document.body.appendChild(video);
    S.video = video;
    pc.ontrack = e => {
      try { e.receiver.jitterBufferTarget = 0; } catch (_) {}
      try { e.receiver.playoutDelayHint = 0; } catch (_) {}
      video.srcObject = e.streams[0] || new MediaStream([e.track]);
      video.play().catch(() => {});
      video.requestVideoFrameCallback(onFrame);
    };
    pc.ondatachannel = e => { if (e.channel.label === 'input-events') S.events = e.channel; };
    await pc.setRemoteDescription({ type: 'offer', sdp: offerSdp });
    await pc.setLocalDescription(await pc.createAnswer());
    await new Promise(r => {
      if (pc.iceGatheringState === 'complete') return r();
      pc.onicegatheringstatechange = () => pc.iceGatheringState === 'complete' && r();
      setTimeout(r, 3000);
    });
    return pc.localDescription.sdp;
  }

  async function waitLive(ms = 20000) {
    const t = performance.now();
    while (performance.now() - t < ms) {
      if (S.events && S.events.readyState === 'open' && S.frames > 10) return { ok: true, frames: S.frames, waited_ms: performance.now() - t };
      await new Promise(r => setTimeout(r, 100));
    }
    return { ok: false, frames: S.frames, pc: S.pc && S.pc.connectionState, dc: S.events && S.events.readyState };
  }

  async function runProbes(n = 60, gapMs = 120, timeoutMs = 1500) {
    S.samples = []; S.lost = 0;
    for (let i = 0; i < n; i++) {
      S.seq = S.seq % MAX_SEQ + 1;
      const seq = S.seq;
      const done = new Promise(resolve => S.pending.set(seq, { t0: performance.now(), resolve }));
      S.pending.get(seq).t0 = performance.now();
      S.events.send(JSON.stringify({ t: 'probe', seq }));
      const ok = await Promise.race([done, new Promise(r => setTimeout(() => r(false), timeoutMs))]);
      if (!ok) { S.pending.delete(seq); S.lost++; }
      await new Promise(r => setTimeout(r, gapMs + Math.random() * gapMs));
    }
    const rtt = S.samples.map(s => s.rtt), recv = S.samples.map(s => s.recv).filter(x => x != null);
    return { n: rtt.length, lost: S.lost, p50: pct(rtt, 0.5), p95: pct(rtt, 0.95), min: pct(rtt, 0), max: pct(rtt, 1),
      recv_p50: pct(recv, 0.5), recv_p95: pct(recv, 0.95), samples: S.samples, stats: await stats() };
  }

  async function stats() {
    const out = {};
    if (!S.pc) return out;
    const r = await S.pc.getStats();
    let pairId = null;
    r.forEach(s => { if (s.type === 'transport' && s.selectedCandidatePairId) pairId = s.selectedCandidatePairId; });
    r.forEach(s => {
      if (s.type === 'inbound-rtp' && s.kind === 'video') {
        Object.assign(out, { decoderImplementation: s.decoderImplementation, powerEfficientDecoder: s.powerEfficientDecoder,
          framesDecoded: s.framesDecoded, framesDropped: s.framesDropped, framesPerSecond: s.framesPerSecond,
          frameWidth: s.frameWidth, frameHeight: s.frameHeight, avgDecodeMs: s.framesDecoded ? 1000 * s.totalDecodeTime / s.framesDecoded : null,
          avgJitterBufferMs: s.jitterBufferEmittedCount ? 1000 * s.jitterBufferDelay / s.jitterBufferEmittedCount : null,
          pliCount: s.pliCount, nackCount: s.nackCount, packetsLost: s.packetsLost, codecId: s.codecId });
      }
      if (s.id === pairId) out.rttMs = s.currentRoundTripTime * 1000;
    });
    if (pairId) {
      const p = r.get(pairId), l = r.get(p.localCandidateId), rm = r.get(p.remoteCandidateId);
      out.path = { local: l && `${l.candidateType} ${l.address}:${l.port}`, remote: rm && `${rm.candidateType} ${rm.address}:${rm.port}` };
    }
    if (out.codecId) { const c = r.get(out.codecId); out.codec = c && `${c.mimeType} ${c.sdpFmtpLine || ''}`; }
    return out;
  }

  function grabFrame() {
    const c = document.createElement('canvas');
    c.width = S.video.videoWidth; c.height = S.video.videoHeight;
    c.getContext('2d').drawImage(S.video, 0, 0);
    return c.toDataURL('image/jpeg', 0.8);
  }

  window.spike = { start, waitLive, runProbes, stats, grabFrame, state: S };
})();
