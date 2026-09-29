// Stage 6 (01a0dbd6): three viewers live (force_relay), 250 ms samplers on
// each; the orchestrator kills the host's runtime socket in core (a link
// blip) and touches /out/blip-go-done; the samplers run on for 30 s. Then J
// reloads (a fresh peer after the resume) and must go live.
import { connect, APP, sleep, log, save, SAMPLER_START, SAMPLER_STOP, analyse } from './rv-lib-01a0dbd6.mjs'
import fs from 'node:fs'
const [canvas, SID] = process.argv.slice(2)
const c = await connect()
const users = { K: 'ryan', O: 'octavia', J: 'jordan' }
const v = {}
const res = { canvas, session: SID }
async function liveNow(x, ms = 30000) { const t = Date.now(); while (Date.now() - t < ms) { const s = await x.state(); if (s && s.state === 'live' && s.framesDecoded > 0) return Date.now() - t; await sleep(250) } return null }
for (const [n, u] of Object.entries(users)) { v[n] = await c.viewer(n, u); await v[n].login(); await v[n].goto(`${APP}/canvases/${canvas}`); res[`${n}_live_ms`] = await liveNow(v[n]); log(n, 'live', res[`${n}_live_ms`]) }
for (const n of Object.keys(v)) await v[n].ev(SAMPLER_START)
await sleep(5000)
fs.writeFileSync('/out/blip-ready', String(Date.now()))
log('READY; waiting for the blip')
while (!fs.existsSync('/out/blip-go-done')) await sleep(100)
res.blip_at = new Date(+fs.readFileSync('/out/blip-go-done', 'utf8')).toISOString()
log('blip done; sampling 30 s')
await sleep(30000)
for (const n of Object.keys(v)) { const s = await v[n].ev(SAMPLER_STOP); res[`${n}_series`] = analyse(s); res[`${n}_states`] = [...new Set(s.map(x => x.state))]; res[`${n}_pcIds`] = [...new Set(s.map(x => x.pcId))] ; log(n, JSON.stringify(res[`${n}_series`]), res[`${n}_states`], 'pcs', res[`${n}_pcIds`]) }
// After the resume: a fresh peer (reload) must still be signalled.
await v.J.goto(`${APP}/canvases/${canvas}`)
res.J_reload_live_ms = await liveNow(v.J)
log('J after reload live in', res.J_reload_live_ms)
res.final = {}
for (const n of Object.keys(v)) res.final[n] = await v[n].state()
save('blip-01a0dbd6.json', res)
await c.closeAll()
