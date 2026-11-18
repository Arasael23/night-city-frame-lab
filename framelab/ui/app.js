/* Night City Frame Lab - window logic (also used, read-only, inside exported reports). */
'use strict';

const REPORT = window.FL_REPORT || null;
const TOKEN = new URLSearchParams(location.search).get('t') || '';
const $ = (s, el) => (el || document).querySelector(s);
const $$ = (s, el) => Array.from((el || document).querySelectorAll(s));
const esc = (v) => String(v == null ? '' : v).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const css = (n) => getComputedStyle(document.documentElement).getPropertyValue(n).trim();

const GROUPS = {
  shader: { label: 'shader / RT pipeline build', color: '--c-shader' },
  world: { label: 'world streaming + entity spawns', color: '--c-world' },
  redscript: { label: 'REDscript mods', color: '--c-redscript' },
  cet: { label: 'CET Lua mods', color: '--c-cet' },
  gpu: { label: 'GPU bound (heavy frame)', color: '--c-gpu' },
  vram: { label: 'VRAM over budget', color: '--c-vram' },
  disk: { label: 'heavy streaming (lots of data read)', color: '--c-disk' },
  ram: { label: 'system RAM full', color: '--c-ram' },
  driver: { label: 'driver (interrupts / DPCs)', color: '--c-driver' },
  paging: { label: 'game memory read back from disk', color: '--c-paging' },
  reset: { label: 'screen reset', color: '--c-reset' },
  menu: { label: 'a game menu opened or closed', color: '--c-menu' },
  unexplained: { label: 'not explained', color: '--c-unexplained' },
  other: { label: 'GPU memory', color: '--c-unexplained' },
};
const gcol = (g) => css((GROUPS[g] || GROUPS.unexplained).color);
// 2.5: the session keys can be changed in Settings. KEYS: the current ones; repKeys: the ones a recorded session used.
const KEYS = () => (S.state && S.state.keys) || { felt: 'F10', mark: 'F11' };
const repKeys = (r) => ({ felt: (r && r.meta && r.meta.felt_key) || 'F10', mark: (r && r.meta && r.meta.mark_key) || 'F11' });

const S = { state: null, sessions: [], cur: null, curId: null, spikeSel: null, tab: 'start', charts: [], modSort: 'score', modFilter: '', cmpIds: null, cmpData: {}, settingsDirty: false };

async function api(path, body) {
  const opt = { method: body ? 'POST' : 'GET', headers: { 'X-Token': TOKEN } };
  if (body) { opt.headers['Content-Type'] = 'application/json'; opt.body = JSON.stringify(body); }
  const r = await fetch('/api/' + path, opt);
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || ('HTTP ' + r.status));
  return j;
}

function toast(msg, err) {
  const t = $('#toast');
  t.textContent = msg;
  t.className = 'toast' + (err ? ' err' : '');
  clearTimeout(toast.h);
  toast.h = setTimeout(() => t.classList.add('hidden'), err ? 7000 : 3500);
}

// a question with Cancel and OK. Esc or a click outside the box is Cancel; Enter in a text field in it is OK.
function confirmBox(html, yes) {
  const m = $('#modal');
  $('#modal-text').innerHTML = html;
  m.classList.remove('hidden');
  const close = () => { m.classList.add('hidden'); document.removeEventListener('keydown', key, true); };
  const ok = () => { close(); yes(); };
  const key = (e) => {
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(); }
    else if (e.key === 'Enter' && e.target && e.target.tagName === 'INPUT' && m.contains(e.target)) { e.preventDefault(); ok(); }
  };
  document.addEventListener('keydown', key, true);
  $('#modal-yes').onclick = ok;
  $('#modal-no').onclick = close;
  m.onclick = (e) => { if (e.target === m) close(); };
  const inp = $('input', m);
  if (inp) { inp.focus(); inp.select(); } else $('#modal-no').focus();
}

const fmt = {
  ms: (v) => v == null || v < 0 ? '–' : (v >= 100 ? v.toFixed(0) : v.toFixed(1)) + ' ms',
  fps: (v) => !v || !isFinite(v) ? '–' : v.toFixed(0) + ' fps',
  t: (s) => { if (s == null) return '–'; const m = Math.floor(s / 60), x = s - m * 60; return m + ':' + (x < 10 ? '0' : '') + x.toFixed(1); },
  dur: (s) => { s = Math.round(s || 0); const m = Math.floor(s / 60); return m ? m + ' min ' + (s % 60) + ' s' : s + ' s'; },
  mb: (v) => v == null ? '–' : (v >= 1024 ? (v / 1024).toFixed(1) + ' GB' : v.toFixed(0) + ' MB'),
  n: (v) => v == null ? '–' : Number(v).toLocaleString('en-US'),
  p: (v, d = 1) => v == null ? '–' : Number(v).toFixed(d),
};

/* ------------------------------------------------------------------ pages (2.5)
   Frame Lab 2.5: Start here, Record, Sessions, Compare, Mods, PC, Settings.
   Frame Lab 2.5E (Experimental) adds Optimise and Experiments. A page can hold more than one view
   (Mods: check, conflicts, cost while playing; PC: checks, VRAM): the views keep their old names. */
const EX = () => !!(S.state && S.state.experimental);
function pages() {
  const head = [['start', 'Start here', ['start']], ['capture', 'Record', ['capture']], ['sessions', 'Sessions', ['sessions']], ['compare', 'Compare', ['compare']]];
  return EX() ? head.concat([['mods', 'Mods', ['mods', 'conflicts', 'precheck']], ['vram', 'VRAM', ['vram']], ['pc', 'PC checks', ['checks']], ['optimise', 'Optimise', ['optimise', 'workbench']], ['gameopts', 'Game options', ['gameopts']], ['experiments', 'Experiments', ['experiments']], ['settings', 'Settings', ['settings']]])
    : head.concat([['mods', 'Mods', ['optimise', 'conflicts', 'mods', 'precheck']], ['vram', 'VRAM', ['vram']], ['pc', 'PC checks', ['checks']], ['settings', 'Settings', ['settings']]]);
}
const VIEW_LABEL = { precheck: 'Before you install', workbench: 'Workbench', optimise: 'Check your mods', conflicts: 'Conflicts and errors', mods: 'Cost while playing', checks: 'PC checks', vram: 'Graphics memory (VRAM)' };
const pageOf = (t) => pages().find(p => p[2].includes(t)) || pages()[0];
function renderNav() {
  const ed = EX() ? 'E' : 'main';
  if (S.navFor === ed) return;
  S.navFor = ed;
  const cnt = (p) => { const c = S.counts, n = !c ? 0 : p[0] === 'mods' ? c.mods : p[0] === 'pc' ? c.pc : 0; return n ? `<span class="cnt" title="${n} to look at">${n}</span>` : ''; };
  $('#tabs').innerHTML = pages().map(p => `<button data-tab="${p[2][0]}">${esc(p[1])}${cnt(p)}</button>`).join('');
  $$('#tabs button').forEach(b => b.onclick = () => showTab(b.dataset.tab));
  if (!pages().some(p => p[2].includes(S.tab))) S.tab = 'start';
  markNav();
}
function markNav() {
  const pg = pageOf(S.tab);
  $$('#tabs button').forEach(b => b.classList.toggle('on', pg[2].includes(b.dataset.tab)));
  const sub = $('#subtabs');
  if (pg[2].length > 1) {
    sub.className = 'subtabs';
    sub.innerHTML = pg[2].map(v => `<button data-sub="${v}" class="${v === S.tab ? 'on' : ''}">${esc(v === 'optimise' && EX() ? 'Optimise' : VIEW_LABEL[v] || v)}</button>`).join('');
    $$('[data-sub]', sub).forEach(b => b.onclick = () => {
      // Workbench: its tab, pressed again inside a project, goes back to the list of mods and projects
      if (b.dataset.sub === 'workbench' && S.tab === 'workbench' && S.wb && S.wb.id) {
        if (S.wb.dirty && !confirm('Leave without saving?')) return;
        S.wb.id = null; S.wb.dirty = false;
      }
      showTab(b.dataset.sub);
    });
  } else { sub.className = 'subtabs hidden'; sub.innerHTML = ''; }
}
function showTab(t) {
  if (S.state && !pages().some(p => p[2].includes(t))) t = 'start'; // a view this edition doesn't have
  S.tab = t;
  markNav();
  $$('.tab').forEach(s => s.classList.toggle('on', s.id === 'tab-' + t));
  if (t === 'start') renderStart();
  if (t === 'sessions') loadSessions();
  if (t === 'compare') renderCompare();
  if (t === 'mods') renderMods();
  if (t === 'optimise') renderOptimise();
  if (t === 'gameopts') renderGameOpts();
  if (t === 'workbench') renderWorkbench();
  if (t === 'precheck') renderPrecheck();
  if (t === 'conflicts') renderConflicts();
  if (t === 'checks') renderChecks();
  if (t === 'experiments') renderExperiments();
  if (t === 'vram') renderVram();
  if (t === 'settings') { S.setInfo = null; renderSettings(true); }
  if (t === 'capture') renderCapture();
}
$$('#tabs button').forEach(b => b.onclick = () => showTab(b.dataset.tab));

/* ------------------------------------------------------------------ polling */
async function poll() {
  try {
    S.state = await api('state');
    renderNav();
    renderTop();
    if (S.tab === 'capture') {
      const act = S.state.config.active;
      // the Optimise schedule of the picked build. Not asked for while a session is set up or recording:
      // working it out reads the build's mod folders, and nothing should touch the disk during a recording.
      const quiet = ['idle', 'done', 'error'].includes(S.state.capture.state) && !S.state.game.running;
      if (EX() && quiet && (S.schedFor !== act || !S.schedAt || Date.now() - S.schedAt > 10000)) {
        S.schedAt = Date.now();
        api('optimise/schedule', { build: act }).then(x => { S.sched = x; S.schedFor = act; }).catch(() => { S.sched = null; S.schedFor = act; });
      }
      renderCapture();
    }
    if (S.tab === 'settings') renderSettings(false);
    const c = S.state.capture;
    if (c.state === 'done' && S.lastDone !== c.session_id) {
      S.lastDone = c.session_id;
      S.sessionsLoaded = false;
      S.startSesAt = 0; // Start here picks up the new recording
      if (S.tab === 'start') renderStart();
    }
    if (S.tab === 'start' && (S.startCap !== c.state || S.startBuild !== S.state.config.active)) renderStart();
  } catch (e) {
    $('#topstatus').innerHTML = '<span class="pill warn">' + esc(e.message) + '</span>';
  }
  setTimeout(poll, 1000);
}

// one build for the whole app: the header picker and every page's build buttons set the same one
async function setBuild(i) {
  const st = S.state;
  if (!st || !st.config.builds[i]) return;
  const changed = i !== st.config.active;
  if (changed) {
    try { await api('settings', Object.assign({}, st.config, { active: i })); } catch (e) { toast(e.message, true); return; }
    st.config.active = i;
    S.counts = null; S.ck = null; S.op = null; S.ex = null; S.vrOpen = null; S.lt = null;
    S.topBuildSig = null;
    toast('Build: ' + st.config.builds[i].name);
  }
  S.ckBuild = S.opBuild = S.cfBuild = S.vrBuild = S.exBuild = i;
  if (S.pre) { S.pre.build = i; S.pre.res = null; }
  renderTop();
  showTab(S.tab);
}

function renderTop() {
  const st = S.state;
  const parts = [];
  const c = st.capture;
  if (c.state === 'recording') parts.push('<span class="pill rec">● REC ' + fmt.dur(c.recording_s) + '</span>' + (c.focus ? '<span class="pill warn">' + esc(KEYS().mark) + ' mark ' + fmt.dur(c.focus_s) + '</span>' : ''));
  // 2.5: the build is picked here, for every page
  const bs = st.config.builds || [];
  if (bs.length) {
    const sig = bs.map(b => b.name).join('|') + '#' + st.config.active;
    if (S.topBuildSig !== sig || !$('#topbuild')) {
      S.topBuildSig = sig;
      parts.push(`<label class="topbuild"><span class="long">BUILD</span> <select id="topbuild">${bs.map((b, i) => `<option value="${i}" ${i === st.config.active ? 'selected' : ''}>${esc(b.name)}</option>`).join('')}</select></label>`);
    } else parts.push(S.topBuildHtml || '');
  }
  parts.push(st.game.running ? '<span class="pill ok" title="' + esc(st.config.builds[st.game.build] ? st.config.builds[st.game.build].name : '') + '">Game running' + (st.config.builds[st.game.build] ? '<span class="long"> · ' + esc(st.config.builds[st.game.build].name) + '</span>' : '') + '</span>' : '<span class="pill"><span class="long">Game not running</span><span class="short">Game off</span></span>');
  if (st.experimental) parts.push('<span class="pill e" title="Frame Lab 2.5E: Experiments and Optimise change settings and mod files on this PC, each with a backup and Undo">2.5E<span class="long"> Experimental</span></span>');
  const keep = $('#topbuild'), busy = keep && document.activeElement === keep;
  if (!busy) {
    $('#topstatus').innerHTML = parts.join('');
    const sel = $('#topbuild');
    if (sel) {
      S.topBuildHtml = sel.closest('label').outerHTML;
      sel.onchange = async () => {
        const i = +sel.value;
        setBuild(i);
      };
    }
  }
  renderStatus();
  loadCounts();
  const ban = $('#banner');
  if (!st.admin) {
    ban.className = 'banner';
    ban.innerHTML = 'Frame Lab is not running as administrator, so PresentMon can\'t record frame and GPU timing. Close it and start it again (it asks for admin on start).';
  } else ban.className = 'banner hidden';
}

// the status line at the bottom: the recorder parts, admin, edition (2.5)
function renderStatus() {
  const st = S.state, cp = st.components || {}, el = $('#statusbar');
  if (!el) return;
  const dot = (ok, warn) => `<i class="${ok ? 'g' : warn ? 'y' : 'n'}">●</i>`;
  const hv = (cp.hitch_pkg || {}).version;
  el.innerHTML = `<span>${dot((cp.presentmon || {}).found, true)}PresentMon</span><span>${dot((cp.hitch_pkg || {}).found, true)}HitchRecorder${hv ? ' ' + esc(hv) : ''}</span><span>${dot((cp.markers || {}).found, true)}markers mod</span>
    <span class="r">${esc(st.gpu || '')} · ${st.admin ? 'admin' : '<span style="color:var(--amber)">not admin</span>'} · Frame Lab ${esc(st.edition || '2.5')} · v${esc(st.version)}</span>`;
}

// counts on the Mods and PC buttons: things to look at in the picked build (read in the background, cached)
async function loadCounts() {
  const st = S.state;
  if (!st || !st.config.builds.length || S.countsBusy) return;
  const b = st.config.active;
  if (S.counts && S.counts.build === b && Date.now() - S.counts.at < 120000) return;
  if (['waiting', 'recording', 'running', 'finishing'].includes(st.capture.state)) return; // nothing that reads the disk while recording
  S.countsBusy = true;
  const out = { build: b, at: Date.now(), mods: 0, pc: 0 };
  try {
    const ck = await api('checks', { build: b, fresh: false });
    (ck.report.groups || []).forEach(g => (g.rows || []).forEach(x => { if (x.status === 'found' || x.status === 'check') out.pc++; }));
  } catch (e) { /* no count */ }
  try {
    const op = await api('optimise', { build: b });
    const r = op && op.report;
    if (r) out.mods = (r.findings || []).filter(f => f.severity === 'high' || f.severity === 'medium').length + (r.mods || []).filter(x => x.update).length;
  } catch (e) { /* no count */ }
  S.counts = out; S.countsBusy = false;
  S.navFor = null; renderNav();
}

/* ------------------------------------------------------------------ light / dark (2.5): follows Windows unless Settings picks one */
function isLight() { const t = document.documentElement.dataset.theme; return t ? t === 'light' : matchMedia('(prefers-color-scheme: light)').matches; }
// lc: a chart colour as it is drawn on this theme (the light theme gets it 38% darker, so lines stay readable on white)
function lc(c) {
  if (!isLight()) return c;
  const m = /^#([0-9a-f]{6})$/i.exec(c) || null, r = /rgba?\((\d+),\s*(\d+),\s*(\d+)/.exec(c);
  const v = m ? [0, 2, 4].map(i => parseInt(m[1].slice(i, i + 2), 16)) : r ? [r[1], r[2], r[3]].map(Number) : null;
  return v ? '#' + v.map(x => Math.round(x * 0.62).toString(16).padStart(2, '0')).join('') : c;
}
function themeIcon() { const b = $('#themebtn'); if (!b) return; const l = isLight(); b.innerHTML = l ? '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z"></path></svg>' : '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" aria-hidden="true"><circle cx="12" cy="12" r="4.5"></circle><path d="M12 2v2.5M12 19.5V22M2 12h2.5M19.5 12H22M4.9 4.9l1.8 1.8M17.3 17.3l1.8 1.8M4.9 19.1l1.8-1.8M17.3 6.7l1.8-1.8"></path></svg>'; b.title = l ? 'Switch to dark' : 'Switch to light'; b.setAttribute('aria-label', b.title); }
function themeGet() { try { return localStorage.getItem('fl_theme') || 'system'; } catch (e) { return 'system'; } }
function themeSet(v) {
  try { localStorage.setItem('fl_theme', v); } catch (e) { /* not kept */ }
  if (v === 'light' || v === 'dark') document.documentElement.dataset.theme = v; else delete document.documentElement.dataset.theme;
  themeIcon(); paintSwatches();
  const sel = $('#set-theme'); if (sel) sel.value = v;
  if (S.tab === 'sessions' && S.cur) renderReport($('#report'), S.cur, false); // charts read the colours when drawn
  else if (S.tab === 'compare' && typeof renderCompare === 'function') renderCompare();
}
// chart legend swatches (<i data-lc="#hex">) take the same colour as their line on this theme
function paintSwatches() { document.querySelectorAll('i[data-lc]').forEach(i => { const c = lc(i.dataset.lc); if (i.dataset.lcd !== c) { i.style.background = c; i.dataset.lcd = c; } }); }
new MutationObserver(paintSwatches).observe(document.body, { childList: true, subtree: true });
// the header button flips what is shown now; Settings > Look can go back to following Windows
document.addEventListener('click', (e) => { if (e.target.closest('#themebtn')) themeSet(isLight() ? 'dark' : 'light'); });
try { matchMedia('(prefers-color-scheme: light)').addEventListener('change', () => { if (themeGet() === 'system') themeSet('system'); }); } catch (e) { /* older WebView */ }
themeIcon();

/* ------------------------------------------------------------------ Start here (2.5): one step at a time
   The newest recording of the picked build gives the steps, biggest win first (the same order as the
   report's "Do this next"). One is shown at a time, with a button straight to where it is done. Done
   sends you to record again; the next recording then says whether it helped. */
const E_NOTE = 'Frame Lab 2.5E (Experimental) can try to fix this for you, but it is highly experimental: it changes settings and mod files on your PC (each with a backup and Undo). It is on the same Nexus page.';
const eNote = (d) => d && d.e && !EX() ? `<div class="enote">${E_NOTE}</div>` : '';
// a do-it-yourself button's target: setting:<key> (a Windows page), usersettings, buildfile:<rel>
function openTarget(t) {
  if (!t) return;
  const b = S.state ? S.state.config.active : 0;
  const body = t.startsWith('setting:') ? { what: 'setting', id: t.slice(8) } : t.startsWith('buildfile:') ? { what: 'buildfile', build: b, rel: t.slice(10) } : { what: t };
  api('open', body).catch(e => toast(e.message, true));
}
const stepKey = (sid, a) => sid + '|' + a.title;
function stepMarks() { try { return JSON.parse(localStorage.getItem('fl_steps') || '{}'); } catch (e) { return {}; } }
function setStepMark(k, v) {
  const m = stepMarks();
  if (v) m[k] = v; else delete m[k];
  try { localStorage.setItem('fl_steps', JSON.stringify(m)); } catch (e) { /* not kept */ }
}
// how much work a step is: 1 = a click or a setting (close or move programs, a game setting), 2 = a Windows
// setting or a restart, 3 = replacing or updating mods, drivers, hardware. Easy ones go first.
function effortOf(a) {
  const d = a.do || {}, g = d.go || '', t = (d.text || '').toLowerCase();
  if (/^(vram|gpuapps)$|^setting:graphics$/.test(g) || /close (them|it|the program)|texture setting|game's settings|frame generation off/.test(t)) return 1;
  if (/^(setting|exp|usersettings|buildfile)[:]/.test(g) || /restart|windows/.test(t)) return 2;
  return 3;
}
function stepsOf(r) {
  const list = r.advice || [];
  const main = list.filter(a => a.do && NEXT_LEVELS.includes(a.level));
  const urgent = main.filter(a => a.level === 'crash' || a.level === 'hw'); // something broken comes first anyway
  const rest = main.filter(a => a.level !== 'crash' && a.level !== 'hw').map((a, i) => [a, i]).sort((x, y) => effortOf(x[0]) - effortOf(y[0]) || x[1] - y[1]).map(x => x[0]);
  return urgent.concat(rest).concat(list.filter(a => a.do && !NEXT_LEVELS.includes(a.level)));
}
const startCard = (n, title, text, btns, cls) => `<div class="card shs ${cls || ''}">${n ? `<div class="step-n">${esc(n)}</div>` : ''}<div class="step-do">${title}</div>
  ${text ? `<div class="step-why">${text}</div>` : ''}${btns ? `<div class="step-btns">${btns}</div>` : ''}</div>`;
// the frame-time trace of a recording (Start here): its frame times, thinned to ~160 points, spikes marked
function traceSvg(r) {
  const tl = r.timeline || {}, T = tl.t || [], F = tl.frame_ms || [];
  if (T.length < 2) return '';
  const W = 320, H = 72, n = Math.min(160, T.length), step = T.length / n, t0 = T[0], t1 = T[T.length - 1] || 1;
  const med = (r.kpi || {}).median_ms || 16, cap = Math.max(med * 5, 50);
  const y = (ms) => H - 6 - Math.min(ms, cap) / cap * (H - 14);
  const x = (t) => (t - t0) / (t1 - t0 || 1) * W;
  const pts = [];
  for (let i = 0; i < n; i++) {
    const a = Math.floor(i * step), b = Math.max(a + 1, Math.floor((i + 1) * step));
    let m = 0; for (let j = a; j < b && j < F.length; j++) m = Math.max(m, F[j] || 0);
    pts.push(x(T[a]).toFixed(1) + ',' + y(m).toFixed(1));
  }
  const dots = (r.spikes || []).slice().sort((p, q) => q.ms - p.ms).slice(0, 12).map(s => `<circle cx="${x(s.t).toFixed(1)}" cy="${y(s.ms).toFixed(1)}" r="3" fill="var(--amber)"></circle>`).join('');
  return `<svg viewBox="0 0 ${W} ${H}" preserveAspectRatio="none" role="img" aria-label="Frame time across the recording"><line x1="0" y1="${y(med * 2).toFixed(1)}" x2="${W}" y2="${y(med * 2).toFixed(1)}" stroke="var(--line2)" stroke-dasharray="2 4"></line><polyline fill="none" stroke="var(--accent)" stroke-width="1.5" vector-effect="non-scaling-stroke" points="${pts.join(' ')}"></polyline>${dots}</svg>`;
}
const stTicks = (k) => { try { return JSON.parse(localStorage.getItem('fl_ticks') || '{}')[k] || []; } catch (e) { return []; } };
function setTick(k, i, on) {
  try { const m = JSON.parse(localStorage.getItem('fl_ticks') || '{}'); const l = new Set(m[k] || []); if (on) l.add(i); else l.delete(i); m[k] = [...l]; localStorage.setItem('fl_ticks', JSON.stringify(m)); } catch (e) { /* not kept */ }
}
async function renderStart() {
  const root = $('#tab-start');
  const st = S.state;
  if (!st) { root.innerHTML = '<div class="empty">Loading ...</div>'; return; }
  const cfg = st.config, b = cfg.builds[cfg.active], cap = st.capture.state;
  S.startCap = cap; S.startBuild = cfg.active;
  const head = (meta) => `<div class="st-head"><div><h1>Start here</h1><p>One thing at a time, the easy ones first. Record again after each one.</p></div>${meta || ''}</div>`;
  let html = '<div class="start">';
  if (!b) {
    html += head() + startCard('Step 1', 'Tell Frame Lab where your game is', 'It finds Cyberpunk 2077 installs from GOG, Steam and Epic, and Mod Organizer 2 setups. Each one becomes a build you can record.', '<button class="btn" data-go="settings">Find my game</button>');
    root.innerHTML = html + '</div>'; return;
  }
  html += head();
  if (!st.admin) html += startCard('', 'Start Frame Lab as administrator', 'Without it PresentMon can\'t record frame and GPU timing. Close Frame Lab and start it again: it asks for admin when it starts.', '', 'warn');
  if (['arming', 'waiting', 'running', 'recording', 'finishing'].includes(cap)) {
    html += startCard('', cap === 'recording' || cap === 'running' ? 'Recording: play as you normally do' : cap === 'finishing' ? 'Working out what happened ...' : 'Ready: start the game',
      cap === 'finishing' ? 'This takes a few seconds. The steps appear here when it is done.' : `Play for about 10 minutes, somewhere busy if you can (a city district, driving, a fight). Press <b>${esc(KEYS().felt)}</b> whenever you feel a stutter. Quit the game when you are done.`,
      '<button class="btn2" data-go="record">Open Record</button>');
    root.innerHTML = html + '</div>'; return;
  }
  if (!S.startSes || Date.now() - (S.startSesAt || 0) > 5000) {
    try { S.startSes = (await api('sessions')) || []; } catch (e) { S.startSes = []; }
    S.startSesAt = Date.now();
    if (S.tab !== 'start') return;
  }
  const mine = S.startSes.filter(x => x.build === b.name && x.analysed);
  if (!mine.length) {
    html += startCard('Step 1', 'Record 10 minutes of normal play in ' + esc(b.name),
      `On <b>Record</b>, press <b>Start</b>, then start the game and play as you normally do, somewhere busy if you can (a city district, driving, a fight). Press <b>${esc(KEYS().felt)}</b> whenever you feel a stutter. Quit the game when you are done: Frame Lab works out what happened and the steps appear here.`,
      '<button class="btn" data-go="record">Go to Record</button>');
    html += startCard('', 'Easy first: free graphics memory (VRAM)', vramGuide(false), '<button class="btn2" data-go="vram">Open VRAM</button>');
    html += startCard('', 'While you wait: check your mods', 'Mods reads the build\'s mod files without starting the game: missing requirements, mods installed twice, plugins that failed to load, scripts that didn\'t compile, and (if you switch it on in Settings) which mods have an update.', `<button class="btn2" data-go="${EX() ? 'optimise' : 'optimise'}">Check your mods</button>`);
    root.innerHTML = html + '</div>'; return;
  }
  const last = mine[0];
  if (!S.startRes || S.startRes.meta.id !== last.id) {
    try { S.startRes = await api('session/' + encodeURIComponent(last.id)); } catch (e) { html += startCard('', 'The newest recording could not be read', esc(e.message), '<button class="btn2" data-go="sessions">Open Sessions</button>'); root.innerHTML = html + '</div>'; return; }
    if (S.tab !== 'start') return;
  }
  const r = S.startRes, k = r.kpi || {}, m = r.meta;
  const when = new Date(m.created), mins = (k.duration_s || 0) / 60;
  const per10 = k.duration_s ? (k.spikes || 0) / (k.duration_s / 600) : 0;
  html = '<div class="start">' + head(`<div class="st-meta"><span>${esc(b.name.toUpperCase())} · ${esc(when.toLocaleString('en-AU', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' }).toUpperCase())} · ${esc(fmt.dur(k.duration_s).toUpperCase())} OF PLAY</span><a data-go="sessions">Full report</a></div>`);
  if (!st.admin) html += startCard('', 'Start Frame Lab as administrator', 'Without it PresentMon can\'t record frame and GPU timing. Close Frame Lab and start it again: it asks for admin when it starts.', '', 'warn');
  const lowCls = k.low1_fps && k.low1_fps < 40 ? 'bad' : k.low1_fps < 60 ? 'warn' : 'good';
  const vCls = /^Rough|^Bad|crash/i.test(r.verdict || '') ? 'bad' : /^Some/i.test(r.verdict || '') ? 'warn' : '';
  html += `<section class="card st-rec" aria-label="Newest recording"><div class="st-nums">
    <div class="st-num"><span class="l">AVERAGE</span><span class="v">${k.avg_fps ? k.avg_fps.toFixed(0) : '–'}<small> fps</small></span><span class="s">median frame ${fmt.ms(k.median_ms)}</span></div>
    <div class="st-num"><span class="l">1% LOW</span><span class="v ${lowCls}">${k.low1_fps ? k.low1_fps.toFixed(0) : '–'}<small> fps</small></span><span class="s">0.1% low ${fmt.fps(k.low01_fps)}</span></div>
    <div class="st-num"><span class="l">STUTTERS</span><span class="v">${per10.toFixed(1)}</span><span class="s">per 10 min · ${k.spikes || 0} in this run</span></div>
    <div class="st-trace"><span class="l">FRAME TIME</span>${traceSvg(r)}</div></div>
    ${r.verdict ? `<div class="st-verdict"><span class="vd"><i class="${vCls}"></i>${esc(r.verdict.replace(/ \d+ things? to do\.$| Nothing needs doing\.$/, ''))}</span><b>${esc((r.verdict.match(/(\d+ things? to do|Nothing needs doing)/) || [''])[0])}</b></div>` : ''}</section>`;
  const fu = r.follow_up;
  if (fu && (fu.rows || []).length) {
    html += `<div class="card"><h2>Did it help? <span class="dimmer small">against the recording before: ${esc(fu.title || fu.session)}</span></h2><table>
      ${fu.rows.map(x => { const t = FOLLOW_TAG[x.verdict] || FOLLOW_TAG.same; return `<tr><td>${esc(x.label)}</td><td class="n">${followNum(x.was, x.unit)}</td><td class="n">${followNum(x.now, x.unit)}</td><td><span class="tag ${t[0]}">${t[1]}</span></td><td class="dim">${x.text ? esc(x.text) : ''}</td></tr>`; }).join('')}</table></div>`;
  }
  if (!r.advice) {
    html += startCard('', 'This recording was analysed by an older Frame Lab', 'Open it in Sessions and press Re-analyse to get the steps, or record again.', '<button class="btn2" data-go="sessions">Open Sessions</button><button class="btn" data-go="record">Record again</button>');
    root.innerHTML = html + '</div>'; return;
  }
  const steps = stepsOf(r), marks = stepMarks(), sid = r.meta.id;
  const todo = steps.filter(a => !marks[stepKey(sid, a)]);
  const nMain = steps.filter(x => NEXT_LEVELS.includes(x.level)).length;
  if (steps.length) {
    const cur = todo[0];
    html += `<ol class="st-track" aria-label="Steps from this recording" style="grid-template-columns: repeat(${steps.length}, minmax(0, 1fr))">${steps.map((x, i) => {
      const mk = marks[stepKey(sid, x)], opt = i >= nMain, cls = mk === 'done' ? 'done' : mk === 'skip' ? 'skip' : x === cur ? 'now' : opt ? 'opt' : '';
      const nd = mk === 'done' ? '<svg width="14" height="14" viewBox="0 0 14 14" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="M3 7.5l2.6 2.5L11 4"></path></svg>' : opt ? '' : String(i + 1);
      return `<li class="${cls}"><span class="ln"><span class="nd">${nd}</span>${i < steps.length - 1 ? '<span class="bar"></span>' : ''}</span><span>${opt ? 'Optional: ' : ''}${esc(x.do.title || x.title)}${mk === 'skip' ? ' · skipped' : ''}</span></li>`;
    }).join('')}</ol>`;
  }
  if (!steps.length) {
    html += startCard('', 'Nothing to fix from this recording', 'No crash, nothing that caused stutters and nothing worth a test. If it still doesn\'t feel right, record the place where it happens (and press ' + esc(KEYS().felt) + ' when you feel it), or check your mods.',
      '<button class="btn" data-go="record">Record again</button><button class="btn2" data-go="optimise">Check your mods</button>', 'good');
  } else if (!todo.length) {
    html += startCard('', 'You\'ve been through every step from this recording', 'Record the same kind of play again: the next recording shows here whether it helped, and gives the next steps.',
      '<button class="btn" data-go="record">Record again</button><button class="btn2" data-stepreset="1">Show the steps again</button>', 'good');
  } else {
    const a = todo[0], d = a.do, n = steps.indexOf(a) + 1, key = stepKey(sid, a), ticks = new Set(stTicks(key));
    const bar = d.bar && d.bar.max > 0 ? `<div class="st-why"><span class="l">WHY</span><span>${esc(a.title)}.</span>
        <div><div class="gauge" aria-hidden="true"><span style="width:${Math.min(100, d.bar.value / d.bar.max * 100).toFixed(0)}%"></span></div>
        <div class="gl"><span>${esc(d.bar.label)}</span><span><b>${d.bar.value} ${esc(d.bar.unit)}</b> of ${d.bar.max} ${esc(d.bar.unit)}</span></div></div>
        ${d.effect ? `<span class="eff">${esc(d.effect)}</span>` : ''}</div>`
      : `<div class="st-why"><span class="l">WHY</span><span>${esc(a.title)}.</span>${d.effect ? `<span class="eff">${esc(d.effect)}</span>` : ''}${a.detail ? `<details class="step-more"><summary>More about this</summary><div class="dim">${esc(a.detail)}</div></details>` : ''}</div>`;
    const body = (d.steps || []).length ? `<div class="st-ticks">${d.steps.map((x, i) => `<label class="${ticks.has(i) ? 'ticked' : ''}"><input type="checkbox" data-tick="${i}" ${ticks.has(i) ? 'checked' : ''}><span>${esc(x)}</span></label>`).join('')}</div>`
      : `<div class="st-ticks"><span>${esc(d.text)}</span></div>`;
    html += `<section class="card st-step" aria-label="Your next step"><div class="kick"><span>${n <= nMain ? 'STEP ' + n + ' OF ' + nMain : 'OPTIONAL'}${ADV_TAG[a.level] ? ' · ' + ADV_TAG[a.level][1].toUpperCase() : ''}</span>${/restart/i.test(d.text) ? '<span>needs a restart</span>' : ''}</div>
      <h2>${esc(d.title || d.text)}</h2>
      <div class="st-cols">${body}${bar}</div>
      <div class="st-btns">${d.go && d.button ? `<button class="btn" data-go="${esc(d.go)}">${esc(d.button)}</button>` : ''}
        <button class="${d.go && d.button ? 'btn2' : 'btn'}" data-step="done" data-k="${esc(key)}">Done: record again</button>
        <button class="skip" data-step="skip" data-k="${esc(key)}">Skip this one</button></div>
      ${eNote(d)}</section>`;
    if (steps.some(x => marks[stepKey(sid, x)])) html += '<div><button class="btn2 small" data-stepreset="1">Start the list over</button></div>';
  }
  root.innerHTML = html + '</div>';
  $$('[data-step]', root).forEach(x => x.onclick = () => {
    setStepMark(x.dataset.k, x.dataset.step);
    if (x.dataset.step === 'done') { showTab('capture'); toast('Record the same kind of play again: Start here then shows whether it helped.'); }
    else renderStart();
  });
  $$('[data-tick]', root).forEach(x => x.onchange = () => { const k2 = stepKey(sid, todo[0]); setTick(k2, +x.dataset.tick, x.checked); x.closest('label').classList.toggle('ticked', x.checked); });
  $$('[data-stepreset]', root).forEach(x => x.onclick = () => { steps.forEach(a => setStepMark(stepKey(sid, a), null)); renderStart(); });
}

/* ------------------------------------------------------------------ capture tab */
function buildTags(b) {
  if (!b.exists) return '<span class="tag r">folder not found</span>';
  const t = ['<span class="tag">game ' + esc(b.game_version) + '</span>'];
  const armedHere = S.state && S.state.capture && ['waiting', 'recording', 'running', 'arming', 'finishing'].includes(S.state.capture.state) && S.state.capture.build === b.name;
  if (b.leftover && !armedHere) t.push('<span class="tag r">recorders left in the game</span>');
  else if (b.leftover) t.push('<span class="tag y">recorders in for this session</span>');
  else t.push('<span class="tag g">no recorders in the game</span>');
  t.push(b.cet ? '<span class="tag">CET</span>' : '<span class="tag">no CET</span>');
  return t.join('');
}

function layerRows(st, b) {
  const comp = st.components;
  const rows = [];
  const add = (dot, name, tag, d) => rows.push('<div class="layer"><div class="dot ' + dot + '"></div><div><b>' + name + '</b> <span class="tag ' +
    (tag === 'in game' ? 'g' : tag === 'outside' ? 'b' : '') + '">' + tag + '</span><div class="d">' + d + '</div></div></div>');
  const armed = ['waiting', 'recording', 'running', 'arming', 'finishing'].includes(st.capture.state);
  if (!b || !b.exists) add('r', 'Engine recorder', 'in game', 'Pick a game build that exists.');
  else if (!comp.hitch_pkg.found) add('r', 'Engine recorder', 'in game', 'HitchRecorder is missing from components\\HitchRecorder.');
  else {
    let d = 'HitchRecorder ' + esc(comp.hitch_pkg.version) + ': loads, world sectors, entity spawns, shader + RT pipeline builds, GPU memory, VRAM budget. Put into the game for the session only and taken out again when you quit.';
    if (b.leftover && !armed) d += ' <b style="color:var(--red)">It (or the markers) is in this build without a session.</b> <button class="link" data-act="remove-leftover">Take it out</button>';
    add(b.leftover && !armed ? 'r' : 'g', 'Engine recorder', 'in game', d);
  }
  add(comp.markers && comp.markers.found ? (b && b.cet ? 'g' : 'y') : 'r', 'Game markers', 'in game',
    comp.markers && comp.markers.found ? 'A small CET mod notes loading screens and the main menu, so they are left out of the stutter list, and every 5 seconds where V is (district, on foot or driving, in a fight), so Frame Lab knows which test route a recording covers. Session only. It only reads, unless a game option is on trial (Optimise): then it also sets that one option while the game runs.' + (b && b.exists && !b.cet ? ' Needs CET, not found in this build.' : '') : 'components\\FrameLabMarkers is missing: loading screens will count as play and no test route can be read.');
  const scripts = (name, ok, text) => {
    if (!st.config.use_script_profilers) return add('', name, 'in game', 'Off in Settings.');
    if (!comp.hitch_pkg.found) return add('r', name, 'in game', 'Needs HitchRecorder (missing).');
    add(ok ? 'g' : 'y', name, 'in game', text);
  };
  const mk = esc(KEYS().mark);
  scripts('REDscript per mod', true, 'HitchRecorder times every script function the game runs and charges it to the mod whose .reds file it came from. Only in the parts you mark with ' + mk + '.');
  scripts('CET Lua per mod', b && b.cet, b && b.cet ? 'HitchRecorder times every CET callback (onUpdate, onDraw, Observe, Override, hotkeys) per mod. Only in the parts you mark with ' + mk + '.' : 'No CET in this build.');
  if (st.config.no_windows_events) add('', 'Windows events', 'outside', 'Off in Settings.');
  else add(st.admin ? 'g' : 'y', 'Windows events', 'outside', 'Which drivers ran interrupts / DPCs and for how long, memory read back from disk (hard page faults) and disk use per program: for the stutters nothing in the game explains.' + (st.admin ? '' : ' Needs admin.'));
  if (!comp.presentmon.found) add('r', 'Frames + GPU time', 'outside', 'PresentMon not found (CapFrameX\\PresentMon or components\\PresentMon).');
  else add(st.admin ? 'g' : 'y', 'Frames + GPU time', 'outside', esc(comp.presentmon.name) + ': real frame times, GPU busy per frame, frame-gen frames.' + (st.admin ? '' : ' Needs admin.'));
  const gi = st.gpu_info || {};
  if (!gi.name) add('y', 'GPU health', 'outside', esc(gi.note || 'No graphics card found.'));
  else {
    const reads = gi.reads || [], miss = ['clock', 'temperature', 'power'].filter(x => !reads.includes(x));
    if (!reads.includes('slowdown reasons')) miss.push('slowdown reasons (NVIDIA only)');
    add(gi.vendor === 'nvidia' && !gi.note ? 'g' : 'y', 'GPU health', 'outside', esc(gi.name) + ' (' + esc(gi.source) + '): ' + esc(reads.join(', ')) + '.' +
      (miss.length ? ' Not given by this card: ' + esc(miss.join(', ')) + '.' : '') + (gi.vendor && gi.vendor !== 'nvidia' ? ' AMD and Intel cards are in beta.' : '') + (gi.note ? ' ' + esc(gi.note) : ''));
  }
  add('g', 'Other programs on the card', 'outside', 'Every 2 s: how much graphics memory each other program holds on the game\'s card (browsers, Discord, OBS ...). The VRAM page shows it live and can move programs to the integrated graphics.');
  add('g', 'PC side', 'outside', 'Windows counters: game CPU, busiest core, disk reads, RAM, other apps on the GPU (OBS).');
  add(st.components.library.found ? 'g' : st.config.library ? 'y' : '', 'Mod names', 'offline', st.components.library.found ? 'Files and scripts traced back to the library zips in ' + esc(st.components.library.path) + '.' : (st.config.library ? 'Library folder not found: ' + esc(st.config.library) + ' (Settings).' : 'Optional: a folder with your downloaded mod zips (Settings) shows which download each file came from. Without it mods are named by their files.'));
  const nm = st.names;
  if (nm === 'ready') add('g', 'File names', 'offline', 'Game file paths from WolvenKit\'s list.');
  else if (nm === 'building') add('b', 'File names', 'offline', 'Building the list from WolvenKit ...');
  else add('y', 'File names', 'offline', (nm && nm.startsWith('error') ? esc(nm) + ' ' : 'Not built yet. ') + '<button class="link" data-act="build-names">Build from WolvenKit</button>');
  return rows.join('');
}

const STATE_TEXT = {
  idle: ['Ready', 'Name the session and press Start session (game closed).'],
  arming: ['Setting up ...', 'Putting HitchRecorder and the markers into the game for this session.'],
  waiting: ['Ready: start the game', 'Everything records by itself from when the game starts until you quit.'],
  running: ['Recording', 'Play normally. Quit the game to finish.'],
  recording: ['Recording', 'Play normally. {mark} where it stutters starts a mark, {mark} again ends it. {felt} when you feel a stutter. Quit the game to finish.'],
  finishing: ['Finishing ...', 'Collecting everything, taking the recorders out of the game, analysing.'],
  done: ['Done', 'The report is ready.'],
  error: ['Problem', ''],
};

function renderCapture() {
  const root = $('#tab-capture');
  const st = S.state;
  if (!st) { root.innerHTML = '<div class="empty">Loading ...</div>'; return; }
  if (!root.dataset.built) {
    root.innerHTML = `<div class="cap-grid">
      <div><h2>Game build</h2><div id="cap-builds"></div><div class="dim small" style="margin-top:8px">Builds and paths: Settings tab.</div></div>
      <div><h2>What gets recorded</h2><div id="cap-layers"></div></div>
      <div class="col">
        <div><h2>Session</h2><div class="card">
          <div class="dim small" style="margin-bottom:5px">Name this session</div>
          <input id="cap-title" style="width:100%" placeholder="e.g. Dogtown drive, PT on, DLSS Q + FG (empty: the build and the time)" maxlength="80">
          <div id="cap-state" style="margin-top:12px"></div>
          <div id="cap-buttons" style="display:flex;gap:10px;margin-top:12px;align-items:center"></div>
          <div id="cap-test"></div>
          <ol class="howto" id="cap-howto">
            <li>Start session (game closed): Frame Lab puts its recorders into the game.</li><li>Start the game and play. Everything records by itself.</li>
            <li id="cap-howto-mark"><b class="kn-mark">F11</b> where it stutters starts a mark, <span class="kn-mark">F11</span> again ends it. Script time per mod (REDscript, CET) is measured only inside marks: that's how you find the mod behind a stutter.</li>
            <li><b class="kn-felt">F10</b> when you feel a stutter: the report shows what happened in the 2 seconds before.</li><li>Quit the game.</li>
            <li>Frame Lab collects, takes the recorders out again and opens the report.</li></ol>
          <div class="steps" id="cap-steps"></div>
        </div></div>
        <div><h2>Live</h2><div class="card" id="cap-live"></div></div>
      </div></div>`;
    root.dataset.built = '1';
    root.addEventListener('click', onCaptureClick);
    const t = $('#cap-title', root);
    try { t.value = localStorage.getItem('fl.title') || ''; } catch (e) { /* storage may be off */ }
    t.oninput = () => { try { localStorage.setItem('fl.title', t.value); } catch (e) { /* ignore */ } };
  }
  const cfg = st.config;
  const K = KEYS();
  $$('.kn-mark', root).forEach(e => { e.textContent = K.mark; });
  $$('.kn-felt', root).forEach(e => { e.textContent = K.felt; });
  const active = st.builds[cfg.active];
  $('#cap-builds').innerHTML = !st.builds.length ? '<div class="dim">No Cyberpunk 2077 install found yet. Settings → <b>Find game installs</b>, or <b>Add a build</b> with the game folder.</div>' : st.builds.map(b => `<div class="build ${b.index === cfg.active ? 'on' : ''}" data-build="${b.index}">
      <div class="name">${esc(b.name)}</div><small>${esc(b.path)}</small><div class="tags">${buildTags(b)}</div></div>`).join('');
  $('#cap-layers').innerHTML = layerRows(st, active);

  const c = st.capture;
  const [big, sub0] = STATE_TEXT[c.state] || [c.state, ''];
  const sub = sub0.replace(/\{mark\}/g, K.mark).replace(/\{felt\}/g, K.felt);
  $('#cap-state').innerHTML = `<div class="bigstate">${esc(big)}${c.title ? ' <span class="dim" style="font-size:13px;font-weight:400">· ' + esc(c.title) + '</span>' : ''}</div>
      <div class="dim">${esc(c.error || sub)}</div>`;
  const busy = ['arming', 'finishing'].includes(c.state);
  const armed = ['waiting', 'running', 'recording'].includes(c.state);
  let btn = '';
  if (c.state === 'done') btn = `<button class="btn" data-act="open-report">Open the report</button><button class="btn2" data-act="reset">New session</button>`;
  else if (armed) btn = `<button class="btn2" data-act="disarm" ${st.game.running ? 'disabled title="Quit the game: the session finishes and cleans up by itself"' : ''}>Cancel and remove</button>`;
  else if (c.state === 'error') btn = `<button class="btn2" data-act="disarm">Try removing again</button><button class="btn2" data-act="reset">Clear</button>`;
  else btn = `<button class="btn" data-act="arm" ${busy || st.game.running || !active || !active.exists ? 'disabled' : ''}>● Start session</button>
      ${st.game.running ? '<span class="dim small">Close the game to start a session.</span>' : '<span class="dim small">In game: ' + esc(K.mark) + ' = mark a part (script time per mod), ' + esc(K.felt) + ' = "I felt a stutter"</span>'}`;
  const bEl = $('#cap-buttons');
  if (bEl.dataset.html !== btn) { bEl.innerHTML = btn; bEl.dataset.html = btn; }
  // Optimise schedule: the routes the next recording of this build should cover
  const sch = S.sched && S.schedFor === cfg.active ? S.sched : null;
  const todo = sch && (sch.step === 'routes' || sch.step === 'retest') ? (sch.routes || []).filter(x => !x.done) : [];
  // while a test recording runs: how far each route is (read from the game every 5 seconds)
  const rlive = c.routes || [];
  const liveHtml = rlive.length && (armed || c.state === 'finishing' || c.state === 'done') ? `<div class="cap-test"><b>Test recording for Optimise.</b> ${c.state === 'done' ? 'What it covered:' : c.state === 'recording' || c.state === 'running' ? 'So far in this recording:' : 'To record, in any order:'}
      <div class="cap-live">${rlive.map(x => `<div class="${x.done ? 'done' : ''}"><i>${x.done ? '✓' : ''}</i><span>${esc(x.ask)}</span><b>${x.done ? 'recorded' : fmt.dur(Math.min(x.seconds, x.need_s)) + ' of ' + routeNeed(x.need_s)}</b></div>`).join('')}</div>
      ${c.trial ? `<div class="cap-trial ${c.trial_off ? 'off' : ''}">Game option on trial in this recording: <b>${esc(c.trial)}</b>.${c.trial_off ? ' It doesn\'t hold: ' + esc(c.trial_off) + '. This recording won\'t count for the trial.' : c.state === 'done' ? ' It held to the end.' : ' Frame Lab sets it when the game starts and checks it every 5 seconds.'}</div>` : ''}
      <span class="dim small">${c.state === 'done' ? 'Optimise shows what is left.' : 'The same line is at the top of the game screen. A route is ticked a few seconds after it is long enough. Pause and menus don\'t count.'}</span></div>` : '';
  const testHtml = liveHtml || (todo.length && active ? `<div class="cap-test"><b>Test recording for Optimise.</b> ${sch.step === 'retest' && sch.trial ? 'The same routes again, in the same places, this time with the option on trial.' : sch.step === 'retest' ? 'The build has changed: the same routes again, in the same places.' : 'Optimise is locked until these are recorded.'} Still to record in ${esc(active.name)}:
      <ul>${todo.map(x => `<li><b>${esc(x.def.name)}</b>, ${routeNeed(x.def.seconds)}: ${esc(x.def.how)}${x.best ? ' <span class="dimmer">(' + fmt.dur(x.best.stat.seconds) + ' so far)</span>' : ''}</li>`).join('')}</ul>
      ${sch.tip ? `<div class="cap-tip">${esc(sch.tip)}</div>` : ''}
      ${sch.trial && sch.step === 'retest' ? `<div class="cap-trial">A game option is on trial: <b>${esc(sch.trial.name)}</b> (${esc(sch.trial.cat)} / ${esc(sch.trial.item)} = ${esc(optText(sch.trial.value))}). Frame Lab sets it in these recordings only.</div>` : ''}
      <span class="dim small">Any order, in one session or several. A line at the top of the game screen shows how far each one is. Script time is measured for the whole run, so no ${esc(K.mark)} is needed. Use the same game settings every time.</span></div>` : '');
  const tEl = $('#cap-test');
  if (tEl.dataset.html !== testHtml) { tEl.innerHTML = testHtml; tEl.dataset.html = testHtml; }
  // a test recording measures script time all the way through: the mark key only flags a spot
  const hm = $('#cap-howto-mark'), markHow = todo.length || rlive.length ? `Play the routes listed above. Script time per mod is measured for the whole run; <b>${esc(K.mark)}</b> only flags a part you want to look at.` : `<b>${esc(K.mark)}</b> where it stutters starts a mark, ${esc(K.mark)} again ends it. Script time per mod (REDscript, CET) is measured only inside marks: that's how you find the mod behind a stutter.`;
  if (hm && hm.dataset.html !== markHow) { hm.innerHTML = markHow; hm.dataset.html = markHow; }
  const stepNo = { idle: 0, arming: 0, waiting: 1, running: 1, recording: c.focus ? 2 : 1, finishing: 4, done: 5, error: -1 }[c.state];
  $$('#cap-howto li').forEach((li, i) => { li.className = i < stepNo ? 'done' : i === stepNo ? 'now' : ''; });
  $('#cap-title').disabled = armed || busy;
  const steps = (c.steps || []).slice().reverse();
  $('#cap-steps').innerHTML = steps.length ? steps.map(s => `<div class="step ${s.kind}"><span class="t">${esc(s.time)}</span><span class="x">${esc(s.text)}</span></div>`).join('') : '<div class="dim small">Nothing yet.</div>';

  // live
  const L = st.live, h = L.hitch, pc = L.pc || {};
  const k = (l, v, s, cls) => `<div class="k ${cls || ''}"><div class="l">${l}</div><div class="v">${v}</div>${s ? '<div class="s">' + s + '</div>' : ''}</div>`;
  let live = '';
  if (h && h.valid) {
    const fps = h.avg_frame_ms > 0 ? 1000 / h.avg_frame_ms : 0;
    const vr = h.vram_budget ? `${fmt.mb(h.vram_usage / 1048576)} / ${fmt.mb(h.vram_budget / 1048576)}` : '–';
    const over = h.vram_budget && h.vram_usage > h.vram_budget;
    live += '<div class="kpi">' + k('game loop', fps ? fps.toFixed(0) + ' fps' : '–', 'last frame ' + fmt.ms(h.last_frame_ms)) +
      (h.capture_mode === 2 ? k(esc(K.mark) + ' mark', h.capturing ? 'on' : 'off', 'recording the whole run', h.capturing ? 'warn' : 'good') : k('recording', h.capturing ? 'yes' : 'no', h.capture_mode === 1 ? esc(K.mark) + ' toggles' : 'always on', h.capturing ? 'good' : '')) +
      k('spikes', h.spikes, '≥ ' + cfg.spike_ms + ' ms', h.spikes > 0 ? 'warn' : '') + k('VRAM (game)', vr, 'used / budget', over ? 'bad' : '') + '</div>';
  } else if (st.game.running) {
    live += '<div class="dim">Game running. No HitchRecorder data yet (still loading, or no session set up).</div>';
  } else {
    live += '<div class="dim">Shows the game\'s frame rate, VRAM and spikes while it runs.</div>';
  }
  const got = v => v != null && v > 0; // readings this card's driver gives
  if (pc.vram_total != null || got(pc.gpu_clock)) {
    live += '<div class="kpi" style="margin-top:10px">' + (got(pc.gpu_clock) ? k('GPU clock', fmt.n(Math.round(pc.gpu_clock)) + ' MHz', pc.throttle ? 'slowing down' : '', pc.throttle ? 'warn' : '') : '') +
      (got(pc.gpu_temp) ? k('GPU temp', fmt.p(pc.gpu_temp, 0) + ' °C') : '') +
      (got(pc.gpu_power) ? k('GPU power', fmt.p(pc.gpu_power, 0) + ' W', 'load ' + fmt.p(pc.gpu_util, 0) + '%') : k('GPU load', fmt.p(pc.gpu_util, 0) + '%')) +
      k('VRAM (all)', fmt.mb(pc.vram_used), 'of ' + fmt.mb(pc.vram_total)) + '</div>';
    if (pc.disk != null || pc.other_gpu != null) live += `<div class="dim small" style="margin-top:8px">game CPU ${fmt.p(pc.game_cpu, 0)}% · disk ${fmt.p(pc.disk, 0)} MB/s · other apps on GPU ${fmt.p(pc.other_gpu, 0)}%</div>`;
  }
  $('#cap-live').innerHTML = live;
}

async function onCaptureClick(e) {
  const b = e.target.closest('[data-build]');
  if (b && !e.target.closest('button')) {
    const cfg = Object.assign({}, S.state.config, { active: +b.dataset.build });
    try { await api('settings', cfg); S.state.config.active = cfg.active; renderCapture(); } catch (x) { toast(x.message, true); }
    return;
  }
  const a = e.target.closest('[data-act]');
  if (!a) return;
  const act = a.dataset.act;
  const st = S.state;
  try {
    if (act === 'arm') {
      const bn = (st.config.builds[st.config.active] || {}).name || '';
      const title = $('#cap-title').value.trim() || ((bn ? bn + ' ' : '') + new Date().toTimeString().slice(0, 5));
      await api('arm', { title, build: st.config.active });
    } else if (act === 'disarm') {
      confirmBox('Take the recorders out of the game and cancel this session?', async () => {
        try { await api('disarm', {}); } catch (x) { toast(x.message, true); }
      });
    } else if (act === 'reset') await api('reset', {});
    else if (act === 'open-report') { S.curId = st.capture.session_id; S.cur = null; showTab('sessions'); }
    else if (act === 'remove-leftover') {
      const b = st.builds[st.config.active];
      confirmBox(`Take HitchRecorder and the loading-screen markers out of <b>${esc(b.name)}</b>?<br><span class="dim">${esc(b.path)}\\red4ext\\plugins\\HitchRecorder, r6\\scripts\\NightCityFrameLab and Frame Lab's CET mod (cyber_engine_tweaks\\mods\\NightCityFrameLab). The game must be closed.</span>`, async () => {
        try { await api('leftover/remove', { build: st.config.active }); toast('Taken out.'); } catch (x) { toast(x.message, true); }
      });
    }
    else if (act === 'build-names') { await api('names/build', {}); toast('Building the file-name list ... (about a minute)'); }
  } catch (x) { toast(x.message, true); }
}

/* ------------------------------------------------------------------ sessions */
async function loadSessions() {
  const root = $('#tab-sessions');
  if (!root.dataset.built) {
    root.innerHTML = `<div class="sess-grid"><div><h2>Sessions <button class="btn2 small" style="float:right" data-sact="folder">Open folder</button></h2><div class="slist" id="slist"></div></div><div id="report"></div></div>`;
    root.dataset.built = '1';
    root.addEventListener('click', onSessionsClick);
  }
  try { S.sessions = (await api('sessions')) || []; } catch (e) { toast(e.message, true); S.sessions = []; }
  if (S.curId && !S.sessions.some(s => s.id === S.curId)) { S.curId = null; S.cur = null; } // removed (cancelled / deleted)
  if (!S.curId && S.sessions.length) S.curId = S.sessions[0].id;
  renderSessionList();
  if (S.curId && (!S.cur || S.cur.meta.id !== S.curId)) openSession(S.curId);
  else if (!S.sessions.length) $('#report').innerHTML = '<div class="empty">No sessions yet. Start one on the Record page.</div>';
}

function renderSessionList() {
  $('#slist').innerHTML = S.sessions.map(s => `<div class="sitem ${s.id === S.curId ? 'on' : ''}" data-sid="${esc(s.id)}">
      <div class="n">${esc(s.title || s.id)}</div>
      <small>${esc(s.build)} · ${esc(s.created)}${s.analysed ? ' · ' + fmt.dur(s.duration_s) + ' · ' + s.spikes + ' spikes · 1% low ' + fmt.fps(s.low1_fps) : s.finished ? '' : ' · not finished'}</small></div>`).join('');
}

async function openSession(id, reanalyse) {
  S.curId = id;
  S.spikeSel = null;
  renderSessionList();
  $('#report').innerHTML = '<div class="empty">' + (reanalyse ? 'Analysing ...' : 'Loading ...') + '</div>';
  try {
    S.cur = await api('session/' + encodeURIComponent(id) + (reanalyse ? '/analyse' : ''), reanalyse ? {} : undefined);
    renderReport($('#report'), S.cur, false);
  } catch (e) {
    // 2.31: a session with nothing usable in it still gets Open folder / Delete (before, it could not be removed from here)
    $('#report').innerHTML = '<div class="empty">This session could not be analysed: ' + esc(e.message) +
      '<div style="margin-top:12px;display:flex;gap:8px;justify-content:center"><button class="btn2 small" data-sact="open">Open folder</button><button class="btn2 small" data-sact="delete">Delete</button></div></div>';
  }
}

async function onSessionsClick(e) {
  const it = e.target.closest('[data-sid]');
  if (it) { openSession(it.dataset.sid); return; }
  const a = e.target.closest('[data-sact]');
  if (!a) return;
  const id = S.curId;
  try {
    switch (a.dataset.sact) {
      case 'folder': await api('open', { what: 'sessions' }); break;
      case 'open': await api('open', { what: 'session', id }); break;
      case 'reanalyse': openSession(id, true); break;
      case 'details': setRepOpen(!repOpen()); if (S.cur) renderReport($('#report'), S.cur, false); break;
      case 'export': { const r = await api('session/' + encodeURIComponent(id) + '/export', {}); toast('Saved: ' + r.path); await api('open', { what: 'session', id }); break; }
      case 'rename': {
        const cur = S.cur && S.cur.meta.title || '';
        confirmBox('<div style="margin-bottom:8px">New name</div><input id="ren" style="width:420px" value="' + esc(cur) + '">', async () => {
          const v = $('#ren').value.trim();
          if (!v) return;
          try { await api('session/' + encodeURIComponent(id) + '/rename', { title: v }); await loadSessions(); openSession(id, true); } catch (x) { toast(x.message, true); }
        });
        break;
      }
      case 'delete':
        confirmBox('Delete this session and all its recorded data?<br><span class="dim">' + esc(id) + '</span>', async () => {
          try { await api('session/' + encodeURIComponent(id) + '/delete', {}); S.curId = null; S.cur = null; await loadSessions(); } catch (x) { toast(x.message, true); }
        });
        break;
    }
  } catch (x) { toast(x.message, true); }
}

/* ------------------------------------------------------------------ report */
function destroyCharts() { S.charts.concat(S.lcharts || []).forEach(c => c.destroy()); S.charts = []; S.lcharts = []; }

function kpiBox(l, v, s, cls) { return `<div class="k ${cls || ''}"><div class="l">${l}</div><div class="v">${v}</div>${s ? '<div class="s">' + s + '</div>' : ''}</div>`; }

function renderReport(el, r, reportOnly) {
  destroyCharts();
  const k = r.kpi, m = r.meta, RK = repKeys(r);
  const lowCls = k.low1_fps && k.low1_fps < 40 ? 'bad' : k.low1_fps < 60 ? 'warn' : 'good';
  const vramCls = k.vram_budget_mb && k.vram_peak_mb > k.vram_budget_mb ? 'bad' : '';
  const seen = new Set((r.spikes || []).map(s => s.group));
  const legend = Object.entries(GROUPS).filter(([g]) => g !== 'other' && seen.has(g)).map(([g, v]) => `<span><i style="background:${gcol(g)}"></i>spike: ${v.label}</span>`).join('');
  const hasGpu = ((r.timeline || {}).gpu_ms || []).some(v => v >= 0);
  el.innerHTML = `
    <div class="rep-head"><div class="title">${esc(m.title || m.id)}</div>
      <div class="dim">${esc(m.build)} · ${new Date(m.created).toLocaleString('en-AU')} · ${k.not_playing_s ? fmt.dur(k.duration_s) + ' of play (' + fmt.dur(k.session_s) + ' recorded, ' + fmt.dur(k.not_playing_s) + ' loading / menu left out)' : fmt.dur(k.duration_s) + ' recorded'}${m.gpu ? ' · ' + esc(m.gpu) : ''}</div>
      <div class="dim small">${(m.experiments || []).length ? (EX() ? 'Experiments on: ' : 'Set by Frame Lab 2.5E or an earlier version during this session: ') + m.experiments.map(esc).join(', ') : (EX() && m.created > '2026-10-31T10' ? 'No Experiments settings on' : '')}</div>
      ${reportOnly ? '' : `<div class="actions"><button class="btn2 small" data-sact="reanalyse">Re-analyse</button><button class="btn2 small" data-sact="export">Export HTML report</button>
      <button class="btn2 small" data-sact="open">Open folder</button><button class="btn2 small" data-sact="rename">Rename</button><button class="btn2 small" data-sact="delete">Delete</button></div>`}</div>
    <div class="kpi" style="margin-bottom:12px">
      ${kpiBox('median frame', fmt.ms(k.median_ms), fmt.fps(k.avg_fps) + ' avg')}
      ${kpiBox('1% low', fmt.fps(k.low1_fps), '0.1% low ' + fmt.fps(k.low01_fps), lowCls)}
      ${kpiBox('spikes ≥ ' + k.spike_ms + ' ms', k.spikes, k.spikes && k.duration_s ? (k.spikes / (k.duration_s / 60)).toFixed(1) + ' per minute' : '', k.spikes > 0 ? 'warn' : 'good')}
      ${kpiBox('stutter', k.stutter_pct != null ? k.stutter_pct.toFixed(2) + '%' : '–', 'frames over 2.5× the average' + (k.frame_sd_ms != null ? ' · spread ' + fmt.ms(k.frame_sd_ms) : ''), k.stutter_pct >= 0.5 ? 'warn' : '')}
      ${k.input_lag_ms ? kpiBox('input lag', fmt.ms(k.input_lag_ms), 'input to screen, median') : ''}
      ${k.cpu_temp_max ? kpiBox('CPU temp max', k.cpu_temp_max.toFixed(0) + ' °C', '', k.cpu_temp_max >= 90 ? 'warn' : '') : ''}
      ${kpiBox('GPU bound', k.gpu_bound_pct >= 0 ? k.gpu_bound_pct.toFixed(0) + '%' : '–', 'frames where the GPU was the limit')}
      ${kpiBox('VRAM peak (game)', k.vram_peak_mb ? fmt.mb(k.vram_play_peak_mb || k.vram_peak_mb) : '–', (k.vram_budget_mb ? 'budget ' + fmt.mb(k.vram_budget_mb) : '') + (k.resets && k.vram_play_peak_mb && k.vram_play_peak_mb < k.vram_peak_mb - 300 ? ' · ' + fmt.mb(k.vram_peak_mb) + ' during screen resets' : '') + (k.others_vram_mb ? ' · rest of the card ' + fmt.mb(k.others_vram_mb) : ''), vramCls)}
      ${kpiBox('script time', ((k.redscript_ms_per_s || 0) + (k.cet_ms_per_s || 0)).toFixed(1) + ' ms/s', 'REDscript ' + fmt.p(k.redscript_ms_per_s) + ' · CET ' + fmt.p(k.cet_ms_per_s))}
      ${r.pacing ? kpiBox('frames shown', fmt.fps(r.pacing.shown_fps), r.pacing.vrr ? 'VRR: shown when ready' : r.pacing.refresh_hz ? (r.pacing.held_pct || 0).toFixed(1) + '% held twice · fixed ' + r.pacing.refresh_hz + ' Hz' : '', !r.pacing.vrr && r.pacing.held_pct >= 5 ? 'warn' : '') : ''}
      ${kpiBox('GPU temp max', k.gpu_temp_max ? k.gpu_temp_max.toFixed(0) + ' °C' : '–', k.throttle_serious ? 'slowed by: ' + esc(k.throttle) : k.throttle ? 'ran into its power limit (normal at full load)' : k.slowdown_known !== false ? 'no slowdown seen' : k.gpu_temp_max ? 'slowdown reasons: NVIDIA cards only' : 'not given by this card', k.throttle_serious ? 'warn' : '')}
    </div>
    ${adviceHtml(r, reportOnly)}
    ${routesHtml(r)}
    <div class="card chart-card"><div class="chart-top"><div class="legend"><span><i style="background:${css('--accent')}"></i>frame time</span>${hasGpu ? `<span><i style="background:${css('--orange')}"></i>GPU busy</span>` : '<span class="dimmer" title="PresentMon could not tell GPU time apart with frame generation on">GPU busy: not recorded (frame generation)</span>'}${legend}${(r.timeline.not_playing || []).length ? '<span><i style="background:rgba(150,150,145,0.45)"></i>loading / menu (left out)</span>' : ''}${(r.timeline.focus || []).length ? '<span><i style="background:rgba(47,196,185,0.35)"></i>' + esc(RK.mark) + ' marked</span>' : ''}${(r.timeline.resets || []).some(x => x.playing) ? `<span><i style="background:${css('--c-reset')}"></i>screen reset</span>` : ''}</div><button class="btn2 small" id="ch-all" style="display:none">Show all</button></div>
      <div class="readout" id="ch-read">Drag across any chart to zoom in · double-click or <b>Show all</b> to zoom out · click a spike bar for its details · move the mouse over a chart to read the values here · the corner button opens a chart larger, to zoom with the mouse wheel and move it by dragging</div><div id="ch-main"></div></div>
    <div class="grid2" style="grid-template-columns:1.7fr 1fr"><div class="card chart-card"><div class="legend"><span><i style="background:${css('--accent')}"></i>frame time at each percentile: the L-shape (play only, loading and menus left out)</span></div>
      <div class="readout" id="ls-read">Flat and low is smooth. Where the line turns up shows how many frames were slow and by how much: the right end is the slowest 1 in 10,000.</div><div id="ch-lshape"></div></div>
      <div class="card"><h2>Percentiles</h2><div id="r-pct"></div></div></div>
    <div class="grid3" style="grid-template-columns:1fr 1fr 1fr"><div class="card chart-card"><div class="legend"><span><i data-lc="#8cc4ff"></i>VRAM used by the game</span><span><i style="background:${css('--red')}"></i>budget</span>${(r.timeline.vram_others_mb || []).length ? '<span><i data-lc="#f0a6ff"></i>everything else on the card (right axis)</span>' : ''}</div><div id="ch-vram"></div></div>
      <div class="card chart-card"><div class="legend">${(r.timeline.clock_mhz || []).length ? '<span><i data-lc="#c3a6ff"></i>GPU clock (MHz)</span>' : ''}${(r.timeline.temp_c || []).length ? '<span><i data-lc="#ff9a6b"></i>GPU temp (°C)</span>' : ''}${!(r.timeline.clock_mhz || []).length || !(r.timeline.temp_c || []).length ? '<span class="dimmer">' + [(r.timeline.clock_mhz || []).length ? '' : 'clock', (r.timeline.temp_c || []).length ? '' : 'temperature'].filter(Boolean).join(' and ') + ': not given by this card</span>' : ''}</div><div id="ch-gpu"></div></div>
      <div class="card chart-card"><div class="legend"><span><i style="background:${css('--amber')}"></i>disk reads (MB/s)</span><span><i data-lc="#7ee0c8"></i>game CPU (%)</span><span><i data-lc="#ff9aa8"></i>other apps on GPU (%)</span></div><div id="ch-pc"></div></div></div>
    <div class="grid3" style="grid-template-columns:1fr 1fr 1fr"><div class="card chart-card"><div class="legend"><span><i data-lc="#9fe870"></i>CPU clock (MHz)</span><span><i data-lc="#ffb36b"></i>cores at 90%+</span></div><div id="ch-cpu"></div></div>
      <div class="card chart-card"><div class="legend"><span><i data-lc="#ff6b8a"></i>driver interrupts / DPC (% of CPU)</span><span><i data-lc="#b0a0ff"></i>game GPU memory in system RAM (MB)</span>${(r.timeline.ram_free_mb || []).length ? '<span><i data-lc="#9adf8f"></i>free system RAM (MB)</span>' : ''}</div><div id="ch-win"></div></div>
      <div class="card chart-card"><div class="legend"><span><i data-lc="#6fd3ff"></i>frames shown per second</span>${r.pacing && r.pacing.refresh_hz ? '<span><i data-lc="#ff8a9a"></i>held for 2+ refreshes (%)</span>' : ''}</div><div id="ch-disp"></div></div></div>
    ${reportOnly ? '' : `<div class="rep-more"><button class="btn2" data-sact="details">${repOpen() ? 'Hide the details' : 'Show the details: every spike, cost per mod, engine work'}</button></div>`}
    <div class="rep-details ${reportOnly || repOpen() ? '' : 'collapsed'}">
    <div class="grid3">
      <div class="card"><h2>What caused the spikes</h2><div id="r-causes"></div></div>
      <div class="card"><h2>Spikes <span class="dimmer" style="text-transform:none;letter-spacing:0">(click one)</span></h2><div class="scroll" id="r-spikes"></div></div>
      <div class="card" id="r-detail"></div>
    </div>
    <div class="card" style="margin-top:14px"><h2>Cost by mod</h2><div class="scroll" id="r-mods"></div></div>
    <div class="rep-grid">
      <div class="card"><h2>Engine work recorded</h2><div class="scroll" id="r-engine"></div></div>
      <div class="card"><h2>Shaders / pipelines</h2><div id="r-shaders"></div></div>
      <div class="card"><h2>Windows (drivers, paging, disk)</h2><div class="scroll" id="r-win"></div></div>
      <div class="card"><h2>Other programs on the graphics card</h2><div id="r-gpuapps"></div></div>
    </div>
    <div class="rep-grid"><div class="card"><h2>Layers in this session</h2><div id="r-layers"></div></div><div class="card"><h2>Notes</h2><div id="r-warn"></div></div></div></div>`;

  // causes
  const cs = r.causes || [];
  const tot = cs.reduce((a, c) => a + c.count, 0) || 1;
  $('#r-causes', el).innerHTML = cs.length ? `<div class="bar" style="margin-bottom:10px">${cs.map(c => `<div style="width:${c.count / tot * 100}%;background:${gcol(c.group)}"></div>`).join('')}</div>
    <table>${cs.map(c => `<tr><td><span class="swatch" style="background:${gcol(c.group)}"></span>${esc(c.cause)}</td><td class="n">${c.count}</td><td class="n">${c.pct.toFixed(0)}%</td><td class="n dim" style="white-space:nowrap">${(c.total_ms / 1000).toFixed(1)} s</td></tr>`).join('')}</table>` :
    '<div class="dim">No spikes over ' + k.spike_ms + ' ms. Smooth run.</div>';

  // spikes list
  const sp = r.spikes || [];
  $('#r-spikes', el).innerHTML = sp.length ? `<table><tr><th>#</th><th>time</th><th class="n">frame</th><th>cause</th><th></th></tr>${sp.map(s => `<tr class="click" data-spike="${s.id}">
      <td class="dim">${s.id}</td><td>${fmt.t(s.t)}</td><td class="n">${fmt.ms(s.ms)}</td><td><span class="swatch" style="background:${gcol(s.group)}"></span>${esc(s.cause)}</td><td>${s.focus ? '<span class="tag y" title="inside a part you marked with ' + esc(RK.mark) + '">' + esc(RK.mark) + '</span>' : ''}${s.near_mark ? '<span title="you pressed the marker key near this">★</span>' : ''}</td></tr>`).join('')}</table>` : '<div class="dim">None.</div>';
  $('#r-spikes', el).onclick = (e) => { const tr = e.target.closest('[data-spike]'); if (tr) selectSpike(el, r, +tr.dataset.spike); };
  if (sp.length) selectSpike(el, r, (S.spikeSel && sp.find(s => s.id === S.spikeSel)) ? S.spikeSel : sp.slice().sort((a, b) => b.ms - a.ms)[0].id);
  else $('#r-detail', el).innerHTML = '<h2>Spike detail</h2><div class="dim">No spikes.</div>';

  renderModTable($('#r-mods', el), r.mods || [], 25);
  const eng = r.engine || [];
  $('#r-engine', el).innerHTML = eng.length ? `<table><tr><th>work</th><th class="n">count</th><th class="n">unique</th><th class="n">main thr.</th><th class="n">avg</th><th class="n">max</th><th class="n">≥5 ms</th></tr>${eng.map(e => `<tr><td>${esc(e.kind)}</td><td class="n">${fmt.n(e.count)}</td><td class="n">${fmt.n(e.unique)}</td><td class="n">${e.main_pct.toFixed(0)}%</td><td class="n">${e.avg_ms.toFixed(2)}</td><td class="n">${e.max_ms.toFixed(1)}</td><td class="n">${e.over_5ms}</td></tr>`).join('')}</table>` : '<div class="dim">No engine layer in this session.</div>';
  const sh = r.shaders || {};
  $('#r-shaders', el).innerHTML = sh.recorded ? `<table>
      <tr><td>pipelines built</td><td class="n">${fmt.n(sh.total)}</td></tr>
      <tr><td>built after the first 2 minutes (= not pre-warmed)</td><td class="n">${fmt.n(sh.after_warmup)}</td></tr>
      <tr><td>of those slow (≥ 5 ms)</td><td class="n">${fmt.n(sh.slow_after_warmup)}</td></tr>
      <tr><td>ray tracing pipeline builds</td><td class="n">${fmt.n(sh.rt_builds)}${sh.rt_builds ? ' <span class="dim">(slowest ' + fmt.ms(sh.rt_slowest_ms) + ')</span>' : ''}</td></tr>
      <tr><td>pipeline cache lookups</td><td class="n">${sh.cache_lookups ? fmt.n(sh.cache_lookups) + ' <span class="dim">(' + sh.cache_misses + ' not found)</span>' : 'none: the game doesn\'t use a pipeline cache'}</td></tr>
      <tr><td>built with a cached blob</td><td class="n">${fmt.n(sh.cached_blobs)}</td></tr></table>` : '<div class="dim">Not recorded.</div>';
  const wv = r.windows;
  $('#r-win', el).innerHTML = wv && wv.recorded ? `<table>
      ${(wv.drivers || []).slice(0, 6).map(d => `<tr><td>${esc(d.name)}${d.what ? ' <span class="dimmer">' + esc(d.what) + '</span>' : ''}</td><td class="n">${(d.ms_per_s || 0).toFixed(2)} ms/s</td><td class="n dim">longest ${fmt.ms(d.max_ms)}</td><td class="n">${d.spikes ? d.spikes + ' stutter' + (d.spikes > 1 ? 's' : '') : ''}</td></tr>`).join('')}
      ${(wv.paging || []).slice(0, 4).map(p => `<tr><td>${esc(p.name)} <span class="dimmer">read back from disk</span></td><td class="n">${fmt.n(p.count || 0)} faults</td><td class="n dim">${(p.mb || 0).toFixed(0)} MB</td><td></td></tr>`).join('')}
      ${(wv.disk || []).slice(0, 4).map(p => `<tr><td>${esc(p.name)} <span class="dimmer">disk</span></td><td class="n">${(p.mb || 0).toFixed(0)} MB</td><td class="n dim">slowest ${fmt.ms(p.max_ms)}</td><td></td></tr>`).join('')}
    </table>` : '<div class="dim">Not recorded in this session (needs Frame Lab running as administrator).</div>';
  const ga = r.gpu_apps;
  const gaWhat = (a) => a.shared ? '<span class="dimmer" title="Its number includes the game\'s own memory (it is hooked into the game: overlay / desktop)">shares the game\'s memory</span>' : a.class === 'suggest' ? '<span class="tag b">could move</span>' : a.class === 'keep' || a.class === 'game' ? '<span class="tag g">keep</span>' : a.class === 'windows' ? '<span class="dimmer">Windows</span>' : '';
  $('#r-gpuapps', el).innerHTML = ga && ga.recorded ? `<div class="dim small" style="margin-bottom:4px">Everything but the game on the card (programs, Windows, driver), typical while playing: ${fmt.mb(ga.others_typ_mb || 0)} (up to ${fmt.mb(ga.others_max_mb || 0)}), of which programs that could run on integrated graphics: ${fmt.mb(ga.movable_typ_mb || 0)}${(ga.apps || []).some(a => a.shared) ? '. * hooked into the game: Windows counts the game\'s memory for it too' : ''}</div><table>
      <tr><th>program</th><th class="n">typical</th><th class="n">most</th><th></th></tr>
      ${(ga.apps || []).slice(0, 10).map(a => `<tr><td title="${esc(a.reason || '')}">${esc(a.name)}</td><td class="n">${a.shared ? '<span class="dimmer">' + fmt.mb(a.typ_mb) + '*</span>' : fmt.mb(a.typ_mb)}</td><td class="n ${a.shared ? 'dimmer' : 'dim'}">${fmt.mb(a.max_mb)}</td><td>${gaWhat(a)}</td></tr>`).join('')}
    </table>` : '<div class="dim">Not recorded in this session.</div>';
  $('#r-layers', el).innerHTML = '<table>' + (r.layers || []).map(l => `<tr><td><span class="dot ${l.ok ? 'g' : 'y'}" style="display:inline-block;margin:0 8px 0 0"></span>${esc(l.name)}</td><td class="dim">${esc(l.detail)}</td></tr>`).join('') +
    `<tr><td>Frame times from</td><td class="dim">${esc(k.frame_source || '–')}</td></tr></table>`;
  const w = r.warnings || [];
  $('#r-warn', el).innerHTML = (w.length ? '<ul class="warnlist">' + w.map(x => '<li>' + esc(x) + '</li>').join('') + '</ul>' : '<div class="dim">Nothing unusual.</div>') +
    '<div class="dimmer small" style="margin-top:10px">Causes are worked out from what overlapped each slow frame. They are strong hints, not proof: a stall can also come from work none of the layers can see.</div>';

  $('#r-pct', el).innerHTML = pctTable(r);

  // charts
  const tok = S.repTok = (S.repTok || 0) + 1; // only the latest render draws (two quick renders drew every chart twice)
  setTimeout(() => { if (tok === S.repTok) { destroyCharts(); drawCharts(el, r); } }, 0); // charts are always shown
}

// the details of a report (spikes, mods, engine) open or not; remembered on this PC (the charts are always shown)
function repOpen() { if (REPORT) return true; try { return localStorage.getItem('fl_details') === '1'; } catch (e) { return false; } }
function setRepOpen(v) { try { localStorage.setItem('fl_details', v ? '1' : '0'); } catch (e) { /* not kept */ } }

function selectSpike(el, r, id) {
  S.spikeSel = id;
  const s = (r.spikes || []).find(x => x.id === id);
  $$('#r-spikes tr[data-spike]', el).forEach(tr => tr.classList.toggle('sel', +tr.dataset.spike === id));
  if (!s) return;
  const mx = Math.max(s.ms, ...(s.parts || []).map(p => p.ms), 1);
  const parts = (s.parts || []).slice(0, 10).map(p => `<tr><td>${esc(p.label)}</td><td style="width:38%"><div class="bar" style="height:8px"><div style="width:${Math.min(100, p.ms / mx * 100)}%;background:${gcol(p.group)}"></div></div></td><td class="n">${fmt.ms(p.ms)}</td></tr>`).join('');
  const items = (s.items || []).slice(0, 8).map(i => `<tr><td class="n">${fmt.ms(i.ms)}</td><td class="dim">${esc(i.kind)}${i.main ? '' : ' ·bg'}</td><td style="word-break:break-all">${esc(i.label)}${i.mod && i.label.indexOf(i.mod) < 0 ? ' <span class="tag b">' + esc(i.mod) + '</span>' : ''}</td></tr>`).join('');
  const facts = [];
  if (s.gpu_ms >= 0) facts.push('GPU busy ' + fmt.ms(s.gpu_ms));
  if (s.budget_mb) facts.push(`VRAM ${fmt.mb(s.vram_mb)} / ${fmt.mb(s.budget_mb)}` + (s.vram_mb > s.budget_mb ? ' <b style="color:var(--red)">over</b>' : ''));
  if (s.disk_mbs) facts.push('game reading ' + s.disk_mbs.toFixed(0) + ' MB/s');
  if (s.read_max_ms) facts.push('longest disk read ' + fmt.ms(s.read_max_ms) + (s.read_max_ms >= 15 ? ' (slow)' : ''));
  if (s.defender_mb) facts.push('Defender reading ' + s.defender_mb.toFixed(0) + ' MB');
  if (s.cpu_pct) facts.push('game CPU ' + s.cpu_pct.toFixed(0) + '% (busiest core ' + s.max_core_pct.toFixed(0) + '%)');
  if (s.throttle) facts.push('GPU slowed by ' + esc(s.throttle));
  if (s.other_gpu) facts.push('other app on GPU: ' + esc(s.other_gpu));
  if (s.cpu_clock_mhz) facts.push('CPU clock ' + fmt.n(s.cpu_clock_mhz) + ' MHz');
  if (s.dpc_pct >= 1) facts.push('driver interrupts ' + s.dpc_pct.toFixed(1) + '% of the CPU');
  if (s.shared_mb >= 50) facts.push(fmt.n(s.shared_mb) + ' MB of the game\'s GPU memory in system RAM');
  if (s.pages_sec >= 200) facts.push(fmt.n(s.pages_sec) + ' hard page faults/s');
  if (s.ram_free_mb && s.ram_free_mb < 2048) facts.push('free system RAM ' + fmt.n(s.ram_free_mb) + ' MB');
  if (s.reset && s.after_reset) facts.push('while the extra memory from the screen reset at ' + esc(s.reset.split(': ')[0]) + ' was still held');
  $('#r-detail', el).innerHTML = `<h2>Spike ${s.id} · ${fmt.t(s.t)} · ${fmt.ms(s.ms)}${s.frames > 1 ? ' · ' + s.frames + ' frames' : ''}</h2>
    <div><span class="swatch" style="background:${gcol(s.group)}"></span><b style="color:var(--white)">${esc(s.cause)}</b>${s.focus ? ' <span class="tag y">in your ' + esc(repKeys(r).mark) + ' mark</span>' : ''}${s.near_mark ? ' <span class="tag y">★ near your marker</span>' : ''}</div>
    <div class="verdict">${esc(s.verdict)}</div>
    <table style="margin-top:8px">${parts}</table>
    ${items ? '<h2 style="margin-top:10px">Biggest single items</h2><table>' + items + '</table>' : ''}
    ${(s.win || []).length ? '<h2 style="margin-top:10px">Windows at that moment</h2><table>' + s.win.map(w => `<tr><td class="dim">${w.kind === 'driver' ? 'driver' : w.kind === 'paging' ? 'read back from disk' : 'disk'}</td><td>${esc(w.name)}${w.what ? ' <span class="dimmer">' + esc(w.what) + '</span>' : ''}</td><td class="n">${w.kind === 'driver' ? fmt.ms(w.ms) + ' (longest ' + fmt.ms(w.max_ms) + ')' : w.kind === 'paging' ? w.count + ' faults · ' + (w.mb || 0).toFixed(1) + ' MB · ' + fmt.ms(w.ms) : (w.mb || 0).toFixed(1) + ' MB' + (w.max_ms ? ' · longest read ' + fmt.ms(w.max_ms) : '')}</td></tr>`).join('') + '</table>' : ''}
    ${facts.length ? '<div class="dim small" style="margin-top:8px">' + facts.join(' · ') + '</div>' : ''}
    ${REPORT ? '' : `<div class="trace-row"><button class="btn2 small" id="r-trace">Timeline of these 6 seconds (Perfetto)</button><span class="dim small" id="r-trace-msg">every game thread, millisecond by millisecond</span></div>`}`;
  const tb = $('#r-trace', el);
  if (tb) tb.onclick = async () => {
    const msg = $('#r-trace-msg', el);
    tb.disabled = true; msg.textContent = 'writing ...';
    try {
      const out = await api('sessions/' + encodeURIComponent(S.curId) + '/trace', { t: s.t, span: 6 });
      msg.innerHTML = 'Saved <b>' + esc(out.path.split(/[\\/]/).pop()) + '</b> in the session folder. Open <a href="https://ui.perfetto.dev" target="_blank" rel="noopener">ui.perfetto.dev</a> and drop the file on it.';
      api('open', { what: 'session', id: S.curId }).catch(() => {});
    } catch (e) { msg.textContent = String(e.message || e); }
    tb.disabled = false;
  };
  if (S.mainChart && S.mainChart.setSelect) {
    const u = S.mainChart;
    const x0 = u.valToPos(Math.max(0, s.t - 2), 'x'), x1 = u.valToPos(s.t + 2, 'x');
    u.setSelect({ left: x0, width: Math.max(2, x1 - x0), top: 0, height: u.bbox.height / devicePixelRatio }, false);
  }
}

const ADV_TAG = { crash: ['crash', 'crash'], hw: ['hw', 'needs a look'], fix: ['fix', 'caused stutters'], try: ['try', 'worth a test'], info: ['info', 'good to know'], good: ['good', 'fine'] };
function adviceHtml(r, reportOnly) {
  const list = r.advice;
  if (!list) return reportOnly ? '' : '<div class="card adv"><h2>What to try</h2><div class="dim">This session was analysed by an older Frame Lab. Press <b>Re-analyse</b> to get suggestions.</div></div>';
  if (!list.length) return '';
  const rows = list.map(a => `<div class="adv-row ${a.level}"><span class="adv-tag ${a.level}">${ADV_TAG[a.level] ? ADV_TAG[a.level][1] : esc(a.level)}</span>
      <div class="adv-body"><div class="adv-title">${esc(a.title)}</div>${a.detail ? `<div class="adv-detail">${esc(a.detail)}</div>` : ''}
      ${(a.items || []).length ? '<ul>' + a.items.map(i => '<li>' + esc(i) + '</li>').join('') + '</ul>' : ''}</div></div>`).join('');
  // sessions analysed before 2.5 have no verdict: the old list as it was
  if (!r.verdict) return `<div class="card adv"><h2>What to try <span class="dimmer small">worked out from this session's numbers</span></h2>${rows}</div>`;
  return nextHtml(r, reportOnly) + `<details class="cf card adv"${reportOnly ? ' open' : ''}><summary><h2><span class="chev">›</span> All findings <span class="dimmer small">${list.length}: what was measured, and why</span></h2></summary>${rows}</details>`;
}

/* test routes (2.5): which of the Optimise schedule's routes this recording covers, each with its own numbers */
const routeNum = (v, d = 1) => v ? Number(v).toFixed(d).replace(/\.0$/, '') : '–';
const routeNeed = (s) => s >= 120 ? (s / 60).toFixed(0) + ' min' : s.toFixed(0) + ' s';
function routesHtml(r) {
  const rt = r.routes;
  if (!rt || !(r.meta.test || rt.lines > 0)) return '';
  const rows = (rt.routes || []).map(x => `<tr><td>${esc(x.name)}</td>
    <td>${x.done ? '<span class="tag g">covered</span>' : x.seconds > 0 ? '<span class="tag">not yet</span> <span class="dim small">' + esc(x.missing || '') + '</span>' : '<span class="dimmer">not in this recording</span>'}</td>
    <td class="n">${x.seconds > 0 ? fmt.dur(x.seconds) + ' <span class="dimmer">of ' + routeNeed(x.need_s) + '</span>' : '–'}</td>
    <td class="n">${routeNum(x.avg_fps)}</td><td class="n">${routeNum(x.low1_fps)}</td><td class="n">${x.frames ? String(x.stutters_10) : '–'}</td>
    <td class="n">${x.script_s ? routeNum(x.script_ms_s, 2) : '–'}</td>
    <td class="dim small">${(x.scripts || []).slice(0, 3).map(o => esc(o.owner) + ' ' + o.ms_per_sec.toFixed(2)).join(' · ')}</td></tr>`).join('');
  const seen = (rt.seen || []).slice(0, 8).map(p => `${esc(p.place)} <span class="dimmer">${[p.walk_s ? fmt.dur(p.walk_s) + ' on foot' : '', p.drive_s ? fmt.dur(p.drive_s) + ' driving' : '', (p.fight_s || 0) + (p.carfight_s || 0) ? fmt.dur((p.fight_s || 0) + (p.carfight_s || 0)) + ' in a fight' : ''].filter(Boolean).join(', ')}</span>`).join(' · ');
  return `<div class="card next"><h2>Test routes <span class="dimmer small">${r.meta.test ? 'a test recording for Optimise: script time measured for the whole run' : 'what this recording covers of the Optimise test routes'}</span></h2>
    ${rt.note ? `<div class="dim" style="margin-bottom:8px">${esc(rt.note)}</div>` : ''}
    <table><tr><th>route</th><th></th><th class="n">counted</th><th class="n">avg fps</th><th class="n">1% low</th><th class="n">stutters / 10 min</th><th class="n">mod scripts ms/s</th><th>most of it</th></tr>${rows}</table>
    ${seen ? `<div class="dim small" style="margin-top:8px">Where this recording was (as the game names its districts): ${seen}</div>` : ''}
    ${((r.options || {}).trials || []).map(t => `<div class="cap-trial ${t.off ? 'off' : ''}" style="margin-top:10px">Game option on trial in this recording: <b>${esc(t.cat)} / ${esc(t.item)} = ${esc(optText(t.want || ''))}</b> (the game's own value: ${esc(optText(t.was || '')) || 'not read'}). ${t.off ? 'It didn\'t hold: ' + esc(t.off) + '. The recording doesn\'t count for the trial.' : 'It held from start to end' + (t.again ? ', and was set again ' + t.again + ' time' + (t.again === 1 ? '' : 's') + ' when V entered the world because another mod had set it on the way in.' : '.')}</div>`).join('')}
    ${(r.options || {}).asked ? `<div class="dimmer small" style="margin-top:6px">Game options read: this game has ${((r.options || {}).values || []).length} of the ${r.options.asked} options on Frame Lab's list (Optimise > Game options).</div>` : ''}</div>`;
}

/* "Do this next" (2.5): the verdict, at most five things to do, and how the last ones worked out */
const NEXT_LEVELS = ['crash', 'hw', 'fix', 'try'];
// the VRAM page, Start here and the READMEs say this first: the easiest frames to win are usually here
const vramGuide = (here) => 'The game only gets a set share of your graphics card\'s memory (Frame Lab shows it as the <b>budget</b> in every recording). When it runs out, it has to move textures in and out in the middle of a frame: that is a stutter. <b>Even a few hundred MB helps.</b> Easiest first: close the programs that don\'t need the game\'s card, or set them to the integrated graphics (browsers, Discord, launchers: ' + (here ? '<b>Programs on the graphics card</b> below lists them' : 'the VRAM page lists them') + '); then use lighter texture mods (2K instead of 4K: ' + (here ? '<b>Mods</b> below shows' : 'the VRAM page shows') + ' what each one costs); then lower the game\'s texture quality one step.';
const FOLLOW_TAG = { better: ['g', 'better'], same: ['', 'about the same'], worse: ['r', 'worse'] };
function followNum(v, u) {
  if (u === 'MB') return fmt.mb(v);
  return (Math.abs(v) >= 100 ? v.toFixed(0) : v.toFixed(1).replace(/\.0$/, '')) + (u ? ' ' + u : '');
}
function nextHtml(r, reportOnly) {
  const list = r.advice || [];
  const main = list.filter(a => a.do && NEXT_LEVELS.includes(a.level)), opt = list.filter(a => a.do && !NEXT_LEVELS.includes(a.level));
  const acts = main.concat(main.length < 5 ? opt : []).slice(0, 5);
  const btn = (d) => !reportOnly && d.go && d.button ? `<button class="btn2 small" data-go="${esc(d.go)}">${esc(d.button)}</button>` : '';
  let h = `<div class="card next"><h2>Do this next <span class="dimmer small">from this session's numbers, most important first</span></h2>
    <div class="next-verdict">${esc(r.verdict)}</div>
    ${acts.length ? '<ol class="next-list">' + acts.map(a => `<li class="${a.level}"><div class="next-do"><b>${esc(a.do.text)}</b>${btn(a.do)}</div>
      <div class="next-why">${esc(a.title)}${a.do.effect ? ' <span class="dimmer">· ' + esc(a.do.effect) + '</span>' : ''}</div>${reportOnly ? '' : eNote(a.do)}</li>`).join('') + '</ol>' : ''}
    ${main.length > acts.length ? `<div class="dimmer small">${main.length - acts.length} more under All findings.</div>` : ''}</div>`;
  const fu = r.follow_up;
  if (fu && (fu.rows || []).length) {
    h += `<div class="card next"><h2>Did it help? <span class="dimmer small">against the session before this one in ${esc(r.meta.build || 'this build')}: ${esc(fu.title || fu.session)}, ${esc(new Date(fu.created).toLocaleString('en-AU'))}</span></h2>
      <table><tr><th>what</th><th class="n">then</th><th class="n">now</th><th></th><th>what the last report said to do</th></tr>
      ${fu.rows.map(x => { const t = FOLLOW_TAG[x.verdict] || FOLLOW_TAG.same; return `<tr><td>${esc(x.label)}</td><td class="n">${followNum(x.was, x.unit)}</td><td class="n">${followNum(x.now, x.unit)}</td><td><span class="tag ${t[0]}">${t[1]}</span></td>
        <td class="dim">${x.text ? esc(x.text) + (x.done === 'yes' ? ' <span class="tag g">done</span>' : x.done === 'no' ? ' <span class="tag">not done</span>' : '') : ''}</td></tr>`; }).join('')}</table>
      ${(fu.changed || []).length ? `<div class="dim small" style="margin-top:8px">Experiments changed since then: ${fu.changed.map(esc).join(' · ')}</div>` : ''}
      ${fu.note ? `<div class="dimmer small" style="margin-top:6px">${esc(fu.note)}</div>` : ''}</div>`;
  }
  return h;
}
// where a "Do this next" button leads: an Experiments setting (opened and scrolled to), a tab, or a Nexus page
function goAction(go) {
  if (!go) return;
  if (go.startsWith('setting:')) { api('open', { what: 'setting', id: go.slice(8) }).catch(e => toast(e.message, true)); return; }
  if (go === 'record') { showTab('capture'); return; }
  if (go.startsWith('nexus:')) { api('open', { what: 'nexus', id: go.slice(6) }).catch(e => toast(e.message, true)); return; }
  if (go.startsWith('exp:')) {
    const id = go.slice(4);
    S.exOpen = S.exOpen || new Set(); S.exOpen.add(id);
    showTab('experiments');
    let n = 0;
    const t = setInterval(() => {
      const el = document.querySelector('.exl[data-x="' + id + '"]');
      if (!el && ++n < 30) return;
      clearInterval(t);
      if (el) { el.scrollIntoView({ block: 'center' }); el.classList.add('flash'); setTimeout(() => el.classList.remove('flash'), 2500); }
    }, 200);
    return;
  }
  showTab(go);
}
document.addEventListener('click', (e) => { const b = e.target.closest('[data-go]'); if (b) { e.preventDefault(); goAction(b.dataset.go); } });

function renderModTable(el, mods, limit) {
  const rows = mods.slice(0, limit || 9999);
  el.innerHTML = rows.length ? `<table><tr><th>mod (library zip)</th><th>kind</th><th class="n">REDscript ms/s</th><th class="n">CET ms/s</th><th class="n">files loaded</th><th class="n">in spikes</th></tr>
    ${rows.map(m => `<tr><td>${esc(m.name)}${m.note ? '<div class="dimmer small">' + esc(m.note) + '</div>' : ''}</td><td class="dim">${esc((m.kinds || []).join(', '))}</td>
      <td class="n">${m.redscript_ms_per_s ? m.redscript_ms_per_s.toFixed(2) : '–'}</td><td class="n">${m.cet_ms_per_s ? m.cet_ms_per_s.toFixed(2) : '–'}</td>
      <td class="n">${m.files_loaded ? fmt.n(m.files_loaded) : '–'}</td><td class="n">${m.in_spikes || '–'}</td></tr>`).join('')}</table>` :
    '<div class="dim">No per-mod data in this session (no part marked with ' + esc(repKeys(REPORT || S.cur).mark) + ' or script time off, no mod files loaded).</div>';
}

function drawCharts(el, r) {
  const tl = r.timeline || {};
  const W = Math.max(400, $('#ch-main', el).clientWidth - 4);
  const axisStroke = css('--dim'), grid = { stroke: css('--rule'), width: 1 }, axFont = '12.5px "JetBrains Mono", Consolas, monospace';
  // time labels: whole seconds normally, tenths once zoomed in to under half a minute
  const tvals = (u, v) => { const span = u.scales.x.max - u.scales.x.min; return v.map(x => span < 30 ? fmt.t(x) : fmt.t(x).replace(/\.\d$/, '')); };
  const axes = (yl) => [{ stroke: axisStroke, font: axFont, labelFont: axFont, grid, ticks: grid, values: tvals }, { stroke: axisStroke, font: axFont, labelFont: axFont, grid, ticks: grid, label: yl, size: 50 }];
  const sync = uPlot.sync('fl');
  // a drag has to be at least 10 px to zoom, so a click (which often moves a pixel) never zooms in by accident
  const tFull = [Math.min(...[tl.t, tl.vram_t, tl.samp_t].filter(a => a && a.length).map(a => a[0])), Math.max(...[tl.t, tl.vram_t, tl.samp_t].filter(a => a && a.length).map(a => a[a.length - 1]))];
  const at = (ts, vs, t) => { if (!ts || !ts.length || !vs) return null; let lo = 0, hi = ts.length - 1; while (lo < hi) { const m = (lo + hi) >> 1; if (ts[m] < t) lo = m + 1; else hi = m; } if (lo > 0 && Math.abs(ts[lo - 1] - t) < Math.abs(ts[lo] - t)) lo--; return Math.abs(ts[lo] - t) > 2 ? null : vs[lo]; };
  const readout = (t, into) => {
    const box = into || $('#ch-read', el);
    if (!box || t == null || !isFinite(t)) return;
    const f = at(tl.t, tl.frame_ms, t), g = at(tl.t, tl.gpu_ms, t), vr = at(tl.vram_t, tl.vram_mb, t), ck = at(tl.samp_t, tl.clock_mhz, t), tc = at(tl.samp_t, tl.temp_c, t),
      pw = at(tl.samp_t, tl.power_w, t), dk = at(tl.samp_t, tl.disk_mbs, t), cp = at(tl.samp_t, tl.cpu_pct, t), og = at(tl.samp_t, tl.other_gpu_pct, t);
    const inList = (list) => (list || []).some(g => t >= g[0] && t <= g[1]);
    const parts = ['<b>' + fmt.t(t) + '</b>'];
    if (f != null) parts.push('frame ' + fmt.ms(f) + (f > 0 ? ' (' + (1000 / f).toFixed(0) + ' fps)' : ''));
    if (g != null && g >= 0) parts.push('GPU busy ' + fmt.ms(g));
    if (vr != null) parts.push('VRAM ' + fmt.mb(vr));
    const vro = at(tl.vram_t, tl.vram_others_mb, t);
    if (vro != null && vro > 0) parts.push('rest of the card ' + fmt.mb(vro));
    if (ck != null) parts.push('GPU ' + fmt.n(Math.round(ck)) + ' MHz');
    if (tc != null) parts.push(tc.toFixed(0) + ' °C');
    if (pw != null) parts.push(pw.toFixed(0) + ' W');
    if (dk != null) parts.push('disk ' + dk.toFixed(0) + ' MB/s');
    if (cp != null) parts.push('game CPU ' + cp.toFixed(0) + '%');
    if (og >= 1) parts.push('other apps on GPU ' + og.toFixed(0) + '%');
    const cc = at(tl.samp_t, tl.cpu_clock_mhz, t), dp = at(tl.samp_t, tl.dpc_pct, t), sh = at(tl.samp_t, tl.shared_mb, t);
    if (cc) parts.push('CPU ' + fmt.n(cc) + ' MHz');
    if (dp >= 1) parts.push('driver interrupts ' + dp.toFixed(1) + '%');
    if (sh >= 50) parts.push(fmt.n(sh) + ' MB GPU memory in RAM');
    const rf = at(tl.samp_t, tl.ram_free_mb, t);
    if (rf != null && rf > 0) parts.push((rf < 1024 ? '<b style="color:var(--red)">' : '') + 'RAM free ' + fmt.mb(rf) + (rf < 1024 ? '</b>' : ''));
    const rs = (tl.resets || []).find(x => Math.abs(x.t - t) < Math.max(0.5, (S.mainChart ? (S.mainChart.scales.x.max - S.mainChart.scales.x.min) / 300 : 1)));
    if (rs) parts.push('<span style="color:var(--c-reset)">screen reset ' + esc(rs.clock) + '</span>');
    if (inList(tl.not_playing)) parts.push('<span class="dimmer">loading / menu</span>');
    if (inList(tl.focus)) parts.push('<span style="color:var(--accent)">' + esc(repKeys(r).mark) + ' mark</span>');
    box.innerHTML = parts.join(' · ');
  };
  const zoomed = (u) => { const b = $('#ch-all', el); if (b) b.style.display = (u.scales.x.max - u.scales.x.min) < (tFull[1] - tFull[0]) * 0.98 ? '' : 'none'; };
  const hooksAll = { setCursor: [(u) => { if (u.cursor.left >= 0) readout(u.posToVal(u.cursor.left, 'x')); }], setScale: [(u, k) => { if (k === 'x') zoomed(u); }] };
  const cursor = { sync: { key: 'fl' }, drag: { x: true, y: false, dist: 10 } };
  const gaps = tl.gaps || [], notPlay = tl.not_playing || [], focus = tl.focus || [];
  const shadeGaps = (u) => {
    const c = u.ctx;
    c.save();
    const band = (list, fill) => { c.fillStyle = fill; list.forEach(g => { const a = u.valToPos(g[0], 'x', true), b = u.valToPos(g[1], 'x', true); c.fillRect(a, u.bbox.top, Math.max(1, b - a), u.bbox.height); }); };
    band(gaps, 'rgba(255,255,255,0.04)');
    band(notPlay, 'rgba(150,150,145,0.14)');
    band(focus, 'rgba(47,196,185,0.08)');
    (r.markers || []).forEach(m => {
      const x = u.valToPos(m.t, 'x', true);
      c.strokeStyle = m.kind === 'mark' ? css('--accent') : (m.kind === 'felt' ? lc('#e8a24a') : css('--line2'));
      if (m.kind === 'felt') { c.setLineDash([]); c.lineWidth = 2 * devicePixelRatio; } else { c.setLineDash([3, 4]); c.lineWidth = 1; }
      c.beginPath(); c.moveTo(x, u.bbox.top); c.lineTo(x, u.bbox.top + u.bbox.height); c.stroke();
    });
    // screen resets: solid where the game took memory, dashed where it gave it back
    (tl.resets || []).forEach(m => {
      if (!m.playing) return;
      const x = u.valToPos(m.t, 'x', true);
      c.strokeStyle = css('--c-reset'); c.globalAlpha = 0.85;
      c.setLineDash(m.release ? [4, 3] : []); c.lineWidth = 1.5 * devicePixelRatio;
      c.beginPath(); c.moveTo(x, u.bbox.top); c.lineTo(x, u.bbox.top + u.bbox.height); c.stroke();
      c.globalAlpha = 1;
    });
    c.restore();
  };
  // main chart
  if (tl.t && tl.t.length) {
    const fr = tl.frame_ms, maxF = Math.max(...fr);
    const yMax = Math.min(Math.max(60, maxF * 1.08), 400);
    const gpu = tl.gpu_ms.map(v => v < 0 ? null : v);
    const spikes = r.spikes || [];
    const mainOpts = (w, h, view) => ({
      width: w, height: h, cursor: view ? VIEW_CURSOR() : cursor, scales: { x: { time: false }, y: { range: (u, lo, hi) => [0, Math.min(Math.max(40, (hi || 0) * 1.12), yMax)] } }, axes: axes('ms'),
      series: [{}, { label: 'frame', stroke: css('--accent'), width: 1.2, points: { show: false } },
        { label: 'GPU', stroke: css('--orange'), width: 1, points: { show: false } }],
      hooks: {
        drawClear: [shadeGaps],
        setCursor: view ? [(u) => { if (u.cursor.left >= 0) readout(u.posToVal(u.cursor.left, 'x'), view.read); }] : hooksAll.setCursor,
        setScale: view ? [] : hooksAll.setScale,
        draw: [(u) => {
          const c = u.ctx;
          c.save();
          c.lineWidth = 3 * devicePixelRatio;
          spikes.forEach(s => {
            const x = u.valToPos(s.t, 'x', true), y = u.valToPos(Math.min(s.ms, yMax), 'y', true), y0 = u.valToPos(0, 'y', true);
            c.strokeStyle = gcol(s.group);
            c.beginPath(); c.moveTo(x, y0); c.lineTo(x, y); c.stroke();
          });
          [16.7, 33.3, 50].forEach(v => {
            const y = u.valToPos(v, 'y', true);
            c.strokeStyle = 'rgba(255,255,255,0.10)'; c.setLineDash([3, 3]); c.lineWidth = 1;
            c.beginPath(); c.moveTo(u.bbox.left, y); c.lineTo(u.bbox.left + u.bbox.width, y); c.stroke();
          });
          c.restore();
        }],
      },
    });
    const spikeAt = (u) => {
      const t = u.posToVal(u.cursor.left, 'x');
      let best = null, bd = 1e9;
      spikes.forEach(s => { const d = Math.abs(s.t - t); if (d < bd) { bd = d; best = s; } });
      const span = u.scales.x.max - u.scales.x.min;
      return best && bd < span / 50 ? best : null;
    };
    const u = new uPlot(mainOpts(W, 300), [tl.t, fr, gpu], $('#ch-main', el));
    u.over.addEventListener('click', () => { const s = spikeAt(u); if (s) selectSpike(el, r, s.id); });
    sync.sub(u);
    S.charts.push(u);
    S.mainChart = u;
    addExpand($('#ch-main', el), {
      title: 'Frame time', legend: $('.chart-top .legend', el), xFull: tFull, minSpan: 0.05, yKeys: ['y'],
      from: () => [u.scales.x.min, u.scales.x.max],
      build: (host, w, h, view) => {
        const v = new uPlot(mainOpts(w, h, view), [tl.t, fr, gpu], host);
        v.over.addEventListener('click', () => {
          if (v.flMoved) return;
          const s = spikeAt(v);
          if (!s) return;
          selectSpike(el, r, s.id);
          view.read.innerHTML = '<b>Spike ' + s.id + '</b> · ' + fmt.t(s.t) + ' · ' + fmt.ms(s.ms) + ' · <span class="swatch" style="background:' + gcol(s.group) + '"></span>' + esc(s.cause) + ' <span class="dimmer">(details are under the charts when you close this)</span>';
        });
        return v;
      },
    });
  } else $('#ch-main', el).innerHTML = '<div class="empty">No frame data.</div>';
  const small = (id, data, series, scales, axesDef) => {
    const host = $(id, el);
    if (!data[0] || !data[0].length) { host.innerHTML = '<div class="empty" style="padding:20px">Not recorded.</div>'; return; }
    const keep = dupOpts({ series, scales, axesDef }); // the viewer builds its own chart from this copy (uPlot changes the objects it gets)
    const w = Math.max(250, host.clientWidth - 4);
    const u = new uPlot({ width: w, height: 150, cursor, scales: Object.assign({ x: { time: false } }, scales), axes: axesDef, series: [{}].concat(series), hooks: { drawClear: [shadeGaps], setCursor: hooksAll.setCursor, setScale: hooksAll.setScale } }, data, host);
    sync.sub(u);
    S.charts.push(u);
    const card = host.closest('.card');
    addExpand(host, {
      title: card && $('.legend span', card) ? $('.legend span', card).textContent.trim() : '', legend: card ? $('.legend', card) : null,
      xFull: tFull, minSpan: 0.2, yKeys: Object.keys(scales).filter(k => k !== 'x').concat(Object.keys(scales).includes('y') ? [] : ['y']),
      from: () => [u.scales.x.min, u.scales.x.max],
      build: (vh, vw, hh, view) => { const o = dupOpts(keep); return new uPlot({ width: vw, height: hh, cursor: VIEW_CURSOR(), scales: Object.assign({ x: { time: false } }, o.scales), axes: o.axesDef, series: [{}].concat(o.series),
        hooks: { drawClear: [shadeGaps], setCursor: [(v) => { if (v.cursor.left >= 0) readout(v.posToVal(v.cursor.left, 'x'), view.read); }] } }, data, vh); },
    });
  };
  const ax2 = (l1, l2) => [{ stroke: axisStroke, font: axFont, labelFont: axFont, grid, ticks: grid, values: tvals },
    { stroke: axisStroke, font: axFont, labelFont: axFont, grid, ticks: grid, label: l1, size: 50, scale: 'a' }, { side: 1, stroke: axisStroke, font: axFont, labelFont: axFont, grid: { show: false }, label: l2, size: 54, scale: 'b' }];
  const vo = (tl.vram_others_mb || []).length === (tl.vram_t || []).length && (tl.vram_t || []).length > 0;
  const gAx = (u, v) => v.map(x => (x / 1024).toFixed(1) + 'G');
  small('#ch-vram', vo ? [tl.vram_t, tl.vram_mb || [], tl.budget_mb || [], tl.vram_others_mb] : [tl.vram_t || [], tl.vram_mb || [], tl.budget_mb || []],
    [{ stroke: lc('#8cc4ff'), width: 1.3, scale: 'y', points: { show: false } }, { stroke: css('--red'), width: 1, dash: [5, 4], scale: 'y', points: { show: false } }]
      .concat(vo ? [{ stroke: lc('#f0a6ff'), width: 1, scale: 'o', points: { show: false } }] : []),
    { y: { range: (u, a, b) => [Math.max(0, Math.min(a, b) * 0.9), Math.max(a, b) * 1.04] }, o: { range: (u, a, b) => [0, Math.max(1024, b * 1.25)] } },
    [{ stroke: axisStroke, font: axFont, labelFont: axFont, grid, ticks: grid, values: tvals }, { stroke: axisStroke, font: axFont, labelFont: axFont, grid, ticks: grid, size: 50, values: gAx }]
      .concat(vo ? [{ side: 1, stroke: axisStroke, font: axFont, labelFont: axFont, grid: { show: false }, size: 45, scale: 'o', values: gAx }] : []));
  {
    const hc = (tl.clock_mhz || []).length > 0, ht = (tl.temp_c || []).length > 0;
    const clockS = { stroke: lc('#c3a6ff'), width: 1, scale: 'a', points: { show: false } }, tempS = { stroke: lc('#ff9a6b'), width: 1, scale: 'b', points: { show: false } };
    const sc = { a: { range: (u, a, b) => [Math.max(0, a * 0.9), b * 1.05] }, b: { range: (u, a, b) => [Math.max(0, a - 5), b + 5] } };
    const one = (scale, label) => [{ stroke: axisStroke, font: axFont, labelFont: axFont, grid, ticks: grid, values: tvals }, { stroke: axisStroke, font: axFont, labelFont: axFont, grid, ticks: grid, label, size: 50, scale }];
    if (hc && ht) small('#ch-gpu', [tl.samp_t, tl.clock_mhz, tl.temp_c], [clockS, tempS], sc, ax2('MHz', '°C'));
    else if (hc) small('#ch-gpu', [tl.samp_t, tl.clock_mhz], [clockS], sc, one('a', 'MHz'));
    else if (ht) small('#ch-gpu', [tl.samp_t, tl.temp_c], [tempS], sc, one('b', '°C'));
    else { const h = $('#ch-gpu', el); if (h) h.innerHTML = '<div class="empty" style="padding:20px">This card\'s driver gives no clock or temperature to Frame Lab.</div>'; }
  }
  small('#ch-pc', [tl.samp_t || [], tl.disk_mbs || [], tl.cpu_pct || [], tl.other_gpu_pct || []],
    [{ stroke: css('--amber'), width: 1, scale: 'a', points: { show: false } }, { stroke: lc('#7ee0c8'), width: 1, scale: 'b', points: { show: false } },
      { stroke: lc('#ff9aa8'), width: 1, scale: 'b', points: { show: false } }],
    { a: { range: (u, a, b) => [0, Math.max(50, b * 1.1)] }, b: { range: [0, 100] } }, ax2('MB/s', '%'));
  if ((tl.cpu_clock_mhz || []).length) {
    small('#ch-cpu', [tl.samp_t, tl.cpu_clock_mhz, tl.cores_busy],
      [{ stroke: lc('#9fe870'), width: 1, scale: 'a', points: { show: false } }, { stroke: lc('#ffb36b'), width: 1, scale: 'b', points: { show: false } }],
      { a: { range: (u, a, b) => [Math.max(0, a * 0.9), b * 1.05] }, b: { range: (u, a, b) => [0, Math.max(4, b + 1)] } }, ax2('MHz', 'cores'));
    const ramFree = (tl.ram_free_mb || []).length === tl.samp_t.length;
    small('#ch-win', ramFree ? [tl.samp_t, tl.dpc_pct, tl.shared_mb, tl.ram_free_mb] : [tl.samp_t, tl.dpc_pct, tl.shared_mb],
      [{ stroke: lc('#ff6b8a'), width: 1, scale: 'a', points: { show: false } }, { stroke: lc('#b0a0ff'), width: 1, scale: 'b', points: { show: false } }]
        .concat(ramFree ? [{ stroke: lc('#9adf8f'), width: 1, scale: 'b', points: { show: false } }] : []),
      { a: { range: (u, a, b) => [0, Math.max(5, b * 1.2)] }, b: { range: (u, a, b) => [0, Math.max(100, b * 1.1)] } }, ax2('%', 'MB'));
  } else {
    ['#ch-cpu', '#ch-win'].forEach(id => { const h = $(id, el); if (h) h.innerHTML = '<div class="empty" style="padding:20px">Not recorded in this session.</div>'; });
  }
  if ((tl.disp_t || []).length) {
    const held = (tl.disp_held || []).length === tl.disp_t.length;
    small('#ch-disp', held ? [tl.disp_t, tl.disp_fps, tl.disp_held] : [tl.disp_t, tl.disp_fps],
      [{ stroke: lc('#6fd3ff'), width: 1, scale: 'a', points: { show: false } }].concat(held ? [{ stroke: lc('#ff8a9a'), width: 1, scale: 'b', points: { show: false } }] : []),
      { a: { range: (u, a, b) => [0, Math.max(60, b * 1.1)] }, b: { range: (u, a, b) => [0, Math.max(20, b * 1.1)] } }, ax2('fps', '%'));
  } else { const h = $('#ch-disp', el); if (h) h.innerHTML = '<div class="empty" style="padding:20px">Not recorded in this session (PresentMon).</div>'; }
  const showAll = () => S.charts.forEach(u => u.setScale('x', { min: tFull[0], max: tFull[1] }));
  const btn = $('#ch-all', el);
  if (btn) btn.onclick = showAll;
  S.charts.forEach(u => u.over.addEventListener('dblclick', showAll));
  // 2.5: the L-shape (own x axis: not zoomed or synced with the time charts)
  const lh = $('#ch-lshape', el);
  if (lh) {
    const lu = lshapeChart(lh, [{ label: 'frame time', color: css('--accent'), pts: r.lshape }], Math.max(300, lh.clientWidth - 4), 230,
      (i, p, vals) => { const box = $('#ls-read', el); if (box) box.innerHTML = lsHead(p) + ': <b>' + fmt.ms(vals[0]) + '</b>' + (vals[0] > 0 ? ' (' + (1000 / vals[0]).toFixed(0) + ' fps)' : ''); });
    if (lu) {
      (S.lcharts = S.lcharts || []).push(lu); // not in S.charts: those share the time axis
      addExpand(lh, { title: 'L-shape: frame time at each percentile', legend: lh.closest('.card') && $('.legend', lh.closest('.card')), xFull: [lsx(0), lsx(99.99)], minSpan: 0.05, yKeys: ['y'],
        build: (vh, w, h, view) => lshapeChart(vh, [{ label: 'frame time', color: css('--accent'), pts: r.lshape }], w, h,
          (i, p, vals) => { view.read.innerHTML = lsHead(p) + ': <b>' + fmt.ms(vals[0]) + '</b>' + (vals[0] > 0 ? ' (' + (1000 / vals[0]).toFixed(0) + ' fps)' : ''); }, true) });
    } else lh.innerHTML = '<div class="empty" style="padding:20px">' + (REPORT ? 'Not in this report.' : 'Re-analyse this session to get the L-shape.') + '</div>';
  }
}

/* ------------------------------------------------------------------ chart viewer (2.31) */
// Every chart has an open-larger button. The larger chart zooms with the mouse wheel (time) or Ctrl + wheel
// (height), moves when dragged, and goes back to the whole session on double-click or Reset.
const VIEW_CURSOR = () => ({ drag: { x: false, y: false, setScale: false }, points: { size: 6 } });
const dupOpts = (v) => Array.isArray(v) ? v.map(dupOpts) : (v && typeof v === 'object' && v.constructor === Object) ? Object.fromEntries(Object.entries(v).map(([k, x]) => [k, dupOpts(x)])) : v;
const CV = { u: null, opt: null, ctl: null };

function addExpand(host, opt) {
  if (!host) return;
  const card = host.closest('.card') || host.parentElement;
  if (!card) return;
  const old = $('.cv-open', card);
  if (old) old.remove();
  const b = document.createElement('button');
  b.className = 'cv-open';
  b.title = 'Open larger: zoom in and out, move along the session';
  b.setAttribute('aria-label', 'Open this chart larger');
  b.innerHTML = '<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"><path d="M2 6V2h4M10 2h4v4M14 10v4h-4M6 14H2v-4" fill="none" stroke="currentColor" stroke-width="1.6"/></svg>';
  b.onclick = (e) => { e.stopPropagation(); openChartView(opt); };
  card.classList.add('has-cv');
  card.appendChild(b);
}

function chartViewEl() {
  let v = $('#chartview');
  if (v) return v;
  v = document.createElement('div');
  v.id = 'chartview';
  v.className = 'chartview hidden';
  v.innerHTML = `<div class="cv-box" role="dialog" aria-modal="true" aria-label="Chart">
    <div class="cv-top"><div class="cv-title"></div><div class="cv-tools">
      <button class="btn2 small" data-cv="out" title="Zoom out (-)">&minus;</button><button class="btn2 small" data-cv="in" title="Zoom in (+)">+</button>
      <button class="btn2 small" data-cv="left" title="Move left (&larr;)">&larr;</button><button class="btn2 small" data-cv="right" title="Move right (&rarr;)">&rarr;</button>
      <button class="btn2 small" data-cv="reset" title="Whole session (0 or double-click)">Reset</button><button class="btn2 small" data-cv="close" title="Close (Esc)">Close</button></div></div>
    <div class="legend cv-legend"></div><div class="readout cv-read"></div><div class="cv-host"></div>
    <div class="cv-hint dimmer small">Mouse wheel: zoom in and out · drag: move · Ctrl + wheel: zoom the height · double-click or Reset: whole session · Esc: close</div></div>`;
  document.body.appendChild(v);
  v.addEventListener('mousedown', (e) => { if (e.target === v) closeChartView(); });
  $$('[data-cv]', v).forEach(b => b.onclick = () => {
    const a = b.dataset.cv, c = CV.ctl;
    if (a === 'close') return closeChartView();
    if (!c) return;
    if (a === 'in') c.zoom(0.6); else if (a === 'out') c.zoom(1 / 0.6); else if (a === 'left') c.pan(-0.25); else if (a === 'right') c.pan(0.25); else if (a === 'reset') c.reset();
  });
  return v;
}

function openChartView(opt) {
  const v = chartViewEl();
  closeChartView(true);
  v.classList.remove('hidden');
  $('.cv-title', v).textContent = opt.title || '';
  $('.cv-legend', v).innerHTML = opt.legend ? opt.legend.innerHTML : '';
  const read = $('.cv-read', v);
  read.innerHTML = '<span class="dimmer">Move the mouse over the chart to read the values.</span>';
  const host = $('.cv-host', v);
  host.innerHTML = '';
  const size = () => [Math.max(320, host.clientWidth - 4), Math.max(220, Math.min(window.innerHeight - 260, 900))];
  const [w, h] = size();
  const u = opt.build(host, w, h, { read });
  if (!u) { host.innerHTML = '<div class="empty">Nothing to show.</div>'; return; }
  CV.u = u; CV.opt = opt;
  CV.ctl = panZoom(u, opt);
  const from = opt.from && opt.from();
  if (from && isFinite(from[0]) && isFinite(from[1]) && (from[1] - from[0]) < (opt.xFull[1] - opt.xFull[0]) * 0.98) CV.ctl.setX(from[0], from[1]); // open at the same zoom as the page
  CV.resize = () => { if (CV.u) { const [a, b] = size(); CV.u.setSize({ width: a, height: b }); } };
  window.addEventListener('resize', CV.resize);
}

function closeChartView(quiet) {
  const v = $('#chartview');
  if (CV.u) { CV.u.destroy(); CV.u = null; }
  if (CV.resize) window.removeEventListener('resize', CV.resize);
  CV.ctl = null; CV.opt = null; CV.resize = null;
  if (v && !quiet) v.classList.add('hidden');
}

document.addEventListener('keydown', (e) => {
  const v = $('#chartview');
  if (!v || v.classList.contains('hidden') || !CV.ctl) return;
  if (e.target && /^(INPUT|SELECT|TEXTAREA)$/.test(e.target.tagName)) return;
  const k = e.key, c = CV.ctl;
  if (k === 'Escape') closeChartView();
  else if (k === '+' || k === '=') c.zoom(0.6);
  else if (k === '-' || k === '_') c.zoom(1 / 0.6);
  else if (k === 'ArrowLeft') c.pan(-0.15);
  else if (k === 'ArrowRight') c.pan(0.15);
  else if (k === '0' || k === 'Home') c.reset();
  else return;
  e.preventDefault();
});

// opt: xFull [min, max] of the data, minSpan (smallest x window), yKeys (scales the height zoom applies to)
function panZoom(u, opt) {
  const full = opt.xFull, over = u.over;
  let yz = null; // { scale: [min, max] } once the height has been zoomed
  const cur = () => [u.scales.x.min, u.scales.x.max];
  const applyY = () => { if (yz) Object.entries(yz).forEach(([k, r]) => { if (u.scales[k]) u.setScale(k, { min: r[0], max: r[1] }); }); };
  const setX = (a, b) => {
    const whole = full[1] - full[0];
    let span = Math.min(whole, Math.max(opt.minSpan || 0, b - a));
    a = Math.max(full[0], Math.min(a, full[1] - span));
    u.setScale('x', { min: a, max: a + span });
    applyY();
  };
  const zoom = (f, at) => { const [a, b] = cur(); if (at == null) at = (a + b) / 2; setX(at - (at - a) * f, at + (b - at) * f); };
  const pan = (frac) => { const [a, b] = cur(); const d = (b - a) * frac; setX(a + d, b + d); };
  const zoomY = (f, fy) => {
    yz = yz || {};
    (opt.yKeys || ['y']).forEach(k => {
      const s = u.scales[k];
      if (!s || s.min == null || s.max == null) return;
      const at = s.min + (s.max - s.min) * fy;
      yz[k] = [at - (at - s.min) * f, at + (s.max - at) * f];
    });
    applyY();
  };
  const reset = () => { yz = null; u.setScale('x', { min: full[0], max: full[1] }); };
  over.addEventListener('wheel', (e) => {
    e.preventDefault();
    const rc = over.getBoundingClientRect(), f = e.deltaY > 0 ? 1.25 : 0.8;
    if (e.ctrlKey || e.metaKey || e.shiftKey) zoomY(f, 1 - (e.clientY - rc.top) / rc.height);
    else zoom(f, u.posToVal(e.clientX - rc.left, 'x'));
  }, { passive: false });
  over.addEventListener('mousedown', (e) => {
    if (e.button !== 0) return;
    const x0 = e.clientX, y0 = e.clientY, [a, b] = cur(), w = over.clientWidth, h = over.clientHeight;
    const y0z = yz ? dupOpts(yz) : null;
    u.flMoved = false;
    over.classList.add('cv-grab');
    const mv = (ev) => {
      const dx = ev.clientX - x0, dy = ev.clientY - y0;
      if (Math.abs(dx) + Math.abs(dy) > 3) u.flMoved = true;
      if (!u.flMoved) return;
      if (y0z) Object.entries(y0z).forEach(([k, r]) => { const d = dy / h * (r[1] - r[0]); yz[k] = [r[0] + d, r[1] + d]; });
      const d = -dx / w * (b - a);
      setX(a + d, b + d);
    };
    const up = () => { window.removeEventListener('mousemove', mv); window.removeEventListener('mouseup', up); over.classList.remove('cv-grab'); setTimeout(() => { u.flMoved = false; }, 0); };
    window.addEventListener('mousemove', mv);
    window.addEventListener('mouseup', up);
  });
  over.addEventListener('dblclick', reset);
  return { zoom, pan, reset, setX };
}

/* ------------------------------------------------------------------ L-shape (2.5) */
// x = -log10(100 - percentile): 0 / 90 / 99 / 99.9 / 99.99 sit evenly apart, so the slow end gets room
const lsx = (p) => -Math.log10(Math.max(0.01, 100 - p));
const LS_TICKS = [[0, '0'], [90, '90'], [99, '99'], [99.9, '99.9'], [99.99, '99.99']];
const lsHead = (p) => { const rest = +(100 - p).toFixed(3); return p === 0 ? '<b>fastest frame</b>' : `<b>${p}th percentile</b> · ${rest}% of frames took longer than`; };

// sets: [{label, color, dash, pts: [[percentile, ms], ...]}]; onHover(index, percentile, [ms per set])
function lshapeChart(host, sets, width, height, onHover, free) { // free: the chart viewer zooms and moves it
  const base = sets.find(x => x.pts && x.pts.length);
  if (!base) return null;
  const pcts = base.pts.map(q => q[0]);
  const xs = pcts.map(lsx);
  const ys = sets.map(x => { const m = new Map((x.pts || []).map(q => [q[0], q[1]])); return pcts.map(p => m.has(p) ? m.get(p) : null); });
  const all = ys.flat().filter(v => v != null);
  const yTop = Math.min(Math.max(20, Math.max(...all) * 1.08), 250);
  const axisStroke = css('--dim'), grid = { stroke: css('--rule'), width: 1 }, axFont = '12.5px "JetBrains Mono", Consolas, monospace';
  const u = new uPlot({
    width, height, legend: { show: false },
    cursor: { drag: { x: false, y: false, setScale: false }, points: { size: 7 } },
    scales: free ? { x: { time: false }, y: { range: () => [0, yTop] } } : { x: { time: false, range: [lsx(0), lsx(99.99)] }, y: { range: [0, yTop] } },
    axes: [{ stroke: axisStroke, font: axFont, labelFont: axFont, grid, ticks: grid, label: 'percentile of frames (slowest on the right)', labelSize: 20,
      splits: () => LS_TICKS.map(t => lsx(t[0])), values: (u, v) => v.map(x => { const t = LS_TICKS.find(t => Math.abs(lsx(t[0]) - x) < 1e-6); return t ? t[1] : ''; }) },
    { stroke: axisStroke, font: axFont, labelFont: axFont, grid, ticks: grid, label: 'ms', size: 50 }],
    series: [{}].concat(sets.map(x => ({ label: x.label, stroke: x.color, width: 2, dash: x.dash || undefined, points: { show: false }, spanGaps: true }))),
    hooks: {
      draw: [(u) => {
        const c = u.ctx;
        c.save();
        [16.7, 33.3, 50].forEach(v => {
          if (v > yTop) return;
          const y = u.valToPos(v, 'y', true);
          c.strokeStyle = 'rgba(255,255,255,0.10)'; c.setLineDash([3, 3]); c.lineWidth = 1;
          c.beginPath(); c.moveTo(u.bbox.left, y); c.lineTo(u.bbox.left + u.bbox.width, y); c.stroke();
        });
        c.restore();
      }],
      setCursor: [(u) => { const i = u.cursor.idx; if (i != null && onHover) onHover(i, pcts[i], ys.map(a => a[i])); }],
    },
  }, [xs].concat(ys), host);
  return u;
}

// the percentiles people quote, from the L-shape
function pctTable(r) {
  const l = r.lshape || [];
  if (!l.length) return '<div class="dim">' + (REPORT ? 'Not in this report.' : 'Re-analyse this session to get the percentiles.') + '</div>';
  const at = (p) => { const q = l.find(x => Math.abs(x[0] - p) < 1e-9); return q ? q[1] : null; };
  const rows = [[50, 'median', 'half the frames'], [90, '', '1 in 10'], [95, '', '1 in 20'], [99, '1% low', '1 in 100'], [99.9, '0.1% low', '1 in 1,000'], [99.99, '0.01% low', '1 in 10,000']];
  return `<table><tr><th>percentile</th><th></th><th class="n">frame time</th><th class="n">fps</th><th class="n">slower</th></tr>${rows.map(([p, name, one]) => {
    const v = at(p);
    return `<tr><td class="n" style="text-align:left">${p}</td><td class="dim" style="white-space:nowrap">${name}</td><td class="n">${fmt.ms(v)}</td><td class="n">${v > 0 ? (1000 / v).toFixed(0) : '–'}</td><td class="n dim">${one}</td></tr>`;
  }).join('')}</table><div class="dimmer small" style="margin-top:8px">Frame time a given share of frames stayed under. "Slower" is how many frames took longer than that.</div>`;
}

window.addEventListener('resize', () => {
  clearTimeout(window._rs);
  window._rs = setTimeout(() => {
    if (REPORT) renderReport($('#report'), REPORT, true);
    else if (S.tab === 'sessions' && S.cur) renderReport($('#report'), S.cur, false);
  }, 200);
});

/* ------------------------------------------------------------------ compare */
// 2.5: up to six sessions side by side. The first one is the baseline every other one is measured
// against; "What changed" goes step by step (each session against the one before it), which is how
// a bisect reads. Colours are the six validated categorical slots; slots that are hard to tell apart
// with colour-blindness also get a dash, and every line has its number in the legend and readout.
const CMP_MAX = 6;
const CMP_COL = ['#3987e5', '#d95926', '#199e70', '#c98500', '#d55181', '#9085e9'];
const CMP_DASH = [null, null, null, [7, 4], [2, 3], [7, 4]];
const cmpSw = (i) => `<svg width="24" height="10" style="vertical-align:-1px" aria-hidden="true"><line x1="1" y1="5" x2="23" y2="5" stroke="${CMP_COL[i]}" stroke-width="2.5" ${CMP_DASH[i] ? 'stroke-dasharray="' + CMP_DASH[i].join(',') + '"' : ''}/></svg>`;
const cmpTag = (i) => `<span class="cmp-n" style="border-color:${CMP_COL[i]}">${i + 1}</span>`;

function cmpChanges(A, B) {
  const out = [];
  const ea = (A.meta.experiments || []), eb = (B.meta.experiments || []);
  eb.filter(x => !ea.includes(x)).forEach(x => out.push('<div><span class="tag g">Experiments on</span> ' + esc(x) + '</div>'));
  ea.filter(x => !eb.includes(x)).forEach(x => out.push('<div><span class="tag">Experiments off</span> ' + esc(x) + '</div>'));
  if (A.meta.build && B.meta.build && A.meta.build !== B.meta.build) out.push(`<div><span class="tag y">other build</span> ${esc(A.meta.build)} → ${esc(B.meta.build)} (different mods and settings: this compares the two builds rather than one change)</div>`);
  const sa = A.snapshot, sb = B.snapshot;
  if (!sa || !sb) { out.push('<div class="dim">Mods and settings: needs both sessions recorded with Frame Lab 2.0 or newer.</div>'); return out.join(''); }
  [['Game', 'game_version'], ['Graphics driver', 'driver'], ['Windows', 'windows']].forEach(([l, k]) => {
    if (sa[k] && sb[k] && sa[k] !== sb[k]) out.push(`<div><span class="tag">${l}</span> ${esc(sa[k])} → ${esc(sb[k])}</div>`);
  });
  const ma = {}, mb = {};
  (sa.mods || []).forEach(m => { ma[m.path] = m; });
  (sb.mods || []).forEach(m => { mb[m.path] = m; });
  const lst = (arr, tag, cls) => arr.slice(0, 30).map(p => `<div><span class="tag ${cls}">${tag}</span> ${esc(p)}</div>`).join('') + (arr.length > 30 ? `<div class="dimmer small">and ${arr.length - 30} more</div>` : '');
  out.push(lst(Object.keys(mb).filter(p => !ma[p]), 'mod added', 'g'), lst(Object.keys(ma).filter(p => !mb[p]), 'mod removed', ''),
    lst(Object.keys(mb).filter(p => ma[p] && (ma[p].size !== mb[p].size || ma[p].mtime !== mb[p].mtime)), 'mod updated', 'y'));
  const setA = sa.settings || {}, setB = sb.settings || {};
  Object.keys(setB).filter(k => k in setA && setA[k] !== setB[k]).slice(0, 30)
    .forEach(k => out.push(`<div><span class="tag y">setting</span> ${esc(k.replace(/^\//, ''))}: ${esc(setA[k])} → ${esc(setB[k])}</div>`));
  return out.join('') || '<div class="dim">Same game, driver, Windows, mods and game settings.</div>';
}

async function renderCompare() {
  const root = $('#tab-compare');
  if (!S.sessions.length) { try { S.sessions = (await api('sessions')) || []; } catch (e) { /* shown below */ } }
  const an = S.sessions.filter(s => s.analysed);
  if (an.length < 2) { root.innerHTML = '<div class="empty">Compare needs two analysed sessions (e.g. the same drive before and after a change).</div>'; return; }
  const have = new Set(an.map(s => s.id));
  S.cmpIds = (S.cmpIds || []).filter(id => have.has(id)).slice(0, CMP_MAX);
  if (S.cmpIds.length < 2) S.cmpIds = [an[1].id, an[0].id];
  S.cmpHide = S.cmpHide || new Set();
  const ids = S.cmpIds;
  const opts = (sel) => an.map(s => `<option value="${esc(s.id)}" ${s.id === sel ? 'selected' : ''}>${esc(s.title || s.id)} · ${esc(s.created)}</option>`).join('');
  root.innerHTML = `<div class="cmp-pick2">${ids.map((id, i) => `<div class="cmp-slot">${cmpSw(i)}${cmpTag(i)}<span class="dim small" style="width:58px">${i === 0 ? 'baseline' : ''}</span><select data-slot="${i}">${opts(id)}</select>${ids.length > 2 ? `<button class="btn2 small" data-rm="${i}" title="take out of the comparison">✕</button>` : ''}</div>`).join('')}
    ${ids.length < CMP_MAX && an.length > ids.length ? '<button class="btn2 small" id="cmp-add">+ Add a session</button>' : ''}
    <div class="dimmer small">Up to ${CMP_MAX}. The first is the baseline the others are measured against. Bisecting: add the sessions in the order you made them.</div></div><div id="cmp-body"><div class="empty">Loading ...</div></div>`;
  $$('select[data-slot]', root).forEach(sel => sel.onchange = (e) => { S.cmpIds[+e.target.dataset.slot] = e.target.value; renderCompare(); });
  $$('[data-rm]', root).forEach(b => b.onclick = () => { S.cmpIds.splice(+b.dataset.rm, 1); renderCompare(); });
  const addB = $('#cmp-add', root);
  if (addB) addB.onclick = () => { const next = an.find(s => !S.cmpIds.includes(s.id)); if (next) { S.cmpIds.push(next.id); renderCompare(); } };
  const get = async (id) => S.cmpData[id] || (S.cmpData[id] = await api('session/' + encodeURIComponent(id)));
  let R;
  try { R = await Promise.all(ids.map(get)); } catch (e) { $('#cmp-body').innerHTML = '<div class="empty">' + esc(e.message) + '</div>'; return; }
  if (S.cmpIds !== ids || S.tab !== 'compare') return; // changed while loading
  (S.cmpCharts || []).forEach(u => u.destroy());
  S.cmpCharts = [];
  const n = R.length, A = R[0];
  const head = (extra) => `<tr><th></th>${R.map((r, i) => `<th class="n" title="${esc(r.meta.title || r.meta.id)}">${cmpTag(i)}</th>`).join('')}${extra || ''}</tr>`;
  const better = (d, lowerBetter) => d === 0 ? '' : (lowerBetter ? d < 0 : d > 0) ? 'better' : 'worse';
  // value + change against the baseline
  const cell = (v, base, f, lowerBetter, i) => {
    if (v == null || !isFinite(v)) return '<td class="n dimmer">–</td>';
    if (i === 0 || base == null || !isFinite(base) || base === 0) return `<td class="n">${f(v)}</td>`;
    const pct = 100 * (v - base) / Math.abs(base);
    const cls = Math.abs(pct) < 0.05 ? '' : better(v - base, lowerBetter);
    return `<td class="n">${f(v)}<div class="delta ${cls} small">${pct > 0 ? '+' : ''}${pct.toFixed(1)}%</div></td>`;
  };
  const metric = (label, get, f, lowerBetter) => { const vals = R.map(get); return `<tr><td>${label}</td>${vals.map((v, i) => cell(v, vals[0], f, lowerBetter, i)).join('')}</tr>`; };
  const perMin = (k) => k.duration_s ? k.spikes / (k.duration_s / 60) : null;
  const lsAt = (r, p) => { const q = (r.lshape || []).find(x => Math.abs(x[0] - p) < 1e-9); return q ? q[1] : null; };
  // noise check against the baseline (30 s stretches of play)
  const bstat = (bl, key) => { const v = (bl || []).map(b => b[key]).filter(x => isFinite(x) && x > 0); const k = v.length; if (k < 3) return null; const m = v.reduce((a, b) => a + b, 0) / k; const sd = Math.sqrt(v.reduce((a, b) => a + (b - m) * (b - m), 0) / (k - 1)); return { m, se: sd / Math.sqrt(k) }; };
  const verdict = (a, b) => {
    if (!a || !b) return '<span class="dimmer">needs 90 s of play</span>';
    const d = b.m - a.m, se = Math.sqrt(a.se * a.se + b.se * b.se), t = se > 0 ? d / se : 0, pct = 100 * d / a.m;
    if (Math.abs(t) < 2 || Math.abs(pct) < 2) return '<span class="dim">within noise</span>';
    return `<b class="delta ${d < 0 ? 'better' : 'worse'}">${d < 0 ? 'better' : 'worse'} ${Math.abs(pct).toFixed(1)}%</b>`;
  };
  const vrow = (label, key) => { const a = bstat(A.blocks, key); return `<tr><td>${label}</td><td class="n dimmer">baseline</td>${R.slice(1).map(r => `<td class="n">${verdict(a, bstat(r.blocks, key))}</td>`).join('')}</tr>`; };

  let html = `<div class="card chart-card"><div class="chart-top"><div class="legend" id="cmp-legend">${R.map((r, i) => `<span class="cmp-leg ${S.cmpHide.has(ids[i]) ? 'off' : ''}" data-leg="${i}" title="click to hide or show">${cmpSw(i)}${cmpTag(i)} ${esc(r.meta.title || r.meta.id)}${r.lshape ? '' : ' <span class="dimmer">(no L-shape: Re-analyse it, if its recording is still there)</span>'}</span>`).join('')}</div></div>
    <div class="readout" id="cmp-read">The L-shape of each session: flat and low is smooth, the right end is the slowest frames. Move the mouse over the chart to read every session at one percentile; click a name to hide it.</div><div id="cmp-lshape"></div></div>`;
  // fps bars: average, 1% low, 0.1% low
  const bars = [['average', r => r.kpi.avg_fps], ['1% low', r => r.kpi.low1_fps], ['0.1% low', r => r.kpi.low01_fps]];
  const bmax = Math.max(1, ...R.map(r => r.kpi.avg_fps || 0)) * 1.05;
  html += `<div class="grid2" style="grid-template-columns:1fr 1.6fr"><div class="card"><h2>Frames per second</h2>${bars.map(([lab, f]) => `<div class="fbar-g"><div class="dim small">${lab}</div>${R.map((r, i) => { const v = f(r); return `<div class="fbar" title="${esc(r.meta.title || r.meta.id)}: ${v ? v.toFixed(1) : '–'} fps">${cmpTag(i)}<div class="fbar-t"><div style="width:${v ? Math.min(100, v / bmax * 100) : 0}%;background:${CMP_COL[i]}"></div></div><span class="n">${v ? v.toFixed(0) : '–'}</span></div>`; }).join('')}</div>`).join('')}
    <div class="dimmer small" style="margin-top:6px">Play only (loading screens and menus left out).</div></div>`;
  html += `<div class="card"><h2>Numbers <span class="dimmer small" style="text-transform:none;letter-spacing:0">change against the baseline</span></h2><div class="scroll-x"><table>${head()}
    ${metric('median frame (ms)', r => r.kpi.median_ms, v => fmt.p(v, 2), true)}
    ${metric('average fps', r => r.kpi.avg_fps, v => fmt.p(v, 0), false)}
    ${metric('1% low fps', r => r.kpi.low1_fps, v => fmt.p(v, 0), false)}
    ${metric('0.1% low fps', r => r.kpi.low01_fps, v => fmt.p(v, 0), false)}
    ${metric('99.99th percentile (ms)', r => lsAt(r, 99.99), v => fmt.p(v, 1), true)}
    ${metric('spikes per minute', r => perMin(r.kpi), v => fmt.p(v, 2), true)}
    ${metric('stutter %', r => r.kpi.stutter_pct, v => fmt.p(v, 2), true)}
    ${metric('frame time spread (ms)', r => r.kpi.frame_sd_ms, v => fmt.p(v, 2), true)}
    ${metric('input lag (ms)', r => r.kpi.input_lag_ms || null, v => fmt.p(v, 1), true)}
    ${metric('GPU bound %', r => r.kpi.gpu_bound_pct >= 0 ? r.kpi.gpu_bound_pct : null, v => fmt.p(v, 0), true)}
    ${metric('VRAM peak (MB)', r => r.kpi.vram_peak_mb || null, v => fmt.p(v, 0), true)}
    ${metric('script time (ms/s)', r => (r.kpi.redscript_ms_per_s || 0) + (r.kpi.cet_ms_per_s || 0), v => fmt.p(v, 1), true)}
    ${vrow('typical frame vs noise', 'median_ms')}${vrow('slow frames vs noise', 'p99_ms')}
    </table></div><div class="dimmer small" style="margin-top:8px">Only a fair comparison when the runs cover the same route and settings. "vs noise": each session is cut into 30 s stretches of play; a change counts when it is bigger than how much those stretches differ.</div></div></div>`;
  // what changed, step by step
  html += `<div class="card" style="margin-top:14px"><h2>What changed</h2>${R.slice(1).map((r, j) => `<div class="cmp-step"><div class="cmp-step-h">${cmpTag(j)} → ${cmpTag(j + 1)} <span class="dim">${esc(R[j].meta.title || R[j].meta.id)} → ${esc(r.meta.title || r.meta.id)}</span></div>${cmpChanges(R[j], r)}</div>`).join('')}</div>`;
  // spike causes and mods
  const groups = Object.keys(GROUPS).filter(g => g !== 'other');
  const cnt = (r, g) => { const c = (r.causes || []).find(x => x.group === g); return c ? c.count : 0; };
  const used = groups.filter(g => R.some(r => cnt(r, g) > 0));
  html += `<div class="grid2"><div class="card"><h2>Spike causes</h2>${used.length ? `<table>${head()}${used.map(g => metric('<span class="swatch" style="background:' + gcol(g) + '"></span>' + GROUPS[g].label, r => cnt(r, g), v => String(v), true)).join('')}</table>` : '<div class="dim">No spikes in any of these sessions.</div>'}</div>`;
  const mods = {};
  R.forEach((r, i) => (r.mods || []).forEach(m => { (mods[m.name] = mods[m.name] || [])[i] = m; }));
  const sc = (m) => m ? (m.redscript_ms_per_s || 0) + (m.cet_ms_per_s || 0) : null;
  const list = Object.entries(mods).map(([name, arr]) => ({ name, v: R.map((_, i) => sc(arr[i])), sp: R.map((_, i) => arr[i] ? arr[i].in_spikes || 0 : 0) }))
    .filter(x => x.v.some(v => (v || 0) > 0.2) || x.sp.some(v => v > 0)).sort((a, b) => Math.max(...b.v.map(v => v || 0)) - Math.max(...a.v.map(v => v || 0))).slice(0, 40);
  html += `<div class="card"><h2>Mods: script time (ms/s)</h2>${list.length ? `<div class="scroll"><table>${head()}${list.map(x => `<tr><td>${esc(x.name)}${x.sp.some(v => v) ? '<div class="dimmer small">in spikes: ' + x.sp.join(' · ') + '</div>' : ''}</td>${x.v.map((v, i) => v == null ? '<td class="n dimmer">not there</td>' : cell(v, x.v[0], y => y.toFixed(2), true, i)).join('')}</tr>`).join('')}</table></div>` : '<div class="dim">No per-mod script time in these sessions (needs marked parts).</div>'}</div></div>`;
  $('#cmp-body').innerHTML = html;

  // the overlaid L-shapes
  const host = $('#cmp-lshape');
  const u = lshapeChart(host, R.map((r, i) => ({ label: String(i + 1), color: CMP_COL[i], dash: CMP_DASH[i], pts: r.lshape })), Math.max(400, host.clientWidth - 4), 320,
    (i, p, vals) => { const box = $('#cmp-read'); if (box) box.innerHTML = lsHead(p) + ': ' + vals.map((v, j) => S.cmpHide.has(ids[j]) ? '' : `${cmpTag(j)} <b>${fmt.ms(v)}</b>`).filter(Boolean).join(' &nbsp; '); });
  if (!u) { host.innerHTML = '<div class="empty" style="padding:20px">None of these sessions has an L-shape yet: open each once (or press Re-analyse) and come back.</div>'; return; }
  S.cmpCharts.push(u);
  ids.forEach((id, i) => { if (S.cmpHide.has(id)) u.setSeries(i + 1, { show: false }); });
  addExpand(host, { title: 'Compare: L-shapes', legend: $('#cmp-legend'), xFull: [lsx(0), lsx(99.99)], minSpan: 0.05, yKeys: ['y'],
    build: (vh, w, h, view) => {
      const v = lshapeChart(vh, R.map((r, i) => ({ label: String(i + 1), color: CMP_COL[i], dash: CMP_DASH[i], pts: r.lshape })), w, h,
        (i, p, vals) => { view.read.innerHTML = lsHead(p) + ': ' + vals.map((x, j) => S.cmpHide.has(ids[j]) ? '' : `${cmpTag(j)} <b>${fmt.ms(x)}</b>`).filter(Boolean).join(' &nbsp; '); }, true);
      ids.forEach((id, i) => { if (S.cmpHide.has(id)) v.setSeries(i + 1, { show: false }); });
      return v;
    } });
  $$('[data-leg]', root).forEach(el => el.onclick = () => {
    const i = +el.dataset.leg, id = ids[i], off = !S.cmpHide.has(id);
    if (off) S.cmpHide.add(id); else S.cmpHide.delete(id);
    el.classList.toggle('off', off);
    u.setSeries(i + 1, { show: !off });
  });
}

/* ------------------------------------------------------------------ mods */
async function renderMods() {
  const root = $('#tab-mods');
  if (!S.cur) {
    try { S.sessions = (await api('sessions')) || []; } catch (e) { /* ignore */ }
    const first = S.sessions.find(s => s.analysed);
    if (first) { S.curId = first.id; try { S.cur = await api('session/' + encodeURIComponent(first.id)); } catch (e) { /* ignore */ } }
  }
  if (!S.cur) { root.innerHTML = '<div class="empty">Open a session first.</div>'; return; }
  const mods = (S.cur.mods || []).filter(m => !S.modFilter || m.name.toLowerCase().includes(S.modFilter.toLowerCase()));
  const key = S.modSort;
  const val = (m) => key === 'name' ? m.name.toLowerCase() : key === 'script' ? (m.redscript_ms_per_s + m.cet_ms_per_s) : key === 'files' ? m.files_loaded : key === 'spikes' ? m.in_spikes : m.score;
  mods.sort((a, b) => key === 'name' ? (val(a) < val(b) ? -1 : 1) : val(b) - val(a));
  root.innerHTML = `<div class="rep-head"><div class="title">Cost while playing <span class="dim" style="font-size:.6em">in “${esc(S.cur.meta.title)}”</span></div><div class="dim">${mods.length} mods with recorded cost</div>
    <div class="actions"><input id="modq" placeholder="filter" value="${esc(S.modFilter)}"></div></div>
    <div class="card tbl-scroll"><table><tr><th class="sort" data-k="name">mod (library zip)</th><th>kind</th><th class="n sort" data-k="script">script ms/s</th><th class="n">REDscript</th><th class="n">CET</th><th class="n sort" data-k="files">files loaded</th><th class="n">load ms</th><th class="n sort" data-k="spikes">in spikes</th><th class="n">ms in spikes</th><th class="n sort" data-k="score">cost score</th></tr>
    ${mods.map(m => `<tr><td>${esc(m.name)}${m.zip ? '<div class="dimmer small">' + esc(m.zip) + '</div>' : ''}${m.note ? '<div class="dimmer small">' + esc(m.note) + '</div>' : ''}</td><td class="dim">${esc((m.kinds || []).join(', '))}</td>
      <td class="n">${(m.redscript_ms_per_s + m.cet_ms_per_s).toFixed(2)}</td><td class="n dim">${m.redscript_ms_per_s ? m.redscript_ms_per_s.toFixed(2) : '–'}</td><td class="n dim">${m.cet_ms_per_s ? m.cet_ms_per_s.toFixed(2) : '–'}</td>
      <td class="n">${m.files_loaded ? fmt.n(m.files_loaded) : '–'}</td><td class="n dim">${m.load_ms ? m.load_ms.toFixed(0) : '–'}</td><td class="n">${m.in_spikes || '–'}</td><td class="n dim">${m.spike_ms ? m.spike_ms.toFixed(1) : '–'}</td><td class="n">${m.score.toFixed(2)}</td></tr>`).join('')}</table>
    <div class="dimmer small" style="margin-top:10px">script ms/s: main-thread time per second of play (1 ms/s ≈ 0.1% of one CPU core). Files loaded: files from the mod's archives read during the capture. In spikes: slow frames where the mod's scripts or files took ≥ 0.5 ms. Cost score = script ms/s + file read ms/s + spike share; for sorting only.</div></div>`;
  $$('th.sort', root).forEach(th => th.onclick = () => { S.modSort = th.dataset.k; renderMods(); });
  const q = $('#modq', root);
  q.oninput = () => { S.modFilter = q.value; clearTimeout(q._h); q._h = setTimeout(() => { renderMods(); const n = $('#modq'); n.focus(); n.setSelectionRange(n.value.length, n.value.length); }, 250); };
}

/* ------------------------------------------------------------------ VRAM */
// a mod's name without the library's "<Nexus ID> <version> <date>" tail
const modName = (m) => m.nexus ? m.label.replace(new RegExp('\\s+' + m.nexus + '\\s+v?\\d.*$'), '') : m.label;
const gbx = (mb) => { const s = mb < -0.05 ? '−' : '', a = Math.abs(mb || 0); return s + (a >= 1024 ? (a / 1024).toFixed(2) + ' GB' : a.toFixed(0) + ' MB'); };
async function renderVram() {
  const page = $('#tab-vram');
  if (!S.state) { page.innerHTML = '<div class="empty">Loading ...</div>'; return; }
  if (!$('#vr-mods', page)) page.innerHTML = '<div id="vr-top"></div><div id="vr-apps"></div><div id="vr-mods"></div>';
  if (!S.vrSub) S.vrSub = 'apps'; // the easy fix first
  const apps = S.vrSub === 'apps';
  $('#vr-apps', page).style.display = apps ? '' : 'none';
  $('#vr-mods', page).style.display = apps ? 'none' : '';
  const st = S.state;
  S.vrBuild = st.config.active || 0; // the header's build
  const top = $('#vr-top', page);
  if (apps) {
    top.innerHTML = `<div class="rep-head"><div class="title">VRAM</div></div><div class="card vr-guide">${vramGuide(true)}</div>${vramSeg()}`;
    bindVram(top);
    if (!S.gaAt || Date.now() - S.gaAt > 4000) renderGpuApps();
    return;
  }
  const root = $('#vr-mods', page);
  let v;
  try { v = await api('vram', { build: S.vrBuild }); } catch (e) { root.innerHTML = '<div class="card"><span class="lose">' + esc(e.message) + '</span></div>'; return; }
  if (S.tab !== 'vram' || S.vrSub !== 'mods') return;
  const busy = v.state === 'scanning', r = v.report;
  S.vr = r;
  if (busy) { clearTimeout(S.vrTimer); S.vrTimer = setTimeout(() => { if (S.tab === 'vram') renderVram(); }, 1000); }
  top.innerHTML = `<div class="rep-head"><div class="title">VRAM</div>
    <div class="cf-builds">${st.config.builds.map((b, i) => `<button class="btn2 small" data-b="${i}" style="${i === S.vrBuild ? 'border-color:var(--accent);color:var(--white)' : ''}">${esc(b.name)}</button>`).join('')}</div>
    <div class="actions">${r ? '<button class="btn2" id="vr-copy">Copy summary</button>' : ''}<button class="btn" id="vr-run" ${busy ? 'disabled' : ''}>${busy ? 'Scanning ...' : 'Scan build'}</button></div></div><div class="card vr-guide">${vramGuide(true)}</div>${vramSeg()}`;
  bindVram(top);
  let html = '';
  if (v.state && v.state.startsWith('error')) html += `<div class="card"><span class="lose">${esc(v.state)}</span></div>`;
  if (!r) {
    html += `<div class="card dim">Not scanned yet. Scan build reads the file tables of the build's mod archives and of the game's own archives (a few seconds) and adds up the data each mod sends to the graphics card: textures and meshes.
      It compares every mod file with the game file it replaces, so you see what a mod really adds. With a session of this build it also shows how much of it the game actually loaded.</div>`;
    root.innerHTML = html; return;
  }
  const cmp = r.vanilla_files > 0;
  const mods = r.mods || [];
  const added = (m) => cmp ? m.added_mb : m.mb;
  const t4 = r.tex_4k != null ? r.tex_4k : mods.reduce((a, m) => a + (m.tex_4k || 0), 0), t8 = r.tex_8k != null ? r.tex_8k : mods.reduce((a, m) => a + (m.tex_8k || 0), 0);
  html += `<div class="sub">Scanned ${esc(new Date(r.generated).toLocaleString('en-AU', { day: 'numeric', month: 'short', hour: 'numeric', minute: '2-digit' }))} in ${r.took_s.toFixed(1)} s · ${r.archives} mod archives${cmp ? ' compared with the game\'s own ' + fmt.n(r.vanilla_files) + ' files' : ''}${r.session ? ' · loaded = your session ' + esc(r.session) : ' · no session of this build yet, so "loaded" is unknown'}</div>`;
  const frac = (x) => r.total_mb > 0 ? Math.max(0, Math.min(1, x / r.total_mb)) : 0;
  const kp = (l, val, s, dot, f) => `<div class="k"><div class="l">${dot ? '<span class="dot6"></span>' : ''}${l}</div><div class="v">${val}</div><div class="s">${s}</div>${f != null ? `<div class="kbar"><i style="width:${(f * 100).toFixed(0)}%"></i></div>` : ''}</div>`;
  html += '<div class="kpi5">' +
    (cmp ? kp('Added on top of the game', gbx(r.added_mb), 'beyond the game files mods replace', r.added_mb >= 1024, frac(r.added_mb)) : kp('Added on top of the game', '–', 'scan again to compare with the game', false, null)) +
    kp('GPU data in mods', gbx(r.total_mb), cmp ? gbx(r.van_mb) + ' of it replaces game files' : 'textures and meshes the mods could load', false, 1) +
    kp('Loaded last session', r.session ? gbx(r.loaded_mb) : '–', r.session ? 'what the game really used' : 'no session of this build yet', false, r.session ? frac(r.loaded_mb) : null) +
    kp('Never used', gbx(r.shadowed_mb || 0), 'copies another mod overrides', (r.shadowed_mb || 0) >= 100, frac(r.shadowed_mb || 0)) +
    kp('Upscaled textures', cmp ? fmt.n(r.upscaled || 0) : '–', '3.5× the game\'s file or more', (r.upscaled || 0) > 0, null) + '</div>';
  // findings
  const finds = [];
  if (!cmp) finds.push(['Scan again', 'This scan is from an older Frame Lab, or the game\'s own archives weren\'t found. Scan build again to see what each mod adds on top of the game.']);
  else {
    const know = [];
    const big = mods.filter(m => m.mb >= 1024 && m.added_mb < m.mb * 0.1).sort((a, b) => b.mb - a.mb)[0];
    if (big) know.push(`<b>${esc(modName(big))}</b> holds ${gbx(big.mb)}, but ${gbx(big.van_mb)} of it replaces the game's own files (${fmt.n(big.replaced)} files). It adds ${gbx(big.added_mb)}.`);
    const most = mods.slice().sort((a, b) => b.added_mb - a.added_mb)[0];
    if (most && most.added_mb >= 100) know.push(`<b>${esc(modName(most))}</b> adds the most: ${gbx(most.added_mb)}${most.upscaled ? ', ' + most.upscaled + ' upscaled texture' + (most.upscaled > 1 ? 's' : '') : ''}.`);
    if (know.length) finds.push(['Worth knowing', know.join(' ')]);
  }
  const c = r.changes;
  if (c) {
    const lab = {};
    mods.forEach(m => (m.archives || []).forEach(a => { lab[a] = modName(m); }));
    const names = (l) => { const n = [...new Set(l.map(a => lab[a] || a.split('/').pop()))]; return esc(n.slice(0, 5).join(', ')) + (n.length > 5 ? ' and ' + (n.length - 5) + ' more' : ''); };
    const parts = [];
    if (c.added.length) parts.push('added ' + names(c.added));
    if (c.changed.length) parts.push('changed ' + names(c.changed));
    if (c.removed.length) parts.push('removed ' + names(c.removed));
    const since = new Date(c.since).toLocaleString('en-AU', { day: 'numeric', month: 'short', hour: 'numeric', minute: '2-digit' });
    finds.push(['Since last scan', parts.length ? 'Since ' + esc(since) + ': ' + parts.join(' · ') + '.' : 'Nothing changed since ' + esc(since) + '.']);
  } else if (cmp) finds.push(['Since last scan', '<span class="dimmer">First scan with this version. The next scan lists the mods added, changed or removed since this one.</span>']);
  if (cmp) {
    const br = r.broken || [];
    const ch = [br.length ? `<span class="badc">${br.length} broken archive${br.length > 1 ? 's' : ''}</span>: ${br.slice(0, 3).map(b => '<b>' + esc(b.archive) + '</b> (' + esc(b.reason) + ')').join(', ')}` : '<span class="goodc">No broken archives</span>',
      r.same ? `<span class="warnc">${fmt.n(r.same)} file${r.same > 1 ? 's' : ''} identical to the game's own</span> (${gbx(r.same_mb)}, they change nothing)` : '<span class="goodc">no files identical to the game\'s own</span>',
      r.no_mips ? `<span class="warnc">${fmt.n(r.no_mips)} texture${r.no_mips > 1 ? 's' : ''} without mips</span> (${gbx(r.no_mips_mb)}, always loaded at full size)` : '<span class="goodc">every texture has mips</span>',
      t4 ? `${fmt.n(t4)} texture${t4 > 1 ? 's' : ''} 4K or bigger${t8 ? `, <span class="warnc">${fmt.n(t8)} of them 8K</span>` : ', none 8K'}` : 'no 4K textures'];
    const shr = mods.reduce((a, m) => a + (m.shrink_mb || 0), 0), one = mods.reduce((a, m) => a + (m.one_lod || 0), 0);
    if (shr >= 1) ch.push(`one level less on every 2K+ mod texture would save <b>${gbx(shr)}</b>`);
    if (one) ch.push(`<span class="warnc">${fmt.n(one)} big mesh${one > 1 ? 'es' : ''} without LODs</span>`);
    finds.push(['Checks', ch.join(' · ')]);
  }
  html += finds.map(f => `<div class="find"><span class="fk">${f[0]}</span><span>${f[1]}</span></div>`).join('');
  // the table
  const key = S.vrSort || (cmp ? 'added' : 'mb');
  const val = (m) => key === 'added' ? added(m) : key === 'loaded' ? m.loaded_mb : key === 'tex4k' ? (m.tex_8k || 0) * 1e4 + (m.tex_4k || 0) : m.mb;
  const list = mods.slice().sort((a, b) => val(b) - val(a));
  const shown = S.vrAll ? list : list.slice(0, 15);
  const maxAdd = Math.max(1, ...list.map(added));
  const sortBtn = (k, t) => `<button data-sort="${k}" class="${key === k ? 'on' : ''}">${t}</button>`;
  html += `<div class="sec2"><h2><span class="no">01</span>Mods by ${key === 'added' ? 'what they add' : key === 'loaded' ? 'what the game loaded' : key === 'tex4k' ? '4K and 8K textures' : 'GPU data'}</h2><span class="dimmer small">click a mod for its files and lighter versions</span>
    <div class="r"><span>Sort by</span>${cmp ? sortBtn('added', 'added') : ''}${sortBtn('mb', 'GPU data')}${r.session ? sortBtn('loaded', 'loaded') : ''}${sortBtn('tex4k', '4K+')}</div></div>`;
  const row = (m) => {
    const open = S.vrOpen && S.vrOpen.has(m.label);
    const notes = [];
    if (cmp && m.replaced && m.mb >= 100 && Math.abs(m.added_mb) < m.mb * 0.05) notes.push('replaces ' + fmt.n(m.replaced) + ' game files with ones of about the same size');
    if (m.shadowed_mb >= 1) notes.push(gbx(m.shadowed_mb) + ' of it overridden by another mod (never used)');
    if (m.same) notes.push(fmt.n(m.same) + ' file' + (m.same > 1 ? 's' : '') + ' identical to the game\'s own');
    let h = `<tr class="m${open ? ' open' : ''}" data-m="${esc(m.label)}"><td title="${esc(m.label)}">${esc(modName(m))}${m.nexus ? `<span class="meta link" data-nx="${esc(m.nexus)}" title="Open the mod's Nexus page">Nexus ${esc(m.nexus)}</span>` : ''}${notes.map(n => '<div class="note">' + esc(n) + '</div>').join('')}</td>
      <td class="hi">${gbx(added(m))}<div class="rbar"><i style="width:${Math.max(0, added(m) / maxAdd * 100).toFixed(0)}%"></i></div></td><td>${gbx(m.mb)}</td><td>${cmp && m.van_mb >= 0.5 ? gbx(m.van_mb) : '–'}</td>
      <td>${r.session ? (m.loaded_mb >= 0.5 ? gbx(m.loaded_mb) : '0') : '–'}</td><td>${m.upscaled || '–'}</td><td>${m.tex_4k ? m.tex_4k + (m.tex_8k ? `<div class="warnc">${m.tex_8k} 8K</div>` : '') : '–'}</td><td>${m.shadowed_mb >= 0.5 ? gbx(m.shadowed_mb) : '–'}</td></tr>`;
    if (open) {
      const up = (m.up || []).map(u => `<div class="f"><u>${u.van_mb > 0 ? Math.round(u.mb / u.van_mb) + '×' : 'new'}</u>${esc(u.name)}<i>game ${u.van_mb.toFixed(1)} MB → ${u.mb.toFixed(1)} MB${u.size ? ', ' + esc(u.size.replace('≈', 'about ')) : ''}</i></div>`).join('');
      const big = (m.big || []).map(f => `<div class="f">${f.loaded ? '<u style="color:var(--accent)">loaded</u>' : ''}${f.no_mips ? '<u>no mips</u>' : ''}${esc(f.name)}<i>${f.mb.toFixed(1)} MB · ${esc(f.kind)}${f.dims ? ' ' + esc(f.dims) : f.size ? ' ' + esc(f.size) : ''}</i></div>`).join('');
      h += `<tr class="files"><td colspan="8">${up ? '<div class="f"><i style="margin:0">Upscaled the most</i></div>' + up + (m.upscaled > (m.up || []).length ? `<div class="f"><i style="margin:0">and ${m.upscaled - m.up.length} more upscaled</i></div>` : '') : ''}
        ${big ? '<div class="f" style="margin-top:6px"><i style="margin:0">Biggest files</i></div>' + big : ''}
        ${m.shrink_n ? `<div class="f" style="margin-top:6px"><i style="margin:0">One level less on its ${m.shrink_n} texture${m.shrink_n > 1 ? 's' : ''} of 2K or more would save <b>${gbx(m.shrink_mb)}</b> of VRAM (exact: their top levels). Shrinking itself isn't in this test build.</i></div>` : ''}
        ${m.one_lod ? `<div class="f" style="margin-top:6px"><i style="margin:0">${m.one_lod} mesh${m.one_lod > 1 ? 'es' : ''} of 2 MB+ with a single level of detail (${gbx(m.one_lod_mb)}): drawn at full detail at any distance</i></div>` + (m.one_lod_list || []).map(x => `<div class="f">${esc(x)}</div>`).join('') : ''}
        ${(m.tex_mb >= 150 || m.tex_4k) ? `<div class="lt" data-lt="${esc(m.label)}">${S.lt && S.lt[m.label] ? ltHtml(S.lt[m.label]) : '<div class="f"><i style="margin:6px 0 0">Lighter version: looking ...</i></div>'}</div>` : ''}
        <div class="f"><i style="margin:6px 0 0">${esc((m.archives || []).join(', '))}${m.nexus ? ' · <span class="meta link" data-nx="' + esc(m.nexus) + '" style="margin:0">open on Nexus</span>' : ''}</i></div></td></tr>`;
    }
    return h;
  };
  html += `<table class="vt"><tr><th>Mod</th><th>Added</th><th>GPU data</th><th>Replaces</th><th>Loaded last session</th><th>Upscaled</th><th>4K+ textures</th><th>Never used</th></tr>${shown.map(row).join('')}</table>`;
  if (list.length > 15) html += `<div class="more"><button class="btn2 small" id="vr-all">${S.vrAll ? 'Show the top 15' : 'Show all ' + list.length + ' mods'}</button></div>`;
  html += `<div class="foot">GPU data: the unpacked size of the parts of each file that go to the graphics card (a texture with its mip levels, a mesh's vertex and index data), read from the archives' file tables.
    Replaces: the size of the game's own files the mod swaps out; Added = GPU data − Replaces, which can be below zero when a mod's files are smaller than the game's.
    Upscaled: textures 3.5× the size of the game's file or more. 4K+: textures 4096 pixels or more on their longest side (8K: 8192 or more), read from the texture's header or, when that can't be read, estimated from its size. Never used: copies of files another mod wins over (load order).
    Textures without mips are found because their data is exactly width × height × bytes per pixel; UI images, icons and colour LUTs don't need mips and aren't counted.</div>`;
  root.innerHTML = html;
  $$('[data-sort]', root).forEach(b => b.onclick = () => { S.vrSort = b.dataset.sort; renderVram(); });
  const all = $('#vr-all', root);
  if (all) all.onclick = () => { S.vrAll = !S.vrAll; renderVram(); };
  $$('tr.m', root).forEach(tr => tr.onclick = (e) => {
    if (e.target.closest('[data-nx]')) return;
    S.vrOpen = S.vrOpen || new Set();
    const k = tr.dataset.m;
    if (S.vrOpen.has(k)) S.vrOpen.delete(k); else S.vrOpen.add(k);
    renderVram();
  });
  $$('[data-nx]', root).forEach(a => a.onclick = (e) => { e.stopPropagation(); api('open', { what: 'nexus', id: a.dataset.nx }).catch(err => toast(err.message, true)); });
  ltLoad(root);
}

// ---- lighter version of a heavy texture mod (2.5E swaps it, 2.5 shows the steps)
async function ltLoad(root) {
  clearTimeout(S.ltTimer);
  const els = $$('[data-lt]', root);
  if (!els.length) return;
  S.lt = S.lt || {};
  let again = false;
  for (const el of els) {
    const mod = el.dataset.lt;
    try { S.lt[mod] = await api('vram/lighter', { build: S.vrBuild, mod }); } catch (e) { S.lt[mod] = { error: e.message }; }
    const cur = $$('[data-lt]', root).find(x => x.dataset.lt === mod);
    if (!cur) continue;
    cur.innerHTML = ltHtml(S.lt[mod]);
    ltBind(cur, mod);
    again = again || !!S.lt[mod].nexus;
  }
  // keep looking for the download while a panel is open
  if (again) S.ltTimer = setTimeout(() => { if (S.tab === 'vram' && S.vrSub === 'mods') ltLoad($('#vr-mods')); }, 4000);
}
function ltHtml(i) {
  if (i.error) return `<div class="f"><i style="margin:6px 0 0">Lighter version: ${esc(i.error)}</i></div>`;
  const mb = (x) => x >= 1000 ? (x / 1024).toFixed(2) + ' GB' : Math.round(x) + ' MB';
  const dlmb = (b) => mb(b / 1048576);
  let h = `<div class="ltbox"><b>Lighter version</b> <span class="dimmer small">now ${mb(i.mb)} of GPU data</span>`;
  if ((i.variants || []).length) h += `<div class="small" style="margin-top:4px">On Nexus: ${i.variants.map(v => `<span class="op-nx" data-ltfile="${v.file_id}">${esc(v.name)}</span> <span class="dimmer">(${dlmb(v.bytes)} download)</span>`).join(' · ')}</div>`;
  else if (i.nexus) h += `<div class="dim small" style="margin-top:4px">Many texture mods come in lighter versions (2K instead of 4K, or 1K). The mod's Files page shows if this one does.</div>`;
  if (i.nexus) h += `<div style="margin-top:6px"><button class="btn2 small" data-ltfiles="1">Open its files on Nexus</button></div>`;
  else h += `<div class="dim small" style="margin-top:4px">Frame Lab doesn't know this mod's Nexus page, so it can't find the download by itself. Its lighter version (if it has one) replaces ${esc((i.archives || []).map(a => a.split('/').pop()).join(', '))} in archive\\pc\\mod.</div>`;
  const auto = EX() && !i.managed;
  if (i.nexus) h += `<div class="dim small" style="margin-top:6px">${auto ? 'Download the lighter file in your browser (Manual or Slow download). Frame Lab finds it in' : 'Frame Lab looks for the download in'} <span class="mono">${esc(i.downloads)}</span> and checks it before anything changes: the same game files as the installed mod, and less GPU data.</div>`;
  for (const c of (i.cands || [])) {
    const st = c.state === 'checking' ? '<span class="dim">checking ...</span>'
      : c.state === 'ready' ? `<span class="win">GPU data ${mb(i.mb)} → ${mb(c.mb)}: saves about ${mb(i.mb - c.mb)}</span> <span class="dimmer small">same game files</span>${auto ? (i.locked ? ` <span class="dimmer small" title="${esc(i.locked)}">the swap opens once the build has been measured (Optimise: test round)</span>` : ` <button class="btn small" data-ltzip="${esc(c.zip)}" data-ltsave="${esc(mb(i.mb - c.mb))}">Swap it in</button>`) : ''}`
      : `<span class="dimmer">${esc(c.problem || 'not usable')}</span>`;
    h += `<div class="small" style="margin-top:6px;line-height:1.6"><span class="mono">${esc(c.zip)}</span><br>${st}</div>`;
  }
  if (!auto) {
    const arcs = (i.archives || []).map(a => a.split('/').pop()).join(', ');
    h += i.managed && i.managed !== 'REDmod'
      ? `<div class="dim small" style="margin-top:6px">${esc(i.managed)} manages this build's mod files, so the swap belongs there: install the lighter download, turn the old version off, then Scan build here again.</div>`
      : `<ol class="dim small" style="margin:6px 0 0 18px;padding:0"><li>Close the game.</li><li>Download the lighter file from the mod's Files page.</li><li>In the game folder's <span class="mono">archive\\pc\\mod</span>, move <span class="mono">${esc(arcs)}</span> somewhere safe (to go back later) and put the new .archive in.</li><li>Scan build here again: the saving shows in the table.</li></ol>${EX() ? '' : `<div class="enote">${E_NOTE}</div>`}`;
  }
  return h + '</div>';
}
function ltBind(el, mod) {
  const i = S.lt[mod] || {};
  $$('[data-ltfiles]', el).forEach(b => b.onclick = (e) => { e.stopPropagation(); api('open', { what: 'nexusfiles', id: String(i.nexus) }).catch(err => toast(err.message, true)); });
  $$('[data-ltfile]', el).forEach(b => b.onclick = (e) => { e.stopPropagation(); api('open', { what: 'nexusfiles', id: String(i.nexus), file: b.dataset.ltfile }).catch(err => toast(err.message, true)); });
  $$('[data-ltzip]', el).forEach(b => b.onclick = (e) => {
    e.stopPropagation();
    confirmBox(`Swap <b>${esc(mod)}</b> for <b>${esc(b.dataset.ltzip)}</b>?<br><br>It saves about ${esc(b.dataset.ltsave)} of graphics memory. Close the game first. The installed archives go into Frame Lab's data folder and Undo (Optimise > Fixes made on this PC) puts them back exactly. It counts as a change in the test round.`, async () => {
      try { await api('vram/lighter/apply', { build: S.vrBuild, mod, zip: b.dataset.ltzip }); toast('Swapped. Frame Lab is scanning the build again.'); delete S.lt[mod]; setTimeout(renderVram, 600); }
      catch (err) { toast(err.message, true); }
    });
  });
  el.onclick = (e) => e.stopPropagation(); // clicks inside don't fold the row
}
function vramSeg() {
  // two clear choices, the easy one first (VRAM guide: programs before mods)
  const n = S.gaSuggN || 0;
  const card = (sub, no, title, sub2, extra) => `<button data-sub="${sub}" class="vc ${S.vrSub === sub ? 'on' : ''}"><span class="vc-n">${no}</span><span class="vc-b"><span class="vc-t">${title}${extra || ''}</span><span class="vc-s">${sub2}</span></span></button>`;
  return `<div class="vchoose">${card('apps', '1', 'Programs on the graphics card', 'Easiest: move browsers, Discord and launchers off the game\'s card', n ? ` <span class="badge warn">${n} to move</span>` : '')}${card('mods', '2', 'Mods', 'What each mod costs in graphics memory, and lighter versions of texture mods')}</div>`;
}
function vramSummary(r) {
  const b = (S.state.config.builds[S.vrBuild] || {}).name || r.build;
  const cmp = r.vanilla_files > 0;
  const L = [`Frame Lab VRAM scan: ${b}, ${new Date(r.generated).toLocaleString('en-AU')}`,
    `GPU data in mods: ${gbx(r.total_mb)}` + (cmp ? `, of which ${gbx(r.van_mb)} replaces the game's own files. Added on top of the game: ${gbx(r.added_mb)}` : ''),
    r.session ? `Loaded in the last session: ${gbx(r.loaded_mb)}` : '',
    `Never used (overridden by another mod): ${gbx(r.shadowed_mb || 0)}` + (cmp ? `. Upscaled textures: ${r.upscaled || 0}. Textures without mips: ${r.no_mips}` : '') + (r.tex_4k != null ? `. 4K or bigger: ${r.tex_4k} (8K: ${r.tex_8k || 0})` : ''), '',
    cmp ? 'Mods by what they add:' : 'Mods by GPU data:'];
  (r.mods || []).slice().sort((x, y) => (cmp ? y.added_mb - x.added_mb : y.mb - x.mb)).slice(0, 15).forEach((m, i) => {
    L.push(`${i + 1}. ${modName(m)}: ${cmp ? 'adds ' + gbx(m.added_mb) + ' (' + gbx(m.mb) + ' GPU data)' : gbx(m.mb)}${m.upscaled ? ', ' + m.upscaled + ' upscaled' : ''}${m.tex_8k ? ', ' + m.tex_8k + ' 8K textures' : ''}`);
  });
  return L.filter((l, i) => l !== '' || i === 4).join('\n');
}
async function copyText(t) {
  try { await navigator.clipboard.writeText(t); return true; } catch (e) { /* fall back */ }
  const ta = document.createElement('textarea');
  ta.value = t; document.body.appendChild(ta); ta.select();
  let ok = false;
  try { ok = document.execCommand('copy'); } catch (e) { /* ignore */ }
  ta.remove();
  return ok;
}
function bindVram(root) {
  $$('[data-b]', root).forEach(b => b.onclick = () => setBuild(+b.dataset.b));
  $$('[data-sub]', root).forEach(b => b.onclick = () => { S.vrSub = b.dataset.sub; if (S.vrSub === 'apps') S.gaAt = 0; renderVram(); });
  const run = $('#vr-run', root);
  if (run) run.onclick = async () => { run.disabled = true; try { await api('vram/scan', { build: S.vrBuild }); } catch (e) { toast(e.message, true); } setTimeout(renderVram, 300); };
  const cp = $('#vr-copy', root);
  if (cp) cp.onclick = async () => { if (S.vr && await copyText(vramSummary(S.vr))) toast('Summary copied'); else toast('Could not copy', true); };
}

/* programs on the graphics card (VRAM tab, live) */
async function renderGpuApps(note) {
  const host = $('#vr-apps');
  if (!host) return;
  clearTimeout(S.gaTimer);
  S.gaAt = Date.now();
  let g;
  try { g = await api('gpuapps'); } catch (e) { host.innerHTML = '<div class="card"><span class="lose">' + esc(e.message) + '</span></div>'; return; }
  if (S.tab !== 'vram' || S.vrSub !== 'apps') return;
  const playing = g.game_running || (S.state && S.state.game && S.state.game.running);
  if (!playing) S.gaTimer = setTimeout(() => { if (S.tab === 'vram' && S.vrSub === 'apps' && !S.gaHold) renderGpuApps(); }, 5000); // paused while the game runs
  const gb = (mb) => mb >= 1000 ? (mb / 1024).toFixed(2) + ' GB' : Math.round(mb) + ' MB';
  const k = (l, val, sub, cls) => `<div class="k ${cls || ''}"><div class="l">${l}</div><div class="v">${val}</div>${sub ? '<div class="s">' + sub + '</div>' : ''}</div>`;
  const main = g.main, ig = g.integrated;
  const cardWord = !main || main.vendor === 0x10de ? 'the NVIDIA card' : 'the game\'s card';
  const short = (n) => (n || '').replace(/^NVIDIA GeForce /, '').replace(/^AMD Radeon\(TM\) /, 'Radeon ').replace(/^Intel\(R\) /, 'Intel ');
  const prefTxt = (a) => a.pref === '1' ? 'Power saving' : a.pref === '2' ? 'High performance' : a.pref === '0' ? 'Let Windows decide' :
    a.pref === '1073741824' ? 'Chosen GPU: ' + esc(short(a.pref_gpu) || 'another card') : a.pref ? 'other (' + esc(a.pref) + ')' : '<span class="dimmer">not set</span>';
  const list = g.apps || [];
  const sugg = list.filter(a => a.class === 'suggest' && a.can && a.running && !a.to_igpu && !a.shared && a.main_mb >= 20);
  if (S.gaSuggN !== sugg.length) { S.gaSuggN = sugg.length; const b = $('.vc[data-sub="apps"] .vc-t'); if (b) b.innerHTML = 'Programs on the graphics card' + (sugg.length ? ` <span class="badge warn">${sugg.length} to move</span>` : ''); }
  let html = `<div><div class="rep-head" style="margin-bottom:8px"><span class="dimmer small">${playing ? 'paused while the game runs · ' + new Date().toLocaleTimeString('en-AU') : 'right now · refreshes every 5 s'}</span>
    <div class="actions">${playing ? '<button class="btn2" id="ga-refresh">Refresh</button>' : ''}<button class="btn" id="ga-all" ${!ig || !sugg.length ? 'disabled' : ''}>Move all suggested${sugg.length ? ' (' + sugg.length + ')' : ''}</button></div></div>`;
  if (g.error) html += `<div class="gnote" style="margin-bottom:10px">${esc(g.error)}</div>`;
  if (g.game_warn) html += `<div class="gnote" style="margin-bottom:10px">${esc(g.game_warn)}</div>`;
  html += '<div class="kpi" style="margin-bottom:10px">' +
    k(main ? esc(short(main.name)) : 'graphics card', main ? gb(main.used_mb) : '–', main ? 'in use of ' + gb(main.mem_mb) + (g.game_mb >= 1 ? ' · the game ' + gb(g.game_mb) : ' · game not running') : '') +
    k('everything else on it', gb(g.others_mb || 0), 'programs, Windows and the driver: all but the game', g.others_mb >= 1500 ? 'warn' : '') +
    k('suggested to move', sugg.length ? gb(g.movable_mb || 0) : '–', sugg.length ? sugg.length + ' program' + (sugg.length > 1 ? 's' : '') + ' that don\'t need ' + cardWord : 'nothing left to move', sugg.length ? '' : 'good') +
    k('integrated graphics', ig ? esc(short(ig.name)) : 'not found', ig ? gb(ig.used_mb) + ' in use · uses system RAM' : 'switched off or none (see below)', ig ? '' : 'warn') + '</div>';
  if (!ig) {
    const known = (g.drivers || []).filter(d => /radeon\(tm\) graphics|radeon graphics|uhd graphics|iris|intel\(r\) graphics|vega/i.test(d));
    const basic = (g.drivers || []).some(d => /basic display/i.test(d));
    html += `<div class="gnote" style="margin-bottom:10px"><b>Windows doesn't see integrated graphics, so there is nothing to move programs to yet.</b>
      ${known.length ? ' Windows has a driver for <b>' + esc(known[0]) + '</b> from before, so it is most likely switched off in the BIOS.' : ''}${basic ? ' One display adapter runs without its driver (Microsoft Basic Display Adapter).' : ''}
      Many processors have it (Ryzen 7000 / 9000 and Ryzen G models, most Intel ones without an F at the end) but boards often switch it off when a graphics card is in.
      <ol style="margin:6px 0 0 18px;padding:0"><li>BIOS: AMD boards: Advanced → NB / AMD CBS → <i>Integrated Graphics</i> → <b>Forces</b> or <b>Enabled</b> (not Auto). Intel boards: Advanced → System Agent / Graphics → <i>iGPU Multi-Monitor</i> → <b>Enabled</b>.</li>
      <li>Keep the monitor plugged into the graphics card, and if the BIOS asks for a primary / boot display, keep that on the graphics card (PCIe / PEG).</li><li>Install the integrated graphics driver (AMD: the Radeon graphics driver from amd.com, the driver-only / minimal install is enough; Intel: the Intel graphics driver), restart, and come back here.</li></ol></div>`;
  }
  const row = (a) => {
    let act = '';
    if (a.class === 'game') act = '<span class="dim">the game</span>';
    else if (a.class === 'keep') act = '<span class="tag g">keep</span>';
    else if (a.class === 'windows') act = '<span class="dimmer">Windows, stays</span>';
    else if (a.class === 'store') act = '<span class="dimmer">Windows Settings</span>';
    else if (a.to_igpu) {
      act = a.ours && a.pref === '1' ? `<button class="btn2 small" data-undo="${esc(a.path)}" title="Puts back exactly what Windows had for it before (${esc(a.since || '')})">Undo</button>` : '<span class="dimmer">set in Windows</span>';
      if (!a.exists) act = `<button class="btn2 small" data-undo="${esc(a.path)}" title="That version of the program is gone (it updated): this takes its old setting out">Remove old setting</button>`;
    } else if (a.can && a.exists) act = `<button class="btn2 small ${a.class === 'other' ? 'warn' : ''}" data-move="${esc(a.path)}" data-name="${esc(a.name)}" data-other="${a.class === 'other' ? 1 : 0}" ${ig ? '' : 'disabled'}>Move to integrated</button>`;
    let st = '';
    if (a.to_igpu) st = a.restart ? '<span class="tag y" title="The setting is made; the program picks it up when it starts again">restart it</span>' : (a.running && a.igpu_mb >= 1 ? '<span class="tag g">on integrated</span>' : '');
    if (!a.running) st = '<span class="tag">not running</span>' + (a.exists ? '' : ' <span class="tag">old version</span>');
    return `<tr><td title="${esc(a.path || '')}"><b>${esc(a.name)}</b>${a.procs > 1 ? ' <span class="dimmer">×' + a.procs + '</span>' : ''} ${st}${a.versioned && a.can ? ' <span class="tag" title="Its folder is named after its version and Windows keeps the setting per file path: after the program updates, move it again (Frame Lab lists it here again).">set again after updates</span>' : ''}
        <div class="dimmer small">${esc(a.reason)}</div></td>
      <td class="n">${a.shared ? '<span class="dimmer" title="Windows shows ' + gb(a.main_mb) + ', more than everything but the game together: it holds handles to the game\'s own memory (overlay / desktop), so that number is not its own">shares the game\'s</span>' : a.main_mb >= 1 ? '<b>' + gb(a.main_mb) + '</b>' : '–'}</td><td class="n">${a.igpu_shared ? '<span class="dimmer" title="Windows shows ' + gb(a.igpu_mb) + ', more than the integrated graphics hold in all: memory it shares with a program on the other card (the game, for an overlay), not its own">shares the game\'s</span>' : a.igpu_mb >= 1 ? gb(a.igpu_mb) : '–'}</td>
      <td>${prefTxt(a)}</td><td style="white-space:nowrap">${act}</td></tr>`;
  };
  html += list.length ? `<table><tr><th>program</th><th class="n">on ${esc(short(main ? main.name : 'main card'))}</th><th class="n">on integrated</th><th>Windows setting</th><th></th></tr>${list.map(row).join('')}</table>` :
    '<div class="dim">No program holds graphics memory right now.</div>';
  html += `<div class="dimmer small" style="margin-top:8px">Every program that draws with the graphics card keeps its images in that card's memory, and it all comes out of the same memory the game uses.
    A program moved to the integrated graphics keeps its images in system RAM instead and Windows copies its window to the screen; for browsers, chat and launchers you won't notice.
    Keep games, OBS and video editors on ${cardWord}. <b>The change applies the next time the program starts</b> (close it fully, also from the tray). Undo puts back exactly what Windows had before.</div>
    <details style="margin-top:8px"><summary class="small" style="cursor:pointer">How to do it by hand, and how to check it</summary><div class="small dim" style="margin-top:6px;line-height:1.55">
      <b>Windows Settings:</b> Settings → System → Display → Graphics. Pick the program from the list (or <i>Add desktop app</i> and browse to its .exe; Store apps: <i>Microsoft Store app</i>), open it (Options, or the dropdown next to it) and choose <b>Power saving</b>. Then close the program fully and start it again. This is the same setting the buttons above change.<br>
      <b>Check:</b> Task Manager → Details → right-click a column title → Select columns → <i>Dedicated GPU memory</i> and <i>GPU engine</i>. The Performance tab shows which GPU number is which card.<br>
      <b>Without integrated graphics:</b> switch off hardware acceleration in the program (it then draws with the CPU: frees its VRAM, costs some CPU, video playback gets heavier). Chrome / Edge / Vivaldi: Settings → System → <i>Use graphics acceleration when available</i>. Discord: Settings → Advanced → <i>Hardware Acceleration</i>. Spotify: Settings → Compatibility. Steam: Settings → Interface → <i>GPU accelerated rendering in web views</i>.<br>
      <b>Programs that update into a new folder</b> (Discord, Slack and other apps whose path has app-1.2.3 in it) lose the setting with each update, because Windows keeps it per file path.</div></details>`;
  if (note) html += `<div class="gnote ok" style="margin-top:8px">${note}</div>`;
  html += '</div>';
  const open = $('details', host) && $('details', host).open;
  host.innerHTML = html;
  if (open) $('details', host).open = true;
  const set = async (path, on) => {
    S.gaHold = true;
    try { await api('gpuapps/set', { path, on }); } catch (e) { toast(e.message, true); }
    S.gaHold = false; renderGpuApps(on ? 'Done. It moves the next time the program starts: close it fully (also from the tray) and open it again.' : '');
  };
  $$('[data-move]', host).forEach(b => b.onclick = () => {
    const go = () => set(b.dataset.move, true);
    if (b.dataset.other === '1') confirmBox(`<b>${esc(b.dataset.name)}</b> isn't on Frame Lab's list of programs that are fine on integrated graphics.<br><br>Move it only if it isn't a game, a recorder or a video tool. Undo puts back what was there.`, go);
    else go();
  });
  $$('[data-undo]', host).forEach(b => b.onclick = () => set(b.dataset.undo, false));
  const rf = $('#ga-refresh', host);
  if (rf) rf.onclick = () => renderGpuApps();
  const all = $('#ga-all', host);
  if (all) all.onclick = () => confirmBox(`Move these to the integrated graphics?<br><br><b>${sugg.map(a => esc(a.name)).join(', ')}</b><br><br>Each one moves the next time it starts (close it fully, also from the tray, and open it again). Undo on each row puts back exactly what was there.`, async () => {
    S.gaHold = true;
    try { const r = await api('gpuapps/all', {}); S.gaHold = false; renderGpuApps('Moved ' + (r.moved || []).length + ': ' + esc((r.moved || []).join(', ')) + '. Close and reopen them to apply.'); }
    catch (e) { S.gaHold = false; toast(e.message, true); renderGpuApps(); }
  });
}

/* ------------------------------------------------------------------ experiments */
async function renderExperiments(note) {
  const root = $('#tab-experiments');
  if (!S.state) { root.innerHTML = '<div class="empty">Loading ...</div>'; return; }
  const st = S.state;
  S.exBuild = st.config.active || 0; // the header's build
  if (!S.ex) root.innerHTML = '<div class="empty">Reading the current Windows settings ...</div>';
  try { S.ex = await api('experiments', { build: S.exBuild }); } catch (e) { root.innerHTML = '<div class="card"><span class="lose">' + esc(e.message) + '</span></div>'; return; }
  if (S.tab !== 'experiments') return;
  const list = S.ex.list || [], groups = S.ex.groups || [{ id: '', title: 'Settings', sub: '' }];
  S.exOpen = S.exOpen || new Set();
  const cpu = S.ex.cpu;
  const noBtn = (x) => !x.ok || x.info || (x.id === 'gamebar' && x.on && !x.ours);
  const row = (x) => {
    const open = S.exOpen.has(x.id);
    const state = (x.action && x.id !== 'hvci') || !x.ok ? '' : `<span class="st${x.on ? ' on' : ''}"><i></i>${x.on ? 'On' : 'Off'}</span>`;
    const btn = noBtn(x) ? '' : x.action ? `<button class="btn2 small" data-run="${x.id}">${x.id === 'hvci' ? 'Open Windows Security' : 'Run now'}</button>`
      : x.on ? `<button class="btn2 small" data-set="${x.id}" data-on="0">${x.ours ? 'Undo' : 'Turn off'}</button>` : `<button class="btn2 small${x.warn ? ' warn' : ''}" data-set="${x.id}" data-on="1">Turn on</button>`;
    const em = !x.ok && x.detail ? x.detail : x.short;
    let h = `<div class="exl${open ? ' open' : ''}${!x.ok ? ' na' : ''}" data-x="${x.id}"><span class="ch">›</span>
      <div class="nm">${esc(x.name)}${em ? `<em class="${x.warn && x.short === em ? 'warnc' : ''}">${esc(em)}</em>` : ''}</div>
      <div>${state}</div><div class="bt">${btn}</div></div>`;
    if (open) {
      const meta = [];
      if (x.detail && x.ok) meta.push('Now: ' + esc(x.detail));
      meta.push('Takes effect ' + esc(x.when));
      if (x.ours) meta.push('switched on by Frame Lab ' + esc(x.since) + ' (Undo puts back exactly what was there)');
      else if (x.on && !x.action && !x.info && !noBtn(x)) meta.push('switched on outside Frame Lab: Turn off sets the Windows default');
      h += `<div class="exd"><div class="w">${esc(x.what)}</div><div class="e">${esc(x.expect)}</div>${x.risk ? `<div class="x">Risk: ${esc(x.risk)}</div>` : ''}<div class="m">${meta.join(' · ')}</div></div>`;
    }
    return h;
  };
  let n = 0, body = '';
  const no = () => `<span class="no">${String(++n).padStart(2, '0')}</span>`;
  groups.forEach(g => {
    const items = list.filter(x => (x.group || '') === g.id || (!g.id));
    if (items.length) {
      const sub = g.id === 'cpu' && cpu && cpu.cores ? g.sub + ' · ' + cpu.name + ', ' + (cpu.hybrid ? cpu.p_cores + ' performance + ' + cpu.e_cores + ' efficiency cores' : cpu.cores + ' cores') : g.sub;
      body += `<div class="exg"><div class="gh"><h2>${no()}${esc(g.title)}</h2><span>${esc(sub)}</span></div>${items.map(row).join('')}</div>`;
    }
    if (g.id === 'cpu') body += nvProfileHtml(no);
  });
  root.innerHTML = `<div class="rep-head"><div class="title">Experiments</div>
    <div class="cf-builds">${st.config.builds.map((b, i) => `<button class="btn2 small" data-b="${i}" style="${i === S.exBuild ? 'border-color:var(--accent);color:var(--white)' : ''}">${esc(b.name)}</button>`).join('')}</div>
    <div class="actions"><button class="btn2" id="ex-undoall" title="Every setting Frame Lab changed (Experiments and Checks fixes) goes back to exactly what was there before">Undo everything</button><button class="btn2" id="ex-cmp">Compare sessions</button></div></div>
    <div class="dim" style="max-width:900px">Windows and driver settings people change to make games run better. Switch one on, play a session, then compare it with a session without it.
      Every session remembers what was on. Change one thing at a time. Click a setting for what it does.</div>
    ${note ? `<div style="margin-top:10px" class="${note.err ? 'lose' : 'win'}">${esc(note.text)}</div>` : ''}${body}`;
  $$('[data-b]', root).forEach(b => b.onclick = () => setBuild(+b.dataset.b));
  $('#ex-cmp', root).onclick = () => showTab('compare');
  $('#ex-undoall', root).onclick = async (e) => {
    const b = e.currentTarget;
    if (!b.dataset.armed) { b.dataset.armed = '1'; b.textContent = 'Click again to undo everything'; b.classList.add('warn'); setTimeout(() => { if (b.isConnected) { delete b.dataset.armed; b.textContent = 'Undo everything'; b.classList.remove('warn'); } }, 4000); return; }
    b.disabled = true; b.textContent = '...';
    try {
      const r = await api('undo/everything', {});
      const errs = r.errors || [];
      renderExperiments({ err: errs.length > 0, text: (r.undone ? `Put back ${r.undone} change${r.undone === 1 ? '' : 's'}: everything Frame Lab changed is back to how it was.` : 'Nothing to undo: Frame Lab has no changes on.') + (errs.length ? ' Could not undo: ' + errs.join('; ') : '') });
    } catch (err) { renderExperiments({ err: true, text: 'Undo everything: ' + err.message }); }
  };
  $$('.exl', root).forEach(el => el.onclick = (e) => {
    if (e.target.closest('button')) return;
    const id = el.dataset.x;
    if (S.exOpen.has(id)) S.exOpen.delete(id); else S.exOpen.add(id);
    renderExperiments(note);
  });
  $$('[data-set]', root).forEach(b => b.onclick = async () => {
    const x = list.find(e => e.id === b.dataset.set), on = b.dataset.on === '1';
    if (on && x.warn && !exConfirm(x)) return;
    b.disabled = true; b.textContent = '...';
    try { await api('experiments/set', { id: x.id, on, build: S.exBuild }); renderExperiments({ text: x.name + (on ? ': on.' : ': back to how it was.') + ' Takes effect ' + x.when + '.' }); }
    catch (e) { renderExperiments({ err: true, text: x.name + ': ' + e.message }); }
  });
  $$('[data-run]', root).forEach(b => b.onclick = async () => {
    const x = list.find(e => e.id === b.dataset.run);
    if (x.warn && !exConfirm(x)) return;
    b.disabled = true; b.textContent = '...';
    try { const r = await api('experiments/run', { id: x.id, build: S.exBuild }); renderExperiments({ text: r.message }); }
    catch (e) { renderExperiments({ err: true, text: x.name + ': ' + e.message }); }
  });
}
// the NVIDIA driver's settings for the game, read only
function nvProfileHtml(no) {
  const p = S.ex.nvprofile, err = S.ex.nverror;
  if ((!p || !p.length) && (!err || /^no NVIDIA driver/.test(err))) return '';
  let h = `<div class="exg"><div class="gh"><h2>${no()}NVIDIA driver profile</h2><span>Cyberpunk 2077 · what the driver uses now · only the NVIDIA switches under Graphics driver change it</span></div>`;
  if (p && p.length) {
    h += '<div class="kvg">' + p.map(s => `<div class="kv"${s.note ? ` title="${esc(s.note)}"` : ''}><span class="a">${esc(s.name)}</span><span class="b">${esc(s.value)}<small>${esc(s.from)}</small></span></div>`).join('') + '</div>';
    h += '<div class="foot" style="margin-top:8px">Read from the NVIDIA driver: the game\'s own profile first, then the global profile. Change them in the NVIDIA app or NVIDIA Control Panel. A frame cap or V-sync set here applies on top of the game\'s own settings.</div>';
  } else h += `<div class="dimmer" style="padding:9px 0">Could not read the driver profile: ${esc(err)}</div>`;
  return h + '</div>';
}
// Experiments: click twice to switch on a risky one (its own name, apart from the session dialog's confirmBox)
function exConfirm(x) {
  if (exConfirm._armed === x.id) { exConfirm._armed = null; return true; }
  exConfirm._armed = x.id;
  toast(x.name + ': ' + (x.risk || 'are you sure?') + ' Click again to go ahead.', true);
  return false;
}

/* ------------------------------------------------------------------ checks */
// read only, except the few fixes a row offers; every fix is kept in the undo journal
const CK_CLASS = { fine: 'ok', check: 'chk', found: 'chk', info: 'nfo', na: 'na' };
const CK_RISKY = { bcd_clear: 'changes the Windows boot settings (needs a restart). If BitLocker is on, have the recovery key at hand.', cpusets_clear: 'changes a Windows kernel setting (needs a restart).' };
async function renderChecks(note, fresh) {
  const root = $('#tab-checks');
  if (!S.state) { root.innerHTML = '<div class="empty">Loading ...</div>'; return; }
  const st = S.state;
  S.ckBuild = st.config.active || 0; // the header's build
  if (!S.ck || fresh) root.innerHTML = '<div class="empty">Reading the PC and the game folder ...</div>';
  try { S.ck = await api('checks', { build: S.ckBuild, fresh: !!fresh }); } catch (e) { root.innerHTML = '<div class="card"><span class="lose">' + esc(e.message) + '</span></div>'; return; }
  if (S.tab !== 'checks') return;
  const rep = S.ck.report, undo = new Set((S.ck.undo || []).map(u => u.id));
  S.ckOpen = S.ckOpen || new Set();
  let n = 0, body = '', flagged = 0;
  const no = () => `<span class="no">${String(++n).padStart(2, '0')}</span>`;
  for (const g of rep.groups || []) {
    body += `<div class="exg"><div class="gh"><h2>${no()}${esc(g.title)}</h2><span>${esc(g.sub)}</span></div>`;
    for (const x of g.rows || []) {
      const cls = CK_CLASS[x.status] || 'nfo', key = g.id + '/' + x.id, open = S.ckOpen.has(key) && x.what;
      if (cls === 'chk') flagged++;
      let btn = '';
      if (x.action) btn = `<button class="btn2 small${cls === 'chk' ? ' warn' : ''}" data-ck="${esc(x.action)}">${esc(x.action_label)}</button>`;
      else if (x.diy && !(x.undo_id && undo.has(x.undo_id))) btn = `<button class="btn2 small${cls === 'chk' ? ' warn' : ''}" data-ckhow="${esc(key)}">How to fix</button>`;
      else if (x.undo_id && undo.has(x.undo_id)) btn = `<button class="btn2 small" data-ckundo="${esc(x.undo_id)}">Undo</button>`;
      body += `<div class="exl${cls === 'na' ? ' na' : ''}${open ? ' open' : ''}" data-ckrow="${esc(key)}"><span class="ch">${x.what ? '›' : ''}</span>
        <div class="nm">${esc(x.name)}<em class="${cls === 'chk' ? 'warnc' : ''}">${esc(x.detail)}</em></div>
        <div>${x.label ? `<span class="st ${cls}"><i></i>${esc(x.label)}</span>` : ''}</div><div class="bt">${btn}</div></div>`;
      if (open) body += `<div class="exd"><div class="w">${esc(x.what)}</div>${x.diy && !x.action ? `<div class="diy"><b>Do it yourself:</b> ${esc(x.diy)}${x.open ? ` <button class="btn2 small" data-ckopen="${esc(x.open)}">${esc(x.open_label || 'Open')}</button>` : ''}</div>` : ''}${x.efix && !EX() ? `<div class="enote">${E_NOTE}</div>` : ''}${x.undo_id && undo.has(x.undo_id) ? '<div class="m">Fixed by Frame Lab: Undo puts back exactly what was there.</div>' : ''}</div>`;
    }
    body += '</div>';
  }
  const at = rep.at ? new Date(rep.at).toLocaleString('en-AU', { day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit' }) : '';
  root.innerHTML = `<div class="rep-head"><div class="title">PC checks</div>
    <div class="cf-builds">${st.config.builds.map((b, i) => `<button class="btn2 small" data-b="${i}" style="${i === S.ckBuild ? 'border-color:var(--accent);color:var(--white)' : ''}">${esc(b.name)}</button>`).join('')}</div>
    <div class="actions"><button class="btn" id="ck-run">Run checks</button></div></div>
    <div class="dim" style="max-width:900px">Things that quietly cost frames or add stutter. Frame Lab only reads here. ${EX() ? 'Where something can be changed, the row says how, and Undo puts it back.' : 'Where something should change, <b>How to fix</b> gives the steps and opens the right Windows page.'}
    Checked ${esc(at)} · ${flagged ? flagged + ' to look at' : 'nothing to look at'} · run again after you change something.</div>
    ${note ? `<div style="margin-top:10px" class="${note.err ? 'lose' : 'win'}">${esc(note.text)}</div>` : ''}${body}`;
  $$('[data-b]', root).forEach(b => b.onclick = () => setBuild(+b.dataset.b));
  $('#ck-run', root).onclick = () => renderChecks(null, true);
  $$('[data-ckrow]', root).forEach(el => el.onclick = (e) => {
    if (e.target.closest('button')) return;
    const k = el.dataset.ckrow;
    if (S.ckOpen.has(k)) S.ckOpen.delete(k); else S.ckOpen.add(k);
    renderChecks(note);
  });
  $$('[data-ck]', root).forEach(b => b.onclick = async () => {
    const id = b.dataset.ck;
    if (id.startsWith('goto:')) { showTab(id.slice(5)); return; }
    if (CK_RISKY[id] && !ckConfirm(id)) return;
    b.disabled = true; b.textContent = '...';
    try { const r = await api('checks/act', { build: S.ckBuild, id }); renderChecks({ text: r.message }, true); }
    catch (e) { renderChecks({ err: true, text: e.message }, true); }
  });
  $$('[data-ckhow]', root).forEach(b => b.onclick = () => { S.ckOpen.add(b.dataset.ckhow); renderChecks(note); });
  $$('[data-ckopen]', root).forEach(b => b.onclick = (e) => { e.stopPropagation(); openTarget(b.dataset.ckopen); });
  $$('[data-ckundo]', root).forEach(b => b.onclick = async () => {
    b.disabled = true; b.textContent = '...';
    try { await api('undo/one', { id: b.dataset.ckundo }); renderChecks({ text: 'Put back how it was.' }, true); }
    catch (e) { renderChecks({ err: true, text: e.message }, true); }
  });
}
function ckConfirm(id) {
  if (ckConfirm._armed === id) { ckConfirm._armed = null; return true; }
  ckConfirm._armed = id;
  toast('This ' + CK_RISKY[id] + ' Click again to go ahead.', true);
  return false;
}

/* ------------------------------------------------------------------ conflicts */
async function loadConflicts() {
  const st = S.state;
  if (!st) return;
  S.cfBuild = st.config.active || 0; // the header's build
  try { S.cf = await api('conflicts', { build: S.cfBuild }); } catch (e) { S.cf = { error: e.message }; }
  if (S.cf && S.cf.state === 'checking') { clearTimeout(S.cfTimer); S.cfTimer = setTimeout(() => { if (S.tab === 'conflicts') renderConflicts(); }, 1000); }
}

async function renderConflicts() {
  const root = $('#tab-conflicts');
  if (!S.state) { root.innerHTML = '<div class="empty">Loading ...</div>'; return; }
  await loadConflicts();
  const st = S.state, cf = S.cf || {}, r = cf.report;
  const k = (l, v, s, cls) => `<div class="k ${cls || ''}"><div class="l">${l}</div><div class="v">${v}</div>${s ? '<div class="s">' + s + '</div>' : ''}</div>`;
  const checking = cf.state === 'checking';
  let html = `<div class="rep-head"><div class="title">Conflicts and errors</div>
    <div class="cf-builds">${st.config.builds.map((b, i) => `<button class="btn2 small ${i === S.cfBuild ? 'on' : ''}" data-b="${i}" style="${i === S.cfBuild ? 'border-color:var(--accent);color:var(--white)' : ''}">${esc(b.name)}</button>`).join('')}</div>
    <div class="actions"><button class="btn" id="cf-run" ${checking ? 'disabled' : ''}>${checking ? 'Checking ...' : 'Check build'}</button></div></div>`;
  if (cf.error) html += `<div class="card"><span class="lose">${esc(cf.error)}</span></div>`;
  if (cf.state && cf.state.startsWith('error')) html += `<div class="card"><span class="lose">${esc(cf.state)}</span></div>`;
  if (!r) {
    html += `<div class="card"><div class="dim">Not checked yet. Check build reads the build's files without starting the game (a few seconds):</div>
      <ul class="dim" style="margin:8px 0 0 18px;padding:0">
      <li>archives that replace the same game file, which one wins, and whether the game loaded those files in your last session</li>
      <li>REDscript mods that replace the same game function (only one replacement runs)</li>
      <li>TweakXL files that set the same value differently</li>
      <li>errors and warnings in the last logs of RED4ext and its plugins (ArchiveXL, TweakXL, Codeware ...), REDscript and CET mods</li></ul></div>`;
    root.innerHTML = html; bindConflicts(root); return;
  }
  const A = r.archives || [], pairs = r.pairs || [];
  const harmless = (p) => p.patch || p.slot || p.same === p.files;
  const real = pairs.filter(p => !harmless(p)), patches = pairs.filter(harmless);
  const realLoaded = real.reduce((a, p) => a + p.loaded, 0);
  const logE = (r.logs || []).reduce((a, l) => a + l.errors, 0), logW = (r.logs || []).reduce((a, l) => a + l.warnings, 0);
  const tw = r.tweaks || [], twReal = tw.filter(t => !t.patch);
  const when = new Date(r.generated);
  html += `<div class="dim small" style="margin:-4px 0 10px">Checked ${esc(when.toLocaleString('en-AU'))} in ${r.took_s.toFixed(1)} s · ${A.length} mod archives, ${r.scripts_scanned} .reds files, ${r.tweaks_scanned} tweak files${r.session ? ' · "loaded in game" = your session ' + esc(r.session) : ' · no session of this build yet, so "loaded in game" is unknown'}</div>`;
  html += '<div class="kpi">' +
    k('archive clashes to check', real.length, real.reduce((a, p) => a + p.files, 0) + ' files · ' + (r.session ? realLoaded + ' loaded in game' : 'not played yet'), real.length ? (realLoaded ? 'bad' : 'warn') : 'good') +
    k('harmless', patches.length, 'patches, framework slots, identical copies', '') +
    k('files in 2+ archives', fmt.n(r.contested), 'only the first loaded counts', '') +
    k('REDscript', (r.scripts || []).length, 'functions replaced by 2+ mods', (r.scripts || []).length ? 'bad' : 'good') +
    k('TweakXL', twReal.length, 'values set differently' + (tw.length > twReal.length ? ' · ' + (tw.length - twReal.length) + ' by patches' : ''), twReal.length ? 'warn' : 'good') +
    k('log problems', logE + ' / ' + logW, 'errors / warnings', logE ? 'bad' : logW ? 'warn' : 'good') + '</div>';

  // archives
  // 2.5E: let the losing archive win (archive\pc\mod\modlist.txt), one pair at a time
  const cfWinBtn = (w, l, p) => {
    if (!EX() || p.same === p.files) return '';
    const loose = (x) => /^archive\/pc\/mod\/[^/]+$/.test(x.rel);
    if (!loose(w) || !loose(l)) return '<div class="dimmer small" style="margin-top:8px">A REDmod archive loads by its mods folder\'s name, so Frame Lab can\'t change this order.</div>' + cfMergeBtns(w, l, p);
    return `<div style="margin-top:10px"><button class="btn2 small" data-cfwin="${esc(w.rel)}" data-cflose="${esc(l.rel)}" data-cfname="${esc(l.label)}">Let ${esc(l.label)} win</button> <span class="dimmer small">puts it before ${esc(w.label)} in archive\\pc\\mod\\modlist.txt; Undo is in Optimise</span></div>` + cfMergeBtns(w, l, p);
  };
  // 2.5E: keep both. Text files (.json) are merged by Frame Lab; scenes and quest files open side by side in the Workbench
  const cfMergeBtns = (w, l, p) => {
    if (!EX() || p.same === p.files) return '';
    const kinds = p.kinds || '', json = kinds.split(',').every(k => /^\s*\d+ json\s*$/.test(k)) && kinds.trim() !== '';
    const scene = /scene|questphase|quest|journal/.test(kinds);
    const ids = `data-mw="${esc(w.rel)}" data-ml="${esc(l.rel)}"`;
    let h = '';
    if (json) h += `<div style="margin-top:8px"><button class="btn2 small" data-cfmerge ${ids}>Merge both into one</button> <span class="dimmer small">both mods' entries in one new archive that loads first; where both change the same entry, ${esc(w.label)}'s stays. Neither mod is changed; Undo is in Optimise</span>
      <div class="small" data-mstate ${ids}></div></div>`;
    if (scene) h += `<div style="margin-top:8px"><button class="btn2 small" data-cfwb ${ids}>Open both in the Workbench</button> <span class="dimmer small">scenes and quest files can only be merged by hand: both archives unpacked and readable, side by side</span></div>`;
    return h;
  };
  const pairRow = (p) => {
    const w = A[p.winner], l = A[p.loser];
    const note = p.same === p.files ? '<span class="tag g">identical copies</span>' : p.slot ? '<span class="tag g">framework slot</span>' : p.patch ? '<span class="tag g">patch, on purpose</span>' : p.order !== 'sure' ? '<span class="tag y">' + esc(p.order) + '</span>' : (p.loaded ? '<span class="tag r">used in game</span>' : '<span class="tag">check</span>');
    const files = (p.sample || []).map(f => `<div>${f.loaded ? '<span class="tag r">loaded</span> ' : ''}${f.same ? '<span class="tag g">identical</span> ' : ''}<span class="mono">${esc(f.name || f.hash)}</span></div>`).join('') + (p.files > (p.sample || []).length ? `<div class="dimmer">+ ${p.files - p.sample.length} more</div>` : '') +
      (p.same && p.same < p.files ? `<div class="dimmer">${p.same} of the ${p.files} files are identical in both</div>` : '');
    return `<details class="cf"><summary class="cf-row"><div><span class="chev">›</span><span class="win">${esc(w.label)}</span><div class="dimmer small" style="margin-left:12px">#${w.order} · ${esc(w.rel.split('/').pop())}</div></div><div class="dimmer">›</div>
      <div><span class="lose">${esc(l.label)}</span><div class="dimmer small">#${l.order} · ${esc(l.rel.split('/').pop())}</div></div>
      <div class="n">${fmt.n(p.files)}</div><div class="n">${r.session ? (p.loaded ? '<b class="lose">' + p.loaded + '</b>' : '0') : '–'}</div><div class="dim">${esc(p.kinds)}</div><div>${note}</div></summary>
      <div class="cf-files">${files}${cfWinBtn(w, l, p)}</div></details>`;
  };
  const head = `<div class="cf-row head"><div>wins (loads first)</div><div></div><div>its files are ignored</div><div class="n">files</div><div class="n">loaded in game</div><div>file types</div><div></div></div>`;
  html += `<div class="cf-sec card"><h2>Archives<span class="dim">two mods replace the same game file: the archive that loads first is used, the other one's copy is ignored</span></h2>`;
  html += real.length ? head + real.map(pairRow).join('') : '<div class="dim">No clashes between mods (apart from compatibility patches).</div>';
  if (patches.length) html += `<details class="cf" style="margin-top:10px"><summary class="dim"><span class="chev">›</span>${patches.length} harmless: compatibility patches / merges that win on purpose, framework placeholder files that mods are meant to replace, and byte-identical copies</summary>${head}${patches.map(pairRow).join('')}</details>`;
  html += `<div class="dimmer small" style="margin-top:10px">Load order: ${esc(r.order)}. ${EX() ? 'Open a clash and press Let … win to make the other mod\'s files the ones used (Frame Lab writes archive\\pc\\mod\\modlist.txt; Undo is in Optimise > Fixes made on this PC).' : 'To make a mod win, rename its .archive so it sorts earlier (a leading # or 0) — or remove the one you don\'t want.'} File names show once the file-name list is built (Settings > Parts > File-name list).</div></div>`;

  // what the downloaded mods set a clashing TweakXL value to (no game value: TweakXL only sees mods)
  const twOthers = (key) => {
    const o = (cf.tweak_others || {})[key], nm = cf.mod_names || {};
    if (!o || !o.length) return '';
    return `<div class="dimmer small" style="font-family:var(--sans);margin-top:4px">In the downloaded mods: ${o.slice(0, 4).map(x => `<b>${esc(x.value)}</b> (${x.mods.length} mod${x.mods.length === 1 ? '' : 's'}${x.mods.length <= 2 ? ': ' + x.mods.map(id => esc(nm[id] || 'Nexus ' + id)).join(', ') : ''})`).join(' · ')}${o.length > 4 ? ' · …' : ''}</div>`;
  };
  // REDscript
  const sc = r.scripts || [];
  html += `<div class="cf-sec card"><h2>REDscript<span class="dim">two mods replace the same game function: only one of the replacements runs</span></h2>` +
    (sc.length ? `<table><tr><th>function</th><th>replaced by</th><th>also wrapped by (fine)</th></tr>${sc.map(x => `<tr><td class="mono">${esc(x.target)}</td><td>${x.mods.map(m => '<div class="lose">' + esc(m) + '</div>').join('')}</td><td class="dim">${esc((x.wraps || []).join(', ') || '–')}</td></tr>`).join('')}</table>`
      : `<div class="dim">None: no game function is replaced by more than one mod (${r.scripts_scanned} .reds files read).</div>`) + '</div>';

  // TweakXL
  html += `<div class="cf-sec card"><h2>TweakXL<span class="dim">the same game value set to different things: only one sticks</span></h2>` +
    (tw.length ? `<table><tr><th>value</th><th>mod</th><th>sets it to</th><th></th></tr>${tw.map(t => t.values.map((v, i) => `<tr><td class="mono">${i ? '' : esc(t.key) + twOthers(t.key)}</td><td>${esc(v.mod)}<div class="dimmer small">${esc(v.file)}</div></td><td class="mono">${esc(v.value)}</td><td>${i || !t.patch ? '' : '<span class="tag g">patch</span>'}${EX() ? ` <button class="btn2 small" data-twkey="${esc(t.key)}" data-twval="${esc(v.value)}" data-twmod="${esc(v.mod)}">Use this</button>` : ''}</td></tr>`).join('')).join('')}</table>
      <div class="dimmer small" style="margin-top:8px">Simple "key: value" lines only; lists and appends combine and are not clashes.</div>`
      : `<div class="dim">None found (${r.tweaks_scanned} tweak files read).</div>`) + '</div>';

  // logs (2.5E: a named fix next to the errors Frame Lab knows what to do about)
  const LF = cf.log_fixes || [];
  const lfBtns = (log, text) => !EX() ? '' : LF.filter(f => f.log === log && f.text === text).map(f =>
    ` <button class="btn2 small" data-lfkind="${esc(f.kind)}" data-lffile="${esc(f.file)}" data-lfindex="${f.index || 0}" data-lftitle="${esc(f.title)}" title="${esc(f.what)}">${f.kind === 'file_off' ? 'Switch ' + esc(f.file.split('/').pop()) + ' off' : 'Fix: ' + esc(f.title)}</button>` +
    (f.kind === 'file_off' && /\.(reds|xl|ya?ml)$/i.test(f.file) ? ` <button class="btn2 small" data-wbedit="${esc(f.file)}" title="edit it by hand, with checks">Open in Workbench</button>` : '')).join('');
  const nFix = LF.length;
  const logs = (r.logs || []).filter(l => l.errors || l.warnings), clean = (r.logs || []).length - logs.length;
  html += `<div class="cf-sec card"><h2>Logs<span class="dim">errors and warnings in the last log of each framework and plugin · ${clean} logs without any${EX() && nFix ? ' · ' + nFix + ' with a fix (open a log)' : ''}</span></h2>` +
    (logs.length ? logs.map(l => `<details class="cf"><summary class="cf-row" style="grid-template-columns:minmax(0,1fr) 90px 90px minmax(0,2fr)"><div><span class="chev">›</span><b>${esc(l.name)}</b></div>
      <div class="n">${l.errors ? '<span class="lvl-e">' + l.errors + ' errors</span>' : '<span class="dimmer">0 errors</span>'}</div><div class="n">${l.warnings ? '<span class="lvl-w">' + l.warnings + ' warn.</span>' : ''}</div>
      <div class="dimmer small">${esc(l.file)} · ${esc(new Date(l.when).toLocaleString('en-AU'))}</div></summary>
      <div class="cf-files loglines">${(l.lines || []).map(x => `<div><span class="${x.level === 'error' ? 'lvl-e' : 'lvl-w'}">${x.level === 'error' ? 'E' : 'W'}</span>${x.count > 1 ? ' <span class="dimmer">×' + x.count + '</span>' : ''} ${esc(x.text)}${lfBtns(l.name, x.text)}</div>`).join('')}</div></details>`).join('')
      : '<div class="dim">No errors or warnings.</div>') + '</div>';
  if (r.notes && r.notes.length) html += `<div class="cf-sec card dim">${r.notes.map(esc).join('<br>')}</div>`;
  root.innerHTML = html;
  bindConflicts(root);
}

function bindConflicts(root) {
  $$('[data-b]', root).forEach(b => b.onclick = () => setBuild(+b.dataset.b));
  $$('[data-cfmerge]', root).forEach(b => b.onclick = async () => {
    if (!confirm('Merge both mods\' versions of these text files?\n\nFrame Lab unpacks both archives with the WolvenKit console, puts the entries of both into one new archive that loads before both, and installs it (the two mods stay as they are; Undo in Optimise takes it out). Close the game first.')) return;
    b.disabled = true;
    try { await api('conflicts/merge', { build: S.cfBuild, winner: b.dataset.mw, loser: b.dataset.ml }); cfMergePoll(root); } catch (e) { toast(e.message, true); b.disabled = false; }
  });
  $$('[data-cfwb]', root).forEach(b => b.onclick = async () => {
    b.disabled = true;
    try { await api('conflicts/workbench', { build: S.cfBuild, winner: b.dataset.mw, loser: b.dataset.ml }); toast('Both archives are being unpacked in the Workbench.'); showTab('workbench'); } catch (e) { toast(e.message, true); b.disabled = false; }
  });
  cfMergePoll(root);
  $$('[data-cfwin]', root).forEach(b => b.onclick = async () => {
    if (!confirm(`Let ${b.dataset.cfname} win this clash?\n\nFrame Lab writes archive\\pc\\mod\\modlist.txt with it before the other archive (every other archive keeps its place). The old file is kept and Undo puts it back. Close the game first.`)) return;
    b.disabled = true;
    try { await api('conflicts/fix', { build: S.cfBuild, kind: 'archive_win', winner: b.dataset.cfwin, loser: b.dataset.cflose }); toast(b.dataset.cfname + ' now wins. It counts from the next game start.'); setTimeout(renderConflicts, 800); }
    catch (e) { toast(e.message, true); b.disabled = false; }
  });
  $$('[data-wbedit]', root).forEach(b => b.onclick = () => openInWorkbench(S.cfBuild, b.dataset.wbedit, 0));
  $$('[data-lfkind]', root).forEach(b => b.onclick = async () => {
    const off = b.dataset.lfkind === 'file_off';
    if (!confirm(`${b.dataset.lftitle}?\n\n${b.title}\n\n${off ? 'The file goes into Frame Lab\'s own data folder; Undo (Optimise > Fixes made on this PC) puts it back.' : 'The original is kept and Undo puts it back.'} Close the game first.`)) return;
    b.disabled = true;
    try { await api('conflicts/fix', { build: S.cfBuild, kind: b.dataset.lfkind, file: b.dataset.lffile, index: +b.dataset.lfindex }); toast('Done. It counts from the next game start.'); setTimeout(renderConflicts, 800); }
    catch (e) { toast(e.message, true); b.disabled = false; }
  });
  $$('[data-twkey]', root).forEach(b => b.onclick = async () => {
    if (!confirm(`${b.dataset.twkey}: use ${b.dataset.twmod}'s value (${b.dataset.twval})?\n\nFrame Lab adds one small file under r6\\tweaks\\zzzzzz_FrameLab that TweakXL reads last. Undo removes it. Close the game first.`)) return;
    b.disabled = true;
    try { await api('conflicts/fix', { build: S.cfBuild, kind: 'tweak_pick', key: b.dataset.twkey, value: b.dataset.twval }); toast('Picked. It counts from the next game start.'); setTimeout(renderConflicts, 800); }
    catch (e) { toast(e.message, true); b.disabled = false; }
  });
  const run = $('#cf-run', root);
  if (run) run.onclick = async () => { try { await api('conflicts/check', { build: S.cfBuild }); setTimeout(renderConflicts, 300); } catch (e) { toast(e.message, true); } };
}

/* ------------------------------------------------------------------ optimise (2.5) */
async function loadOptimise() {
  const st = S.state;
  if (!st) return;
  S.opBuild = st.config.active || 0; // the header's build
  try { S.op = await api('optimise', { build: S.opBuild }); } catch (e) { S.op = { error: e.message }; }
  if (S.op && (S.op.state === 'checking' || S.op.conflict_state === 'checking')) { clearTimeout(S.opTimer); S.opTimer = setTimeout(() => { if (S.tab === 'optimise') renderOptimise(); }, 1000); }
}

/* the test schedule (2.5): Optimise is locked until the build has been measured, and every change is
   measured again on the same routes */
const SCHED_STEPS = [['routes', 'Record the test routes'], ['conflicts', 'Conflict check'], ['analyse', 'Analyse the build'], ['fix', 'Fix'], ['retest', 'Test again']];
const SCHED_ORDER = ['start', 'routes', 'conflicts', 'analyse', 'fix', 'retest', 'decide'];
const ROUND_TAG = { better: ['g', 'better'], same: ['', 'no difference'], worse: ['r', 'worse'], mixed: ['y', 'mixed'] };
function schedRouteRows(sc, compare) {
  const num = (run, k, d) => run && run.stat[k] ? Number(run.stat[k]).toFixed(d == null ? 1 : d).replace(/\.0$/, '') : '–';
  const st10 = (run) => run && run.stat.frames ? String(+Number(run.stat.stutters_10).toFixed(1)) : '–';
  const pair = (a, b) => compare ? `<span class="dimmer">${a} →</span> ${b}` : b;
  return (sc.routes || []).map(x => {
    const run = x.run || (compare ? null : x.base);
    let state;
    const inWhere = x.where ? ` <span class="dim small">in ${esc(x.where)}</span>` : '';
    if (x.done && x.run) state = `<span class="tag g">recorded</span>${inWhere} <span class="dimmer small">· ${esc(x.run.title)}, ${esc(new Date(x.run.when).toLocaleString('en-AU', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' }))}</span>`;
    else if (x.done) state = '<span class="tag g">measured</span>' + inWhere;
    else if (x.best) state = `<span class="tag y">${fmt.dur(x.best.stat.seconds)} of ${routeNeed(x.def.seconds)}</span> <span class="dimmer small">· ${esc(x.best.title)}</span>`;
    else state = '<span class="tag">not recorded yet</span>';
    if (x.rejected) state += `<div class="sched-rej">${esc(x.rejected)}</div>`;
    return `<tr><td><b>${esc(x.def.name)}</b><div class="dim small">${esc(x.def.how)}</div></td><td class="n">${routeNeed(x.def.seconds)}</td><td>${state}</td>
      <td class="n">${pair(num(x.base, 'avg_fps'), num(run, 'avg_fps'))}</td><td class="n">${pair(num(x.base, 'low1_fps'), num(run, 'low1_fps'))}</td>
      <td class="n">${pair(st10(x.base), st10(run))}</td><td class="n">${pair(num(x.base, 'script_ms_s', 2), num(run, 'script_ms_s', 2))}</td></tr>`;
  }).join('');
}
// the test recordings of this step in plain words: what was played, and what it did for each route
function schedRecs(sc) {
  const R = sc.recs || [];
  if (!R.length) return '';
  return `<div class="sched-recs"><div class="dim small">What your recordings counted as:</div>${R.map(x => `<div class="sched-rec"><b>${esc(x.title)}</b> <span class="dimmer small">${esc(new Date(x.when).toLocaleString('en-AU', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' }))}</span>
    <div>${esc(x.did)}</div>${x.note ? `<div class="sched-rej">It doesn't count: ${esc(x.note)}.</div>` : ''}
    <ul>${(x.counts || []).map(t => `<li>${esc(t)}</li>`).join('')}</ul></div>`).join('')}</div>`;
}
function schedTable(sc, compare) {
  return `<table class="sched-routes"><tr><th>route</th><th class="n">needs</th><th></th><th class="n">avg fps</th><th class="n">1% low</th><th class="n">stutters / 10 min</th><th class="n">mod scripts ms/s</th></tr>${schedRouteRows(sc, compare)}</table>`;
}
function schedResult(op) {
  const L = op.schedule.last;
  if (!L) return '';
  const tag = ROUND_TAG[L.kind] || ROUND_TAG.same;
  const rows = (L.result || []).map(c => {
    const t = ROUND_TAG[c.verdict] || ROUND_TAG.same;
    return (c.rows || []).map((x, i) => { const f = FOLLOW_TAG[x.verdict] || FOLLOW_TAG.same; return `<tr${i === 0 ? ' class="sched-first"' : ''}>${i === 0 ? `<td rowspan="${c.rows.length}"><b>${esc(c.name)}</b><div><span class="tag ${t[0]}">${t[1]}</span></div></td>` : ''}
      <td>${esc(x.label)}</td><td class="n">${followNum(x.was, x.unit)}</td><td class="n">${followNum(x.now, x.unit)}</td><td><span class="tag ${f[0]}">${f[1]}</span></td></tr>`; }).join('');
  }).join('');
  const inPlace = (op.fixes || []).filter(f => !f.undone && !f.gone && (L.changes || []).some(c => c.fix === f.id && !c.undo));
  const changes = (L.changes || []).map(c => `<li>${esc(c.text)}</li>`).concat((L.by_hand || []).map(c => `<li>Mod files changed outside Frame Lab: ${esc(c)}</li>`)).join('');
  return `<div class="cf-sec card next sched-result ${esc(L.kind || '')}"><h2>Result of round ${L.n}<span class="dim">the same routes before and after, ${esc(new Date(L.closed).toLocaleString('en-AU'))}</span></h2>
    <div class="next-verdict"><span class="tag ${tag[0]}">${tag[1]}</span> ${esc(L.verdict || '')}</div>
    ${changes ? `<div class="dim small" style="margin:6px 0 2px">What was changed in this round:</div><ul class="sched-changes">${changes}</ul>` : ''}
    <table><tr><th>route</th><th>what</th><th class="n">before</th><th class="n">after</th><th></th></tr>${rows}</table>
    <div class="dimmer small" style="margin-top:8px">A number counts as changed when it moved by at least a tenth (and by 2 fps, 2 stutters per 10 minutes or 0.5 ms). Two runs of the same route are never identical: smaller moves are noise. Test recordings time every script call, which costs a little frame rate on both sides alike.</div>
    ${inPlace.length && (L.kind === 'worse' || L.kind === 'mixed') ? `<div style="margin-top:10px"><button class="btn2" id="sc-undo-round" data-ids="${esc(inPlace.map(f => f.id).join(','))}">Undo this round's ${inPlace.length} fix${inPlace.length === 1 ? '' : 'es'}</button> <span class="dim small">puts the original files back; the routes are then recorded once more to confirm.</span></div>` : ''}</div>`;
}
function schedHtml(op) {
  const sc = op.schedule;
  if (!sc) return '';
  const at = SCHED_ORDER.indexOf(sc.step);
  const cls = (id) => { const i = SCHED_ORDER.indexOf(id); return i < at ? 'done' : i === at ? 'now' : ''; };
  const steps = SCHED_STEPS.map(([id, label], i) => `<div class="sched-step ${cls(id)}"><i>${cls(id) === 'done' ? '✓' : i + 1}</i><span>${label}${id === 'routes' && sc.step === 'routes' ? ' · ' + sc.done + ' of ' + (sc.routes || []).length : ''}${id === 'retest' && sc.step === 'retest' ? ' · ' + sc.done + ' of ' + (sc.routes || []).length : ''}</span></div>`).join('');
  const changes = (sc.changes || []).map(c => `<li>${esc(c.text)}</li>`).concat((sc.by_hand || []).map(c => `<li>Mod files changed outside Frame Lab: ${esc(c)}</li>`)).join('');
  let body = '';
  if (sc.step === 'start') {
    body = `<div class="dim" style="max-width:920px">Optimise changes nothing until the build has been measured. Record the ${(sc.routes || []).length} kinds of play below (about ${Math.ceil((sc.routes || []).reduce((a, x) => a + x.def.seconds, 0) / 60) + 1} minutes in all, in one session or several), run the conflict check and the analysis: then the fixes open.
      After every change the same is recorded again, in the same districts and with the same game settings, and Frame Lab shows each one before and after. The game has to start and run for this: a build that doesn't start is a job for PC checks and the game's logs, not for Optimise.</div>
      ${schedTable(sc, false)}
      ${sc.tip ? `<div class="sched-tip">${esc(sc.tip)}</div>` : ''}
      <div style="margin-top:12px"><button class="btn" id="sc-start">Start the test round</button> <span class="dim small">From then on, new sessions of this build are test recordings until the routes are in.</span></div>`;
  } else if (sc.step === 'routes' || sc.step === 'retest') {
    body = (sc.step === 'retest' && changes ? `<div class="dim small" style="margin-bottom:2px">Changed since the last measurement:</div><ul class="sched-changes">${changes}</ul>` : '') +
      schedTable(sc, sc.step === 'retest') + (sc.tip ? `<div class="sched-tip">${esc(sc.tip)}</div>` : '') + schedRecs(sc) +
      `<div style="margin-top:12px"><button class="btn" data-go="capture">Open Record</button> <span class="dim small">The next session of this build is a test recording: script time is measured for the whole run. Record them in any order; a line at the top of the game screen and the Record page show how far each one is. Keep the game settings the same for every recording.</span></div>` +
      (sc.trial ? `<div style="margin-top:10px"><button class="btn2 small" id="opt-drop">Drop the trial</button> <span class="dim small">Ends the trial without a result. Nothing was changed in the build, so the fixes open again straight away.</span></div>` : '');
  } else if (sc.step === 'decide') {
    body = (changes ? `<ul class="sched-changes">${changes}</ul>` : '') + schedTable(sc, true) +
      `<div class="sched-decide"><button class="btn" id="opt-keep">Keep it</button> <button class="btn2" id="opt-drop">Drop it</button>
        <div class="dim small"><b>Keep:</b> Frame Lab puts a small Cyber Engine Tweaks mod of its own into this build that sets this one option every time the game starts (close the game first). You can take it out again at any time. The next change is then measured against the numbers with the option.<br>
        <b>Drop:</b> nothing stays behind. The option was only ever set while you recorded, and the numbers from before the trial stay the baseline.</div></div>`;
  } else if (sc.step === 'conflicts') {
    const busy = op.conflict_state === 'checking';
    body = `<div style="margin-top:4px"><button class="btn" id="sc-conf" ${busy ? 'disabled' : ''}>${busy ? 'Checking ...' : 'Run the conflict check'}</button> <span class="dim small">Reads the build's archives, REDscript and TweakXL files and the logs of the test recordings. It changes nothing.</span></div>
      <details class="cf" style="margin-top:10px"><summary class="dim small"><span class="chev">›</span>the routes as recorded</summary>${schedTable(sc, false)}</details>`;
  } else if (sc.step === 'analyse') {
    const busy = op.state === 'checking';
    body = `<div style="margin-top:4px"><button class="btn" id="sc-ana" ${busy ? 'disabled' : ''}>${busy ? 'Analysing ...' : 'Analyse the build'}</button> <span class="dim small">Reads every mod file and puts it next to the recordings and the conflict check. After this the fixes are open.</span></div>`;
  } else {
    body = `<details class="cf"><summary class="dim small"><span class="chev">›</span>the numbers the next change is measured against</summary>${schedTable(sc, false)}</details>`;
  }
  const head = sc.step === 'decide' ? 'keep or drop the option' : sc.trial ? 'a game option is on trial' : sc.step === 'start' ? 'Optimise is locked' : sc.locked ? 'Optimise is locked' : sc.step === 'retest' ? 'retest needed' : 'fixes are open';
  return `<div class="cf-sec card sched ${sc.trial ? 'trial' : sc.locked ? 'locked' : 'open'}"><h2>Test schedule<span class="dim">${head}${sc.round ? ' · round ' + sc.round : ''}${sc.started ? ' · started ' + esc(sc.started) : ''}</span></h2>
    <div class="sched-steps">${steps}</div>
    <div class="sched-why ${sc.locked ? 'locked' : ''}">${sc.step === 'decide' ? '<span class="tag y">your call</span> ' : sc.trial ? '<span class="tag y">on trial</span> ' : sc.locked ? '<span class="tag r">locked</span> ' : ''}${esc(sc.why || '')}</div>
    ${body}
    ${sc.step !== 'start' ? '<div class="sched-foot"><button class="btn2 small" id="sc-restart">Start over</button> <span class="dimmer small">a new baseline from the build as it is now; fixes already made stay in place.</span></div>' : ''}</div>` + schedResult(op);
}

/* game options, one at a time (2.5): one of the game's own options on trial, judged on the test routes */
/* ------------------------------------------------------------------ Game options (2.5E)
   The game's own options with a slider or a switch, set by Frame Lab's small CET mod. While the game
   runs the mod picks a change up within seconds, so the page never re-renders under a slider: only
   the values and the status line are updated in place. */
const goUnit = (r, v) => {
  let t = optText(String(v));
  if (r.kind === 'num' && !isNaN(+v) && String(v).trim() !== '') t = String(Math.abs(+v) >= 100 ? Math.round(+v) : Math.round(+v * 100) / 100);
  return r.unit && r.kind === 'num' ? t + ' ' + r.unit : t;
};
const goCur = (r) => r.set !== '' && r.set != null ? r.set : r.off_text && r.start ? r.start : (r.now || r.build || r.game || r.start || String(r.min + Math.round((r.max - r.min) / 2 / r.step) * r.step));
const goKnown = (r) => !!(r.now || r.build || r.game);
function goInfoHtml(r, st) {
  const parts = [];
  if (r.off_text) parts.push(`game's own <b>${esc(r.off_text)}</b>`);
  else if (r.game) parts.push(`game's own <b>${esc(goUnit(r, r.game))}</b>`);
  if (r.build) parts.push(`this build starts at <b>${esc(goUnit(r, r.build))}</b> <span class="dimmer">(${esc(r.build_from)})</span>`);
  if (r.now && st.read && !(r.off_text && !r.set)) parts.push(`in the game when last read <b>${esc(goUnit(r, r.now))}</b> <span class="dimmer">(${esc(st.read)})</span>`);
  let h = parts.length ? `<div class="small go-vals">${parts.join(' · ')}</div>` : `<div class="small dimmer go-vals">Its value is read the next time the game starts with Frame Lab's options mod in.</div>`;
  const others = (r.users || []).filter(u => !/\.ini$/i.test(u));
  if (others.length) h += `<div class="small dimmer">Also named by ${esc(others.join(', '))}: if ${others.length === 1 ? 'it sets' : 'they set'} it again while you play, Frame Lab's mod sets yours back a few times, then leaves it and says so here.</div>`;
  if (r.lost && r.set) h += `<div class="sched-rej">Another mod keeps setting this one${others.length ? ' (' + esc(others.join(', ')) + ')' : ''}: your value doesn't hold. Change it in that mod's own settings instead.</div>`;
  if (r.kept) h += `<div class="small dimmer">Kept after a trial in Optimise.</div>`;
  return h;
}
function goCtlHtml(r, dis) {
  const set = r.set !== '' && r.set != null;
  const reset = `<button class="btn2 small" data-goreset="${esc(r.id)}" ${dis || !set ? 'disabled' : ''}>Reset</button>`;
  if (r.kind === 'bool') {
    const cur = set ? r.set : (r.now || r.game || '');
    const b = (v, t) => `<button class="btn2 small ${set && cur === v ? 'on' : ''}" data-gobool="${esc(r.id)}" data-v="${v}" ${dis ? 'disabled' : ''}>${t}</button>`;
    return `<div class="go-sw">${b('true', 'On')}${b('false', 'Off')}${reset}</div>
      <div class="small ${set ? '' : 'dimmer'}" data-goval="${esc(r.id)}">${set ? 'Frame Lab sets it ' + (r.set === 'true' ? 'on' : 'off') : 'Not set: ' + (cur ? optText(cur) + ', the game\'s own' : 'the game\'s own')}</div>`;
  }
  const v = goCur(r), mark = r.game && !r.off_text ? `<datalist id="gol-${esc(r.id)}"><option value="${esc(r.game)}"></option></datalist>` : '';
  return `<div class="go-slide"><span class="dimmer small">${esc(r.low || '')}</span>
      <input type="range" min="${r.min}" max="${r.max}" step="${r.step}" value="${esc(v)}" data-gorange="${esc(r.id)}" ${mark ? `list="gol-${esc(r.id)}"` : ''} ${dis ? 'disabled' : ''}>
      <span class="dimmer small">${esc(r.high || '')}</span>${mark}</div>
    <div class="go-val"><span data-goval="${esc(r.id)}" class="${set ? '' : 'dimmer'}">${set ? '<b>' + esc(goUnit(r, r.set)) + '</b> set by Frame Lab' : esc(r.off_text ? 'not set: the game\'s own (' + r.off_text + ')' : goKnown(r) ? goUnit(r, v) + ', not set' : 'not set: the game\'s own, not read yet')}</span>${reset}</div>`;
}
function goRowHtml(r, st) {
  const dis = !st.can || !!st.lock || r.missing;
  const set = r.set !== '' && r.set != null;
  return `<div class="go-row ${set ? 'is-set' : ''}" data-goid="${esc(r.id)}">
    <div class="go-main"><div class="go-name">${esc(r.name)}${set ? ' <span class="tag y">set</span>' : ''}${r.missing ? ' <span class="tag">not in this game</span>' : ''}</div>
      <div class="dim small">${esc(r.what)}</div>
      <div class="small"><span class="dimmer">What you give up:</span> ${esc(r.cost)}</div>
      ${r.try ? `<div class="small"><span class="dimmer">How to see it:</span> ${esc(r.try)}</div>` : ''}
      <div data-goinfo="${esc(r.id)}">${goInfoHtml(r, st)}</div>
      <div class="dimmer small mono">${esc(r.cat)} / ${esc(r.item)}</div></div>
    <div class="go-ctl">${goCtlHtml(r, dis)}</div></div>`;
}
function goLiveHtml(st) {
  if (!st.can) return `<span class="lose">${esc(st.why || '')}</span>`;
  const n = st.changes ? `${st.changes} option${st.changes === 1 ? '' : 's'} set by Frame Lab in ${esc(st.build)}. ` : '';
  if (!st.mod) return n + `Frame Lab's options mod isn't in ${esc(st.build)} yet. It goes in with the first option you set (game closed), or put it in now so you can set them while you play.`;
  if (!st.running) return n + `Frame Lab's options mod is in ${esc(st.build)}. The game is closed: what you set here is in the game from its next start.`;
  if (st.live === 'in') return n + `<span class="win">The game is running and has every value set here.</span> Changes go in within a couple of seconds.`;
  if (st.live === 'waiting') return n + `Sending it to the game ...`;
  return n + `<span class="warnt">The game is running but hasn't taken the newest values:</span> the options mod wasn't in when it started. It sets them from the next start.`;
}
async function renderGameOpts() {
  const root = $('#tab-gameopts');
  if (!root) return;
  if (!S.state) { root.innerHTML = '<div class="empty">Loading ...</div>'; return; }
  const bi = S.state.config.active;
  let st;
  try { st = await api('gameopts', { build: bi }); } catch (e) { root.innerHTML = '<div class="card"><span class="lose">' + esc(e.message) + '</span></div>'; return; }
  if (S.tab !== 'gameopts') return;
  S.go = st; S.goBuild = bi;
  const groups = [];
  for (const r of st.rows) { let g = groups.find(x => x.name === r.group); if (!g) groups.push(g = { name: r.group, rows: [] }); g.rows.push(r); }
  root.innerHTML = `<div class="rep-head"><div class="title">Game options</div>
      <div class="cf-builds">${S.state.config.builds.map((b, i) => `<button class="btn2 small ${i === bi ? 'on' : ''}" data-b="${i}" style="${i === bi ? 'border-color:var(--accent);color:var(--white)' : ''}">${esc(b.name)}</button>`).join('')}</div></div>
    <div class="card go-top"><div class="dim" style="max-width:920px">The game's own options: the ones mods like Ultra+ set through Cyber Engine Tweaks. Move a slider or press On / Off and Frame Lab's small CET mod sets it in the game, within a couple of seconds while you play. Reset gives the game its own value back. Nothing is written to the game's files or to your in-game settings, and taking the mod out puts everything back. To measure what one costs in frames, put it on trial in Optimise.</div>
      <div class="go-live" id="go-live">${goLiveHtml(st)}</div>
      ${st.lock ? `<div class="sched-rej">${esc(st.lock)}</div>` : ''}
      <div style="margin-top:10px" id="go-modbtn">${goModBtn(st)}</div></div>
    ${groups.map(g => `<div class="card go-group"><h2>${esc(g.name)}</h2>${g.rows.map(r => goRowHtml(r, st)).join('')}</div>`).join('')}`;
  bindGameOpts(root);
  goTick();
}
function goModBtn(st) {
  return `${st.mod ? `<button class="btn2 small" id="go-out" ${st.running ? 'disabled title="Close the game first"' : ''}>Take the options mod out (everything set here goes back)</button>` : (st.can ? `<button class="btn2 small" id="go-in" ${st.running ? 'disabled title="Close the game first"' : ''}>Put the options mod in</button>` : '')}`;
}
function goApply(st) {
  // the status after a change: the values and controls of every row, without re-rendering the page
  S.go = st;
  const live = $('#go-live'); if (live) live.innerHTML = goLiveHtml(st);
  const mb = $('#go-modbtn'); if (mb && !mb.contains(document.activeElement)) { mb.innerHTML = goModBtn(st); bindGameOpts(mb); }
  for (const r of st.rows) {
    const row = $(`[data-goid="${CSS.escape(r.id)}"]`);
    if (!row) continue;
    const info = $('[data-goinfo]', row); if (info) info.innerHTML = goInfoHtml(r, st);
    if (S.goDrag === r.id) continue;
    const dis = !st.can || !!st.lock || r.missing, set = r.set !== '' && r.set != null;
    row.classList.toggle('is-set', set);
    const nm = $('.go-name', row); if (nm) nm.innerHTML = esc(r.name) + (set ? ' <span class="tag y">set</span>' : '') + (r.missing ? ' <span class="tag">not in this game</span>' : '');
    const ctl = $('.go-ctl', row);
    if (!ctl) continue;
    const focus = document.activeElement && ctl.contains(document.activeElement) ? document.activeElement.dataset.gorange : null;
    ctl.innerHTML = goCtlHtml(r, dis); bindGameOpts(ctl);
    if (focus) { const inp = $('[data-gorange]', ctl); if (inp) inp.focus(); } // arrow keys keep working on the slider
  }
}
function goTick() {
  clearTimeout(S.goTimer);
  if (!S.go || !S.go.running) return;
  S.goTimer = setTimeout(async () => {
    if (S.tab !== 'gameopts') return;
    try { const st = await api('gameopts', { build: S.goBuild }); if (S.tab === 'gameopts' && S.goBuild === S.state.config.active) goApply(st); } catch (e) { /* next time */ }
    goTick();
  }, 2000);
}
async function goSet(id, value) {
  try { const st = await api('gameopts/set', { build: S.goBuild, id, value }); goApply(st); goTick(); }
  catch (e) { toast(e.message, true); renderGameOpts(); }
}
function bindGameOpts(root) {
  $$('[data-b]', root).forEach(b => b.onclick = () => setBuild(+b.dataset.b));
  const gin = $('#go-in', root), gout = $('#go-out', root);
  if (gin) gin.onclick = async () => { gin.disabled = true; try { await api('gameopts/mod', { build: S.goBuild, on: true }); toast('Frame Lab\'s options mod is in: start the game and set the options while you play.'); } catch (e) { toast(e.message, true); } renderGameOpts(); };
  if (gout) gout.onclick = () => confirmBox('Take Frame Lab\'s options mod out of this build?<br><span class="dim">Every option set here, and any kept after a trial in Optimise, is the game\'s own again from the next start.</span>', async () => {
    try { await api('gameopts/mod', { build: S.goBuild, on: false }); toast('Taken out: every option is the game\'s own again.'); } catch (e) { toast(e.message, true); } renderGameOpts();
  });
  $$('[data-gorange]', root).forEach(inp => {
    const id = inp.dataset.gorange, r = () => (S.go.rows || []).find(x => x.id === id);
    inp.oninput = () => { S.goDrag = id; const o = r(); const v = $(`[data-goval="${CSS.escape(id)}"]`); if (o && v) { v.className = ''; v.innerHTML = '<b>' + esc(goUnit(o, inp.value)) + '</b>'; } };
    inp.onchange = () => { S.goDrag = null; goSet(id, inp.value); };
  });
  $$('[data-gobool]', root).forEach(b => b.onclick = () => goSet(b.dataset.gobool, b.dataset.v));
  $$('[data-goreset]', root).forEach(b => b.onclick = () => goSet(b.dataset.goreset, ''));
}

const OPT_STATE = { missing: ['', 'not in this game'], same: ['', 'already so'], trial: ['y', 'on trial'], kept: ['g', 'kept'], taken: ['', 'another value in use'], unread: ['', 'not read yet'] };
function optionsHtml(op) {
  const o = op.options, sc = op.schedule;
  if (!o || !sc) return '';
  const intro = `<div class="dim" style="max-width:920px">The game has options of its own, the ones mods set through Cyber Engine Tweaks. Frame Lab can put one of them on trial: it is set only while you record the test routes again, then every route is shown before and after and you keep it or drop it. Nothing is written to the game's files during a trial.
    Frame Lab doesn't know which of these helps on your build: the trial is how you find out. Each one costs something, and that is written next to it.</div>`;
  if (!o.can) return `<div class="cf-sec card opts"><h2>Game options, one at a time</h2>${intro}<div class="sched-tip">${esc(o.why || '')}</div></div>`;
  const kept = (o.kept || []).length ? `<div class="opt-kept"><div class="dim small">Kept in this build (set by Frame Lab's own small mod, <span class="mono">NightCityFrameLabOptions</span>, at every game start):</div>
    <table><tr><th>option</th><th class="n">kept at</th><th class="n">the game's own</th><th>since</th><th></th></tr>${o.kept.map(k => `<tr><td><b>${esc(k.name)}</b><div class="dimmer small mono">${esc(k.cat)} / ${esc(k.item)}</div></td>
      <td class="n">${esc(optText(k.value))}</td><td class="n">${k.was ? esc(optText(k.was)) : '–'}</td><td class="dim">${k.page ? '<a href="#" data-go="gameopts">Game options page</a>' : k.when && !k.when.startsWith('0001') ? esc(new Date(k.when).toLocaleDateString('en-AU', { day: 'numeric', month: 'short' })) : '–'}</td>
      <td><button class="btn2 small" data-opt-remove="${esc(k.id)}" ${sc.trial ? 'disabled' : ''}>Take it out</button></td></tr>`).join('')}</table></div>` : '';
  const notes = (o.notes || []).map(n => `<div class="sched-tip">${esc(n)}</div>`).join('');
  const rowHtml = (x) => {
    const t = OPT_STATE[x.state];
    const val = x.state === 'kept' ? `<span class="dimmer">${esc(x.now || '?')} →</span> ${esc(x.try)}` : x.now ? (x.try && x.state !== 'same' ? `<span class="dimmer">${esc(x.now)} →</span> ${esc(x.try)}` : esc(x.now)) : '–';
    // a mod can set the option again at any time; a config file only gives it its starting value
    const mods = (x.users || []).filter(u => !/\.ini$/i.test(u)), inis = (x.users || []).filter(u => /\.ini$/i.test(u));
    const users = (mods.length ? `<div class="sched-rej">${esc(mods.join(', '))} ${mods.length === 1 ? 'sets' : 'set'} this option too. If it is set again while you play, the trial can't hold, and Frame Lab will say so.</div>` : '') +
      (inis.length ? `<div class="dimmer small">Its starting value in this build comes from ${esc(inis.join(', '))} (in engine\\config).</div>` : '');
    const act = t ? `<span class="tag ${t[0]}">${t[1]}</span>` : `<button class="btn2 small" data-opt-try="${esc(x.id)}" ${o.open ? '' : 'disabled'}>Try it</button>`;
    return `<tr><td><b>${esc(x.name)}</b><div class="dim small">${esc(x.what)}</div><div class="small"><span class="dimmer">What you give up:</span> ${esc(x.cost)}</div>${users}${x.past ? `<div class="dimmer small">${esc(x.past)}</div>` : ''}</td>
      <td class="mono small dim">${esc(x.cat)} /<br>${esc(x.item)}</td><td class="n">${val}</td><td class="opt-act">${act}</td></tr>`;
  };
  const rows = o.rows || [], here = rows.filter(x => x.state !== 'missing'), gone = rows.filter(x => x.state === 'missing');
  const groups = [];
  for (const x of here) { let g = groups.find(g => g.name === x.group); if (!g) groups.push(g = { name: x.group, rows: [] }); g.rows.push(x); }
  const head = '<tr><th>what the trial does</th><th>the game\'s name for it</th><th class="n">now → on trial</th><th></th></tr>';
  const list = groups.map(g => `<tr class="opt-group"><td colspan="4">${esc(g.name || 'Other')}</td></tr>${g.rows.map(rowHtml).join('')}`).join('');
  const state = sc.step === 'decide' ? 'waiting for your call' : sc.trial ? 'one on trial' : o.open ? 'ready' : 'not now';
  const self = `<div class="small" style="margin-top:8px">To set one yourself with a slider, without a trial: <a href="#" data-go="gameopts">Game options</a>.</div>`;
  return `<div class="cf-sec card opts"><h2>Game options, one at a time<span class="dim">${state}${(o.kept || []).length ? ' · ' + o.kept.length + ' kept' : ''}</span></h2>${intro}${self}${notes}${kept}
    ${o.wait ? `<div class="sched-why" style="margin-top:10px">${esc(o.wait)}</div>` : ''}
    ${here.length ? `<table class="opt-list">${head}${list}</table>` : ''}
    ${gone.length ? `<details class="cf" style="margin-top:8px"><summary class="dim small"><span class="chev">›</span>${gone.length} more that this game doesn't have, or that are another kind of value here</summary><table class="opt-list">${gone.map(rowHtml).join('')}</table></details>` : ''}
    <div class="dimmer small" style="margin-top:8px">${o.read ? 'Values as read in ' + esc(o.read) + ', after every mod had set its own. ' : ''}A trial is a round of its own: one option, the same routes, the same settings. It needs Cyber Engine Tweaks and game 2.5 or later.</div></div>`;
}
const optText = (v) => v === 'true' ? 'on' : v === 'false' ? 'off' : (v !== '' && !isNaN(+v) ? String(+v) : v);

// the mods with a newer version on Nexus, under their "Do this next" line
function opUpdList(r) {
  const up = (r.mods || []).filter(m => m.update);
  if (!up.length) return '';
  return `<div id="op-updlist" class="op-updlist ${S.opUpdOpen ? '' : 'hidden'}"><table>${up.map(m => `<tr><td>${esc(m.name)}</td><td class="dim">${esc(m.version || '–')} → <b>${esc(m.latest)}</b></td>
    <td>${m.nexus ? `<a href="#" data-nx="${esc(String(m.nexus))}">Nexus page</a>` : ''}</td></tr>`).join('')}</table>
    <div class="dimmer small">Update each one from its Nexus page (read its latest posts first), then record again.</div></div>`;
}
// the merges on the Conflicts tab: their state, filled in place every 2 seconds while one runs
async function cfMergePoll(root) {
  clearTimeout(S.cfMergeT);
  let busy = false;
  for (const el of $$('[data-mstate]', root)) {
    let j;
    try { j = (await api('conflicts/merge/status', { build: S.cfBuild, winner: el.dataset.mw, loser: el.dataset.ml })).job; } catch (e) { continue; }
    if (!j) { el.innerHTML = ''; continue; }
    const btn = el.parentElement.querySelector('[data-cfmerge]');
    const run = ['unpacking', 'merging', 'packing'].includes(j.state);
    busy = busy || run;
    if (btn) btn.disabled = run || j.state === 'done';
    const said = { unpacking: 'Unpacking both archives ...', merging: 'Merging ...', packing: 'Packing and installing ...' };
    if (run) el.innerHTML = `<span class="dim">${said[j.state]}</span>`;
    else if (j.state === 'done') {
      const add = j.files.reduce((a, f) => a + f.added, 0), ch = j.files.reduce((a, f) => a + f.changed, 0);
      el.innerHTML = `<span class="win">Merged:</span> ${j.files.length} file${j.files.length === 1 ? '' : 's'}, ${add} entr${add === 1 ? 'y' : 'ies'} only ${esc(j.loser_label)} had ${add === 1 ? 'is' : 'are'} in now${ch ? `, ${ch} both change (${esc(j.winner_label)}'s kept)` : ''}. <span class="mono dimmer">${esc(j.archive)}</span>. It counts from the next game start.`;
    } else el.innerHTML = `<span class="lose">${esc(j.state.replace(/^error: /, 'Not merged: '))}</span>`;
  }
  if (busy) S.cfMergeT = setTimeout(() => { if (S.tab === 'conflicts') cfMergePoll(root); }, 2000);
}
const OP_SEV = { high: ['fix', 'needs fixing'], medium: ['try', 'worth checking'], info: ['info', 'good to know'] };
const nxId = (u) => { const m = /\/mods\/(\d+)/.exec(u || ''); return m ? m[1] : ''; };
const opBytes = (b) => b >= 1073741824 ? (b / 1073741824).toFixed(1) + ' GB' : b >= 1048576 ? (b / 1048576).toFixed(0) + ' MB' : b >= 1024 ? (b / 1024).toFixed(0) + ' KB' : (b || 0) + ' B';

// one fix in words (Fixes made on this PC)
function fixLine(x) {
  const at = `<span class="mono">${esc(x.file)}</span> line ${x.line}`;
  switch (x.kind) {
    case 'recipe': return esc(x.what || 'ready-made fix') + (x.files && x.files.length > 1 ? ` <span class="dimmer">(${x.files.length} files)</span>` : '');
    case 'lua_throttle': return `${at}: onUpdate ${x.hz}× a second`;
    case 'lua_throttle_call': return `${at}: that call ${x.hz}× a second`;
    case 'lua_hubguard': return `<span class="mono">${esc(x.file)}</span>: dialog helper only while its hub is on screen`;
    case 'reds_arraycontains': return `${at}: ArrayContains instead of a loop`;
    case 'archive_order': return esc(x.what || 'archive load order');
    case 'tweak_pick': return esc(x.what || 'TweakXL value');
    case 'file_off': return `<span class="mono">${esc(x.file)}</span>: ${esc(x.what || 'switched off')}`;
    case 'xl_cooked_type': return `<span class="mono">${esc(x.file)}</span>: ${esc(x.what || 'node type for 2.5')}`;
    case 'workbench': return esc(x.what || 'edited in the Workbench');
    case 'texture_swap': return esc(x.what || 'lighter textures');
    case 'merge': return esc(x.what || 'two mods merged');
  }
  return `${at}: ${esc(x.kind)}`;
}

const RULE_NAME = { lua_throttle_call: 'slow down one call', lua_hubguard: 'dialog helper', reds_arraycontains: 'ArrayContains' };

// 2.5E Optimise stage 2: the Script fixes card
function scriptFixesHtml(op, M, locked) {
  const sf = op.script_fns, fx = (op.fixes || []).filter(x => !x.undone && !x.gone);
  const recipes = (op.recipes || []).filter(r => r.state !== 'done' || fx.some(x => x.recipe === r.id));
  const rules = M.flatMap(m => (m.fixes || []).map(f => Object.assign({ mod: m.name, nexus: m.nexus }, f)));
  const ruleReady = rules.filter(f => f.state === 'ready'), ruleDone = rules.filter(f => f.state === 'done');
  let h = `<div class="cf-sec card"><h2>Script fixes<span class="dim">what each mod script costs, and the changes Frame Lab can make to it</span></h2>`;
  // measured
  if (sf && (sf.fns || []).length) {
    const routes = [...new Set(sf.fns.flatMap(f => Object.keys(f.routes || {})))];
    h += `<h3>Measured <span class="dimmer small">${esc(sf.title || sf.session)} · ${esc(sf.created)} · ${fmt.dur(sf.seconds)} of script timing${sf.test ? ' (test recording)' : ''}</span></h3>
      <table><tr><th>mod</th><th>function</th><th>where</th><th class="n">ms per s</th><th class="n">calls per s</th>${routes.map(r => `<th class="n">${esc(r)}</th>`).join('')}</tr>
      ${sf.fns.slice(0, 15).map(f => `<tr><td>${esc(f.mod)}</td><td class="mono">${esc(f.fn)}${f.wraps ? ' <span class="tag b" title="wraps a function of the game, so it runs whenever the game calls it">wraps</span>' : ''}</td><td class="mono dim">${f.file ? `<a class="wb-link" data-wbedit="${esc(f.file)}" data-wbeline="${f.line}" title="open it in the Workbench">${esc(f.file)}:${f.line}</a>` : '<span class="dimmer">not found in the build</span>'}</td><td class="n">${f.main_ms_s.toFixed(2)}</td><td class="n">${fmt.n(Math.round(f.calls_s))}</td>${routes.map(r => `<td class="n">${f.routes && f.routes[r] != null ? f.routes[r].toFixed(2) : '–'}</td>`).join('')}</tr>`).join('')}</table>
      <div class="dimmer small" style="margin-top:6px">Own time on the game's main thread (what can hold a frame back), per second of play. Over 1 ms per second is worth a look; a function that wraps one of the game's runs every time the game calls it.</div>`;
  } else {
    h += `<div class="dim small">No time per script function yet. A test recording (Start the test round above) measures every mod REDscript function with HitchRecorder 1.3; the list shows up here after that.</div>`;
  }
  // known from the downloaded mods: what runs very often in the build's mods (before or next to the measurements)
  const LK = op.likely || [];
  if (LK.length) {
    h += `<details class="cf" style="margin-top:14px" ${sf && (sf.fns || []).length ? '' : 'open'}><summary><span class="chev">›</span><b>Likely costs</b> <span class="dimmer small">known from the downloaded copies of ${LK.length} of your mods: code that runs very often. A test recording shows what it really costs.</span></summary>
      <table><tr><th>mod</th><th>runs very often</th></tr>${LK.slice(0, 20).map(l => `<tr><td>${esc(l.mod)} <span class="op-nx" data-nx="${l.nexus}">Nexus</span></td><td class="dim small">${(l.lua || []).map(f => `CET onUpdate every frame, calls the game: <span class="mono">${esc(f.file.split('/').slice(-2).join('/'))}</span> (${f.lines} lines)`).concat((l.hot || []).length ? [`wraps ${l.hot.length} game function${l.hot.length === 1 ? '' : 's'} that run often: <span class="mono">${esc(l.hot.slice(0, 3).join(', '))}${l.hot.length > 3 ? ', …' : ''}</span>`] : []).join('<br>')}</td></tr>`).join('')}</table></details>`;
  }
  // ready-made
  if (recipes.length) {
    const btn = (r) => {
      const x = fx.find(f => f.recipe === r.id);
      if (x) return `<span class="tag g">in place</span> <button class="btn2 small" data-undo="${esc(x.id)}">Undo</button>`;
      if (r.state !== 'ready') return `<span class="dimmer small">${esc(r.note || r.state)}</span>`;
      if (locked) return '<span class="dimmer small">locked until the retest is done</span>';
      return `<button class="btn2 small" data-recipe="${esc(r.id)}">Apply</button>`;
    };
    const how = (r) => r.rule ? '<div class="dimmer small">made by a general rule, read by hand and checked to load; not played one by one: measure it</div>' : '<div class="dimmer small">made and played by hand</div>';
    h += `<h3 style="margin-top:16px">Ready-made fixes <span class="dimmer small">each one for one exact version of the mod's files; anything else is left alone</span></h3>
      <table><tr><th>mod</th><th>what it changes</th><th></th></tr>${recipes.map(r => `<tr><td>${esc(r.mod)}${r.nexus ? ' <span class="op-nx" data-nx="' + r.nexus + '">Nexus</span>' : ''}${r.kind === 'fix25' ? ' <span class="tag y">2.5 fix</span>' : ''}</td><td class="dim">${esc(r.what)}${how(r)}</td><td style="white-space:nowrap">${btn(r)}</td></tr>`).join('')}</table>`;
  }
  // by rule
  if (ruleReady.length) {
    const measured = (f) => sf && (sf.fns || []).find(x => x.file && x.file.toLowerCase() === f.file.toLowerCase());
    const sorted = ruleReady.slice().sort((a, b) => (measured(b) ? 1 : 0) - (measured(a) ? 1 : 0) || a.mod.localeCompare(b.mod));
    const act = (f) => {
      if (locked) return '<span class="dimmer small">locked until the retest is done</span>';
      const hz = f.rule === 'lua_throttle_call' ? `<select data-rhz="${esc(f.file)}:${f.line}" title="how often it still runs">${[5, 10, 20].map(n => `<option value="${n}" ${n === 10 ? 'selected' : ''}>${n}×/s</option>`).join('')}</select> ` : '';
      return hz + `<button class="btn2 small" data-rule="${esc(f.rule)}" data-mod="${esc(f.mod)}" data-file="${esc(f.file)}" data-line="${f.line}" data-title="${esc(f.title)}">Apply</button>`;
    };
    h += `<h3 style="margin-top:16px">Fixes by rule <span class="dimmer small">small changes Frame Lab can make to any copy of a mod; each one was checked on 7,388 downloaded mods</span></h3>
      <table><tr><th>mod</th><th>change</th><th>where</th><th></th></tr>${sorted.slice(0, 40).map(f => `<tr title="${esc(f.what)}"><td>${esc(f.mod)}</td><td>${esc(f.title)} <span class="dimmer small">${esc(RULE_NAME[f.rule] || f.rule)}</span><div class="mono dimmer small" style="margin-top:2px">${esc(f.code.length > 110 ? f.code.slice(0, 110) + '…' : f.code)}</div></td><td class="mono dim"><a class="wb-link" data-wbedit="${esc(f.file)}" data-wbeline="${f.line}" title="open it in the Workbench">${esc(f.file)}:${f.line}</a>${measured(f) ? ' <span class="tag y">measured</span>' : ''}</td><td style="white-space:nowrap">${act(f)}</td></tr>`).join('')}</table>
      ${sorted.length > 40 ? `<div class="dimmer small">${sorted.length - 40} more not shown.</div>` : ''}`;
  }
  if (!recipes.length && !ruleReady.length) h += `<div class="dim small" style="margin-top:10px">No ready-made fix and no rule fits the scripts of this build${ruleDone.length ? ` (${ruleDone.length} already changed)` : ''}.</div>`;
  h += `<div class="dimmer small" style="margin-top:10px">Every fix keeps the original file and has an exact Undo; it only changes a file that is exactly as expected. After a fix the same test routes are recorded again and Frame Lab shows before and after. A REDscript change is checked at the next game start: if the game's script compile names the file, this page says so first, with Undo. Changed mod files are for your PC only: never upload or share them.</div></div>`;
  return h;
}

async function renderOptimise() {
  const root = $('#tab-optimise');
  if (!S.state) { root.innerHTML = '<div class="empty">Loading ...</div>'; return; }
  await loadOptimise();
  if (S.tab !== 'optimise') return;
  const st = S.state, op = S.op || {}, r = op.report;
  const k = kpiBox;
  const checking = op.state === 'checking';
  const sc = EX() ? op.schedule : null, shut = EX() && (!sc || ['start', 'routes', 'conflicts'].includes(sc.step)); // 2.5E: the analysis waits for the test round; 2.5 has no schedule
  let html = `<div class="rep-head"><div class="title">${EX() ? 'Optimise' : 'Check your mods'}</div>
    <div class="cf-builds">${st.config.builds.map((b, i) => `<button class="btn2 small ${i === S.opBuild ? 'on' : ''}" data-b="${i}" style="${i === S.opBuild ? 'border-color:var(--accent);color:var(--white)' : ''}">${esc(b.name)}</button>`).join('')}</div>
    <div class="actions">${r ? '<button class="btn2" id="op-copy">Copy summary</button>' : ''}<button class="btn" id="op-run" ${checking || shut ? 'disabled' : ''} ${shut ? 'title="Locked until the test routes are recorded and the conflict check is done"' : ''}>${checking ? (EX() ? 'Analysing ...' : 'Checking ...') : (EX() ? 'Analyse build' : 'Check this build')}</button></div></div>`;
  if (op.error) html += `<div class="card"><span class="lose">${esc(op.error)}</span></div>`;
  if (op.state && op.state.startsWith('error')) html += `<div class="card"><span class="lose">${esc(op.state)}</span></div>`;
  if (EX()) html += schedHtml(op);
  if (sc && ['start', 'routes', 'conflicts', 'analyse'].includes(sc.step)) { // locked: nothing to act on yet
    root.innerHTML = html + opFoot(); bindOptimise(root); return;
  }
  const locked = !!(sc && sc.locked);
  for (const x of (op.fixes || []).filter(f => f.broke)) html += `<div class="card warnbox"><b>${esc(x.mod)}: this fix broke the scripts.</b> <span class="dim">${esc(x.broke)}</span> <button class="btn" data-undo="${esc(x.id)}">Undo it</button></div>`;
  if (EX()) html += optionsHtml(op);
  const liveLine = op.live_updates ? 'Nexus update check is on (Settings): only the Nexus ids of the mods Frame Lab recognises are sent.' : 'Nexus update check is off. Switch it on in Settings to see which mods have a newer version.';
  if (!r) {
    html += `<div class="card"><div class="dim">Not checked yet. ${EX() ? 'Analyse build' : 'Check this build'} reads the build's mod files without starting the game (a few seconds) and lists:</div>
      <ul class="dim" style="margin:8px 0 0 18px;padding:0">
      <li>every mod in the build and which Nexus page it comes from, with its version (by its files, Mod Organizer 2 or Vortex, or your mod library)</li>
      <li>frameworks (RED4ext, CET, redscript, ArchiveXL, TweakXL, Codeware ...): installed version, and the ones your mods need but aren't there</li>
      <li>requirements other mods list that aren't installed, and REDscript imports that no installed mod provides</li>
      <li>mods installed twice, files in the wrong folder, zips that were never unpacked</li>
      <li>plugins that failed to load on the last start (RED4ext log) and scripts that didn't compile</li>
      <li>CET mods that do their work every frame</li></ul>
      <div class="dimmer small" style="margin-top:10px">${esc(liveLine)}${op.index === false ? ' · The mod index (components\\Optimise) is missing, so mods are named by folder only.' : ''}</div></div>`;
    html += opFoot();
    root.innerHTML = html; bindOptimise(root); return;
  }
  const F = r.findings || [], M = r.mods || [], FW = r.frameworks || [];
  if (!r.files && !M.length) { // an unmodded copy: nothing to list
    html += `<div class="dim small" style="margin:10px 0 10px">Analysed ${esc(new Date(r.generated).toLocaleString('en-AU'))} · game ${esc(r.game_version || '?')}</div>
      <div class="card"><div class="dim">${(r.notes || []).map(esc).join('<br>') || 'No mod files in this build.'}</div></div>` + opFoot();
    root.innerHTML = html; bindOptimise(root); return;
  }
  const lua = F.filter(f => f.kind === 'lua_every_frame'), rest = F.filter(f => f.kind !== 'lua_every_frame');
  const n = (s) => rest.filter(f => f.severity === s).length;
  const fwIn = FW.filter(f => f.installed), fwMiss = FW.filter(f => !f.installed && f.needed);
  const updates = M.filter(m => m.update).length;
  const when = new Date(r.generated);
  const live = r.live ? (r.live.error ? 'Nexus update check failed: ' + r.live.error : `Nexus checked for ${r.live.asked} mods`) : (op.live_updates ? 'Nexus update check on: runs with the next check' : 'Nexus update check off (Settings)');
  html += `<div class="dim small" style="margin:10px 0 10px">Analysed ${esc(when.toLocaleString('en-AU'))} in ${r.took_s.toFixed(1)} s · game ${esc(r.game_version || '?')} · ${fmt.n(r.files)} mod files · ${esc(live)}</div>`;
  html += '<div class="kpi">' +
    k('needs fixing', n('high'), n('high') ? 'mods that break or don\'t load' : 'nothing broken found', n('high') ? 'bad' : 'good') +
    k('worth checking', n('medium'), 'may cause problems', n('medium') ? 'warn' : 'good') +
    k('mods', M.length, r.recognised + ' matched to a Nexus page', '') +
    k('frameworks', fwIn.length, fwMiss.length ? fwMiss.length + ' needed but missing' : 'everything your mods need is there', fwMiss.length ? 'bad' : '') +
    k('CET every frame', lua.length, lua.length ? 'can be slowed down (below)' : 'none found', lua.length ? 'warn' : 'good') +
    (r.live && !r.live.error ? k('updates on Nexus', updates, updates ? 'newer version available' : 'all up to date', updates ? 'warn' : 'good') : '') + '</div>';

  // findings
  const nx = (l) => nxId(l.url) ? `<span class="op-nx" data-nx="${nxId(l.url)}">${esc(l.text || 'Nexus page')}</span>` : esc(l.text || '');
  const linkList = (links) => {
    if (!links || !links.length) return '';
    if (links.length <= 4 && !links.some(l => l.note)) return '<div class="op-links">' + links.map(nx).join('') + '</div>';
    const rows = '<div class="op-list">' + links.map(l => `<div>${nx(l)}${l.note ? ' <span class="dimmer">' + esc(l.note) + '</span>' : ''}</div>`).join('') + '</div>';
    return links.length > 6 ? `<details class="cf"><summary class="dim small"><span class="chev">›</span>show the ${links.length} mods</summary>${rows}</details>` : rows;
  };
  const fRow = (f) => {
    const [cls, label] = OP_SEV[f.severity] || OP_SEV.info;
    return `<div class="adv-row"><div class="adv-tag ${cls}">${label}</div><div class="adv-body"><div class="adv-title">${esc(f.title)}</div>${f.detail ? '<div class="adv-detail">' + esc(f.detail) + '</div>' : ''}${linkList(f.links)}</div></div>`;
  };
  const A = r.actions || [], topA = A.slice(0, 6);
  const found = rest.length ? rest.map(fRow).join('') : '<div class="dim">Nothing to fix: every framework your mods need is there, nothing is installed twice or in the wrong place, and the last start loaded every plugin.</div>';
  if (r.verdict) {
    html += `<div class="cf-sec card next"><h2>Do this next<span class="dim">most important first</span></h2><div class="next-verdict">${esc(r.verdict)}</div>
      ${topA.length ? '<ol class="next-list">' + topA.map((a, i) => `<li class="${a.severity}"><div class="next-do"><b>${esc(a.text)}</b>${a.button && (EX() || a.act !== 'slow') ? `<button class="btn2 small" data-opact="${i}" ${locked && a.act === 'slow' ? 'disabled title="Locked until the retest is done"' : ''}>${esc(a.act === 'updates' && S.opUpdOpen ? 'Hide them' : a.button)}</button>` : ''}</div>${a.why ? `<div class="next-why">${esc(a.why)}</div>` : ''}${a.act === 'slow' && !EX() ? `<div class="enote">${E_NOTE}</div>` : ''}${a.act === 'updates' ? opUpdList(r) : ''}</li>`).join('') + '</ol>' : ''}
      ${A.length > topA.length ? `<div class="dimmer small">${A.length - topA.length} more under What Frame Lab found.</div>` : ''}</div>
      <details class="cf cf-sec card adv"><summary><h2><span class="chev">›</span> What Frame Lab found<span class="dim">${rest.length} item${rest.length === 1 ? '' : 's'}: everything it read, most important first</span></h2></summary>${found}</details>`;
  } else {
    html += `<div class="cf-sec card adv"><h2>What Frame Lab found<span class="dim">most important first</span></h2>` + found + '</div>';
  }

  // CET every frame
  if (lua.length) {
    const hs = M.flatMap(m => (m.lua || []).map(h => Object.assign({ mod: m.name, nexus: m.nexus }, h)));
    const cnt = (kd) => hs.filter(h => h.kind === kd).length;
    const fx = (op.fixes || []).filter(x => !x.undone && !x.gone);
    const fixOf = (h) => fx.find(x => x.file === h.file && x.line === h.line);
    const act = (h) => {
      if (h.kind === 'candidate' && !EX()) return '<span class="dimmer small">2.5E can try it</span>';
      if (h.kind === 'candidate' && locked) return '<span class="dimmer small">locked until the retest is done</span>';
      if (h.kind === 'candidate') return `<select data-hz="${esc(h.file)}:${h.line}" title="how often it still runs">${[5, 10, 20].map(n => `<option value="${n}" ${n === 10 ? 'selected' : ''}>${n}× a second</option>`).join('')}</select> <button class="btn2 small" data-slow="${esc(h.file)}" data-line="${h.line}" data-mod="${esc(h.mod)}">Slow down</button>`;
      const x = fixOf(h);
      return x ? `<span class="tag g">${x.hz}× a second</span> <button class="btn2 small" data-undo="${esc(x.id)}">Undo</button>` : '<span class="tag g">slowed down</span>';
    };
    html += `<div class="cf-sec card"><h2>CET mods that work every frame<span class="dim">their onUpdate code runs on every frame although a few times a second would do</span></h2>
      <table><tr><th>mod</th><th>file</th><th class="n">line</th><th>what it does</th><th></th></tr>${hs.filter(h => h.kind === 'candidate' || h.kind === 'done').map(h => `<tr><td>${esc(h.mod)}${h.nexus ? ' <span class="op-nx" data-nx="' + h.nexus + '">Nexus</span>' : ''}</td><td class="mono dim">${esc(h.file)}</td><td class="n">${h.line}</td><td class="dim">${esc(h.why)}</td><td style="white-space:nowrap">${act(h)}</td></tr>`).join('')}</table>
      ${EX() ? `<div class="dimmer small" style="margin-top:8px">Slow down adds one line inside that onUpdate (every other line stays where it is): it then runs that many times a second and gets the time since it last ran, so its timers keep counting right. The original file is kept and Undo puts it back exactly. Close the game first; it counts from the next start. Check the mod still does its thing in game afterwards.</div>` :
        `<div class="dimmer small" style="margin-top:8px">What you can do: look in the mod's own settings for an update rate or a lighter mode, check its Nexus page for a newer version, or tell its author which file and line run every frame (copy it from this table).</div><div class="enote">${E_NOTE}</div>`}
      <div class="dimmer small" style="margin-top:8px">${hs.length} onUpdate handler${hs.length === 1 ? '' : 's'} read: ${cnt('own timer')} already time themselves, ${cnt('every frame')} move the camera, vehicles, sound or the UI and must run every frame, ${cnt('cheap')} return straight away${cnt('done') ? ', ' + cnt('done') + ' already slowed down' : ''}.</div></div>`;
  }

  // script fixes (2.5E Optimise stage 2): what each script function cost, ready-made fixes, fixes by rule
  if (EX()) html += scriptFixesHtml(op, M, locked);

  // fixes made on this PC (2.5)
  if (EX() && (op.fixes || []).length) {
    const stat = (x) => x.undone ? `<span class="dim">undone ${esc(x.undone)}</span>` : x.gone ? `<span class="dim">${esc(x.gone)}</span>` : '<span class="win">in place</span>';
    html += `<div class="cf-sec card"><h2>Fixes made on this PC<span class="dim">each with its original kept; Undo puts the file back exactly</span></h2>
      <table><tr><th>when</th><th>mod</th><th>change</th><th>state</th><th></th></tr>${op.fixes.map(x => `<tr><td class="dim">${esc(x.applied)}</td><td>${esc(x.mod)}</td><td class="dim">${fixLine(x)}</td><td>${stat(x)}</td><td>${!x.undone && !x.gone ? `<button class="btn2 small" data-undo="${esc(x.id)}">Undo</button>` : ''}</td></tr>`).join('')}</table></div>`;
  }

  // frameworks
  html += `<div class="cf-sec card"><h2>Frameworks<span class="dim">the tools other mods are built on</span></h2><table><tr><th>framework</th><th>installed</th><th class="n">files that need it</th><th></th></tr>` +
    FW.filter(f => f.installed || f.needed).map(f => `<tr><td>${esc(f.name)}</td><td>${f.installed ? '<span class="win">' + esc(f.version || 'installed') + '</span>' : '<span class="lose">missing</span>'}</td><td class="n">${f.needed || '–'}</td><td><span class="op-nx" data-nx="${f.nexus}">Nexus page</span></td></tr>`).join('') + '</table></div>';

  // mods
  const q = (S.opFilter || '').toLowerCase();
  const show = M.filter(m => (!q || (m.name + ' ' + (m.kinds || []).join(' ') + ' ' + (m.where || []).join(' ')).toLowerCase().includes(q)) && (!S.opUnknown || !m.nexus) && (!S.opUpd || m.update));
  const howTag = (m) => m.how === 'folder name' ? '<span class="tag">by folder name</span>' : `<span class="dimmer">${esc(m.how)}</span>${m.exact ? ' <span class="tag g">exact files</span>' : ''}`;
  const ver = (m) => (m.version ? (m.approx ? '<span class="dimmer" title="the closest known upload: its files are not an exact match">about </span>' : '') + esc(m.version) : '<span class="dimmer">–</span>') + (m.update ? ` <span class="tag y">${esc(m.latest)} on Nexus</span>` : '');
  const lim = S.opAll ? show.length : 60;
  html += `<div class="cf-sec card"><h2>Mods in this build<span class="dim">${M.length} mods · ${r.recognised} matched to a Nexus page</span></h2>
    <div style="display:flex;gap:14px;align-items:center;margin-bottom:8px;flex-wrap:wrap"><input id="op-q" placeholder="filter by name, type or folder" value="${esc(S.opFilter || '')}" style="width:280px">
      <label class="dim small"><input type="checkbox" id="op-unk" ${S.opUnknown ? 'checked' : ''}> only mods without a Nexus page</label>
      ${r.live && !r.live.error ? `<label class="dim small"><input type="checkbox" id="op-upd" ${S.opUpd ? 'checked' : ''}> only mods with an update</label>` : ''}</div>
    <table><tr><th>mod</th><th>version</th><th>type</th><th class="n">files</th><th class="n">size</th><th>recognised by</th></tr>${show.slice(0, lim).map(m => `<tr><td>${m.framework ? '<b>' + esc(m.name) + '</b>' : esc(m.name)}${m.nexus ? ' <span class="op-nx" data-nx="' + m.nexus + '">Nexus</span>' : ''}<div class="dimmer small" style="word-break:break-all">${esc((m.where || [])[0] || '')}${(m.where || []).length > 1 ? ' + ' + (m.where.length - 1) + ' more' : ''}</div></td>
      <td>${ver(m)}</td><td class="dim">${esc((m.kinds || []).join(', '))}</td><td class="n">${fmt.n(m.files)}</td><td class="n">${opBytes(m.size)}</td><td>${howTag(m)}</td></tr>`).join('')}</table>
    ${show.length > 60 ? `<div class="more"><button class="btn2 small" id="op-all">${S.opAll ? 'Show the first 60' : 'Show all ' + show.length + ' mods'}</button></div>` : ''}
    <div class="dimmer small" style="margin-top:8px">Matched files: the files in the build are the exact files of an upload on Nexus (path and size; "exact files" when all of them are there). Mods Frame Lab can't place are listed by their folder or archive name.</div></div>`;
  if (r.notes && r.notes.length) html += `<div class="cf-sec card dim">${r.notes.map(esc).join('<br>')}</div>`;
  html += opFoot();
  root.innerHTML = html;
  bindOptimise(root);
}

function opFoot() {
  if (!EX()) return `<div class="foot">The check only reads your mod files: Frame Lab 2.5 never changes them. Updates come from each mod's own Nexus page.</div>`;
  return `<div class="foot">The analysis only reads. A fix changes a mod file only when you press its button, on this PC only, with the original kept and an exact Undo.
    Mod files it changes are for your PC: never upload or share them, the original mod authors' permissions still apply.</div>`;
}

function opSummary(op) {
  const r = op.report, F = r.findings || [];
  const lines = [`Night City Frame Lab ${S.state.version} · Optimise · ${r.build} · game ${r.game_version || '?'} · ${(r.mods || []).length} mods (${r.recognised} on Nexus) · ${r.files} mod files`];
  const fw = (r.frameworks || []).filter(f => f.installed || f.needed).map(f => f.name + ' ' + (f.installed ? (f.version || 'installed') : 'MISSING'));
  if (fw.length) lines.push('Frameworks: ' + fw.join(', '));
  ['high', 'medium', 'info'].forEach(s => F.filter(f => f.severity === s).forEach(f => lines.push(`[${OP_SEV[s][1]}] ${f.title}`)));
  return lines.join('\n');
}

// Optimise is a long page: a row of links to its sections, made from their titles, under the header
function opJump(root) {
  const old = $('.op-jump', root); if (old) old.remove();
  const secs = $$('.cf-sec', root).filter(sec => sec.querySelector(':scope > h2, :scope > summary > h2'));
  if (secs.length < 3) return;
  const label = (sec) => { const h = sec.querySelector(':scope > h2, :scope > summary > h2'); return [...h.childNodes].filter(n => n.nodeType === 3).map(n => n.textContent).join('').trim(); };
  const row = document.createElement('div');
  row.className = 'op-jump';
  row.innerHTML = '<span class="dimmer small">Go to:</span> ' + secs.map((sec, i) => { sec.id = sec.id || 'op-sec-' + i; return `<a href="#" data-jump="${sec.id}">${esc(label(sec))}</a>`; }).join('');
  const head = $('.rep-head', root);
  if (head) head.after(row); else root.prepend(row);
  $$('[data-jump]', row).forEach(a => a.onclick = (e) => {
    e.preventDefault();
    const t = document.getElementById(a.dataset.jump);
    if (!t) return;
    if (t.tagName === 'DETAILS') t.open = true;
    t.scrollIntoView({ block: 'start', behavior: 'smooth' });
  });
}
function bindOptimise(root) {
  opJump(root);
  $$('[data-b]', root).forEach(b => b.onclick = () => { S.opAll = false; setBuild(+b.dataset.b); });
  const run = $('#op-run', root);
  if (run) run.onclick = async () => { run.disabled = true; try { await api('optimise/check', { build: S.opBuild }); setTimeout(renderOptimise, 300); } catch (e) { toast(e.message, true); run.disabled = false; } };
  // the test schedule
  const scStart = $('#sc-start', root), scRe = $('#sc-restart', root), scConf = $('#sc-conf', root), scAna = $('#sc-ana', root), scUndo = $('#sc-undo-round', root);
  const startRound = async () => { try { await api('optimise/schedule/start', { build: S.opBuild }); S.schedAt = 0; toast('Test round started: the next sessions of this build are test recordings.'); renderOptimise(); } catch (e) { toast(e.message, true); } };
  if (scStart) scStart.onclick = startRound;
  if (scRe) scRe.onclick = () => confirmBox('Start the test round over?<br><span class="dim">The rounds so far are forgotten and every route is recorded again as the new baseline. Fixes already made stay in place (undo them first if you want the baseline without them).</span>', startRound);
  if (scConf) scConf.onclick = async () => { scConf.disabled = true; try { await api('conflicts/check', { build: S.opBuild }); setTimeout(renderOptimise, 300); } catch (e) { toast(e.message, true); scConf.disabled = false; } };
  if (scAna) scAna.onclick = async () => { scAna.disabled = true; try { await api('optimise/check', { build: S.opBuild }); setTimeout(renderOptimise, 300); } catch (e) { toast(e.message, true); scAna.disabled = false; } };
  if (scUndo) scUndo.onclick = () => confirmBox('Undo every fix of this round?<br><span class="dim">The original files are put back. Close the game first. The routes are then recorded once more.</span>', async () => {
    let ok = 0; const errs = [];
    for (const id of scUndo.dataset.ids.split(',')) { try { await api('optimise/undo', { build: S.opBuild, id }); ok++; } catch (e) { errs.push(e.message); } }
    toast(errs.length ? `${ok} undone, ${errs.length} not: ${errs[0]}` : `${ok} undone: the original files are back.`, errs.length > 0);
    setTimeout(renderOptimise, 600);
  });
  // game options on trial
  $$('[data-opt-try]', root).forEach(b => b.onclick = async () => {
    b.disabled = true;
    try { await api('optimise/option/try', { build: S.opBuild, id: b.dataset.optTry }); S.schedAt = 0; toast('On trial. The next sessions of this build set it: record the same routes again.'); renderOptimise(); }
    catch (e) { toast(e.message, true); b.disabled = false; }
  });
  const optKeep = $('#opt-keep', root), optDrop = $('#opt-drop', root);
  if (optKeep) optKeep.onclick = async () => { optKeep.disabled = true; try { await api('optimise/option/keep', { build: S.opBuild }); S.schedAt = 0; toast('Kept: it is set at every game start from now on.'); renderOptimise(); } catch (e) { toast(e.message, true); optKeep.disabled = false; } };
  if (optDrop) optDrop.onclick = async () => { optDrop.disabled = true; try { await api('optimise/option/drop', { build: S.opBuild }); S.schedAt = 0; toast('Dropped: nothing of it is left in the build.'); renderOptimise(); } catch (e) { toast(e.message, true); optDrop.disabled = false; } };
  $$('[data-opt-remove]', root).forEach(b => b.onclick = () => confirmBox('Take this option out again?<br><span class="dim">The game uses its own value again from the next start. Close the game first. It is a change like any other: the same routes are recorded once more.</span>', async () => {
    try { await api('optimise/option/remove', { build: S.opBuild, id: b.dataset.optRemove }); S.schedAt = 0; toast('Taken out: the option is the game\'s own again.'); renderOptimise(); } catch (e) { toast(e.message, true); }
  }));
  const cp = $('#op-copy', root);
  if (cp) cp.onclick = async () => { if (S.op && S.op.report && await copyText(opSummary(S.op))) toast('Summary copied'); else toast('Could not copy', true); };
  const q = $('#op-q', root);
  if (q) q.oninput = () => { S.opFilter = q.value; clearTimeout(S.opQT); S.opQT = setTimeout(() => { renderOptimise().then(() => { const i = $('#op-q'); if (i) { i.focus(); i.setSelectionRange(i.value.length, i.value.length); } }); }, 250); };
  [['#op-unk', 'opUnknown'], ['#op-upd', 'opUpd']].forEach(([id, key]) => { const c = $(id, root); if (c) c.onchange = () => { S[key] = c.checked; renderOptimise(); }; });
  $$('[data-slow]', root).forEach(b => b.onclick = async () => {
    const file = b.dataset.slow, line = +b.dataset.line, mod = b.dataset.mod;
    const sel = $(`[data-hz="${CSS.escape(file + ':' + line)}"]`, root), hz = sel ? +sel.value : 10;
    if (!confirm(`${mod}: make its onUpdate (${file}, line ${line}) run ${hz} times a second instead of every frame?\n\nOne line is added inside it; the original file is kept and Undo puts it back. Close the game first.`)) return;
    b.disabled = true;
    try { await api('optimise/fix', { build: S.opBuild, kind: 'lua_throttle', mod, file, line, hz }); toast(mod + ': slowed down to ' + hz + '× a second. It counts from the next game start.'); setTimeout(renderOptimise, 600); }
    catch (e) { toast(e.message, true); b.disabled = false; }
  });
  $$('[data-wbedit]', root).forEach(a => a.onclick = () => openInWorkbench(S.opBuild, a.dataset.wbedit, +a.dataset.wbeline));
  $$('[data-recipe]', root).forEach(b => b.onclick = async () => {
    const id = b.dataset.recipe, rc = ((S.op || {}).recipes || []).find(x => x.id === id);
    if (!rc || !confirm(`${rc.mod}: ${rc.what}\n\nFrame Lab changes ${rc.files.length} file${rc.files.length === 1 ? '' : 's'} of this mod (only if each is exactly the version the fix was made for). The originals are kept and Undo puts them back. Close the game first.`)) return;
    b.disabled = true;
    try { await api('optimise/fix', { build: S.opBuild, kind: 'recipe', recipe: id }); toast(rc.mod + ': fixed. It counts from the next game start; record the same routes again.'); setTimeout(renderOptimise, 600); }
    catch (e) { toast(e.message, true); b.disabled = false; }
  });
  $$('[data-rule]', root).forEach(b => b.onclick = async () => {
    const [mod, file, line, rule] = [b.dataset.mod, b.dataset.file, +b.dataset.line, b.dataset.rule];
    const sel = $(`[data-rhz="${CSS.escape(file + ':' + line)}"]`, root), hz = sel ? +sel.value : 10;
    if (!confirm(`${mod}: ${b.dataset.title} (${file}, line ${line})?\n\nThe line count of the file stays the same; the original is kept and Undo puts it back. Close the game first.`)) return;
    b.disabled = true;
    try { await api('optimise/fix', { build: S.opBuild, kind: rule, mod, file, line, hz }); toast(mod + ': changed. It counts from the next game start; record the same routes again.'); setTimeout(renderOptimise, 600); }
    catch (e) { toast(e.message, true); b.disabled = false; }
  });
  // "Do this next" buttons
  $$('[data-opact]', root).forEach(b => b.onclick = async () => {
    const rep = (S.op || {}).report || {}, a = (rep.actions || [])[+b.dataset.opact];
    if (!a) return;
    if (a.act === 'nexus') { const id = nxId(((a.links || [])[0] || {}).url); if (id) api('open', { what: 'nexus', id }).catch(e => toast(e.message, true)); return; }
    if (a.act === 'conflicts') { S.cfBuild = S.opBuild; showTab('conflicts'); return; }
    if (a.act === 'settings') { showTab('settings'); return; }
    if (a.act === 'updates') { // shown right here: the page doesn't jump down to the mod list
      const l = $('#op-updlist', root); if (!l) return;
      S.opUpdOpen = l.classList.toggle('hidden') ? false : true;
      b.textContent = S.opUpdOpen ? 'Hide them' : a.button;
      return;
    }
    if (a.act === 'slow') {
      const hs = (rep.mods || []).flatMap(m => (m.lua || []).filter(h => h.kind === 'candidate').map(h => ({ mod: m.name, file: h.file, line: h.line })));
      if (!hs.length) return;
      if (!confirm(`Make the onUpdate of ${hs.length} CET mod${hs.length === 1 ? '' : 's'} run 10 times a second instead of every frame?\n\n${hs.map(h => h.mod).join(', ')}\n\nOne line is added inside each; the original files are kept and each has an Undo below. To do them one at a time instead, use the table under this list. Close the game first.`)) return;
      b.disabled = true;
      let ok = 0; const errs = [];
      for (const h of hs) {
        try { await api('optimise/fix', { build: S.opBuild, kind: 'lua_throttle', mod: h.mod, file: h.file, line: h.line, hz: 10 }); ok++; } catch (e) { errs.push(h.mod + ': ' + e.message); }
      }
      toast(errs.length ? `${ok} slowed down, ${errs.length} not: ${errs[0]}` : `${ok} slowed down to 10× a second. It counts from the next game start.`, errs.length > 0);
      setTimeout(renderOptimise, 800);
    }
  });
  $$('[data-undo]', root).forEach(b => b.onclick = async () => {
    b.disabled = true;
    try { await api('optimise/undo', { build: S.opBuild, id: b.dataset.undo }); toast('Undone: the original file is back.'); setTimeout(renderOptimise, 600); }
    catch (e) { toast(e.message, true); b.disabled = false; }
  });
  const all = $('#op-all', root);
  if (all) all.onclick = () => { S.opAll = !S.opAll; renderOptimise(); };
  $$('[data-nx]', root).forEach(a => a.onclick = (e) => { e.stopPropagation(); api('open', { what: 'nexus', id: a.dataset.nx }).catch(err => toast(err.message, true)); });
}

/* ------------------------------------------------------------------ changes on this PC (both editions)
   Frame Lab 2.5 changes nothing itself; an earlier version or 2.5E may have. They can all go back here. */
async function renderChanges(note) {
  const host = $('#set-changes');
  if (!host) return;
  let c;
  try { c = await api('changes'); } catch (e) { host.innerHTML = '<span class="lose">' + esc(e.message) + '</span>'; return; }
  const all = [].concat(c.experiments.map(x => ['Experiments', x]), c.fixes.map(x => ['Checks fix', x]), c.programs.map(x => ['moved to integrated graphics', x]));
  host.innerHTML = (all.length ? `<div class="dim" style="margin-bottom:8px">${EX() ? 'What Frame Lab changed on this PC. Each one keeps what was there before.' : 'Frame Lab 2.5 changes nothing on your PC, but an earlier Frame Lab or 2.5E did. Put everything back sets each one to exactly what was there before.'}</div>
    <table>${all.map(x => `<tr><td>${esc(x[1])}</td><td class="dim">${esc(x[0])}</td></tr>`).join('')}</table>
    <div style="margin-top:10px"><button class="btn2" id="chg-undo">Put everything back</button></div>` :
    `<div class="dim">${EX() ? 'Nothing changed: no Experiments on, no fixes in place, no programs moved.' : 'Nothing: Frame Lab 2.5 doesn\'t change anything on your PC, and no earlier version left a change behind.'}</div>`) +
    (note ? `<div class="${note.err ? 'lose' : 'win'}" style="margin-top:8px">${esc(note.text)}</div>` : '');
  const b = $('#chg-undo', host);
  if (b) b.onclick = () => confirmBox('Put every change back?<br><span class="dim">Experiments, Checks fixes and programs moved to the integrated graphics all go back to exactly what was there before. Some need a restart of Windows or of the program.</span>', async () => {
    try { const r = await api('changes/undo', {}); renderChanges({ err: (r.errors || []).length > 0, text: (r.errors || []).length ? r.undone + ' put back, not: ' + r.errors.join('; ') : r.undone + ' put back.' }); }
    catch (e) { renderChanges({ err: true, text: e.message }); }
  });
}

/* ------------------------------------------------------------------ settings */
function renderSettings(force) {
  const root = $('#tab-settings');
  const st = S.state;
  if (!st) return;
  if (S.settingsDirty && !force) return;
  if (!force && root.dataset.built) { updateSettingsInfo(); return; }
  const c = st.config;
  const keyOpt = (vk) => { const list = S.hotkeys || [{ vk, name: vk === (c.felt_key || 121) ? st.keys.felt : st.keys.mark }]; return list.map(k => `<option value="${k.vk}" ${k.vk === vk ? 'selected' : ''}>${esc(k.name)}</option>`).join(''); };
  root.innerHTML = `<div class="set-grid"><div class="card set-main"><h2>Game builds</h2><div id="set-builds">${c.builds.map((b, i) => buildBlock(i, b)).join('')}</div>
    <div style="display:flex;gap:8px;margin-top:6px"><button class="btn2 small" id="set-find">Find game installs</button><button class="btn2 small" id="set-add">Add a build</button></div>
    <div class="dimmer small" style="margin-top:6px">A build is one Cyberpunk 2077 folder (the one with bin\\x64 in it). Find game installs looks at Steam, GOG, Epic, the running game, the usual folders on every drive and copies next to them.
      If Mod Organizer 2 runs a build, give its MO2 folder: MO2 keeps the mods outside the game folder, so without it Frame Lab sees a game with no mods.</div>
    <h2 style="margin-top:18px">Look</h2><div class="form">
      <label>Light or dark</label><div><select id="set-theme"><option value="system">Follow Windows</option><option value="dark">Dark</option><option value="light">Light</option></select> <span class="dimmer small">changes straight away, on this PC</span></div>
    </div>
    <h2 style="margin-top:18px">In-game keys</h2><div class="form">
      <label>Mark a part</label><div><select id="set-mark">${keyOpt(c.mark_key || 122)}</select> <span class="dimmer small">starts and ends a mark: script time per mod is measured inside marks</span></div>
      <label>"I felt a stutter"</label><div><select id="set-felt">${keyOpt(c.felt_key || 121)}</select> <span class="dimmer small">the report shows the 2 seconds before each press</span></div>
    </div>
    <div class="dimmer small" style="margin-top:6px">Pick keys that the game, your overlays and other mods don't use. Frame Lab and HitchRecorder see the key wherever it is pressed, so only function keys, the navigation keys and the number pad are offered. New keys count from the next session.</div>
    <h2 style="margin-top:18px">Analysis</h2><div class="form">
      <label>Spike threshold (ms)</label><input id="set-spike" type="number" min="16" max="500" value="${c.spike_ms}" style="width:110px">
      <label>Script time per mod</label><label style="color:var(--text)"><input id="set-scripts" type="checkbox" ${c.use_script_profilers ? 'checked' : ''}> time REDscript and CET mods in the parts marked with the mark key (HitchRecorder)</label>
      <label>Frame generation</label><label style="color:var(--text)"><input id="set-ft" type="checkbox" ${c.track_frame_type ? 'checked' : ''}> ask PresentMon to tell real and generated frames apart</label>
      <label>Windows events</label><label style="color:var(--text)"><input id="set-we" type="checkbox" ${c.no_windows_events ? '' : 'checked'}> record which drivers and programs held the PC during the session (Windows' own event tracing, needs admin)</label>
      <label>PresentMon exe</label><input id="set-pm" value="${esc(c.presentmon || '')}" placeholder="automatic: ${esc(st.components.presentmon.path || 'not found')}">
      <label>Mod library (optional)</label><div class="pickrow"><input id="set-lib" value="${esc(c.library)}" placeholder="folder with your mod zips: shows which download each file came from"><button class="btn2 small" data-pick="#set-lib">Browse…</button></div>
      <label>WolvenKit folder (optional)</label><div class="pickrow"><input id="set-wk" value="${esc(c.wolvenkit)}" placeholder="${EX() ? 'not needed: Frame Lab brings its own copy (components\\WolvenKit)' : 'optional: your WolvenKit folder, so the mods check shows file names instead of numbers'}"><button class="btn2 small" data-pick="#set-wk">Browse…</button></div>
    </div>
    <h2 style="margin-top:18px">${EX() ? 'Optimise' : 'Mods'}</h2><div class="form">
      <label>Mod updates</label><label style="color:var(--text)"><input id="set-live" type="checkbox" ${c.live_updates ? 'checked' : ''}> ask Nexus Mods which of your mods have a newer version when a build is checked</label>
    </div>
    <div class="dimmer small" style="margin-top:6px">Off until you switch it on. Only the Nexus ids of the mods Frame Lab recognises are sent (to api.nexusmods.com, no account or key needed); no file names, nothing else about your PC. Without it ${EX() ? 'Optimise' : 'Check your mods'} works the same, just without the update column.</div>
    <div style="margin-top:16px;display:flex;gap:10px"><button class="btn" id="set-save">Save</button><span class="dim small" id="set-msg"></span></div></div>
    <div class="set-side"><div class="card"><h2>Parts</h2><div id="set-info"></div></div>
    <div class="card"><h2>Changes on this PC</h2><div id="set-changes" class="dim">Reading ...</div></div>
    <div class="card"><h2>About</h2><div class="dim">Night City Frame Lab ${esc(st.version)}<br>
      PresentMon by Intel (MIT). uPlot by Leon Sorokin (MIT). go-webview2 by John Chadwick (MIT). Saira, JetBrains Mono and Jura fonts (SIL OFL 1.1). HitchRecorder uses MinHook by Tsuda Kageyu (BSD-2); its REDscript and CET timing is our own code (mapping script functions to their files at load is an idea from jekky's redscript-dap, MIT; the CET part reads LuaJIT's data layout, MIT).
      File names come from WolvenKit's path list. ${EX() ? 'The WolvenKit console (WolvenKit project, GPL-3.0) is included unchanged in components\\WolvenKit, with its licence and a link to its source.' : 'Frame Lab 2.5 reads it from your own WolvenKit, if you point it there.'}</div></div></div></div>`;
  renderChanges();
  root.dataset.built = '1';
  const th = $('#set-theme', root);
  th.value = themeGet();
  th.addEventListener('change', (e) => { e.stopPropagation(); themeSet(th.value); });
  th.addEventListener('input', (e) => e.stopPropagation());
  const dirty = () => { S.settingsDirty = true; $('#set-msg').textContent = 'unsaved changes'; };
  root.addEventListener('input', dirty);
  root.addEventListener('change', (e) => { if (e.target.matches('[data-bp],[data-bm],#set-lib,#set-wk,#set-pm')) e.target.value = e.target.value.trim().replace(/^"|"$/g, ''); });
  root.addEventListener('change', (e) => { if (e.target.tagName === 'SELECT') dirty(); });
  $$('[data-pick]', root).forEach(b => b.onclick = async () => {
    const inp = $(b.dataset.pick);
    b.disabled = true;
    try { const r = await api('pickfolder', { title: 'Pick a folder', start: inp.value.trim() }); if (r.path) { inp.value = r.path; dirty(); } } catch (x) { toast(x.message, true); }
    b.disabled = false;
  });
  $('#set-add').onclick = () => { $('#set-builds').insertAdjacentHTML('beforeend', buildBlock('n', { name: '', path: '', mo2: '' })); dirty(); };
  $('#set-find').onclick = async () => {
    let r;
    try { r = await api('builds/detect', {}); } catch (x) { toast(x.message, true); return; }
    const have = new Set($$('#set-builds [data-bp]').map(i => i.value.trim().toLowerCase().replace(/[\\/]+$/, '')));
    const add = (r.found || []).filter(f => !have.has(f.path.toLowerCase().replace(/[\\/]+$/, '')));
    add.forEach(f => $('#set-builds').insertAdjacentHTML('beforeend', buildBlock('n', { name: f.name, path: f.path, mo2: '' })));
    if (add.length) { dirty(); toast('Found ' + add.length + ' more: check the names, then Save.'); }
    else toast((r.found || []).length ? 'No new installs: every one found is already listed.' : 'No Cyberpunk 2077 install found. Use Add a build, then Browse… to the game folder.');
  };
  $('#set-builds').onclick = async (e) => {
    const br = e.target.closest('[data-browse]');
    if (br) {
      const row = br.closest('.bblock'), pin = $('[data-bp]', row), nin = $('[data-bn]', row);
      br.disabled = true;
      try {
        const r = await api('pickfolder', { kind: 'build', title: 'Pick the Cyberpunk 2077 folder (the one with bin\\x64 in it)', start: pin.value.trim().replace(/^"|"$/g, '') });
        if (r.path) {
          pin.value = r.path;
          if (!nin.value.trim() && r.name) nin.value = r.name;
          dirty();
          toast(r.game ? 'Game folder set. Press Save.' : 'That folder has no bin\\x64\\Cyberpunk2077.exe: pick the game folder itself.', !r.game);
        }
      } catch (x) { toast(x.message, true); }
      br.disabled = false;
      return;
    }
    const d = e.target.closest('[data-del]');
    if (d) { d.closest('.bblock').remove(); dirty(); return; }
    const u = e.target.closest('[data-usemo2]');
    if (u) { const inp = $('[data-bm]', u.closest('.bblock')); inp.value = u.dataset.usemo2; dirty(); toast('Press Save to use this MO2 instance.'); }
  };
  $('#set-save').onclick = async () => {
    const builds = $$('#set-builds .bblock').map(r => ({ name: $('[data-bn]', r).value.trim(), path: $('[data-bp]', r).value.trim(), mo2: $('[data-bm]', r).value.trim() })).filter(b => b.path);
    const cfg = Object.assign({}, S.state.config, {
      builds, spike_ms: +$('#set-spike').value || 50, use_script_profilers: $('#set-scripts').checked, track_frame_type: $('#set-ft').checked, no_windows_events: !$('#set-we').checked,
      presentmon: $('#set-pm').value.trim(), library: $('#set-lib').value.trim(), wolvenkit: $('#set-wk').value.trim(),
      felt_key: +$('#set-felt').value || 0, mark_key: +$('#set-mark').value || 0, live_updates: $('#set-live').checked,
    });
    if (cfg.felt_key && cfg.felt_key === cfg.mark_key) { toast('The two in-game keys have to be different keys.', true); return; }
    if (cfg.active >= builds.length) cfg.active = 0;
    try { await api('settings', cfg); S.settingsDirty = false; S.setInfo = null; toast('Saved. Re-analyse old sessions to use a new spike threshold.'); S.state = await api('state'); renderSettings(true); } catch (x) { toast(x.message, true); }
  };
  updateSettingsInfo();
  loadSettingsInfo();
}

// one build in Settings: name, folder, and the MO2 instance that runs it (2.5)
function buildBlock(i, b) {
  return `<div class="bblock"><div class="brow b4"><input data-bn="${i}" value="${esc(b.name)}" placeholder="name"><input data-bp="${i}" value="${esc(b.path)}" placeholder="C:\\...\\Cyberpunk 2077"><button class="btn2 small" data-browse="${i}" title="pick the game folder">Browse…</button><button class="btn2 small" data-del="${i}" title="remove">✕</button></div>
    <div class="brow mo2row"><span class="dim small">Mod Organizer 2</span><input data-bm="${i}" value="${esc(b.mo2 || '')}" placeholder="only if MO2 runs this build: its folder (the one with ModOrganizer.ini)"><span></span></div>
    <div class="mo2info small" data-mi="${i}"></div></div>`;
}

// the key list and what each build's MO2 instance looks like (or which instances were found for it)
async function loadSettingsInfo() {
  let info = S.setInfo;
  if (!info) {
    try { info = S.setInfo = await api('settings/info'); } catch (e) { return; }
  }
  S.hotkeys = info.hotkeys || S.hotkeys;
  const c = S.state.config;
  [['#set-mark', c.mark_key || 122], ['#set-felt', c.felt_key || 121]].forEach(([id, vk]) => {
    const sel = $(id);
    if (!sel || sel.options.length > 1) return;
    const cur = +sel.value || vk;
    sel.innerHTML = S.hotkeys.map(k => `<option value="${k.vk}" ${k.vk === cur ? 'selected' : ''}>${esc(k.name)}</option>`).join('');
  });
  (info.mo2 || []).forEach(m => {
    const el = $(`.mo2info[data-mi="${m.index}"]`);
    if (!el) return;
    if (m.instance) {
      const x = m.instance;
      el.innerHTML = `<span class="dot ${m.error ? 'y' : 'g'}" style="display:inline-block;margin:0 6px 0 0"></span>${esc(x.name)} · profile <b>${esc(x.profile)}</b> · ${x.enabled} mods on` +
        `<span class="dimmer"> · mods ${esc(x.mods_dir)} · overwrite ${esc(x.overwrite_dir)}</span>` + (m.error ? `<div style="color:var(--amber)">${esc(m.error)}</div>` : '');
    } else if (m.error) {
      el.innerHTML = `<span style="color:var(--amber)">${esc(m.error)}</span>`;
    } else if ((m.suggested || []).length) {
      el.innerHTML = m.suggested.map(x => `<div>MO2 instance found for this game: <b>${esc(x.name)}</b> <span class="dimmer">${esc(x.dir)} · profile ${esc(x.profile)} · ${x.enabled} mods on</span> <button class="link" data-usemo2="${esc(x.dir)}">Use it</button></div>`).join('');
    } else el.innerHTML = '';
  });
}

function updateSettingsInfo() {
  const st = S.state, cp = st.components;
  const el = $('#set-info');
  if (!el) return;
  const r = (n, ok, d) => `<tr><td><span class="dot ${ok ? 'g' : 'y'}" style="display:inline-block;margin:0 8px 0 0"></span>${n}</td><td class="dim" style="word-break:break-all">${d}</td></tr>`;
  el.innerHTML = '<table>' +
    r('PresentMon', cp.presentmon.found, esc(cp.presentmon.path || 'not found')) +
    r('HitchRecorder (for sessions)', cp.hitch_pkg.found, cp.hitch_pkg.found ? esc(cp.hitch_pkg.version) : 'missing') +
    r('File-name list', st.names === 'ready', esc(st.names || 'not built') + ' <button class="link" id="set-names">' + (st.names === 'ready' ? 'rebuild' : 'build') + '</button>') +
    r('WolvenKit', cp.wolvenkit.found, cp.wolvenkit.found ? (cp.wolvenkit.bundled ? 'Frame Lab\'s own copy: ' : 'your install: ') + esc(cp.wolvenkit.path) : 'missing from components\\WolvenKit' + (cp.wolvenkit.path ? ' and from ' + esc(cp.wolvenkit.path) : '') + ': file names stay numbers') +
    (cp.wolvenkit.found && cp.wolvenkit.cli ? r('.NET 10 runtime', !!cp.wolvenkit.dotnet, cp.wolvenkit.dotnet ? esc(cp.wolvenkit.dotnet) + ' (what the WolvenKit console runs on)' : 'not installed. File names don\'t need it. The WolvenKit console does, to unpack mod files: Microsoft\'s ".NET Runtime 10" from dotnet.microsoft.com') : '') +
    r('Mod library', cp.library.found, esc(cp.library.path)) +
    r(EX() ? 'Mod index (Optimise)' : 'Mod index (Check your mods)', cp.mod_index && cp.mod_index.found, cp.mod_index && cp.mod_index.found ? esc(cp.mod_index.path) : 'missing: mods are named by folder only') + '</table>';
  const b = $('#set-names');
  if (b) b.onclick = async () => { try { await api('names/build', {}); toast('Building the file-name list ...'); } catch (x) { toast(x.message, true); } };
}

/* ------------------------------------------------------------------ start */
if (REPORT) {
  document.body.classList.add('report-only');
  $$('.tab').forEach(t => t.classList.remove('on'));
  const host = $('#tab-sessions');
  host.classList.add('on');
  host.innerHTML = '<div id="report"></div>';
  renderReport($('#report'), REPORT, true);
} else {
  poll();
  setInterval(() => { if (S.tab === 'sessions' && !S.sessionsLoaded) { S.sessionsLoaded = true; loadSessions(); } }, 2000);
}

/* ------------------------------------------------------------------ Workbench (2.5E Optimise stage 4)
   Edit a mod by hand: a copy in Frame Lab's own folder, checks while you type, advice on what to do and
   not, Show changes against the original, and Install through the fix list (original kept, Undo). */
const WB_ADVICE = {
  all: {
    do: ['Change one thing at a time: install, record the same test routes, compare.',
      'Read the mod\'s page first: many values are meant to be changed in its own settings (Mod Settings, the CET overlay), no editing needed.',
      'Look at Show changes before you install: it is exactly what goes into the build.'],
    dont: ['Never upload or share edited mod files: they are the author\'s work and their permissions apply. Tell the author what helped instead.',
      'Don\'t install while the game runs: the game and the frameworks read mod files when it starts.'],
  },
  redscript: {
    do: ['Use @wrapMethod and call wrappedMethod(...): the game\'s own code and other mods\' wraps keep running.',
      'Keep heavy work out of functions that run every frame or key press (OnUpdate, OnAction, OnTick): look a system up once and keep it in a field.',
      'After installing, start the game once: if the script compile fails, Optimise shows it first, with Undo.'],
    dont: ['Don\'t rename classes, functions or fields other files use, and don\'t remove @addMethod functions: every caller then fails to compile, and with one failing script the game runs without ANY mod scripts.',
      'Don\'t use @replaceMethod unless you must: only one mod\'s replacement can run, and other mods\' wraps are lost.'],
  },
  lua: {
    do: ['Let onUpdate checks run a few times a second: add up dt and only work when 0.1 to 0.25 s have passed.',
      'Keep CET\'s event names exactly as written: onInit, onUpdate, onDraw, onOverlayOpen, onOverlayClose, onShutdown.',
      'CET reads mods when the game starts: restart the game after installing.'],
    dont: ['Don\'t put camera, vehicle, UI, sound or animation work on a timer: those have to run every frame or they stutter.',
      'Don\'t add many new local variables at the top of a very large file (Lua allows 200 per function): keep new values in a table.'],
  },
  archivexl: {
    do: ['When ArchiveXL says a node type changed in 2.5, check the index still points at the same object (its position): the Fix button on Conflicts does the common case.',
      'Keep expectedNodes equal to the number of nodes the sector has in this game version.'],
    dont: ['Don\'t guess indexes after a game update: a wrong index deletes or moves a different object, without any error.',
      'Don\'t remove a sector entry to "fix" an error: the edit then silently does nothing.'],
  },
  tweakxl: {
    do: ['Change values: numbers, text, names of records that exist.',
      'For one value the flat form works: Record.property: value.',
      'Lists with !append combine with other mods; a plain value is set by one mod only (Conflicts shows which wins).'],
    dont: ['Don\'t change $type, and check that a $base record exists in this game version.',
      'Don\'t use tabs, and always write "key: value" with a space after the colon.'],
  },
  json: {
    do: ['Change values: numbers, text, and game paths that exist (in the game or in this archive).',
      'Keep the structure: the same keys, entries in the same order.',
      'New meshes, textures or animations belong in WolvenKit itself; this editor is for values.'],
    dont: ['Don\'t change $type, HandleId / HandleRefId, BufferId or Flags.',
      'Don\'t delete an entry other entries point at (a HandleRefId with its number): the game crashes loading the file.'],
  },
  yaml: { do: ['Keep the indentation with spaces, two per level.'], dont: ['Don\'t use tabs.'] },
  ini: { do: ['Change values only; keep section names in [brackets].'], dont: [] },
  text: { do: [], dont: [] },
};
const WB_KIND = { redscript: 'REDscript', lua: 'CET Lua', archivexl: 'ArchiveXL world edit', tweakxl: 'TweakXL tweak', json: 'game file (readable form)', yaml: 'YAML', ini: 'settings', text: 'text', tweak: 'tweak' };

async function renderWorkbench() {
  const root = $('#tab-workbench');
  if (!EX()) { root.innerHTML = ''; return; }
  S.wb = S.wb || {};
  const W = S.wb, bi = S.opBuild != null ? S.opBuild : (S.state ? S.state.config.active || 0 : 0);
  if (!W.id) {
    let d;
    try { d = await api('workbench', { build: bi }); } catch (e) { root.innerHTML = `<div class="card"><span class="lose">${esc(e.message)}</span></div>`; return; }
    if (S.tab !== 'workbench') return;
    const wk = d.wolvenkit || {}, q = (W.q || '').toLowerCase();
    const srcs = (d.sources || []).filter(x => !q || x.rel.toLowerCase().includes(q));
    const groups = [...new Set(srcs.map(x => x.group))];
    const projects = d.projects || [];
    root.innerHTML = `<div class="rep-head"><div class="title">Workbench</div><span class="badge">${esc(d.build || '')}</span></div>
      <div class="dim" style="max-width:980px;margin:-4px 0 14px">Edit a mod by hand. Frame Lab makes a copy in its own folder; you edit the copy with checks while you type and advice on what to do and not, and nothing in the build changes until <b>Install</b>, which keeps the original and has an exact Undo (and counts as a change in the test round).</div>
      <div class="wb-cols">
        <div class="card"><h2>Your projects</h2>${projects.length ? `<table><tr><th>project</th><th>build</th><th>state</th><th></th></tr>${projects.map(p => `<tr><td><b>${esc(p.name)}</b><div class="mono dimmer small">${esc(p.source)}</div></td><td class="dim">${esc(p.build)}</td><td class="dim">${esc(p.state)}${p.installed ? ' · <span class="win">installed</span>' : ''}</td><td style="white-space:nowrap"><button class="btn2 small" data-wbopen="${esc(p.id)}">Open</button> <button class="btn2 small" data-wbdel="${esc(p.id)}" title="Delete the copy (an installed edit stays; Undo it in Optimise)">Delete</button></td></tr>`).join('')}</table>` : '<div class="dim">None yet: open a mod on the right.</div>'}</div>
        <div class="card"><h2>Open a mod <span class="dim">from ${esc(d.build || 'this build')}</span></h2>
          <input id="wb-q" placeholder="find a mod folder or file" value="${esc(W.q || '')}" style="width:100%;margin-bottom:10px">
          <div class="wb-src">${groups.map(g => `<div class="wb-group">${esc(g)}</div>` + srcs.filter(x => x.group === g).slice(0, 60).map(x => `<div class="wb-srow"><span class="mono">${esc(x.rel.split('/').pop())}</span><span class="dimmer small">${x.kind === 'folder' ? 'folder' : x.kind === 'archive' ? 'archive' + (x.size ? ' · ' + (x.size / 1048576).toFixed(1) + ' MB' : '') : 'file'}</span><button class="btn2 small" data-wbnew="${esc(x.rel)}" data-wbkind="${x.kind}">Open</button></div>`).join('')).join('') || '<div class="dim">Nothing matches.</div>'}</div>
          <div class="dimmer small" style="margin-top:10px">Archives are unpacked and turned into readable text by the WolvenKit console that comes with Frame Lab${wk.cli ? '' : ' (not found: components\\WolvenKit)'}; it needs Microsoft's .NET 10 runtime: ${wk.dotnet ? '<span class="win">' + esc(wk.dotnet) + ' found</span>' : '<span class="lose">not on this PC</span> (get ".NET Runtime 10" from Microsoft; scripts, Lua, tweaks and .xl files work without it)'}.</div></div>
      </div>`;
    const qi = $('#wb-q', root);
    if (qi) qi.oninput = () => { W.q = qi.value; clearTimeout(W.qt); W.qt = setTimeout(() => renderWorkbench().then(() => { const i = $('#wb-q'); if (i) { i.focus(); i.setSelectionRange(i.value.length, i.value.length); } }), 250); };
    $$('[data-wbopen]', root).forEach(b => b.onclick = () => { W.id = b.dataset.wbopen; W.path = null; renderWorkbench(); });
    $$('[data-wbdel]', root).forEach(b => b.onclick = async () => { if (!confirm('Delete this project\'s copy? An installed edit stays in the build until you Undo it in Optimise.')) return; try { await api('workbench/delete', { id: b.dataset.wbdel }); renderWorkbench(); } catch (e) { toast(e.message, true); } });
    $$('[data-wbnew]', root).forEach(b => b.onclick = async () => {
      if (b.dataset.wbkind === 'archive' && !confirm('Unpack this archive into the Workbench? The WolvenKit console turns its game files into readable text; a big archive takes a while and needs disk space.')) return;
      b.disabled = true;
      try { const p = await api('workbench/new', { build: bi, source: b.dataset.wbnew }); W.id = p.id; W.path = null; renderWorkbench(); } catch (e) { toast(e.message, true); b.disabled = false; }
    });
    return;
  }
  // ---- a project
  if (!S.op || S.opBuild !== bi) { S.opBuild = bi; await loadOptimise(); }
  let p;
  try { p = await api('workbench/project', { id: W.id }); } catch (e) { W.id = null; return renderWorkbench(); }
  if (S.tab !== 'workbench') return;
  W.proj = p;
  const busy = p.state === 'unpacking' || p.state === 'packing';
  if (busy) { clearTimeout(W.poll); W.poll = setTimeout(() => { if (S.tab === 'workbench' && !W.dirty) renderWorkbench(); }, 1500); }
  const files = p.files || [];
  if (!W.path && files.length) W.path = (files.find(f => f.text) || files[0]).path;
  const fx = ((S.op || {}).fixes || []).find(x => x.id === p.installed && !x.undone && !x.gone);
  const nChanged = files.filter(f => f.changed).length;
  let html = `<div class="wb-backbar"><button class="btn2 small" data-wbback="1">← Back to all mods and projects</button></div>
    <div class="rep-head"><div class="title"><a class="wb-back" data-wbback="1">Workbench</a> › ${esc(p.name)}</div><span class="badge">${esc(p.build)}</span>
    <div class="actions">${fx ? `<span class="tag g">installed</span> <button class="btn2" data-undo-wb="${esc(fx.id)}">Undo install</button>` : ''}<button class="btn" id="wb-install" ${busy || !nChanged ? 'disabled' : ''}>Install into ${esc(p.build)}${nChanged ? ` (${nChanged} file${nChanged === 1 ? '' : 's'})` : ''}</button></div></div>
    <div class="dimmer small" style="margin:-6px 0 12px"><span class="mono">${esc(p.source)}</span> · ${p.kind === 'archive' ? 'archive' : 'files'} · ${esc(p.state)}${p.log ? ` · <a data-wblog="1">WolvenKit output</a>` : ''}</div>`;
  if (W.showLog && p.log) html += `<pre class="card wb-log">${esc(p.log)}</pre>`;
  if (busy) { root.innerHTML = html + `<div class="card"><div class="dim">${p.state === 'unpacking' ? 'Unpacking the archive and turning its game files into readable text with the WolvenKit console …' : 'Packing the archive again and putting it into the build …'}</div></div>`; bindWB(root); return; }
  if (p.state.startsWith('error')) { root.innerHTML = html + `<div class="card warnbox">${esc(p.state)}</div>`; bindWB(root); return; }
  const sc = (S.op || {}).schedule;
  if (sc && (['start', 'routes', 'conflicts', 'analyse'].includes(sc.step) || sc.locked)) html += `<div class="card dim small" style="margin-bottom:12px">You can edit and check now; <b>Install</b> opens once ${['start', 'routes', 'conflicts', 'analyse'].includes(sc.step) ? 'the build has been measured (Optimise: start the test round)' : 'the retest of the last change is recorded'}, like every other fix.</div>`;
  html += `<div class="wb-grid"><div class="wb-files card">${wbTree(files, W.path)}</div><div class="wb-main" id="wb-main"><div class="dim">Pick a file.</div></div><div class="wb-side" id="wb-side"></div></div>`;
  root.innerHTML = html;
  bindWB(root);
  if (W.path) wbOpen(W.path, W.line);
}

function wbTree(files, cur) {
  const byDir = {};
  files.forEach(f => { const d = f.path.includes('/') ? f.path.slice(0, f.path.lastIndexOf('/')) : ''; (byDir[d] = byDir[d] || []).push(f); });
  return Object.keys(byDir).sort().map(d => `${d ? `<div class="wb-dir mono" title="${esc(d)}">${esc(d.length > 40 ? '…' + d.slice(-39) : d)}</div>` : ''}` +
    byDir[d].map(f => `<div class="wb-file ${f.path === cur ? 'on' : ''} ${f.text ? '' : 'bin'}" data-wbfile="${esc(f.path)}" title="${esc(f.path)}">${f.changed ? '<i class="dot"></i>' : ''}<span class="mono">${esc(f.path.split('/').pop())}</span></div>`).join('')).join('');
}

function bindWB(root) {
  const W = S.wb;
  $$('[data-wbback]', root).forEach(a => a.onclick = () => { if (W.dirty && !confirm('Leave without saving?')) return; W.id = null; W.dirty = false; renderWorkbench(); });
  $$('[data-wblog]', root).forEach(a => a.onclick = () => { W.showLog = !W.showLog; renderWorkbench(); });
  $$('[data-wbfile]', root).forEach(el => el.onclick = () => { if (W.dirty && !confirm('Switch without saving?')) return; W.dirty = false; W.line = 0; wbOpen(el.dataset.wbfile); $$('[data-wbfile]', root).forEach(x => x.classList.toggle('on', x === el)); });
  $$('[data-undo-wb]', root).forEach(b => b.onclick = async () => { try { await api('optimise/undo', { build: S.opBuild || 0, id: b.dataset.undoWb }); await loadOptimise(); toast('Undone: the build has the files it had before.'); renderWorkbench(); } catch (e) { toast(e.message, true); } });
  const ins = $('#wb-install', root);
  if (ins) ins.onclick = () => wbInstall(false);
}

async function wbInstall(force) {
  const W = S.wb, p = W.proj;
  if (W.dirty) { toast('Save first (Ctrl+S).', true); return; }
  const bi = (S.state.config.builds || []).findIndex(b => b.name === p.build);
  if (!force && !confirm(`Install the changed files into ${p.build}?\n\nThe originals are kept; Undo (here or in Optimise > Fixes made on this PC) puts them back exactly. Close the game first; after installing, record the same test routes again.`)) return;
  try {
    const r = await api('workbench/install', { build: bi, id: p.id, force });
    toast(r.packing ? 'Packing … the archive goes into the build when it is done.' : 'Installed. It counts from the next game start.');
    await loadOptimise(); renderWorkbench();
  } catch (e) {
    if (/fix the errors first/.test(e.message)) {
      if (confirm('The checks found errors:\n\n' + e.message.replace(/^fix the errors first: /, '').split('; ').join('\n') + '\n\nAn error usually means the file won\'t work in game. Install anyway?')) wbInstall(true);
    } else toast(e.message, true);
  }
}

async function wbOpen(path, line) {
  const W = S.wb, main = $('#wb-main'), side = $('#wb-side');
  if (!main) return;
  W.path = path;
  let d;
  try { d = await api('workbench/read', { id: W.id, path }); } catch (e) { main.innerHTML = `<div class="card"><span class="lose">${esc(e.message)}</span></div>`; return; }
  if (d.binary) {
    main.innerHTML = `<div class="card"><div class="dim">${esc(path)} isn't a text file (${fmt.n(d.size)} bytes): it goes back into the archive as it is. WolvenKit itself is the tool for meshes, textures and other binary files.</div></div>`;
    side.innerHTML = wbAdviceHtml('text', []);
    return;
  }
  W.text = d.text; W.orig = d.original; W.kind = d.kind; W.lint = d.lint || []; W.dirty = false; W.diff = false;
  main.innerHTML = `<div class="wb-bar"><span class="mono">${esc(path)}</span><span class="tag b">${esc(WB_KIND[d.kind] || d.kind)}</span><span id="wb-state" class="dimmer small"></span>
      <span class="wb-tools"><input id="wb-find" placeholder="find (Ctrl+F)" title="Enter: next"><button class="btn2 small" id="wb-diff">Show changes</button><button class="btn2 small" id="wb-revert">Revert to original</button><button class="btn small" id="wb-save">Save</button></span></div>
    <div class="wb-ed" id="wb-ed"><pre class="wb-gutter" id="wb-gut"></pre><textarea id="wb-ta" spellcheck="false" wrap="off"></textarea></div>
    <div class="wb-diffview hidden" id="wb-dv"></div>`;
  const ta = $('#wb-ta'), gut = $('#wb-gut');
  ta.value = d.text;
  const paint = () => {
    const n = ta.value.split('\n').length, marks = {};
    (W.lint || []).forEach(x => { if (x.line && (!marks[x.line] || x.level === 'error')) marks[x.line] = x.level; });
    let g = '';
    for (let i = 1; i <= n; i++) g += marks[i] ? `<b class="lv-${marks[i]}">${i}</b>\n` : i + '\n';
    gut.innerHTML = g;
    gut.scrollTop = ta.scrollTop;
    $('#wb-state').textContent = W.dirty ? 'not saved' : (W.text !== W.orig ? 'changed from the original' : 'as the original');
  };
  ta.onscroll = () => { gut.scrollTop = ta.scrollTop; };
  ta.oninput = () => {
    W.dirty = true; paint();
    clearTimeout(W.ct);
    W.ct = setTimeout(async () => { try { const r = await api('workbench/check', { id: W.id, path, text: ta.value }); W.lint = r.lint || []; paint(); side.innerHTML = wbAdviceHtml(W.kind, W.lint); bindSide(); } catch (e) { /* keep typing */ } }, 500);
  };
  ta.onkeydown = (e) => {
    if (e.key === 'Tab') { e.preventDefault(); const a = ta.selectionStart, b = ta.selectionEnd, ins = W.kind === 'lua' || W.kind === 'redscript' ? '    ' : '  '; ta.setRangeText(ins, a, b, 'end'); ta.oninput(); }
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') { e.preventDefault(); save(); }
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'f') { e.preventDefault(); const f = $('#wb-find'); f.focus(); f.select(); }
  };
  const save = async () => {
    try { const r = await api('workbench/save', { id: W.id, path, text: ta.value }); W.text = ta.value; W.dirty = false; W.lint = r.lint || []; paint(); side.innerHTML = wbAdviceHtml(W.kind, W.lint); bindSide();
      const errs = W.lint.filter(x => x.level === 'error').length; toast(errs ? `Saved, with ${errs} error${errs === 1 ? '' : 's'}: see Checks.` : 'Saved.', !!errs); wbRefreshTree(); }
    catch (e) { toast(e.message, true); }
  };
  $('#wb-save').onclick = save;
  $('#wb-revert').onclick = async () => { if (!confirm('Put this file back to the original? Your changes to it are lost.')) return; try { await api('workbench/revert', { id: W.id, path }); W.dirty = false; wbOpen(path); wbRefreshTree(); } catch (e) { toast(e.message, true); } };
  $('#wb-diff').onclick = async () => {
    W.diff = !W.diff;
    const dv = $('#wb-dv'), ed = $('#wb-ed');
    $('#wb-diff').textContent = W.diff ? 'Back to editing' : 'Show changes';
    dv.classList.toggle('hidden', !W.diff); ed.classList.toggle('hidden', W.diff);
    if (W.diff) { try { const r = await api('workbench/diff', { id: W.id, path, text: ta.value }); dv.innerHTML = wbDiffHtml(W.orig, ta.value, r.hunks || []); } catch (e) { dv.innerHTML = esc(e.message); } }
  };
  const fi = $('#wb-find');
  fi.onkeydown = (e) => {
    if (e.key !== 'Enter' || !fi.value) return;
    e.preventDefault();
    const hay = ta.value.toLowerCase(), needle = fi.value.toLowerCase();
    let at = hay.indexOf(needle, ta.selectionEnd);
    if (at < 0) at = hay.indexOf(needle);
    if (at < 0) { toast('Not found.'); return; }
    ta.focus(); ta.setSelectionRange(at, at + needle.length);
    wbScrollTo(ta, ta.value.slice(0, at).split('\n').length);
  };
  const bindSide = () => $$('[data-wbline]', side).forEach(el => el.onclick = () => wbGoLine(ta, +el.dataset.wbline));
  side.innerHTML = wbAdviceHtml(d.kind, W.lint); bindSide();
  paint();
  if (line) setTimeout(() => wbGoLine(ta, line), 50);
}

function wbScrollTo(ta, line) { const lh = parseFloat(getComputedStyle(ta).lineHeight) || 20; ta.scrollTop = Math.max(0, (line - 6) * lh); }
function wbGoLine(ta, line) {
  if (!line) return;
  const ls = ta.value.split('\n'); let at = 0;
  for (let i = 0; i < line - 1 && i < ls.length; i++) at += ls[i].length + 1;
  ta.focus(); ta.setSelectionRange(at, at + (ls[line - 1] || '').length); wbScrollTo(ta, line);
}
async function wbRefreshTree() {
  const W = S.wb;
  try { const p = await api('workbench/project', { id: W.id }); W.proj = p; const t = $('.wb-files'); if (t) { t.innerHTML = wbTree(p.files || [], W.path); bindWB($('#tab-workbench')); }
    const n = (p.files || []).filter(f => f.changed).length, b = $('#wb-install'); if (b) { b.disabled = !n; b.textContent = `Install into ${p.build}${n ? ` (${n} file${n === 1 ? '' : 's'})` : ''}`; } } catch (e) { /* stays */ }
}

function wbDiffHtml(a, b, hunks) {
  if (!hunks.length) return '<div class="dim" style="padding:12px">No changes: the file is as the original.</div>';
  const A = a.replace(/\r\n/g, '\n').split('\n'), B = b.replace(/\r\n/g, '\n').split('\n');
  const row = (cls, o, n, sign, text) => `<div class="${cls}"><span class="d-n">${o || ''}</span><span class="d-n">${n || ''}</span>${sign} ${esc(text)}</div>`;
  return `<div class="d-head"><span class="d-n">was</span><span class="d-n">now</span></div>` + hunks.map(h => {
    const rows = [], shift = h.b - h.a, after = (h.b + h.bn) - (h.a + h.an);
    for (let i = Math.max(0, h.a - 2); i < h.a; i++) rows.push(row('d-ctx', i + 1, i + 1 + shift, ' ', A[i]));
    for (let i = 0; i < h.an; i++) rows.push(row('d-del', h.a + i + 1, '', '-', A[h.a + i]));
    for (let i = 0; i < h.bn; i++) rows.push(row('d-add', '', h.b + i + 1, '+', B[h.b + i]));
    for (let i = h.a + h.an; i < Math.min(A.length, h.a + h.an + 2); i++) rows.push(row('d-ctx', i + 1, i + 1 + after, ' ', A[i]));
    return `<div class="d-hunk">${rows.join('')}</div>`;
  }).join('') + `<div class="dimmer small" style="padding:8px 12px">${hunks.length} change${hunks.length === 1 ? '' : 's'}; red is the original, green is yours.</div>`;
}

function wbAdviceHtml(kind, lint) {
  const lv = { error: ['E', 'error'], warn: ['!', 'warning'], tip: ['i', 'tip'] };
  const order = { error: 0, warn: 1, tip: 2 };
  const items = (lint || []).slice().sort((a, b) => order[a.level] - order[b.level] || a.line - b.line);
  const A = WB_ADVICE[kind] || WB_ADVICE.text, G = WB_ADVICE.all;
  const list = (xs, cls) => xs.map(x => `<li class="${cls}">${esc(x)}</li>`).join('');
  return `<div class="card wb-checks"><h3>Checks <span class="dimmer small">${items.length ? items.length + ' found' : 'nothing found'}</span></h3>
      ${items.length ? items.map(x => `<div class="wb-item lv-${x.level}" ${x.line ? `data-wbline="${x.line}"` : ''}><b>${lv[x.level][0]}</b><div><div>${x.line ? `<span class="mono dimmer">line ${x.line}</span> ` : ''}${esc(x.text)}</div><div class="dimmer small">${esc(x.why)}</div></div></div>`).join('')
        : '<div class="dim small">No errors, no risky changes. The checks read the file; whether it does what you want shows in game.</div>'}</div>
    <div class="card wb-advice"><h3>Do and don't <span class="dimmer small">${esc(WB_KIND[kind] || kind)}</span></h3>
      <ul class="wb-do">${list(A.do.concat(G.do), 'do')}</ul><ul class="wb-dont">${list(A.dont.concat(G.dont), 'dont')}</ul></div>`;
}

// open a file of the build in the Workbench (from Optimise and Conflicts)
async function openInWorkbench(build, file, line) {
  try { const r = await api('workbench/open', { build, file }); S.wb = { id: r.project.id, path: r.path, line: line || 0 }; showTab('workbench'); }
  catch (e) { toast(e.message, true); }
}

// ---- Before you install (2.5 and 2.5E, read only): what a Nexus mod would do in a build, from the
// downloaded copies of the mods Frame Lab knows. Nothing is downloaded or installed.
const SAVE_WORD = { GiveItem: 'gives items', AddItem: 'gives items', RemoveItem: 'takes items away', Teleport: 'teleports V', SetFactValue: 'sets quest facts', AddFact: 'sets quest facts', CompleteQuest: 'changes quests', SetQuestState: 'changes quests' };
async function renderPrecheck() {
  const root = $('#tab-precheck');
  if (!S.state) { root.innerHTML = '<div class="empty">Loading ...</div>'; return; }
  const st = S.state, P = S.pre = S.pre || {};
  P.build = st.config.active || 0; // the header's build
  const bs = st.config.builds || [];
  let html = `<div class="rep-head"><div class="title">Before you install</div>
    <div class="cf-builds">${bs.map((b, i) => `<button class="btn2 small ${i === P.build ? 'on' : ''}" data-pb="${i}" style="${i === P.build ? 'border-color:var(--accent);color:var(--white)' : ''}">${esc(b.name)}</button>`).join('')}</div></div>
    <div class="dim" style="max-width:980px;margin:-4px 0 14px">Look up a Nexus mod before you add it to <b>${esc((bs[P.build] || {}).name || 'this build')}</b>: which of your mods it would clash with and whose files win, TweakXL values your mods set differently, what it needs, and what its scripts do often. It comes from the downloaded copies of about 6,200 mods, so the newest version of a mod can differ. Nothing is downloaded or installed.</div>
    <div class="card"><input id="pre-q" style="width:100%;max-width:520px" placeholder="Mod name or Nexus number" value="${esc(P.q || '')}" autocomplete="off">
    <div id="pre-hits" style="margin-top:10px">${preHits(P)}</div></div>`;
  if (P.busy) html += `<div class="card" style="margin-top:14px"><span class="dim">Reading ${esc((bs[P.build] || {}).name || 'the build')}'s mods ... (the first look takes a few seconds)</span></div>`;
  if (P.err) html += `<div class="card" style="margin-top:14px"><span class="lose">${esc(P.err)}</span></div>`;
  if (P.res) html += preResult(P.res, bs[P.build]);
  root.innerHTML = html;
  $$('[data-pb]', root).forEach(b => b.onclick = () => { const nx = P.res && P.res.nexus; setBuild(+b.dataset.pb).then(() => { if (nx) preOpen(nx); }); });
  const q = $('#pre-q', root);
  q.oninput = () => {
    P.q = q.value; clearTimeout(P.qt);
    P.qt = setTimeout(async () => {
      if (P.q.trim().length < 2) { P.hits = null; } else {
        try { P.hits = (await api('precheck/search', { q: P.q })).mods || []; } catch (e) { P.hits = null; P.err = e.message; }
      }
      const h = $('#pre-hits'); if (h) { h.innerHTML = preHits(P); bindPreHits(root); }
    }, 250);
  };
  bindPreHits(root);
  $$('[data-nx]', root).forEach(a => a.onclick = (e) => { e.stopPropagation(); api('open', { what: 'nexus', id: a.dataset.nx }).catch(err => toast(err.message, true)); });
  $$('[data-pre]', root).forEach(a => a.onclick = () => preOpen(+a.dataset.pre));
}
function preHits(P) {
  if (!P.hits) return '<div class="dimmer small">Type at least two letters.</div>';
  if (!P.hits.length) return '<div class="dimmer small">No mod with that name in Frame Lab\'s data.</div>';
  return `<div class="pre-hits">${P.hits.map(h => `<button class="btn2 small ${P.res && P.res.nexus === h[0] ? 'on' : ''}" data-pre="${h[0]}">${esc(h[1])} <span class="dimmer">${h[0]}</span></button>`).join('')}</div>`;
}
function bindPreHits(root) { $$('#pre-hits [data-pre]', root).forEach(a => a.onclick = () => preOpen(+a.dataset.pre)); }
async function preOpen(nexus) {
  const P = S.pre;
  P.err = null; P.res = null; P.busy = true;
  if (S.tab === 'precheck') renderPrecheck();
  try { P.res = await api('precheck', { build: P.build, nexus }); } catch (e) { P.err = e.message; }
  P.busy = false;
  if (S.tab === 'precheck') renderPrecheck();
}
function preResult(r, b) {
  const nx = (id, name) => `<span class="op-nx" data-nx="${id}">${esc(name || 'Nexus ' + id)}</span>`;
  const A = (r.archives || []), clash = A.filter(a => (a.clashes || []).length);
  const nClash = clash.reduce((n, a) => n + a.clashes.length, 0);
  const T = r.tweaks || [], R = r.replaces || [], N = r.needs || [], H = r.hot_wraps || [];
  const L = (r.lua || []).filter(f => f.game && !f.gated), S2 = r.save || {};
  const chips = [];
  if (r.installed) chips.push('<span class="badge">already in this build</span>');
  if (N.length) chips.push(`<span class="badge lose">needs ${N.length} mod${N.length === 1 ? '' : 's'} you don't have</span>`);
  const nMods = new Set(clash.flatMap(a => a.clashes.map(c => c.archive))).size;
  if (nClash) chips.push(`<span class="badge warn">same game files as ${nMods} of your mods</span>`);
  if (R.length) chips.push(`<span class="badge lose">${R.length} script clash${R.length === 1 ? '' : 'es'}</span>`);
  if (T.length) chips.push(`<span class="badge warn">${T.length} TweakXL value${T.length === 1 ? '' : 's'} set differently</span>`);
  if (L.length || H.length) chips.push('<span class="badge">runs code often</span>');
  if (!chips.length || (chips.length === 1 && r.installed)) chips.push('<span class="badge win">nothing found that clashes with this build</span>');
  let h = `<div class="card" style="margin-top:14px"><h2 style="margin-bottom:4px">${esc(r.name)} ${nx(r.nexus, 'Nexus ' + r.nexus)}</h2>
    <div class="dimmer small" style="margin-bottom:10px">${r.downloads ? fmt.n(r.downloads) + ' downloads when Frame Lab\'s data was made · ' : ''}checked against ${esc((b || {}).name || 'this build')}</div>
    <div class="pre-chips">${chips.join(' ')}</div></div>`;
  const sec = (title, sub, body) => `<div class="card" style="margin-top:14px"><h2>${title}${sub ? ` <span class="dim">${sub}</span>` : ''}</h2>${body}</div>`;
  if (N.length) h += sec('Needs first', 'requirements this build doesn\'t have', `<ul class="op-list">${N.map(n => `<li>${nx(n[0], n[1] || 'Nexus ' + n[0])}</li>`).join('')}</ul>`);
  if (A.length) {
    // one row per mod of yours, over all of this mod's archives (a collection can have a hundred)
    const by = {};
    clash.forEach(a => a.clashes.forEach(c => {
      const g = by[c.archive] = by[c.archive] || { label: c.label, archive: c.archive, files: 0, n: 0, wins: 0 };
      g.files += c.files; g.n++; if (c.new_wins) g.wins++;
    }));
    const rows = Object.values(by).sort((x, y) => y.files - x.files);
    const after = (g) => g.wins === g.n ? '<span class="warnt">this mod\'s files are used</span>' : !g.wins ? '<span class="dim">your mod\'s files stay</span>' : `<span class="warnt">mixed</span> <span class="dimmer small">this mod's files win in ${g.wins} of its ${g.n} archives</span>`;
    const total = A.reduce((n, a) => n + a.files, 0);
    h += sec('Game files', `${A.length} archive${A.length === 1 ? '' : 's'}, ${fmt.n(total)} files`,
      (rows.length ? `<table><tr><th>your mod with the same files</th><th>files</th><th>after installing</th></tr>${rows.map(g => `<tr><td>${esc(g.label)}<div class="mono dimmer small">${esc(g.archive)}</div></td><td class="mono">${fmt.n(g.files)}${A.length > 1 ? `<div class="dimmer small" style="font-family:var(--sans)">in ${g.n} of its archives</div>` : ''}</td><td>${after(g)}</td></tr>`).join('')}</table>` : '') +
      `<div class="dim small" style="margin:8px 0">${!rows.length ? (A.length === 1 ? 'Its archive shares' : 'Its archives share') + ' no game files with your mods.' : clash.length === A.length ? '' : `${A.length - clash.length} of its ${A.length} archives share no files with your mods.`}</div>` +
      (A.length > 1 ? `<details class="cf"><summary><span class="chev">›</span>Its archives</summary><div class="mono small" style="margin:6px 0;line-height:1.7">${A.map(a => `${esc(a.name)} <span class="dimmer">${fmt.n(a.files)}${(a.clashes || []).length ? ' · clashes with ' + a.clashes.map(c => esc(c.label)).join(', ') : ''}</span>`).join('<br>')}</div></details>` : '') +
      (nClash ? '<div class="dimmer small">The archive that loads first wins each file. Patches and add-ons for a mod clash on purpose; between two mods that do the same thing, pick one. The order can be changed later in Conflicts.</div>' : ''));
  }
  if (R.length) h += sec('Script clashes', 'only one replacement of a game function runs', `<table><tr><th>game function</th><th>replaced already by</th></tr>${R.map(x => `<tr><td class="mono">${esc(x.target)}</td><td class="mono dim small">${x.installed.map(esc).join('<br>')}</td></tr>`).join('')}</table>`);
  if (T.length) h += sec('TweakXL values', 'your mods set these differently', `<table><tr><th>value</th><th>this mod</th><th>your build</th></tr>${T.map(t => `<tr><td class="mono">${esc(t.key)}</td><td class="mono">${esc(t.value)}</td><td>${t.installed.map(v => `<span class="mono">${esc(v.value)}</span> <span class="dimmer small">${esc(v.mod || v.file)}</span>`).join('<br>')}</td></tr>`).join('')}</table><div class="dimmer small" style="margin-top:6px">The file TweakXL reads last wins. If both mods are wanted, Conflicts (2.5E) can pick the value.</div>`);
  if (L.length || H.length) h += sec('Runs often', 'likely costs, before you measure', (L.length ? `<div>CET onUpdate handlers that call the game every frame: ${L.map(f => `<span class="mono">${esc(f.file.split('/').slice(-2).join('/'))}</span> <span class="dimmer small">(${f.lines} lines)</span>`).join(', ')}</div>` : '') +
    (H.length ? `<div style="margin-top:6px">Game functions it hooks that run very often: <span class="mono">${H.slice(0, 8).map(esc).join(', ')}${H.length > 8 ? ', …' : ''}</span></div>` : '') +
    '<div class="dimmer small" style="margin-top:6px">Often cheap. If frames get worse after installing, this is where to look first: record before and after.</div>');
  const sv = Object.entries(S2).filter(([, n]) => n > 0);
  if (sv.length) {
    const words = [...new Set(sv.map(([k]) => SAVE_WORD[k] || k))];
    h += sec('Changes your save', '', `<div>Its scripts ${esc(words.join(', '))} (${sv.map(([k, n]) => `${esc(k)} ×${n}`).join(', ')}). That is normal for many mods; it means removing the mod later doesn't undo those changes. Keep a save from before you install it.</div>`);
  }
  if ((r.recipes || []).length) h += sec('Known fix', '', `<div>Frame Lab has a ready-made fix for this mod${EX() ? ' (Optimise, Script fixes)' : ' (in 2.5E)'}: ${r.recipes.map(esc).join('; ')}</div>`);
  (r.notes || []).forEach(n => { h += `<div class="card" style="margin-top:14px"><span class="dim">${esc(n)}</span></div>`; });
  return h;
}
