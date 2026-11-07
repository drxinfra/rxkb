// Сверка rxkb с сайтом drxinfra.ru: одни и те же входы уходят в инструменты MCP и в страницы
// сайта, открытые в headless Chrome, и ответы сравниваются. Проверяет всё, что сервер повторяет
// за браузером: разбор строки лога, поиск, сайзинг (вместе со ссылкой на расчёт), железо под ИИ,
// самооценку безопасности и чек-лист обновления.
//
//   node hack/parity/site-check.mjs --mcp http://127.0.0.1:8790/mcp --site https://drxinfra.ru/
//
// --site может указывать и на локальную копию сайта: python3 -m http.server 8765 в папке landing,
// тогда --site http://127.0.0.1:8765/. Chrome: переменная CHROME или путь по умолчанию на macOS.
// Код возврата 1, если хоть одно расхождение.
import { spawn } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

const arg = (name, def) => { const i = process.argv.indexOf(name); return i > 0 ? process.argv[i + 1] : def; };
const MCP = arg('--mcp', 'http://127.0.0.1:8790/mcp');
const SITE = arg('--site', 'https://drxinfra.ru/').replace(/\/?$/, '/');
const ONLY = arg('--only', '');
const CH = process.env.CHROME || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';

let rpcId = 0;
async function call(tool, args) {
  const r = await fetch(MCP, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream', 'Mcp-Protocol-Version': '2025-11-25' },
    body: JSON.stringify({ jsonrpc: '2.0', id: ++rpcId, method: 'tools/call', params: { name: tool, arguments: args } }),
  });
  if (r.status === 429) { await new Promise(res => setTimeout(res, 1500)); return call(tool, args); }
  const j = await r.json();
  if (j.error) throw new Error(tool + ': ' + JSON.stringify(j.error));
  if (j.result.isError) throw new Error(tool + ': ' + j.result.content[0].text);
  return j.result.structuredContent;
}

// ---------------------------------------------------------------- Chrome
const prof = fs.mkdtempSync(path.join(os.tmpdir(), 'rxkb-parity-'));
const chrome = spawn(CH, ['--headless=new', '--remote-debugging-port=9334', '--user-data-dir=' + prof, '--disable-gpu', 'about:blank'], { stdio: 'ignore' });
let ws;
for (let i = 0; i < 40 && !ws; i++) {
  await new Promise(r => setTimeout(r, 250));
  try {
    const list = await (await fetch('http://127.0.0.1:9334/json')).json();
    const page = list.find(t => t.type === 'page');
    if (page) { ws = new WebSocket(page.webSocketDebuggerUrl); await new Promise(r => ws.onopen = r); }
  } catch { /* Chrome ещё стартует */ }
}
if (!ws) { console.error('Chrome не запустился: ' + CH); process.exit(2); }
let id = 0; const pend = new Map();
ws.onmessage = e => { const m = JSON.parse(e.data); if (m.id && pend.has(m.id)) { pend.get(m.id)(m.result || m.error); pend.delete(m.id); } };
const send = (method, params = {}) => new Promise(r => { const i = ++id; pend.set(i, r); ws.send(JSON.stringify({ id: i, method, params })); });
await send('Page.enable'); await send('Runtime.enable');
const sleep = ms => new Promise(r => setTimeout(r, ms));
// Ссылки rxkb ведут на https://drxinfra.ru/; при проверке на локальной копии сайта они открываются там.
const local = u => u.replace('https://drxinfra.ru/', SITE);
async function open(url, wait = 400) {
  await send('Page.navigate', { url: 'about:blank' });
  await send('Page.navigate', { url: local(url) });
  await sleep(wait);
}
async function evaluate(expr) {
  const r = await send('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true });
  if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
  return r.result.value;
}
async function until(expr, ms = 5000) {
  for (let t = 0; t < ms; t += 100) { const v = await evaluate(expr); if (v) return v; await sleep(100); }
  return evaluate(expr);
}

let bad = 0, ok = 0;
function check(name, a, b) {
  const sa = JSON.stringify(a), sb = JSON.stringify(b);
  if (sa === sb) { ok++; return; }
  bad++;
  console.log('РАСХОЖДЕНИЕ ' + name + '\n  rxkb: ' + sa + '\n  сайт: ' + sb);
}
const want = s => !ONLY || ONLY.split(',').includes(s);

// ---------------------------------------------------------------- разбор строки лога
if (want('errors')) {
  const texts = [
    'FATAL: sorry, too many clients already',
    '2026-11-07 10:01:02 [Error] Npgsql.PostgresException (0x80004005): 53300: sorry, too many clients already',
    'ERROR:  deadlock detected\nDETAIL:  Process 1234 waits for ShareLock on transaction 5678',
    'RabbitMQ.Client.Exceptions.BrokerUnreachableException: None of the specified endpoints were reachable',
    'index [rx_documents] blocked by: [TOO_MANY_REQUESTS/12/disk usage exceeded flood-stage watermark, index has read-only-allow-delete block]',
    'No space left on device',
    'could not write to file "pg_wal/xlogtemp.123": No space left on device',
    'Back-off restarting failed container webserver in pod rx-webserver-7c9 CrashLoopBackOff',
    'OOMKilled exit code 137',
    'Kerberos: Clock skew too great',
    'upstream timed out (110: Connection timed out) while reading response header from upstream',
    'javax.net.ssl.SSLHandshakeException: PKIX path building failed: unable to find valid certification path to requested target',
    'Не удалось установить блокировку',
    'сервис индексирования не отвечает, поиск не находит документы',
    'совсем непонятная строка без ошибок 12345',
  ];
  for (const t of texts) {
    let mine = [];
    try { mine = (await call('find_error', { text: t, limit: 6 })).matches.map(m => m.id + '.html'); } catch (e) { mine = ['ERR ' + e.message]; }
    await open(SITE + 'errors/#' + encodeURIComponent(t), 300);
    const theirs = await until(`(() => { const a = [...document.querySelectorAll('#errout a.hit')]; return a.length || document.querySelector('#errout p') || !document.getElementById('errq') ? a.map(x => x.getAttribute('href')) : null; })()`, 3000) || [];
    check('find_error «' + t.slice(0, 50) + '»', mine, theirs);
  }
}

// ---------------------------------------------------------------- поиск
if (want('search')) {
  const queries = ['очередь растёт', 'pg_wal', 'сертификат', 'Kerberos', 'перцентили', 'бэкап восстановление', 'Elasticsearch куча', 'обновление RX',
    'RabbitMQ TLS', 'Keycloak', 'сайзинг', 'Astra Linux', 'ИИ помощник', 'kubernetes', 'журналы vector', 'блокировки', 'deadlock', 'мониторинг алерты',
    'nginx', 'простой', 'MinIO', 'Ario', 'безопасность', 'WAL архив', 'jmeter нагрузочное'];
  for (const q of queries) {
    const mine = (await call('search_site', { query: q, limit: 20 })).results.map(r => r.url);
    await open(SITE + 'search.html?q=' + encodeURIComponent(q), 300);
    const theirs = await until(`(() => { const info = document.getElementById('sq-info'); if (!info || /Загружаем/.test(info.textContent)) return null;
      return [...document.querySelectorAll('#sq-out li > a')].slice(0, 20).map(a => a.href); })()`, 8000) || [];
    check('search_site «' + q + '»', mine, theirs.map(u => u.replace(SITE, 'https://drxinfra.ru/')));
  }
}

// ---------------------------------------------------------------- сайзинг RX
if (want('sizing')) {
  const cases = [{}, { users: 100, docsYear: 15000, store: 'fs', fts: false }, { users: 500, docsYear: 100000 },
    { users: 2000, docsYear: 400000, mobile: 200, ha: true, place: 'k8s', k8s: 'ns' },
    { users: 10000, docsYear: 2000000, mobile: 1000, ha: true, place: 'k8s', k8s: 'full', preprod: true, preprodLikeProd: true, dcs: true },
    { users: 50, os: 'windows', db: 'mssql', test: false, backup: false }, { users: 800, active: 400, ario: true, arioPages: 1200, years: 7, logDays: 90 },
    { users: 3500, ha: true, store: 'fs', historyGB: 1500.5, docKB: 2048, ver: '4.12' },
    { users: 300, place: 'k8s', k8s: 'full', testInCluster: false, monitoring: false, pgKind: 'vanilla' },
    { users: 120, mobile: 60, preprod: true, years: 3, logDays: 30, ver: '25.2' }];
  for (const c of cases) {
    const r = await call('size_rx', c);
    await open(r.url, 500);
    const theirs = await evaluate(`[...document.querySelectorAll('#calc-out .ctr')].map(ct => ({
      title: ct.querySelector('.ttl').textContent,
      totals: [...ct.querySelectorAll('.tots b')].map(b => b.textContent),
      rows: [...ct.querySelectorAll('tbody tr')].filter(tr => tr.querySelector('.role')).map(tr => [tr.querySelector('.role').textContent, ...[...tr.querySelectorAll('td.n')].map(td => td.firstChild ? td.firstChild.textContent : '')])
    }))`);
    const fmt = gb => gb >= 1000 ? (Math.round(gb / 100) / 10).toString().replace('.', ',') + ' ТБ' : gb + ' ГБ';
    const mine = r.contours.map(ct => {
      const rows = ct.nodes.filter(n => !n.k8s).map(n => n.storageOnly ? [n.role, 'том', '–', '–'] : [n.role, String(n.n), String(n.cpu), String(n.ram)]);
      const k8s = ct.nodes.filter(n => n.k8s);
      if (ct.k8sQuota) rows.push(['Квота неймспейса', '–', String(ct.k8sQuota.reqCpu), String(ct.k8sQuota.reqRam)]);
      for (const n of k8s) rows.push([n.role, n.n > 1 ? String(n.n) : '–', String(n.cpu), String(n.ram)]);
      if (ct.backupGB) rows.push(['Резервные копии', 'том', '–', '–']);
      return { title: ct.title, totals: [String(ct.totals.vms), String(ct.totals.cpu), String(ct.totals.ram), fmt(ct.totals.disk)], rows };
    });
    check('size_rx ' + JSON.stringify(c), mine, theirs);
  }
}

// ---------------------------------------------------------------- железо под ИИ
if (want('ai')) {
  const cases = [{}, { users: 1000, share: 30, qph: 3 }, { model: 'qwen3-30b-a3b' }, { model: 'qwen3-4b', toolset: 'pilot', kv: 'q8_0' },
    { model: 'qwen3-8b', hw: 'm1pro', users: 50 }, { model: 'gigachat31', target: 20, users: 5000, share: 10 }, { model: 'qwen3-8b', kv: 'q8_0', target: 240 },
    { model: 'qwen3-30b-a3b', hw: 'm1pro' }];
  const n0 = v => v == null ? '—' : Math.round(v).toLocaleString('ru-RU');
  const n1 = v => v == null ? '—' : (Math.round(v * 10) / 10).toLocaleString('ru-RU');
  const sec = v => v == null ? '—' : v < 90 ? n0(v) + ' с' : n1(v / 60) + ' мин';
  for (const c of cases) {
    const r = await call('size_ai', c);
    const i = r.input;
    await open(SITE + 'ai-sizing.html', 600);
    const theirs = await evaluate(`(() => { const f = document.getElementById('ais-form');
      f.elements.model.value = ${JSON.stringify(i.model)}; f.elements.hw.value = ${JSON.stringify(i.hw)};
      for (const [k, v] of Object.entries(${JSON.stringify({ toolset: i.toolset, kv: i.kv })})) [...f.elements[k]].forEach(x => x.checked = x.value === v);
      for (const [k, v] of Object.entries(${JSON.stringify({ users: i.users, share: i.share, qph: i.qph, target: i.target })})) f.elements[k].value = v;
      f.dispatchEvent(new Event('change'));
      const v = [...document.querySelectorAll('#ais-out .sum-i .v')].map(e => e.textContent);
      const rows = [...document.querySelectorAll('#ais-out table.rt')][0];
      return { kpi: v, table: rows ? [...rows.querySelectorAll('tbody tr')].map(tr => [...tr.querySelectorAll('td')].slice(1, 6).map(td => td.textContent)) : [] }; })()`);
    const p = r.plan;
    const mine = {
      kpi: p ? [String(p.servers), p.ram_per_server_gb + ' ГБ', sec(p.answer_p50_sec), p.first_answer_after_restart_sec ? sec(p.first_answer_after_restart_sec) : '—'] : [],
      table: p ? r.all_models_on_this_server.map(m => [m.passed, m.answer_p50_sec ? sec(m.answer_p50_sec) : '—', m.answers_per_hour_per_server ? n0(m.answers_per_hour_per_server) : '—', m.servers ? String(m.servers) : '—', m.ram_per_server_gb ? m.ram_per_server_gb + ' ГБ' : '—']) : [],
    };
    check('size_ai ' + JSON.stringify(c), mine, theirs);
  }
}

// ---------------------------------------------------------------- самооценка безопасности
if (want('security')) {
  const qs = (await call('security_self_check', {})).questions.map(q => q.id);
  const vals = ['y', 'p', 'n', 'u', '-'];
  let seed = 7;
  const rnd = () => (seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648;
  const cases = ['y'.repeat(qs.length), 'n'.repeat(qs.length), 'yyyyyppppp', 'yyyyypnunp-y-y', 'u', 'yyyyyyyyyyyyyyyyyypppp'];
  for (let k = 0; k < 8; k++) cases.push(qs.map(() => vals[Math.floor(rnd() * vals.length)]).join(''));
  for (const s of cases) {
    const answers = {};
    s.split('').forEach((c, i) => { if (c !== '-' && qs[i]) answers[qs[i]] = c; });
    const r = await call('security_self_check', { answers });
    await open(r.url, 300);
    const theirs = await evaluate(`[document.getElementById('sec-score').textContent, document.getElementById('sec-zone').textContent, [...document.querySelectorAll('#sec-gaps li b')].map(b => b.textContent).length]`);
    const zones = { red: 'Красная зона: начинать сейчас', yellow: 'Жёлтая зона: есть заметные дыры', green: 'Зелёная зона: база крепкая' };
    check('security_self_check ' + s, [r.score == null ? '—' : String(r.score), r.zone ? zones[r.zone] : 'Ответьте на вопросы слева', (r.gaps || []).length], theirs);
  }
}

// ---------------------------------------------------------------- чек-лист обновления
if (want('upgrade')) {
  const cases = [{ done: [] }, { done: [1, 2, 3, 8, 9], from_version: '25.2', to_version: '26.2' }, { done: Array.from({ length: 44 }, (_, i) => i + 1) },
    { done: [4, 44, 20], window: 'суббота 22:00–06:00' }, { done: [1], from_version: '4.12 <b>"x"</b>' }];
  for (const c of cases) {
    const r = await call('upgrade_checklist', c);
    await open(r.url, 300);
    const theirs = await evaluate(`[document.getElementById('ck-pct').textContent, document.getElementById('ck-done').textContent,
      [...document.querySelectorAll('#ck-form .ck input')].map((b, i) => b.checked ? i + 1 : 0).filter(Boolean),
      ['from', 'to', 'when'].map(k => document.getElementById('ck-form').elements[k].value)]`);
    const doneText = r.done === r.total ? 'Всё отмечено. Запишите, что пошло не по плану.' : 'Отмечено ' + r.done + ' из ' + r.total;
    const clean = s => String(s || '').replace(/[<>"]/g, '').slice(0, 40).trim();
    check('upgrade_checklist ' + JSON.stringify(c), [r.percent + ' %', doneText, [...new Set(c.done)].sort((a, b) => a - b), [clean(c.from_version), clean(c.to_version), clean(c.window)]], theirs);
  }
}

ws.close(); chrome.kill();
await sleep(500);
try { fs.rmSync(prof, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 }); } catch { /* профиль Chrome уберёт система */ }
console.log((bad ? 'расхождений ' + bad : 'расхождений нет') + ', совпало ' + ok);
process.exit(bad ? 1 : 0);
