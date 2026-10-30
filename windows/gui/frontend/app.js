'use strict';

// ------------------------------------------------------------------ mock (browser preview only)
const Mock = (() => {
  const listeners = {};
  const st = {
    servers: [
      { id: 'a1', name: 'Amsterdam Reality', provider: 'vless', enabled: true, params: { server: 'nl.example.com', port: 443 } },
      { id: 'b2', name: 'Home WireGuard', provider: 'wireguard', enabled: true, params: { endpoint: 'home.example.net:51820' } },
    ],
    rules: [
      { id: 'r1', match: { kind: 'path', value: 'C:\\Program Files\\Mozilla Firefox\\firefox.exe' }, target: { kind: 'server', serverId: 'a1' }, children: 'inherit', learned: [{ match: { kind: 'path', value: 'C:\\Program Files\\Mozilla Firefox\\crashreporter.exe' }, target: { kind: 'server', serverId: 'a1' } }] },
      { id: 'r2', match: { kind: 'name', value: 'Telegram.exe' }, target: { kind: 'server', serverId: 'b2' }, children: 'ask' },
      { id: 'r3', match: { kind: 'store_package', value: 'Microsoft.WindowsTerminal' }, target: { kind: 'direct' }, children: 'inherit' },
    ],
    profile: { childAnswers: [{ match: { kind: 'path', value: 'C:\Windows\System32\cmd.exe' }, target: { kind: 'direct' }, ruleId: 'r2' }], defaultTarget: { kind: 'direct' }, killSwitch: 'vpn_apps', dns: { mode: 'via_server' }, allowLan: true, ipv6: 'auto', strictRoute: true, rules: [] },
    status: { state: 'connected', servers: [{ id: 'a1', healthy: true, checked: true, rttMs: 48 }, { id: 'b2', healthy: false, checked: true, lastError: 'timeout' }], firewallAvailable: true, pendingAsks: [{ askId: 'q1', ruleId: 'r2', parentPath: 'C:\\Telegram\\Telegram.exe', childPath: 'C:\\Windows\\System32\\cmd.exe', shared: true }] },
  };
  const providers = [
    { id: 'shadowsocks', displayName: 'Shadowsocks (AEAD / 2022)', schema: { required: ['server'], properties: { server: { type: 'string', title: 'Сервер' }, port: { type: 'integer', title: 'Порт', default: 8388 }, method: { type: 'string', title: 'Шифр', enum: ['2022-blake3-aes-128-gcm', 'aes-256-gcm'] }, password: { type: 'string', title: 'Пароль', format: 'password' } } } },
    { id: 'vless', displayName: 'VLESS (Reality / TLS)', schema: { properties: { server: { type: 'string', title: 'Сервер' }, port: { type: 'integer', title: 'Порт', default: 443 } } } },
    { id: 'wireguard', displayName: 'WireGuard', schema: { properties: { endpoint: { type: 'string', title: 'Endpoint' } } } },
  ];
  return {
    on(ev, fn) { (listeners[ev] = listeners[ev] || []).push(fn); },
    async call(method, params) {
      await new Promise(r => setTimeout(r, 80));
      switch (method) {
        case 'servers.list': return st.servers;
        case 'rules.list': return st.rules;
        case 'profile.get': return st.profile;
        case 'engine.status': return st.status;
        case 'providers.list': return providers;
        case 'engine.connect': st.status.state = 'connected'; return st.status;
        case 'engine.disconnect': st.status.state = 'off'; return st.status;
        case 'apps.running': return [{ name: 'chrome.exe', path: 'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe' }, { name: 'svchost.exe', path: 'C:\\Windows\\System32\\svchost.exe', system: true }, { name: 'Code.exe', path: 'C:\\Users\\me\\AppData\\Local\\Programs\\Microsoft VS Code\\Code.exe' }];
        case 'apps.installed': return [{ name: 'Steam', path: 'C:\\Program Files (x86)\\Steam\\steam.exe' }];
        case 'activity.list': return [{ app: 'C:\\Program Files\\Mozilla Firefox\\firefox.exe', target: 'Amsterdam Reality', targetId: 'a1', conns: 14, upload: 120000, download: 5400000, hosts: ['youtube.com', 'googlevideo.com'] }, { app: 'C:\\Windows\\explorer.exe', target: 'direct', conns: 2, upload: 2000, download: 9000, hosts: ['msn.com'] }];
        case 'logs.tail': return 'time=2026-10-30T12:00:00 level=INFO msg="kordo service starting"';
        case 'rules.upsert': { const r = Object.assign({ id: 'r' + Date.now() }, params); const i = st.rules.findIndex(x => x.id === r.id); i >= 0 ? st.rules[i] = r : st.rules.push(r); return r; }
        case 'rules.remove': st.rules = st.rules.filter(r => r.id !== params.id); return true;
        case 'profile.set': st.profile = params.profile; return true;
        case 'killswitch.set': st.profile.killSwitch = params.mode; return st.status;
        case 'servers.test': return { tcpMs: 31, httpMs: 120, via: 'running engine' };
        default: return true;
      }
    },
  };
})();


// ------------------------------------------------------------------ bridge
// In the app, Go is reached through Wails events (makeGoBridge); in a plain browser (design
// preview) a small in-memory mock is used instead.
// Inside WebView2 the Wails runtime (window.runtime) is injected
// asynchronously, so it is awaited in init() before anything is rendered.
const inWebView = !!(window.chrome && window.chrome.webview);
let isWails = false;
let Go = null;
async function waitForBridge() {
  if (!inWebView) return false;
  for (let i = 0; i < 200; i++) {
    if (window.runtime && window.runtime.EventsOn && window.runtime.EventsEmit) return true;
    await new Promise(r => setTimeout(r, 25));
  }
  return false;
}

// Event-based bridge to the Go side (see windows/gui/bridge.go).
function makeGoBridge() {
  let seq = 0;
  const pending = new Map();
  window.runtime.EventsOn("bridge:reply", (raw) => {
    const r = JSON.parse(raw);
    const p = pending.get(r.id);
    if (!p) return;
    pending.delete(r.id);
    r.error ? p.reject(new Error(r.error)) : p.resolve(r.result);
  });
  const call = (fn, ...args) => new Promise((resolve, reject) => {
    const id = ++seq;
    pending.set(id, { resolve, reject });
    window.runtime.EventsEmit("bridge:call", JSON.stringify({ id, fn, args }));
  });
  const names = ["Call", "ServiceOnline", "PickExecutable", "Icons", "GetAutostart", "SetAutostart", "SaveText", "Notify", "ShowWindow"];
  return Object.fromEntries(names.map(n => [n, (...args) => call(n, ...args)]));
}

async function rpc(method, params) {
  if (!isWails) return Mock.call(method, params);
  const raw = await Go.Call(method, params === undefined ? '' : JSON.stringify(params));
  return raw ? JSON.parse(raw) : null;
}
function on(event, fn) {
  if (isWails) window.runtime.EventsOn(event, fn);
  else Mock.on(event, fn);
}
async function icons(paths) {
  if (!isWails || !paths.length) return {};
  try { return await Go.Icons(paths); } catch { return {}; }
}

// ------------------------------------------------------------------ helpers
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const base = (p) => String(p || '').split(/[\\/]/).pop();
const fmtBytes = (n) => n < 1024 ? n + ' Б' : n < 1048576 ? (n / 1024).toFixed(1) + ' КБ' : n < 1073741824 ? (n / 1048576).toFixed(1) + ' МБ' : (n / 1073741824).toFixed(2) + ' ГБ';

function toast(text, kind = '') {
  const el = document.createElement('div');
  el.className = 'toast ' + kind;
  el.textContent = text;
  $('#toasts').appendChild(el);
  setTimeout(() => el.remove(), kind === 'bad' ? 7000 : 3500);
}
async function guard(fn, okText) {
  try {
    const r = await fn();
    if (okText) toast(okText, 'ok');
    return r;
  } catch (e) {
    toast(String(e && e.message || e), 'bad');
    throw e;
  }
}

// ------------------------------------------------------------------ state
const S = {
  online: false,
  view: 'home',
  status: { state: 'off', servers: [] },
  servers: [],
  rules: [],
  profile: null,
  providers: [],
  icons: {},
  appsTab: 'all',
  asks: [],
  activityTimer: null,
  appsFilter: '',
  actOnlyRules: true,
};

const STATE_TEXT = {
  off: ['Отключено', 'Нажмите, чтобы подключиться'],
  connecting: ['Подключение…', 'Запуск туннеля и проверка серверов'],
  connected: ['Подключено', 'Выбранные приложения идут через свои серверы'],
  degraded: ['Нестабильно', 'Часть серверов не отвечает, идёт восстановление'],
  reconnecting: ['Переподключение…', 'Ядро перезапускается'],
  blocked: ['Трафик заблокирован', 'Kill switch удерживает трафик до восстановления связи'],
};
const CHILD_TEXT = { inherit: 'Как родитель', direct: 'Напрямую', ask: 'Спрашивать' };
const KILL_TEXT = { off: 'Выключен', vpn_apps: 'VPN-приложения', all: 'Весь трафик' };

async function loadAll() {
  if (!S.online) return;
  try {
    const [servers, rules, profile, status, providers] = await Promise.all([
      rpc('servers.list'), rpc('rules.list'), rpc('profile.get'), rpc('engine.status'),
      S.providers.length ? Promise.resolve(S.providers) : rpc('providers.list'),
    ]);
    S.servers = servers || [];
    S.rules = rules || [];
    S.profile = profile;
    S.providers = providers || [];
    applyStatus(status);
    const paths = [];
    for (const r of S.rules) if (r.match.kind === 'path') paths.push(r.match.value);
    await loadIcons(paths);
  } catch (e) {
    toast('Не удалось загрузить данные: ' + e, 'bad');
  }
  render();
}

async function loadIcons(paths) {
  const need = paths.filter(p => !(p in S.icons));
  if (!need.length) return;
  Object.assign(S.icons, await icons(need));
}

function applyStatus(st) {
  if (!st) return;
  const next = Object.assign({}, S.status, st);
  // The service omits empty fields, so a missing error/nextTry means "cleared".
  if (!('error' in st)) delete next.error;
  if (!('nextTry' in st)) delete next.nextTry;
  S.status = next;
  if (st.pendingAsks) S.asks = st.pendingAsks;
}

// explainError turns an engine error into something a person can act on.
function explainError(msg) {
  const m = String(msg || '');
  if (/access is denied|отказано в доступе/i.test(m) && /tun/i.test(m))
    return { title: 'Нет прав на создание туннеля', hint: 'Служба должна работать от имени SYSTEM. Переустановите её из консоли администратора: kordo-svc.exe install.' };
  if (/^configuration:/i.test(m))
    return { title: 'Ошибка в настройках', hint: m.replace(/^configuration:\s*/i, ''), go: 'settings' };
  if (/address already in use|only one usage of each socket/i.test(m))
    return { title: 'Порт занят другой программой', hint: 'Закройте другие VPN-клиенты и попробуйте снова.' };
  if (/health check failed/i.test(m))
    return { title: 'Сервер не отвечал', hint: 'Проверьте сервер на вкладке «Серверы».', go: 'servers' };
  return { title: 'Не удалось запустить туннель', hint: m };
}

function serverName(id) {
  const s = S.servers.find(s => s.id === id);
  return s ? s.name : '(удалён)';
}
function targetLabel(t) {
  if (!t) return '';
  if (t.kind === 'server') return serverName(t.serverId);
  return t.kind === 'direct' ? 'Напрямую' : 'Блокировать';
}
function targetKey(t) { return t.kind === 'server' ? 's:' + t.serverId : t.kind; }
function keyTarget(k) { return k.startsWith('s:') ? { kind: 'server', serverId: k.slice(2) } : { kind: k }; }
function targetOptions(selected, { allowBlock = true } = {}) {
  const opts = S.servers.map(s => `<option value="s:${esc(s.id)}" ${selected === 's:' + s.id ? 'selected' : ''}>${esc(s.name)}${s.enabled ? '' : ' (выкл.)'}</option>`);
  opts.push(`<option value="direct" ${selected === 'direct' ? 'selected' : ''}>Напрямую</option>`);
  if (allowBlock) opts.push(`<option value="block" ${selected === 'block' ? 'selected' : ''}>Блокировать</option>`);
  return opts.join('');
}
function appIcon(path, cls = '') {
  const src = S.icons[path];
  return `<div class="app-ico ${cls}">${src ? `<img src="${src}" alt="">` : '▣'}</div>`;
}
function ruleTitle(r) {
  if (r.name) return r.name;
  if (r.match.kind === 'store_package') return r.match.value.split('.').pop() + ' (Store)';
  return base(r.match.value);
}

// ------------------------------------------------------------------ render
function render() {
  $$('#nav a').forEach(a => a.classList.toggle('active', a.dataset.view === S.view));
  const pill = $('#svc-pill');
  pill.className = 'pill ' + (S.online ? 'pill-ok' : 'pill-bad');
  pill.textContent = S.online ? 'служба на связи' : 'служба недоступна';
  const main = $('#main');
  if (!S.online) {
    main.innerHTML = `<h1>Служба Kordo недоступна</h1>
      <p class="sub">Графический интерфейс работает без прав администратора и управляет системной службой.</p>
      <div class="card"><p>Установите и запустите службу от имени администратора:</p>
      <pre class="mono">kordo-svc.exe install</pre>
      <p class="muted small">Окно подключится автоматически, как только служба станет доступна.</p></div>`;
    return;
  }
  ({ home: renderHome, servers: renderServers, apps: renderApps, activity: renderActivity, settings: renderSettings, logs: renderLogs })[S.view](main);
}

function renderHome(main) {
  const st = S.status.state || 'off';
  const [title, sub] = STATE_TEXT[st] || [st, ''];
  const cls = st === 'connected' ? 'on' : (st === 'blocked' ? 'blocked' : (st === 'off' ? '' : 'busy'));
  const routed = S.rules.filter(r => r.target.kind === 'server').length;
  const healthRows = (S.status.servers || []).map(h => {
    const dot = !h.checked ? 'warn' : h.healthy ? 'ok' : 'bad';
    const detail = !h.checked ? 'проверка…' : h.healthy ? `${h.rttMs} мс` : 'не отвечает';
    return `<div class="health" title="${esc(h.lastError || '')}"><span class="dot ${dot}"></span><b>${esc(serverName(h.id))}</b><span class="spacer"></span><span class="muted small">${detail}</span></div>`;
  }).join('');
  const ks = S.status.killSwitch || (S.profile && S.profile.killSwitch) || 'off';
  let banners = '';
  if (S.status.error) {
    const e = explainError(S.status.error);
    banners += `<div class="banner bad"><div class="row"><div style="min-width:0;flex:1"><b>${esc(e.title)}</b>
      <div class="small muted" style="margin-top:2px;user-select:text">${esc(e.hint)}</div></div>
      ${e.go ? `<button class="btn sm" data-go="${e.go}">Открыть</button>` : ''}
      <button class="btn sm" data-go="logs">Журнал</button>
      ${st !== 'connected' ? '<button class="btn sm" id="retry">Повторить</button>' : ''}</div></div>`;
  }
  if (S.status.firewallActive && st === 'off') banners += `<div class="banner warn"><div class="row"><span style="flex:1">Фильтры kill switch остались — интернет может быть заблокирован.</span><button class="btn sm" id="clear-fw">Снять фильтры</button></div></div>`;
  if (!S.servers.length) banners += `<div class="banner info">Начните с добавления сервера: «Серверы» → «Добавить сервер».</div>`;
  const asks = S.asks.map(a => `<div class="health"><span class="dot warn"></span><div style="min-width:0"><b>${esc(base(a.parentPath))}</b> запустил <b>${esc(base(a.childPath))}</b></div><span class="spacer"></span><button class="btn sm" data-ask="${esc(a.askId)}">Решить…</button></div>`).join('');
  main.innerHTML = `
    ${banners}
    <div class="card hero">
      <button class="power ${cls}" id="power" title="${st === 'off' ? 'Подключить' : 'Отключить'}">⏻</button>
      <div>
        <div class="state-title">${esc(title)}</div>
        <div class="muted">${esc(sub)}</div>
        ${S.status.nextTry ? `<div class="muted small">Следующая попытка: ${new Date(S.status.nextTry).toLocaleTimeString()}</div>` : ''}
        <div class="stats">
          <div class="stat"><b>${S.servers.filter(s => s.enabled).length}</b><span class="muted small">серверов</span></div>
          <div class="stat"><b>${routed}</b><span class="muted small">приложений через VPN</span></div>
          <div class="stat"><b>${esc(KILL_TEXT[ks] || ks)}</b><span class="muted small">kill switch</span></div>
          <div class="stat"><b>${esc(targetLabel(S.profile && S.profile.defaultTarget))}</b><span class="muted small">остальные приложения</span></div>
        </div>
      </div>
    </div>
    <div class="grid" style="margin-top:14px">
      <div class="card"><h3>Серверы</h3>${healthRows || '<div class="muted small">Состояние появится после подключения.</div>'}</div>
      ${asks ? `<div class="card"><h3>Ждут решения</h3>${asks}</div>` : ''}
    </div>`;
  $('#power').onclick = () => toggleConnect();
  const cf = $('#clear-fw');
  if (cf) cf.onclick = () => guard(() => rpc('killswitch.clear'), 'Фильтры сняты').then(refreshStatus);
  const rt = $('#retry');
  if (rt) rt.onclick = async () => { try { applyStatus(await rpc('engine.connect')); } catch (e) { toast(String(e.message || e), 'bad'); } refreshStatus(); };
  $$('[data-go]').forEach(b => b.onclick = () => go(b.dataset.go));
  $$('[data-ask]').forEach(b => b.onclick = () => showAsk(S.asks.find(a => a.askId === b.dataset.ask)));
}

async function toggleConnect() {
  const st = S.status.state || 'off';
  try {
    const res = await rpc(st === 'off' ? 'engine.connect' : 'engine.disconnect');
    applyStatus(res);
  } catch (e) {
    toast(String(e.message || e), 'bad');
  }
  await refreshStatus();
}
async function refreshStatus() {
  try { applyStatus(await rpc('engine.status')); } catch { }
  if (S.view === 'home') render();
}

// ------------------------------------------------------------------ servers
function serverHost(s) {
  const p = s.params || {};
  return p.endpoint || (p.server ? `${p.server}:${p.port}` : '');
}
function renderServers(main) {
  const cards = S.servers.map(s => {
    const h = (S.status.servers || []).find(h => h.id === s.id);
    const apps = S.rules.filter(r => r.target.kind === 'server' && r.target.serverId === s.id).length;
    const prov = (S.providers.find(p => p.id === s.provider) || {}).displayName || s.provider;
    const killOn = S.profile && S.profile.killSwitch && S.profile.killSwitch !== 'off';
    let health = '<span class="pill pill-off">не проверен</span>';
    if (h && h.checked) health = h.healthy ? `<span class="pill pill-ok">${h.rttMs} мс</span>` : `<span class="pill pill-bad">нет ответа</span>`;
    return `<div class="card">
      <div class="row"><h3 style="margin:0">${esc(s.name)}</h3><span class="spacer"></span>
        <label class="switch" title="Включён"><input type="checkbox" data-en="${esc(s.id)}" ${s.enabled ? 'checked' : ''}><span></span></label></div>
      <div class="row" style="margin:8px 0"><span class="tag">${esc(prov)}</span><span class="path mono">${esc(serverHost(s))}</span></div>
      <div class="row" style="margin-bottom:12px">${s.enabled ? health : `<span class="pill pill-off">выключен</span><span class="muted small">приложения ${killOn ? 'блокируются' : 'идут напрямую'}</span>`}<span class="muted small" id="test-${esc(s.id)}"></span></div>
      <div class="row">
        <button class="btn sm" data-apps="${esc(s.id)}">Приложения (${apps})</button>
        <button class="btn sm" data-test="${esc(s.id)}">Проверить</button>
        <button class="btn sm ghost" data-edit="${esc(s.id)}">Изменить</button>
        <span class="spacer"></span>
        <button class="x" data-del="${esc(s.id)}" title="Удалить">×</button>
      </div></div>`;
  }).join('');
  main.innerHTML = `
    <div class="row"><div><h1>Серверы</h1><p class="sub">Ваши VLESS, Shadowsocks и WireGuard серверы</p></div><span class="spacer"></span>
      <button class="btn primary" id="add-server">+ Добавить сервер</button></div>
    ${S.servers.length ? `<div class="grid">${cards}</div>` : '<div class="card empty">Серверов пока нет. Вставьте ссылку vless:// или ss://, либо импортируйте WireGuard .conf.</div>'}`;
  $('#add-server').onclick = () => showServerDialog();
  $$('[data-apps]').forEach(b => b.onclick = () => { S.appsTab = 's:' + b.dataset.apps; go('apps'); });
  $$('[data-edit]').forEach(b => b.onclick = () => showServerDialog(S.servers.find(s => s.id === b.dataset.edit)));
  $$('[data-del]').forEach(b => b.onclick = async () => {
    const s = S.servers.find(s => s.id === b.dataset.del);
    if (!confirm(`Удалить сервер «${s.name}»? Правила приложений для него тоже будут удалены.`)) return;
    await guard(() => rpc('servers.remove', { id: s.id }), 'Сервер удалён');
    loadAll();
  });
  $$('[data-en]').forEach(cb => cb.onchange = async () => {
    const cur = S.servers.find(s => s.id === cb.dataset.en);
    if (!cb.checked) {
      const n = S.rules.filter(r => r.target.kind === 'server' && r.target.serverId === cur.id).length;
      const killOn = S.profile && S.profile.killSwitch && S.profile.killSwitch !== 'off';
      const what = killOn ? 'будут заблокированы (включён kill switch)' : 'пойдут напрямую, без VPN';
      if (n && !confirm(`Выключить «${cur.name}»? Его приложения (${n}) ${what}. Туннель переподключится.`)) { cb.checked = true; return; }
    }
    const s = Object.assign({}, cur, { enabled: cb.checked });
    await guard(() => rpc('servers.update', s)).catch(() => { });
    loadAll();
  });
  $$('[data-test]').forEach(b => b.onclick = async () => {
    const out = $('#test-' + b.dataset.test);
    out.textContent = 'проверка…';
    b.disabled = true;
    try {
      const r = await rpc('servers.test', { id: b.dataset.test });
      const parts = [];
      if (r.tcpMs) parts.push(`TCP ${r.tcpMs} мс`);
      if (r.tcpError) parts.push('TCP: ошибка');
      if (r.httpMs) parts.push(`HTTP ${r.httpMs} мс`);
      if (r.httpError) parts.push('HTTP: ' + r.httpError);
      out.textContent = parts.join(' · ') || 'нет данных';
      out.title = [r.tcpError, r.httpError, 'через: ' + r.via].filter(Boolean).join('\n');
    } catch (e) { out.textContent = String(e.message || e); }
    b.disabled = false;
  });
}

function schemaForm(schema, values) {
  const props = (schema && schema.properties) || {};
  return Object.entries(props).map(([key, p]) => {
    const v = values[key] ?? p.default ?? (p.type === 'array' ? [] : '');
    let input;
    if (p.enum) input = `<select data-k="${key}">${p.enum.map(o => `<option ${o === v ? 'selected' : ''} value="${esc(o)}">${esc(o || '—')}</option>`).join('')}</select>`;
    else if (p.type === 'boolean') input = `<label class="switch"><input type="checkbox" data-k="${key}" ${v ? 'checked' : ''}><span></span></label>`;
    else if (p.type === 'integer') input = `<input type="number" data-k="${key}" value="${esc(v)}">`;
    else if (p.type === 'array') input = `<input type="text" data-k="${key}" data-arr="1" value="${esc((v || []).join(', '))}" placeholder="через запятую">`;
    else input = `<input type="${p.format === 'password' ? 'password' : 'text'}" data-k="${key}" value="${esc(v)}">`;
    return `<label>${esc(p.title || key)}${(schema.required || []).includes(key) ? ' *' : ''}</label>${input}`;
  }).join('');
}
function readForm(root, schema) {
  const out = {};
  $$('[data-k]', root).forEach(el => {
    const k = el.dataset.k, p = schema.properties[k];
    if (p.type === 'boolean') { if (el.checked) out[k] = true; return; }
    const v = el.value.trim();
    if (v === '') return;
    if (p.type === 'integer') out[k] = parseInt(v, 10);
    else if (el.dataset.arr) out[k] = v.split(',').map(s => s.trim()).filter(Boolean);
    else out[k] = v;
  });
  return out;
}

function modal(title, body, buttons) {
  const root = $('#modal-root');
  root.innerHTML = `<div class="modal-bg"><div class="modal"><header>${esc(title)}<span class="spacer"></span><button class="x" data-close>×</button></header>
    <div class="body">${body}</div><footer>${buttons}</footer></div></div>`;
  const close = () => { root.innerHTML = ''; };
  $('[data-close]', root).onclick = close;
  $('.modal-bg', root).onclick = (e) => { if (e.target.classList.contains('modal-bg')) close(); };
  return { root: $('.modal', root), close };
}

function showServerDialog(existing) {
  let tab = existing ? 'form' : 'link';
  let provider = existing ? existing.provider : (S.providers[0] || {}).id;
  const draw = () => {
    const prov = S.providers.find(p => p.id === provider) || S.providers[0];
    const schema = prov ? prov.schema : { properties: {} };
    const body = `
      ${existing ? '' : `<div class="tabs"><a data-tab="link" class="${tab === 'link' ? 'active' : ''}">Ссылка или .conf</a><a data-tab="form" class="${tab === 'form' ? 'active' : ''}">Вручную</a></div>`}
      <div class="form-grid" style="margin-bottom:12px"><label>Название</label><input type="text" id="srv-name" value="${esc(existing ? existing.name : '')}" placeholder="необязательно"></div>
      ${tab === 'link' ? `
        <textarea id="srv-link" placeholder="vless://…  ss://…  или содержимое WireGuard .conf"></textarea>
        <div class="row" style="margin-top:8px"><label class="btn sm">Открыть .conf файл<input type="file" id="srv-file" accept=".conf,.txt" hidden></label>
        <span class="muted small">Поддерживаются VLESS (TCP/Reality/TLS, ws, grpc, httpupgrade), Shadowsocks (AEAD, 2022), WireGuard.</span></div>`
      : `<div class="form-grid" style="margin-bottom:12px"><label>Протокол</label><select id="srv-prov" ${existing ? 'disabled' : ''}>${S.providers.map(p => `<option value="${p.id}" ${p.id === provider ? 'selected' : ''}>${esc(p.displayName)}</option>`).join('')}</select></div>
         <div class="form-grid" id="srv-form">${schemaForm(schema, existing ? existing.params : {})}</div>`}
      <div class="err" id="srv-err"></div>`;
    const m = modal(existing ? 'Изменить сервер' : 'Добавить сервер', body, `<button class="btn" data-cancel>Отмена</button><button class="btn primary" id="srv-save">${existing ? 'Сохранить' : 'Добавить'}</button>`);
    $('[data-cancel]', m.root).onclick = m.close;
    $$('[data-tab]', m.root).forEach(a => a.onclick = () => { tab = a.dataset.tab; draw(); });
    const sel = $('#srv-prov', m.root);
    if (sel) sel.onchange = () => { provider = sel.value; draw(); };
    const file = $('#srv-file', m.root);
    if (file) file.onchange = async () => {
      const f = file.files[0];
      if (!f) return;
      $('#srv-link', m.root).value = await f.text();
      if (!$('#srv-name', m.root).value) $('#srv-name', m.root).value = f.name.replace(/\.conf$/i, '');
    };
    $('#srv-save', m.root).onclick = async () => {
      const name = $('#srv-name', m.root).value.trim();
      const err = $('#srv-err', m.root);
      err.textContent = '';
      try {
        if (existing) {
          await rpc('servers.update', Object.assign({}, existing, { name: name || existing.name, params: readForm(m.root, schema) }));
          toast('Сервер сохранён', 'ok');
        } else if (tab === 'link') {
          const link = $('#srv-link', m.root).value.trim();
          if (!link) { err.textContent = 'Вставьте ссылку или содержимое .conf'; return; }
          const s = await rpc('servers.add', { link, name });
          toast(`Добавлен сервер «${s.name}»`, 'ok');
        } else {
          const s = await rpc('servers.add', { provider, name, params: readForm(m.root, schema) });
          toast(`Добавлен сервер «${s.name}»`, 'ok');
        }
        m.close();
        loadAll();
      } catch (e) { err.textContent = String(e.message || e); }
    };
  };
  draw();
}

// ------------------------------------------------------------------ apps
function childDecisions(r) {
  const out = (r.learned || []).map((l, i) => ({ name: base(l.match.value), path: l.match.value, tkey: targetKey(l.target), kind: 'learned', i, ruleId: r.id }));
  (S.profile && S.profile.childAnswers || []).forEach((a, ai) => {
    if (a.ruleId === r.id) out.push({ name: base(a.match.value), path: a.match.value, tkey: 'direct', kind: 'answer', ai, ruleId: r.id, key: (a.match.kind + ':' + a.match.value).toLowerCase() });
  });
  return out;
}

function renderApps(main) {
  const count = (k) => S.rules.filter(r => k === 'all' || targetKey(r.target) === k).length;
  const tabs = [['all', 'Все'], ...S.servers.map(s => ['s:' + s.id, s.name]), ['direct', 'Напрямую'], ['block', 'Блокировать']];
  if (!tabs.some(t => t[0] === S.appsTab)) S.appsTab = 'all';
  const def = S.profile ? S.profile.defaultTarget : { kind: 'direct' };
  const asks = S.asks.map(a => `<div class="ask-row"><span class="dot warn"></span><div style="min-width:0;flex:1"><b>${esc(base(a.parentPath))}</b> запустил <b>${esc(base(a.childPath))}</b>
      ${a.shared ? '<span class="tag">общая программа</span>' : ''}<div class="path">${esc(a.childPath)}</div></div>
      <button class="btn sm" data-ans="${esc(a.askId)}" data-c="direct">Напрямую</button>
      <button class="btn sm primary" data-ans="${esc(a.askId)}" data-c="parent">Как родитель</button></div>`).join('');
  main.innerHTML = `
    <div class="row"><div><h1>Приложения</h1><p class="sub">Остальные приложения: <b>${esc(targetLabel(def))}</b>
      <a class="link small" id="to-settings">изменить</a></p></div><span class="spacer"></span>
      <button class="btn primary" id="add-apps" ${S.servers.length ? '' : 'disabled title="Сначала добавьте сервер"'}>+ Добавить</button></div>
    ${asks ? `<div class="card" style="margin-bottom:14px"><h3>Ждут решения</h3>${asks}</div>` : ''}
    <div class="row" style="margin-bottom:12px">
      <div class="tabs" style="margin:0">${tabs.map(([k, v]) => `<a data-apptab="${esc(k)}" class="${S.appsTab === k ? 'active' : ''}">${esc(v)} <span class="muted">${count(k)}</span></a>`).join('')}</div>
      <span class="spacer"></span><input type="text" id="apps-filter" placeholder="Поиск" value="${esc(S.appsFilter)}" style="width:200px"></div>
    <div class="card" style="padding:4px"><div class="rule rule-head"><span></span><span>Приложение</span><span>Маршрут</span><span>Подпроцессы</span><span></span></div><div id="rule-list"></div></div>
    <div class="dropzone" id="dropzone">Перетащите сюда .exe или ярлык</div>`;
  drawRuleList();
  $('#to-settings').onclick = () => go('settings');
  $('#add-apps').onclick = () => showAppPicker();
  $$('[data-apptab]').forEach(a => a.onclick = () => { S.appsTab = a.dataset.apptab; render(); });
  $('#apps-filter').oninput = (e) => { S.appsFilter = e.target.value; drawRuleList(); };
  $$('[data-ans]').forEach(b => b.onclick = async () => {
    await guard(() => rpc('children.answer', { askId: b.dataset.ans, choice: b.dataset.c, remember: true })).catch(() => { });
    S.asks = S.asks.filter(x => x.askId !== b.dataset.ans);
    loadAll();
  });
}

function drawRuleList() {
  const box = $('#rule-list');
  if (!box) return;
  const f = S.appsFilter.trim().toLowerCase();
  const rules = S.rules.filter(r => (S.appsTab === 'all' || targetKey(r.target) === S.appsTab) &&
    (!f || ruleTitle(r).toLowerCase().includes(f) || r.match.value.toLowerCase().includes(f)));
  box.innerHTML = rules.map(r => {
    const kids = childDecisions(r);
    return `
    <div class="rule">
      ${r.match.kind === 'path' ? appIcon(r.match.value) : '<div class="app-ico">' + (r.match.kind === 'store_package' ? '◰' : '▣') + '</div>'}
      <div style="min-width:0">
        <div class="name">${esc(ruleTitle(r))} ${r.match.kind !== 'path' ? `<span class="tag">${r.match.kind === 'name' ? 'по имени' : 'Store'}</span>` : ''}</div>
        <div class="path" title="${esc(r.match.value)}">${esc(r.match.value)}</div>
        ${kids.length ? `<div class="learned">${kids.map(k => `<span class="chip" title="${esc(k.path)}">↳ ${esc(k.name)} <span class="muted">→</span><select class="chip-sel" title="Куда направить ${esc(k.name)}" data-kid-kind="${k.kind}" data-rule="${esc(k.ruleId)}" data-i="${k.kind === 'learned' ? k.i : k.ai}">${targetOptions(k.tkey)}</select><button title="Забыть" ${k.kind === 'learned' ? `data-unlearn="${esc(k.ruleId)}" data-i="${k.i}"` : `data-forget="${esc(k.key)}"`}>×</button></span>`).join('')}</div>` : ''}
      </div>
      <select data-target="${esc(r.id)}">${targetOptions(targetKey(r.target))}</select>
      <select data-children="${esc(r.id)}" title="Что делать с программами, которые запускает это приложение">
        ${Object.entries(CHILD_TEXT).map(([k, v]) => `<option value="${k}" ${r.children === k ? 'selected' : ''}>${v}</option>`).join('')}
      </select>
      <button class="x" data-rm="${esc(r.id)}" title="Убрать правило">×</button>
    </div>`;
  }).join('') || `<div class="empty">${S.rules.length ? 'Ничего не найдено.' : 'Здесь пока пусто. Нажмите «Добавить» или перетащите .exe в окно.'}</div>`;
  $$('[data-target]', box).forEach(sel => sel.onchange = () => updateRule(sel.dataset.target, { target: keyTarget(sel.value) }));
  $$('[data-children]', box).forEach(sel => sel.onchange = () => updateRule(sel.dataset.children, { children: sel.value }));
  $$('[data-rm]', box).forEach(b => b.onclick = async () => { await guard(() => rpc('rules.remove', { id: b.dataset.rm })).catch(() => { }); loadAll(); });
  $$('[data-unlearn]', box).forEach(b => b.onclick = () => {
    const r = S.rules.find(r => r.id === b.dataset.unlearn);
    updateRule(r.id, { learned: r.learned.filter((_, i) => i !== +b.dataset.i) });
  });
  $$('[data-forget]', box).forEach(b => b.onclick = async () => { await guard(() => rpc('children.forget', { key: b.dataset.forget })).catch(() => { }); loadAll(); });
  // Changing a child's route: learned children are edited in place; a
  // "direct" answer becomes a learned child of the app with the new route.
  $$('.chip-sel', box).forEach(sel => sel.onchange = async () => {
    const r = S.rules.find(r => r.id === sel.dataset.rule);
    const target = keyTarget(sel.value);
    if (sel.dataset.kidKind === 'learned') {
      const learned = r.learned.map((l, i) => i === +sel.dataset.i ? Object.assign({}, l, { target, source: 'user' }) : l);
      return updateRule(r.id, { learned });
    }
    if (target.kind === 'direct') return;
    const a = S.profile.childAnswers[+sel.dataset.i];
    try {
      await rpc('children.forget', { key: (a.match.kind + ':' + a.match.value).toLowerCase() });
      await rpc('rules.upsert', Object.assign({}, r, { learned: [...(r.learned || []), { match: a.match, target, source: 'user' }] }));
    } catch (e) { toast(String(e.message || e), 'bad'); }
    loadAll();
  });
}

async function updateRule(id, patch) {
  const r = Object.assign({}, S.rules.find(r => r.id === id), patch);
  if (patch.learned) r.learned = patch.learned;
  await guard(() => rpc('rules.upsert', r)).catch(() => { });
  loadAll();
}

function defaultTargetKey() {
  if (S.appsTab.startsWith('s:') || S.appsTab === 'direct' || S.appsTab === 'block') return S.appsTab;
  const first = S.servers.find(s => s.enabled) || S.servers[0];
  return first ? 's:' + first.id : 'direct';
}

async function addApps(paths, tKey, children) {
  let added = 0;
  for (const p of paths) {
    try {
      await rpc('rules.upsert', { match: { kind: 'path', value: p }, target: keyTarget(tKey), children });
      added++;
    } catch (e) { toast(`${base(p)}: ${e.message || e}`, 'bad'); }
  }
  if (added) toast(`Добавлено приложений: ${added}`, 'ok');
  loadAll();
}

async function showAppPicker(preselected = []) {
  let tab = preselected.length ? 'files' : 'running';
  let list = [], filter = '';
  const chosen = new Set(preselected);
  const existing = new Set(S.rules.map(r => r.match.value.toLowerCase()));
  const fileList = [...preselected];
  const m = modal('Добавить приложения', `
    <div class="tabs"><a data-ptab="running">Запущенные</a><a data-ptab="installed">Установленные</a><a data-ptab="files">Файлы</a></div>
    <div class="row" style="margin-bottom:10px"><input type="text" id="pick-filter" placeholder="Поиск по имени или пути" style="flex:1"><button class="btn" id="pick-file">Выбрать .exe / ярлык…</button></div>
    <div class="picklist" id="picklist"><div class="empty">Загрузка…</div></div>`,
    `<span class="muted small" id="pick-count"></span><span class="spacer"></span>
     <label class="muted small">Маршрут</label><select id="pick-target">${targetOptions(defaultTargetKey())}</select>
     <label class="muted small">Подпроцессы</label><select id="pick-children">${Object.entries(CHILD_TEXT).map(([k, v]) => `<option value="${k}">${v}</option>`).join('')}</select>
     <button class="btn primary" id="pick-add">Добавить</button>`);
  const drawList = async () => {
    $$('[data-ptab]', m.root).forEach(a => a.classList.toggle('active', a.dataset.ptab === tab));
    const box = $('#picklist', m.root);
    let items;
    if (tab === 'files') items = fileList.map(p => ({ path: p, name: base(p) }));
    else items = list;
    const f = filter.toLowerCase();
    items = items.filter(i => !f || (i.name || '').toLowerCase().includes(f) || (i.path || '').toLowerCase().includes(f));
    await loadIcons(items.slice(0, 200).map(i => i.path));
    box.innerHTML = items.length ? items.slice(0, 400).map(i => {
      const dis = i.system || existing.has(i.path.toLowerCase());
      return `<div class="pick ${dis ? 'disabled' : ''}" data-path="${esc(i.path)}" title="${esc(i.path)}">
        <input type="checkbox" ${chosen.has(i.path) ? 'checked' : ''} ${dis ? 'disabled' : ''}>${appIcon(i.path, 'sm')}
        <div style="min-width:0"><div class="name">${esc(i.name)} ${i.store ? '<span class="tag">Store</span>' : ''} ${i.system ? '<span class="tag">системный</span>' : ''} ${existing.has(i.path.toLowerCase()) ? '<span class="tag">уже добавлено</span>' : ''}</div><div class="path">${esc(i.path)}</div></div></div>`;
    }).join('') : `<div class="empty">${tab === 'files' ? 'Выберите файлы кнопкой выше или перетащите их в окно.' : 'Ничего не найдено.'}</div>`;
    $$('.pick', box).forEach(row => row.onclick = (e) => {
      if (row.classList.contains('disabled')) return;
      const p = row.dataset.path, cb = $('input', row);
      if (e.target !== cb) cb.checked = !cb.checked;
      cb.checked ? chosen.add(p) : chosen.delete(p);
      $('#pick-count', m.root).textContent = chosen.size ? `Выбрано: ${chosen.size}` : '';
    });
    $('#pick-count', m.root).textContent = chosen.size ? `Выбрано: ${chosen.size}` : '';
  };
  const loadTab = async () => {
    list = [];
    $('#picklist', m.root).innerHTML = '<div class="empty">Загрузка…</div>';
    try {
      if (tab === 'running') list = ((await rpc('apps.running')) || []).map(a => ({ path: a.path, name: a.name, store: a.store, system: a.system }));
      if (tab === 'installed') list = ((await rpc('apps.installed')) || []).map(a => ({ path: a.path, name: a.name, store: a.store }));
    } catch (e) { toast(String(e.message || e), 'bad'); }
    drawList();
  };
  $$('[data-ptab]', m.root).forEach(a => a.onclick = () => { tab = a.dataset.ptab; loadTab(); });
  $('#pick-filter', m.root).oninput = (e) => { filter = e.target.value; drawList(); };
  $('#pick-file', m.root).onclick = async () => {
    if (!isWails) return;
    const paths = await Go.PickExecutable();
    for (const p of paths || []) { if (!fileList.includes(p)) fileList.push(p); chosen.add(p); }
    tab = 'files';
    drawList();
  };
  $('#pick-add', m.root).onclick = async () => {
    if (!chosen.size) { toast('Ничего не выбрано'); return; }
    const t = $('#pick-target', m.root).value, ch = $('#pick-children', m.root).value;
    m.close();
    await addApps([...chosen], t, ch);
  };
  pickerAddFiles = (paths) => { for (const p of paths) { if (!fileList.includes(p)) fileList.push(p); chosen.add(p); } tab = 'files'; drawList(); };
  loadTab();
}
let pickerAddFiles = null;

// ------------------------------------------------------------------ activity
function hasRule(path) {
  const p = String(path || '').toLowerCase();
  const b = base(p);
  const hit = (m) => m.kind === 'path' ? m.value.toLowerCase() === p
    : m.kind === 'name' ? m.value.toLowerCase() === b
    : m.kind === 'store_package' ? p.includes('\\windowsapps\\' + m.value.toLowerCase() + '_') : false;
  return S.rules.some(r => hit(r.match) || (r.learned || []).some(l => hit(l.match)));
}

async function renderActivity(main) {
  main.innerHTML = `<div class="row"><div><h1>Активность</h1><p class="sub">Куда сейчас идёт трафик приложений</p></div><span class="spacer"></span>
    <label class="row small muted" style="gap:8px">Только приложения с правилами <label class="switch"><input type="checkbox" id="act-only" ${S.actOnlyRules ? 'checked' : ''}><span></span></label></label></div>
    <div class="card" style="padding:4px"><div id="act" class="empty">Загрузка…</div></div>`;
  $('#act-only').onchange = (e) => { S.actOnlyRules = e.target.checked; draw(); };
  const draw = async () => {
    if (S.view !== 'activity') return;
    let list = [];
    try { list = await rpc('activity.list') || []; } catch (e) { const el = $('#act'); if (el) el.innerHTML = `<div class="empty">${esc(e.message || e)}</div>`; return; }
    if (S.actOnlyRules) list = list.filter(e => e.targetId || e.target === 'block' || hasRule(e.app));
    await loadIcons(list.map(e => e.app));
    const el = $('#act');
    if (!el) return;
    if (!list.length) { el.className = 'empty'; el.innerHTML = S.status.state === 'off' ? 'Подключитесь, чтобы увидеть активность.' : 'Активных соединений нет.'; return; }
    el.className = '';
    el.innerHTML = `<table class="act"><colgroup><col style="width:40px"><col><col style="width:180px"><col style="width:70px"><col style="width:150px"><col style="width:28%"></colgroup>
      <tr><th></th><th>Приложение</th><th>Маршрут</th><th>Соед.</th><th>↑ / ↓</th><th>Хосты</th></tr>
      ${list.map(e => `<tr><td>${appIcon(e.app, 'sm')}</td><td style="min-width:0"><div class="name">${esc(base(e.app))}</div><div class="path" title="${esc(e.app)}">${esc(e.app)}</div></td>
      <td>${e.targetId ? `<span class="pill pill-ok">${esc(e.target)}</span>` : e.target === 'block' ? '<span class="pill pill-bad">блок</span>' : '<span class="pill pill-off">напрямую</span>'}</td>
      <td>${e.conns}</td><td class="small">${fmtBytes(e.upload)} / ${fmtBytes(e.download)}</td><td class="small muted"><div class="hosts" title="${esc((e.hosts || []).join('\n'))}">${esc((e.hosts || []).join(', '))}</div></td></tr>`).join('')}</table>`;
  };
  await draw();
  clearInterval(S.activityTimer);
  S.activityTimer = setInterval(draw, 3000);
}

// ------------------------------------------------------------------ settings
function renderSettings(main) {
  const p = S.profile;
  if (!p) { main.innerHTML = '<div class="empty">Загрузка…</div>'; return; }
  const seg = (name, value, opts) => `<div class="seg" data-seg="${name}">${opts.map(([k, v, t]) => `<button data-v="${esc(k)}" class="${value === k ? 'on' : ''}" title="${esc(t || '')}">${esc(v)}</button>`).join('')}</div>`;
  const sw = (name, value) => `<label class="switch"><input type="checkbox" data-sw="${name}" ${value ? 'checked' : ''}><span></span></label>`;
  const row = (label, hint, control) => `<div class="field"><div><b>${label}</b>${hint ? `<div class="hint">${hint}</div>` : ''}</div><div>${control}</div></div>`;
  const inverse = p.defaultTarget.kind === 'server';
  main.innerHTML = `
    <h1>Настройки</h1><p class="sub">Применяются сразу.</p>
    <div class="card">
      ${row('Через VPN идут', '', seg('mode', inverse ? 'inverse' : 'only', [['only', 'Выбранные', 'Только приложения из списка'], ['inverse', 'Все, кроме выбранных', 'Всё через сервер, кроме приложений из списка']]) +
        (inverse ? `<div style="margin-top:8px" class="row"><span class="muted small">Сервер</span><select id="def-server">${S.servers.map(s => `<option value="${esc(s.id)}" ${p.defaultTarget.serverId === s.id ? 'selected' : ''}>${esc(s.name)}</option>`).join('')}</select></div>` : ''))}
      ${row('Kill switch', 'Если туннель упал', seg('kill', p.killSwitch || 'off', [['off', 'Выкл'], ['vpn_apps', 'VPN-приложения', 'Блокировать приложения, назначенные серверам'], ['all', 'Весь трафик', 'Блокировать всё, кроме локальной сети']]) +
        (S.status.firewallAvailable === false ? '<div class="err small">Брандмауэр недоступен</div>' : ''))}
      ${row('DNS', 'Для всех приложений сразу', seg('dns', p.dns.mode || 'via_server', [['via_server', 'Через VPN', 'DoH через сервер — провайдер не видит запросы'], ['doh_direct', 'DoH напрямую'], ['system', 'Системный']]) +
        `<div class="row" style="margin-top:8px"><input type="text" id="doh" value="${esc(p.dns.doh || '')}" placeholder="1.1.1.1" style="width:130px" title="IP DoH-сервера">
        ${p.dns.mode === 'via_server' || !p.dns.mode ? `<select id="dns-server" title="Через какой сервер"><option value="">сервер: авто</option>${S.servers.map(s => `<option value="${esc(s.id)}" ${p.dns.serverId === s.id ? 'selected' : ''}>${esc(s.name)}</option>`).join('')}</select>` : ''}</div>`)}
      ${row('Локальная сеть мимо VPN', '', sw('allowLan', p.allowLan))}
      ${row('IPv6', '', seg('ipv6', p.ipv6 || 'auto', [['auto', 'Авто', 'Блокируется, если у провайдера нет IPv6'], ['block', 'Блокировать']]))}
      ${row('Подключаться при запуске Windows', '', sw('connectOnStart', p.connectOnStart))}
      ${row('Открывать окно при входе', '', '<label class="switch"><input type="checkbox" id="gui-auto"><span></span></label>')}
    </div>
    <details class="card" style="margin-top:14px"><summary class="muted">Дополнительно</summary>
      ${row('Строгая маршрутизация', 'Защита от утечек DNS', sw('strictRoute', p.strictRoute))}
      ${row('Стек TUN', '', seg('stack', p.stack || 'mixed', [['mixed', 'mixed'], ['system', 'system'], ['gvisor', 'gvisor']]))}
      ${row('Фильтры брандмауэра', 'Если пропал интернет после сбоя', '<button class="btn danger" id="fw-clear">Снять все</button>')}
    </details>`;
  const save = async (mut) => {
    const np = JSON.parse(JSON.stringify(S.profile));
    mut(np);
    await guard(() => rpc('profile.set', { profile: np }), 'Сохранено').catch(() => { });
    loadAll();
  };
  $$('[data-seg]').forEach(seg => $$('button', seg).forEach(b => b.onclick = () => {
    const v = b.dataset.v;
    switch (seg.dataset.seg) {
      case 'mode': save(np => {
        if (v === 'only') np.defaultTarget = { kind: 'direct' };
        else { const s = S.servers.find(s => s.enabled) || S.servers[0]; if (!s) { toast('Сначала добавьте сервер', 'bad'); return; } np.defaultTarget = { kind: 'server', serverId: s.id }; }
      }); break;
      case 'kill': guard(() => rpc('killswitch.set', { mode: v }), 'Kill switch: ' + KILL_TEXT[v]).then(loadAll).catch(() => { }); break;
      case 'dns': save(np => { np.dns.mode = v; }); break;
      case 'ipv6': save(np => { np.ipv6 = v; }); break;
      case 'stack': save(np => { np.stack = v; }); break;
    }
  }));
  $$('[data-sw]').forEach(cb => cb.onchange = () => save(np => { np[cb.dataset.sw] = cb.checked; }));
  const ds = $('#def-server');
  if (ds) ds.onchange = () => save(np => { np.defaultTarget = { kind: 'server', serverId: ds.value }; });
  const dsv = $('#dns-server');
  if (dsv) dsv.onchange = () => save(np => { np.dns.serverId = dsv.value; });
  $('#doh').onchange = (e) => save(np => { np.dns.doh = e.target.value.trim(); });
  $('#fw-clear').onclick = () => guard(() => rpc('killswitch.clear'), 'Фильтры сняты').then(refreshStatus);
  const ga = $('#gui-auto');
  if (isWails) { Go.GetAutostart().then(v => ga.checked = v); ga.onchange = () => guard(() => Go.SetAutostart(ga.checked), 'Сохранено'); }
}

// ------------------------------------------------------------------ logs
async function renderLogs(main) {
  let file = renderLogs.file || 'service';
  main.innerHTML = `<div class="row" style="margin-bottom:14px"><h1 style="margin:0">Журнал</h1><span class="spacer"></span>
    <div class="seg"><button data-f="service" class="${file === 'service' ? 'on' : ''}">Служба</button><button data-f="core" class="${file === 'core' ? 'on' : ''}">Ядро</button></div>
    <label class="row small muted" style="gap:8px" title="Ядро пишет каждое соединение. Замедляет сеть — включайте только для диагностики.">Подробно <label class="switch"><input type="checkbox" id="verbose" ${S.profile && S.profile.verboseLog ? 'checked' : ''}><span></span></label></label>
    <button class="btn" id="log-refresh">Обновить</button><button class="btn" id="diag">Диагностика</button></div>
    <pre class="log mono" id="log">Загрузка…</pre>`;
  const load = async () => {
    try {
      const t = await rpc('logs.tail', { lines: 800, file });
      const el = $('#log');
      el.textContent = t || '(пусто)';
      el.scrollTop = el.scrollHeight;
    } catch (e) { $('#log').textContent = String(e.message || e); }
  };
  $$('[data-f]').forEach(b => b.onclick = () => { renderLogs.file = b.dataset.f; renderLogs(main); });
  $('#log-refresh').onclick = load;
  $('#verbose').onchange = async (e) => {
    const np = JSON.parse(JSON.stringify(S.profile));
    np.verboseLog = e.target.checked;
    await guard(() => rpc('profile.set', { profile: np }), e.target.checked ? 'Подробный журнал включён' : 'Подробный журнал выключен').catch(() => { });
    await loadAll();
  };
  $('#diag').onclick = async () => {
    const d = await guard(() => rpc('diagnostics.collect'));
    const text = JSON.stringify(d, null, 2);
    if (isWails) await guard(() => Go.SaveText('kordo-diagnostics.json', text), 'Сохранено');
    else console.log(text);
  };
  load();
}

// ------------------------------------------------------------------ child asks
function showAsk(a) {
  if (!a) return;
  const m = modal('Новый подпроцесс', `
    <p><b>${esc(base(a.parentPath))}</b> запустил <b>${esc(base(a.childPath))}</b>.</p>
    <p class="path">${esc(a.childPath)}</p>
    ${a.shared ? `<div class="banner warn">${esc(base(a.childPath))} — общая системная программа. Если пустить её через VPN, через VPN пойдут <b>все</b> её экземпляры в системе.</div>` : ''}
    <p class="muted small">Пока вы не ответили, он работает по правилу «${esc(a.shared ? 'напрямую' : 'как родитель')}». Решение появится под приложением во вкладке «Приложения», там его можно отменить.</p>`,
    `<button class="btn" data-c="direct">Напрямую</button><button class="btn primary" data-c="parent">Как родитель (${esc(ruleTargetName(a.ruleId))})</button>`);
  $$('[data-c]', m.root).forEach(b => b.onclick = async () => {
    m.close();
    await guard(() => rpc('children.answer', { askId: a.askId, choice: b.dataset.c, remember: true })).catch(() => { });
    S.asks = S.asks.filter(x => x.askId !== a.askId);
    loadAll();
  });
}
function ruleTargetName(ruleId) {
  const r = S.rules.find(r => r.id === ruleId);
  return r ? targetLabel(r.target) : '?';
}

// ------------------------------------------------------------------ navigation & events
function go(view) {
  S.view = view;
  if (view !== 'activity') clearInterval(S.activityTimer);
  render();
}
$$('#nav a').forEach(a => a.onclick = () => go(a.dataset.view));

function bindEvents() {
  on('service:online', (v) => { S.online = !!v; if (S.online) loadAll(); else render(); });
  on('rpc:state.changed', (raw) => {
    const n = typeof raw === 'string' ? JSON.parse(raw) : raw;
    const st = Object.assign({}, n.status, { killSwitch: n.killSwitch });
    applyStatus(st);
    if (S.view === 'home' || S.view === 'servers') render();
  });
  on('rpc:data.changed', () => loadAll());
  on('rpc:child.ask', (raw) => {
    const a = typeof raw === 'string' ? JSON.parse(raw) : raw;
    S.asks.push(a);
    if (isWails) Go.Notify('Kordo', `${base(a.parentPath)} запустил ${base(a.childPath)}`);
    if (!$('#modal-root').innerHTML) showAsk(a);
    if (S.view === 'home') render();
  });
  on('files:dropped', (paths) => {
    if (!paths || !paths.length) { toast('Можно добавлять только .exe и ярлыки на них'); return; }
    if ($('#modal-root').innerHTML && pickerAddFiles) { pickerAddFiles(paths); return; }
    if (!S.servers.length) { toast('Сначала добавьте сервер', 'bad'); return; }
    go('apps');
    showAppPicker(paths);
  });
}

(async function init() {
  isWails = await waitForBridge();
  if (isWails) Go = makeGoBridge();
  bindEvents();
  if (isWails) {
    S.online = await Go.ServiceOnline();
    if (S.online) await loadAll(); else render();
    // events emitted before bindEvents() are lost: reconcile periodically
    setInterval(async () => {
      let v = false;
      try { v = await Go.ServiceOnline(); } catch { }
      if (v !== S.online) { S.online = v; if (v) loadAll(); else render(); }
    }, 1500);
  } else if (inWebView) {
    // bridge missing: never show mock data inside the app
    $("#main").innerHTML = "<h1>Не удалось инициализировать интерфейс</h1><p class=\"sub\">Мост Wails недоступен. Перезапустите Kordo.</p>";
  } else {
    S.online = true;
    await loadAll();
  }
})();

