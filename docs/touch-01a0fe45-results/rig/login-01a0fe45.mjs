// Signs in each named dev user once (creates the user and its general membership).
const cdp = '127.0.0.1:9245', app = 'http://192.168.1.200:4034';
const ver = await (await fetch(`http://${cdp}/json/version`)).json();
const ws = new WebSocket(ver.webSocketDebuggerUrl.replace(/ws:\/\/[^/]+/, `ws://${cdp}`));
await new Promise(r => (ws.onopen = r));
let id = 0; const pend = new Map();
ws.onmessage = m => { const d = JSON.parse(m.data); if (d.id && pend.has(d.id)) { pend.get(d.id)(d); pend.delete(d.id); } };
const send = (method, params = {}, sessionId) => new Promise(r => { const i = ++id; pend.set(i, r); ws.send(JSON.stringify({ id: i, method, params, sessionId })); });
for (const u of process.argv.slice(2)) {
  const { result: { browserContextId } } = await send('Target.createBrowserContext');
  const { result: { targetId } } = await send('Target.createTarget', { url: `${app}/dev/login?as=${u}`, browserContextId });
  await new Promise(r => setTimeout(r, 2500));
  const t = (await send('Target.getTargets')).result.targetInfos.find(x => x.targetId === targetId);
  console.log(u, t && t.url);
  await send('Target.disposeBrowserContext', { browserContextId });
}
ws.close();
