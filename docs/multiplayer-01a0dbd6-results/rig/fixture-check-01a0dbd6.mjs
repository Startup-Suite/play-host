// 01a0dbd6 stage 5: fixture sanity. P1 (jordan) and P2 (ryan) join; P2 holds
// D for 1.5 s, then P1 holds W for 1.5 s; screenshots from P1's page before,
// after P2's input and after P1's input.
import { connect, sleep } from "../cdp.mjs"
const [canvas] = process.argv.slice(2)
const APP = "http://192.168.1.200:4033"
const c = await connect("127.0.0.1:9223")
const st = p => p.eval(`(() => { const el = document.querySelector('[phx-hook=GameStream]'); const q = s => el.querySelector(s); const vis = s => { const x = q(s); return !!x && !x.hidden && x.getClientRects().length > 0 }; return { state: el.dataset.state, leave: vis('[data-gs-leave]'), ring: !q('[data-gs-ring]').hidden, frames: q('video').getVideoPlaybackQuality().totalVideoFrames } })()`)
const waitFor = async (p, f) => { for (let i = 0; i < 80; i++) { const s = await st(p).catch(() => null); if (s && f(s)) return s; await sleep(250) } throw new Error("timeout") }
const click = (p, sel) => p.eval(`document.querySelector('[phx-hook=GameStream] ${sel}').click()`)
const hold = async (p, code, key, vk, ms) => { await p.send("Input.dispatchKeyEvent", { type: "keyDown", code, key, windowsVirtualKeyCode: vk }); await sleep(ms); await p.send("Input.dispatchKeyEvent", { type: "keyUp", code, key, windowsVirtualKeyCode: vk }) }
try {
  const pages = []
  for (const u of ["jordan", "ryan"]) {
    const ctx = await c.newContext()
    const p = await c.newPage("about:blank", { context: ctx })
    await p.goto(`${APP}/dev/login?as=${u}`)
    await p.goto(`${APP}/canvases/${canvas}`)
    await waitFor(p, s => s.state === "live" && s.frames > 5)
    await click(p, "[data-gs-join]")
    await waitFor(p, s => s.leave)
    await click(p, "[data-gs-capture]")
    await waitFor(p, s => s.ring)
    pages.push(p)
  }
  const [P1, P2] = pages
  await sleep(1500)
  await P1.shot("/home/queen/sources/tmp/rig-01a0dbd6/s5/fixture-0-before-01a0dbd6.jpg")
  await hold(P2, "KeyD", "d", 68, 1500)
  await sleep(700)
  await P1.shot("/home/queen/sources/tmp/rig-01a0dbd6/s5/fixture-1-p2-held-d-01a0dbd6.jpg")
  await hold(P1, "KeyW", "w", 87, 1500)
  await sleep(700)
  await P1.shot("/home/queen/sources/tmp/rig-01a0dbd6/s5/fixture-2-p1-held-w-01a0dbd6.jpg")
  console.log("ok")
} finally { await c.closeAll() }
