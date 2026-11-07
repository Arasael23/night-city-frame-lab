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
  disk: { label: 'disk reads', color: '--c-disk' },
  ram: { label: 'system RAM full', color: '--c-ram' },
  driver: { label: 'driver (interrupts / DPCs)', color: '--c-driver' },
  paging: { label: 'game memory read back from disk', color: '--c-paging' },
  reset: { label: 'screen reset', color: '--c-reset' },
  unexplained: { label: 'not explained', color: '--c-unexplained' },
  other: { label: 'GPU memory', color: '--c-unexplained' },
};
const gcol = (g) => css((GROUPS[g] || GROUPS.unexplained).color);

const S = { state: null, sessions: [], cur: null, curId: null, spikeSel: null, tab: 'capture', charts: [], modSort: 'score', modFilter: '', cmpA: '', cmpB: '', cmpData: {}, settingsDirty: false };

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

function confirmBox(html, yes) {
  $('#modal-text').innerHTML = html;
  $('#modal').classList.remove('hidden');
  $('#modal-yes').onclick = () => { $('#modal').classList.add('hidden'); yes(); };
  $('#modal-no').onclick = () => $('#modal').classList.add('hidden');
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

/* ------------------------------------------------------------------ tabs */
function showTab(t) {
  S.tab = t;
  $$('#tabs button').forEach(b => b.classList.toggle('on', b.dataset.tab === t));
  $$('.tab').forEach(s => s.classList.toggle('on', s.id === 'tab-' + t));
  if (t === 'sessions') loadSessions();
  if (t === 'compare') renderCompare();
  if (t === 'mods') renderMods();
  if (t === 'conflicts') renderConflicts();
  if (t === 'checks') renderChecks();
  if (t === 'experiments') renderExperiments();
  if (t === 'vram') renderVram();
  if (t === 'settings') renderSettings(true);
  if (t === 'capture') renderCapture();
}
$$('#tabs button').forEach(b => b.onclick = () => showTab(b.dataset.tab));

/* ------------------------------------------------------------------ polling */
async function poll() {
  try {
    S.state = await api('state');
    renderTop();
    if (S.tab === 'capture') renderCapture();
    if (S.tab === 'settings') renderSettings(false);
    const c = S.state.capture;
    if (c.state === 'done' && S.lastDone !== c.session_id) {
      S.lastDone = c.session_id;
      S.sessionsLoaded = false;
    }
  } catch (e) {
    $('#topstatus').innerHTML = '<span class="pill warn">' + esc(e.message) + '</span>';
  }
  setTimeout(poll, 1000);
}

function renderTop() {
  const st = S.state;
  const parts = [];
  const c = st.capture;
  if (c.state === 'recording') parts.push('<span class="pill rec">● REC ' + fmt.dur(c.recording_s) + '</span>' + (c.focus ? '<span class="pill warn">F11 mark ' + fmt.dur(c.focus_s) + '</span>' : ''));
  if (st.game.running) {
    const b = st.config.builds[st.game.build];
    parts.push('<span class="pill ok">Game running' + (b ? ' · ' + esc(b.name) : '') + '</span>');
  } else parts.push('<span class="pill">Game not running</span>');
  parts.push(st.admin ? '<span class="pill ok">admin</span>' : '<span class="pill warn">not admin</span>');
  parts.push('<span class="dimmer">' + esc(st.gpu || '') + ' · v' + esc(st.version) + '</span>');
  $('#topstatus').innerHTML = parts.join('');
  const ban = $('#banner');
  if (!st.admin) {
    ban.className = 'banner';
    ban.innerHTML = 'Frame Lab is not running as administrator, so PresentMon can\'t record frame and GPU timing. Close it and start it again (it asks for admin on start).';
  } else ban.className = 'banner hidden';
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
  add(comp.markers && comp.markers.found ? (b && b.cet ? 'g' : 'y') : 'r', 'Loading screens', 'in game',
    comp.markers && comp.markers.found ? 'A small script marks loading screens and the main menu (through CET\'s log), so they are left out of the stutter list. Session only.' + (b && b.exists && !b.cet ? ' Needs CET, not found in this build.' : '') : 'components\\FrameLabMarkers is missing: loading screens will count as play.');
  const scripts = (name, ok, text) => {
    if (!st.config.use_script_profilers) return add('', name, 'in game', 'Off in Settings.');
    if (!comp.hitch_pkg.found) return add('r', name, 'in game', 'Needs HitchRecorder (missing).');
    add(ok ? 'g' : 'y', name, 'in game', text);
  };
  scripts('REDscript per mod', true, 'HitchRecorder times every script function the game runs and charges it to the mod whose .reds file it came from. Only in the parts you mark with F11.');
  scripts('CET Lua per mod', b && b.cet, b && b.cet ? 'HitchRecorder times every CET callback (onUpdate, onDraw, Observe, Override, hotkeys) per mod. Only in the parts you mark with F11.' : 'No CET in this build.');
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
      (miss.length ? ' Not given by this card: ' + esc(miss.join(', ')) + '.' : '') + (gi.vendor && gi.vendor !== 'nvidia' ? ' AMD and Intel cards are new in 1.1 (beta).' : '') + (gi.note ? ' ' + esc(gi.note) : ''));
  }
  add('g', 'Other programs on the card', 'outside', 'Every 2 s: how much graphics memory each other program holds on the game\'s card (browsers, Discord, OBS ...). The VRAM tab shows it live and can move programs to the integrated graphics.');
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
  recording: ['Recording', 'Play normally. F11 marks a spot that stutters (F11 again ends the mark). Quit the game to finish.'],
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
          <input id="cap-title" style="width:100%" placeholder="e.g. Dogtown drive, PT on, DLSS Q + FG" maxlength="80">
          <div id="cap-state" style="margin-top:12px"></div>
          <div id="cap-buttons" style="display:flex;gap:10px;margin-top:12px;align-items:center"></div>
          <ol class="howto" id="cap-howto">
            <li>Start session (game closed): Frame Lab puts its recorders into the game.</li><li>Start the game and play. Everything records by itself.</li>
            <li>Optional: F11 at a spot that stutters marks it, F11 again ends the mark. Script time per mod (REDscript, CET) is measured only inside marks.</li><li>Quit the game.</li>
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
  const active = st.builds[cfg.active];
  $('#cap-builds').innerHTML = !st.builds.length ? '<div class="dim">No Cyberpunk 2077 install found yet. Settings → <b>Find game installs</b>, or <b>Add a build</b> with the game folder.</div>' : st.builds.map(b => `<div class="build ${b.index === cfg.active ? 'on' : ''}" data-build="${b.index}">
      <div class="name">${esc(b.name)}</div><small>${esc(b.path)}</small><div class="tags">${buildTags(b)}</div></div>`).join('');
  $('#cap-layers').innerHTML = layerRows(st, active);

  const c = st.capture;
  const [big, sub] = STATE_TEXT[c.state] || [c.state, ''];
  $('#cap-state').innerHTML = `<div class="bigstate">${esc(big)}${c.title ? ' <span class="dim" style="font-size:13px;font-weight:400">· ' + esc(c.title) + '</span>' : ''}</div>
      <div class="dim">${esc(c.error || sub)}</div>`;
  const busy = ['arming', 'finishing'].includes(c.state);
  const armed = ['waiting', 'running', 'recording'].includes(c.state);
  let btn = '';
  if (c.state === 'done') btn = `<button class="btn" data-act="open-report">Open the report</button><button class="btn2" data-act="reset">New session</button>`;
  else if (armed) btn = `<button class="btn2" data-act="disarm" ${st.game.running ? 'disabled title="Quit the game: the session finishes and cleans up by itself"' : ''}>Cancel and remove</button>`;
  else if (c.state === 'error') btn = `<button class="btn2" data-act="disarm">Try removing again</button><button class="btn2" data-act="reset">Clear</button>`;
  else btn = `<button class="btn" data-act="arm" ${busy || st.game.running || !active || !active.exists ? 'disabled' : ''}>● Start session</button>
      ${st.game.running ? '<span class="dim small">Close the game to start a session.</span>' : '<span class="dim small">F11 in game = mark a spot (optional)</span>'}`;
  const bEl = $('#cap-buttons');
  if (bEl.dataset.html !== btn) { bEl.innerHTML = btn; bEl.dataset.html = btn; }
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
      (h.capture_mode === 2 ? k('F11 mark', h.capturing ? 'on' : 'off', 'recording the whole run', h.capturing ? 'warn' : 'good') : k('recording', h.capturing ? 'yes' : 'no', h.capture_mode === 1 ? 'F11 toggles' : 'always on', h.capturing ? 'good' : '')) +
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
      const title = $('#cap-title').value.trim() || 'Capture';
      await api('arm', { title, build: st.config.active });
    } else if (act === 'disarm') {
      confirmBox('Take the recorders out of the game and cancel this session?', async () => {
        try { await api('disarm', {}); } catch (x) { toast(x.message, true); }
      });
    } else if (act === 'reset') await api('reset', {});
    else if (act === 'open-report') { S.curId = st.capture.session_id; S.cur = null; showTab('sessions'); }
    else if (act === 'remove-leftover') {
      const b = st.builds[st.config.active];
      confirmBox(`Take HitchRecorder and the loading-screen markers out of <b>${esc(b.name)}</b>?<br><span class="dim">${esc(b.path)}\\red4ext\\plugins\\HitchRecorder and r6\\scripts\\NightCityFrameLab. The game must be closed.</span>`, async () => {
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
  if (!S.curId && S.sessions.length) S.curId = S.sessions[0].id;
  renderSessionList();
  if (S.curId && (!S.cur || S.cur.meta.id !== S.curId)) openSession(S.curId);
  else if (!S.sessions.length) $('#report').innerHTML = '<div class="empty">No sessions yet. Start one on the Capture tab.</div>';
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
    $('#report').innerHTML = '<div class="empty">This session could not be analysed: ' + esc(e.message) + '</div>';
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
function destroyCharts() { S.charts.forEach(c => c.destroy()); S.charts = []; }

function kpiBox(l, v, s, cls) { return `<div class="k ${cls || ''}"><div class="l">${l}</div><div class="v">${v}</div>${s ? '<div class="s">' + s + '</div>' : ''}</div>`; }

function renderReport(el, r, reportOnly) {
  destroyCharts();
  const k = r.kpi, m = r.meta;
  const lowCls = k.low1_fps && k.low1_fps < 40 ? 'bad' : k.low1_fps < 60 ? 'warn' : 'good';
  const vramCls = k.vram_budget_mb && k.vram_peak_mb > k.vram_budget_mb ? 'bad' : '';
  const seen = new Set((r.spikes || []).map(s => s.group));
  const legend = Object.entries(GROUPS).filter(([g]) => g !== 'other' && seen.has(g)).map(([g, v]) => `<span><i style="background:${gcol(g)}"></i>spike: ${v.label}</span>`).join('');
  const hasGpu = ((r.timeline || {}).gpu_ms || []).some(v => v >= 0);
  el.innerHTML = `
    <div class="rep-head"><div class="title">${esc(m.title || m.id)}</div>
      <div class="dim">${esc(m.build)} · ${new Date(m.created).toLocaleString('en-AU')} · ${k.not_playing_s ? fmt.dur(k.duration_s) + ' of play (' + fmt.dur(k.session_s) + ' recorded, ' + fmt.dur(k.not_playing_s) + ' loading / menu left out)' : fmt.dur(k.duration_s) + ' recorded'}${m.gpu ? ' · ' + esc(m.gpu) : ''}</div>
      <div class="dim small">${(m.experiments || []).length ? 'Experiments on: ' + m.experiments.map(esc).join(', ') : (m.created > '2026-10-31T10' ? 'No Experiments settings on' : '')}</div>
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
    <div class="card chart-card"><div class="chart-top"><div class="legend"><span><i style="background:${css('--accent')}"></i>frame time</span>${hasGpu ? `<span><i style="background:${css('--orange')}"></i>GPU busy</span>` : '<span class="dimmer" title="PresentMon could not tell GPU time apart with frame generation on">GPU busy: not recorded (frame generation)</span>'}${legend}${(r.timeline.not_playing || []).length ? '<span><i style="background:rgba(150,150,145,0.45)"></i>loading / menu (left out)</span>' : ''}${(r.timeline.focus || []).length ? '<span><i style="background:rgba(47,196,185,0.35)"></i>F11 marked</span>' : ''}${(r.timeline.resets || []).some(x => x.playing) ? `<span><i style="background:${css('--c-reset')}"></i>screen reset</span>` : ''}</div><button class="btn2 small" id="ch-all" style="display:none">Show all</button></div>
      <div class="readout" id="ch-read">Drag across any chart to zoom in · double-click or <b>Show all</b> to zoom out · click a spike bar for its details · move the mouse over a chart to read the values here</div><div id="ch-main"></div></div>
    <div class="grid3" style="grid-template-columns:1fr 1fr 1fr"><div class="card chart-card"><div class="legend"><span><i style="background:#8cc4ff"></i>VRAM used by the game</span><span><i style="background:${css('--red')}"></i>budget</span>${(r.timeline.vram_others_mb || []).length ? '<span><i style="background:#f0a6ff"></i>everything else on the card (right axis)</span>' : ''}</div><div id="ch-vram"></div></div>
      <div class="card chart-card"><div class="legend">${(r.timeline.clock_mhz || []).length ? '<span><i style="background:#c3a6ff"></i>GPU clock (MHz)</span>' : ''}${(r.timeline.temp_c || []).length ? '<span><i style="background:#ff9a6b"></i>GPU temp (°C)</span>' : ''}${!(r.timeline.clock_mhz || []).length || !(r.timeline.temp_c || []).length ? '<span class="dimmer">' + [(r.timeline.clock_mhz || []).length ? '' : 'clock', (r.timeline.temp_c || []).length ? '' : 'temperature'].filter(Boolean).join(' and ') + ': not given by this card</span>' : ''}</div><div id="ch-gpu"></div></div>
      <div class="card chart-card"><div class="legend"><span><i style="background:${css('--amber')}"></i>disk reads (MB/s)</span><span><i style="background:#7ee0c8"></i>game CPU (%)</span><span><i style="background:#ff9aa8"></i>other apps on GPU (%)</span></div><div id="ch-pc"></div></div></div>
    <div class="grid3" style="grid-template-columns:1fr 1fr 1fr"><div class="card chart-card"><div class="legend"><span><i style="background:#9fe870"></i>CPU clock (MHz)</span><span><i style="background:#ffb36b"></i>cores at 90%+</span></div><div id="ch-cpu"></div></div>
      <div class="card chart-card"><div class="legend"><span><i style="background:#ff6b8a"></i>driver interrupts / DPC (% of CPU)</span><span><i style="background:#b0a0ff"></i>game GPU memory in system RAM (MB)</span>${(r.timeline.ram_free_mb || []).length ? '<span><i style="background:#9adf8f"></i>free system RAM (MB)</span>' : ''}</div><div id="ch-win"></div></div>
      <div class="card chart-card"><div class="legend"><span><i style="background:#6fd3ff"></i>frames shown per second</span>${r.pacing && r.pacing.refresh_hz ? '<span><i style="background:#ff8a9a"></i>held for 2+ refreshes (%)</span>' : ''}</div><div id="ch-disp"></div></div></div>
    <div class="grid3">
      <div class="card"><h2>What caused the spikes</h2><div id="r-causes"></div></div>
      <div class="card"><h2>Spikes <span class="dimmer" style="text-transform:none;letter-spacing:0">(click one)</span></h2><div class="scroll" id="r-spikes"></div></div>
      <div class="card" id="r-detail"></div>
    </div>
    <div class="grid2">
      <div class="card"><h2>Cost by mod</h2><div class="scroll" id="r-mods"></div></div>
      <div class="card"><h2>Engine work recorded</h2><div class="scroll" id="r-engine"></div><h2 style="margin-top:14px">Shaders / pipelines</h2><div id="r-shaders"></div><h2 style="margin-top:14px">Windows (drivers, paging, disk)</h2><div id="r-win"></div><h2 style="margin-top:14px">Other programs on the graphics card</h2><div id="r-gpuapps"></div></div>
    </div>
    <div class="grid2"><div class="card"><h2>Layers in this session</h2><div id="r-layers"></div></div><div class="card"><h2>Notes</h2><div id="r-warn"></div></div></div>`;

  // causes
  const cs = r.causes || [];
  const tot = cs.reduce((a, c) => a + c.count, 0) || 1;
  $('#r-causes', el).innerHTML = cs.length ? `<div class="bar" style="margin-bottom:10px">${cs.map(c => `<div style="width:${c.count / tot * 100}%;background:${gcol(c.group)}"></div>`).join('')}</div>
    <table>${cs.map(c => `<tr><td><span class="swatch" style="background:${gcol(c.group)}"></span>${esc(c.cause)}</td><td class="n">${c.count}</td><td class="n">${c.pct.toFixed(0)}%</td><td class="n dim" style="white-space:nowrap">${(c.total_ms / 1000).toFixed(1)} s</td></tr>`).join('')}</table>` :
    '<div class="dim">No spikes over ' + k.spike_ms + ' ms. Smooth run.</div>';

  // spikes list
  const sp = r.spikes || [];
  $('#r-spikes', el).innerHTML = sp.length ? `<table><tr><th>#</th><th>time</th><th class="n">frame</th><th>cause</th><th></th></tr>${sp.map(s => `<tr class="click" data-spike="${s.id}">
      <td class="dim">${s.id}</td><td>${fmt.t(s.t)}</td><td class="n">${fmt.ms(s.ms)}</td><td><span class="swatch" style="background:${gcol(s.group)}"></span>${esc(s.cause)}</td><td>${s.focus ? '<span class="tag y" title="inside a part you marked with F11">F11</span>' : ''}${s.near_mark ? '<span title="you pressed the marker key near this">★</span>' : ''}</td></tr>`).join('')}</table>` : '<div class="dim">None.</div>';
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

  // charts
  const tok = S.repTok = (S.repTok || 0) + 1; // only the latest render draws (two quick renders drew every chart twice)
  setTimeout(() => { if (tok === S.repTok) { destroyCharts(); drawCharts(el, r); } }, 0);
}

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
  if (s.disk_mbs) facts.push('disk ' + s.disk_mbs.toFixed(0) + ' MB/s');
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
    <div><span class="swatch" style="background:${gcol(s.group)}"></span><b style="color:var(--white)">${esc(s.cause)}</b>${s.focus ? ' <span class="tag y">in your F11 mark</span>' : ''}${s.near_mark ? ' <span class="tag y">★ near your marker</span>' : ''}</div>
    <div class="verdict">${esc(s.verdict)}</div>
    <table style="margin-top:8px">${parts}</table>
    ${items ? '<h2 style="margin-top:10px">Biggest single items</h2><table>' + items + '</table>' : ''}
    ${(s.win || []).length ? '<h2 style="margin-top:10px">Windows at that moment</h2><table>' + s.win.map(w => `<tr><td class="dim">${w.kind === 'driver' ? 'driver' : w.kind === 'paging' ? 'read back from disk' : 'disk'}</td><td>${esc(w.name)}${w.what ? ' <span class="dimmer">' + esc(w.what) + '</span>' : ''}</td><td class="n">${w.kind === 'driver' ? fmt.ms(w.ms) + ' (longest ' + fmt.ms(w.max_ms) + ')' : w.kind === 'paging' ? w.count + ' faults · ' + (w.mb || 0).toFixed(1) + ' MB · ' + fmt.ms(w.ms) : (w.mb || 0).toFixed(1) + ' MB'}</td></tr>`).join('') + '</table>' : ''}
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
  return `<div class="card adv"><h2>What to try <span class="dimmer small">worked out from this session's numbers</span></h2>
    ${list.map(a => `<div class="adv-row ${a.level}"><span class="adv-tag ${a.level}">${ADV_TAG[a.level] ? ADV_TAG[a.level][1] : esc(a.level)}</span>
      <div class="adv-body"><div class="adv-title">${esc(a.title)}</div>${a.detail ? `<div class="adv-detail">${esc(a.detail)}</div>` : ''}
      ${(a.items || []).length ? '<ul>' + a.items.map(i => '<li>' + esc(i) + '</li>').join('') + '</ul>' : ''}</div></div>`).join('')}</div>`;
}

function renderModTable(el, mods, limit) {
  const rows = mods.slice(0, limit || 9999);
  el.innerHTML = rows.length ? `<table><tr><th>mod (library zip)</th><th>kind</th><th class="n">REDscript ms/s</th><th class="n">CET ms/s</th><th class="n">files loaded</th><th class="n">in spikes</th></tr>
    ${rows.map(m => `<tr><td>${esc(m.name)}${m.note ? '<div class="dimmer small">' + esc(m.note) + '</div>' : ''}</td><td class="dim">${esc((m.kinds || []).join(', '))}</td>
      <td class="n">${m.redscript_ms_per_s ? m.redscript_ms_per_s.toFixed(2) : '–'}</td><td class="n">${m.cet_ms_per_s ? m.cet_ms_per_s.toFixed(2) : '–'}</td>
      <td class="n">${m.files_loaded ? fmt.n(m.files_loaded) : '–'}</td><td class="n">${m.in_spikes || '–'}</td></tr>`).join('')}</table>` :
    '<div class="dim">No per-mod data in this session (no F11 part marked or script time off, no mod files loaded).</div>';
}

function drawCharts(el, r) {
  const tl = r.timeline || {};
  const W = Math.max(400, $('#ch-main', el).clientWidth - 4);
  const axisStroke = css('--dim'), grid = { stroke: '#2a2c2f', width: 1 }, axFont = '12px Inter, "Segoe UI", Arial, sans-serif';
  // time labels: whole seconds normally, tenths once zoomed in to under half a minute
  const tvals = (u, v) => { const span = u.scales.x.max - u.scales.x.min; return v.map(x => span < 30 ? fmt.t(x) : fmt.t(x).replace(/\.\d$/, '')); };
  const axes = (yl) => [{ stroke: axisStroke, font: axFont, labelFont: axFont, grid, ticks: grid, values: tvals }, { stroke: axisStroke, font: axFont, labelFont: axFont, grid, ticks: grid, label: yl, size: 50 }];
  const sync = uPlot.sync('fl');
  // a drag has to be at least 10 px to zoom, so a click (which often moves a pixel) never zooms in by accident
  const tFull = [Math.min(...[tl.t, tl.vram_t, tl.samp_t].filter(a => a && a.length).map(a => a[0])), Math.max(...[tl.t, tl.vram_t, tl.samp_t].filter(a => a && a.length).map(a => a[a.length - 1]))];
  const at = (ts, vs, t) => { if (!ts || !ts.length || !vs) return null; let lo = 0, hi = ts.length - 1; while (lo < hi) { const m = (lo + hi) >> 1; if (ts[m] < t) lo = m + 1; else hi = m; } if (lo > 0 && Math.abs(ts[lo - 1] - t) < Math.abs(ts[lo] - t)) lo--; return Math.abs(ts[lo] - t) > 2 ? null : vs[lo]; };
  const readout = (t) => {
    const box = $('#ch-read', el);
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
    if (inList(tl.focus)) parts.push('<span style="color:var(--accent)">F11 mark</span>');
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
      c.strokeStyle = m.kind === 'mark' ? css('--accent') : (m.kind === 'felt' ? '#e8a24a' : '#4a4c50');
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
    const u = new uPlot({
      width: W, height: 300, cursor, scales: { x: { time: false }, y: { range: (u, lo, hi) => [0, Math.min(Math.max(40, (hi || 0) * 1.12), yMax)] } }, axes: axes('ms'),
      series: [{}, { label: 'frame', stroke: css('--accent'), width: 1.2, points: { show: false } },
        { label: 'GPU', stroke: css('--orange'), width: 1, points: { show: false } }],
      hooks: {
        drawClear: [shadeGaps],
        setCursor: hooksAll.setCursor,
        setScale: hooksAll.setScale,
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
    }, [tl.t, fr, gpu], $('#ch-main', el));
    u.over.addEventListener('click', () => {
      const t = u.posToVal(u.cursor.left, 'x');
      let best = null, bd = 1e9;
      spikes.forEach(s => { const d = Math.abs(s.t - t); if (d < bd) { bd = d; best = s; } });
      const span = u.scales.x.max - u.scales.x.min;
      if (best && bd < span / 50) selectSpike(el, r, best.id);
    });
    sync.sub(u);
    S.charts.push(u);
    S.mainChart = u;
  } else $('#ch-main', el).innerHTML = '<div class="empty">No frame data.</div>';
  const small = (id, data, series, scales, axesDef) => {
    const host = $(id, el);
    if (!data[0] || !data[0].length) { host.innerHTML = '<div class="empty" style="padding:20px">Not recorded.</div>'; return; }
    const w = Math.max(250, host.clientWidth - 4);
    const u = new uPlot({ width: w, height: 150, cursor, scales: Object.assign({ x: { time: false } }, scales), axes: axesDef, series: [{}].concat(series), hooks: { drawClear: [shadeGaps], setCursor: hooksAll.setCursor, setScale: hooksAll.setScale } }, data, host);
    sync.sub(u);
    S.charts.push(u);
  };
  const ax2 = (l1, l2) => [{ stroke: axisStroke, font: axFont, labelFont: axFont, grid, ticks: grid, values: tvals },
    { stroke: axisStroke, font: axFont, labelFont: axFont, grid, ticks: grid, label: l1, size: 50, scale: 'a' }, { side: 1, stroke: axisStroke, font: axFont, labelFont: axFont, grid: { show: false }, label: l2, size: 54, scale: 'b' }];
  const vo = (tl.vram_others_mb || []).length === (tl.vram_t || []).length && (tl.vram_t || []).length > 0;
  const gAx = (u, v) => v.map(x => (x / 1024).toFixed(1) + 'G');
  small('#ch-vram', vo ? [tl.vram_t, tl.vram_mb || [], tl.budget_mb || [], tl.vram_others_mb] : [tl.vram_t || [], tl.vram_mb || [], tl.budget_mb || []],
    [{ stroke: '#8cc4ff', width: 1.3, scale: 'y', points: { show: false } }, { stroke: css('--red'), width: 1, dash: [5, 4], scale: 'y', points: { show: false } }]
      .concat(vo ? [{ stroke: '#f0a6ff', width: 1, scale: 'o', points: { show: false } }] : []),
    { y: { range: (u, a, b) => [Math.max(0, Math.min(a, b) * 0.9), Math.max(a, b) * 1.04] }, o: { range: (u, a, b) => [0, Math.max(1024, b * 1.25)] } },
    [{ stroke: axisStroke, font: axFont, labelFont: axFont, grid, ticks: grid, values: tvals }, { stroke: axisStroke, font: axFont, labelFont: axFont, grid, ticks: grid, size: 50, values: gAx }]
      .concat(vo ? [{ side: 1, stroke: axisStroke, font: axFont, labelFont: axFont, grid: { show: false }, size: 45, scale: 'o', values: gAx }] : []));
  {
    const hc = (tl.clock_mhz || []).length > 0, ht = (tl.temp_c || []).length > 0;
    const clockS = { stroke: '#c3a6ff', width: 1, scale: 'a', points: { show: false } }, tempS = { stroke: '#ff9a6b', width: 1, scale: 'b', points: { show: false } };
    const sc = { a: { range: (u, a, b) => [Math.max(0, a * 0.9), b * 1.05] }, b: { range: (u, a, b) => [Math.max(0, a - 5), b + 5] } };
    const one = (scale, label) => [{ stroke: axisStroke, font: axFont, labelFont: axFont, grid, ticks: grid, values: tvals }, { stroke: axisStroke, font: axFont, labelFont: axFont, grid, ticks: grid, label, size: 50, scale }];
    if (hc && ht) small('#ch-gpu', [tl.samp_t, tl.clock_mhz, tl.temp_c], [clockS, tempS], sc, ax2('MHz', '°C'));
    else if (hc) small('#ch-gpu', [tl.samp_t, tl.clock_mhz], [clockS], sc, one('a', 'MHz'));
    else if (ht) small('#ch-gpu', [tl.samp_t, tl.temp_c], [tempS], sc, one('b', '°C'));
    else { const h = $('#ch-gpu', el); if (h) h.innerHTML = '<div class="empty" style="padding:20px">This card\'s driver gives no clock or temperature to Frame Lab.</div>'; }
  }
  small('#ch-pc', [tl.samp_t || [], tl.disk_mbs || [], tl.cpu_pct || [], tl.other_gpu_pct || []],
    [{ stroke: css('--amber'), width: 1, scale: 'a', points: { show: false } }, { stroke: '#7ee0c8', width: 1, scale: 'b', points: { show: false } },
      { stroke: '#ff9aa8', width: 1, scale: 'b', points: { show: false } }],
    { a: { range: (u, a, b) => [0, Math.max(50, b * 1.1)] }, b: { range: [0, 100] } }, ax2('MB/s', '%'));
  if ((tl.cpu_clock_mhz || []).length) {
    small('#ch-cpu', [tl.samp_t, tl.cpu_clock_mhz, tl.cores_busy],
      [{ stroke: '#9fe870', width: 1, scale: 'a', points: { show: false } }, { stroke: '#ffb36b', width: 1, scale: 'b', points: { show: false } }],
      { a: { range: (u, a, b) => [Math.max(0, a * 0.9), b * 1.05] }, b: { range: (u, a, b) => [0, Math.max(4, b + 1)] } }, ax2('MHz', 'cores'));
    const ramFree = (tl.ram_free_mb || []).length === tl.samp_t.length;
    small('#ch-win', ramFree ? [tl.samp_t, tl.dpc_pct, tl.shared_mb, tl.ram_free_mb] : [tl.samp_t, tl.dpc_pct, tl.shared_mb],
      [{ stroke: '#ff6b8a', width: 1, scale: 'a', points: { show: false } }, { stroke: '#b0a0ff', width: 1, scale: 'b', points: { show: false } }]
        .concat(ramFree ? [{ stroke: '#9adf8f', width: 1, scale: 'b', points: { show: false } }] : []),
      { a: { range: (u, a, b) => [0, Math.max(5, b * 1.2)] }, b: { range: (u, a, b) => [0, Math.max(100, b * 1.1)] } }, ax2('%', 'MB'));
  } else {
    ['#ch-cpu', '#ch-win'].forEach(id => { const h = $(id, el); if (h) h.innerHTML = '<div class="empty" style="padding:20px">Not recorded in this session.</div>'; });
  }
  if ((tl.disp_t || []).length) {
    const held = (tl.disp_held || []).length === tl.disp_t.length;
    small('#ch-disp', held ? [tl.disp_t, tl.disp_fps, tl.disp_held] : [tl.disp_t, tl.disp_fps],
      [{ stroke: '#6fd3ff', width: 1, scale: 'a', points: { show: false } }].concat(held ? [{ stroke: '#ff8a9a', width: 1, scale: 'b', points: { show: false } }] : []),
      { a: { range: (u, a, b) => [0, Math.max(60, b * 1.1)] }, b: { range: (u, a, b) => [0, Math.max(20, b * 1.1)] } }, ax2('fps', '%'));
  } else { const h = $('#ch-disp', el); if (h) h.innerHTML = '<div class="empty" style="padding:20px">Not recorded in this session (PresentMon).</div>'; }
  const showAll = () => S.charts.forEach(u => u.setScale('x', { min: tFull[0], max: tFull[1] }));
  const btn = $('#ch-all', el);
  if (btn) btn.onclick = showAll;
  S.charts.forEach(u => u.over.addEventListener('dblclick', showAll));
}

window.addEventListener('resize', () => {
  clearTimeout(window._rs);
  window._rs = setTimeout(() => {
    if (REPORT) renderReport($('#report'), REPORT, true);
    else if (S.tab === 'sessions' && S.cur) renderReport($('#report'), S.cur, false);
  }, 200);
});

/* ------------------------------------------------------------------ compare */
async function renderCompare() {
  const root = $('#tab-compare');
  if (!S.sessions.length) { try { S.sessions = (await api('sessions')) || []; } catch (e) { /* shown below */ } }
  const opts = (sel) => S.sessions.filter(s => s.analysed).map(s => `<option value="${esc(s.id)}" ${s.id === sel ? 'selected' : ''}>${esc(s.title || s.id)} · ${esc(s.created)}</option>`).join('');
  const an = S.sessions.filter(s => s.analysed);
  if (an.length < 2) { root.innerHTML = '<div class="empty">Compare needs two analysed sessions (e.g. the same drive before and after a change).</div>'; return; }
  if (!S.cmpA) S.cmpA = an[1].id;
  if (!S.cmpB) S.cmpB = an[0].id;
  root.innerHTML = `<div class="cmp-pick"><span class="dim">Before</span><select id="cmpA">${opts(S.cmpA)}</select><span class="dim">After</span><select id="cmpB">${opts(S.cmpB)}</select></div><div id="cmp-body"><div class="empty">Loading ...</div></div>`;
  $('#cmpA').onchange = (e) => { S.cmpA = e.target.value; renderCompare(); };
  $('#cmpB').onchange = (e) => { S.cmpB = e.target.value; renderCompare(); };
  const get = async (id) => S.cmpData[id] || (S.cmpData[id] = await api('session/' + encodeURIComponent(id)));
  let A, B;
  try { [A, B] = await Promise.all([get(S.cmpA), get(S.cmpB)]); } catch (e) { $('#cmp-body').innerHTML = '<div class="empty">' + esc(e.message) + '</div>'; return; }
  const row = (label, a, b, f, lowerBetter) => {
    let cls = '';
    if (a != null && b != null && isFinite(a) && isFinite(b) && a !== b) cls = (lowerBetter ? b < a : b > a) ? 'better' : 'worse';
    const d = (a != null && b != null && isFinite(a) && isFinite(b)) ? (b - a) : null;
    return `<tr><td>${label}</td><td class="n">${f(a)}</td><td class="n">${f(b)}</td><td class="n delta ${cls}">${d == null ? '' : (d > 0 ? '+' : '') + (Math.abs(d) >= 10 ? d.toFixed(0) : d.toFixed(1))}</td></tr>`;
  };
  const ka = A.kpi, kb = B.kpi;
  const perMin = (k) => k.duration_s ? k.spikes / (k.duration_s / 60) : null;
  const ea = (A.meta.experiments || []), eb = (B.meta.experiments || []);
  const added = eb.filter(x => !ea.includes(x)), removed = ea.filter(x => !eb.includes(x));
  let html = (added.length || removed.length) ? `<div class="card" style="margin-bottom:12px"><h2>Experiments changed</h2>
    ${added.map(x => '<div><span class="tag g">switched on</span> ' + esc(x) + '</div>').join('')}${removed.map(x => '<div><span class="tag">switched off</span> ' + esc(x) + '</div>').join('')}
    ${added.length + removed.length > 1 ? '<div class="dimmer small" style="margin-top:6px">More than one setting changed between these sessions, so the difference can\'t be put on one of them.</div>' : ''}</div>` : '';
  // 2.0: what changed between the two sessions (snapshot taken when each session started)
  const sa = A.snapshot, sb = B.snapshot;
  if (A.meta.build && B.meta.build && A.meta.build !== B.meta.build) html += `<div class="card" style="margin-bottom:12px"><h2>Different game builds</h2><div class="dim">Before is from ${esc(A.meta.build)}, after from ${esc(B.meta.build)}: different mods and settings, so this compares the two builds rather than one change.</div></div>`;
  if (!sa || !sb) html += `<div class="card" style="margin-bottom:12px"><h2>What changed</h2><div class="dim">Needs both sessions recorded with Frame Lab 2.0 (it saves the versions, mods and game settings when a session starts).</div></div>`;
  else {
    const ch = [];
    [['Game', 'game_version'], ['Graphics driver', 'driver'], ['Windows', 'windows']].forEach(([l, k]) => {
      if (sa[k] && sb[k] && sa[k] !== sb[k]) ch.push(`<div><span class="tag">${l}</span> ${esc(sa[k])} → ${esc(sb[k])}</div>`);
    });
    const ma = {}, mb = {};
    (sa.mods || []).forEach(m => { ma[m.path] = m; });
    (sb.mods || []).forEach(m => { mb[m.path] = m; });
    const addedM = Object.keys(mb).filter(p => !ma[p]), goneM = Object.keys(ma).filter(p => !mb[p]);
    const updM = Object.keys(mb).filter(p => ma[p] && (ma[p].size !== mb[p].size || ma[p].mtime !== mb[p].mtime));
    const lst = (arr, tag, cls) => arr.slice(0, 30).map(p => `<div><span class="tag ${cls}">${tag}</span> ${esc(p)}</div>`).join('') + (arr.length > 30 ? `<div class="dimmer small">and ${arr.length - 30} more</div>` : '');
    const setA = sa.settings || {}, setB = sb.settings || {};
    const setCh = Object.keys(setB).filter(k => k in setA && setA[k] !== setB[k]);
    const body = ch.join('') + lst(addedM, 'mod added', 'g') + lst(goneM, 'mod removed', '') + lst(updM, 'mod updated', 'y') +
      setCh.slice(0, 30).map(k => `<div><span class="tag y">setting</span> ${esc(k.replace(/^\//, ''))}: ${esc(setA[k])} → ${esc(setB[k])}</div>`).join('');
    html += `<div class="card" style="margin-bottom:12px"><h2>What changed</h2>${body || '<div class="dim">Same game, driver, Windows, mods and game settings in both sessions.</div>'}</div>`;
  }
  // 2.0: is the difference bigger than the noise? (30 s stretches of play in each session)
  const bstat = (bl, key) => { const v = (bl || []).map(b => b[key]).filter(x => isFinite(x) && x > 0); const n = v.length; if (n < 3) return null; const m = v.reduce((a, b) => a + b, 0) / n; const sd = Math.sqrt(v.reduce((a, b) => a + (b - m) * (b - m), 0) / (n - 1)); return { n, m, se: sd / Math.sqrt(n) }; };
  const verdictLine = (label, a, b) => {
    if (!a || !b) return '';
    const d = b.m - a.m, se = Math.sqrt(a.se * a.se + b.se * b.se), t = se > 0 ? d / se : 0, pct = 100 * d / a.m;
    if (Math.abs(t) < 2 || Math.abs(pct) < 2) return `<div>${label}: ${a.m.toFixed(1)} → ${b.m.toFixed(1)} ms, <b>within the normal run-to-run noise</b></div>`;
    return `<div>${label}: ${a.m.toFixed(1)} → ${b.m.toFixed(1)} ms, <b class="delta ${d < 0 ? 'better' : 'worse'}">${d < 0 ? 'better' : 'worse'} by ${Math.abs(pct).toFixed(1)}%</b>, more than the noise between stretches</div>`;
  };
  const vm = verdictLine('Typical frame (median)', bstat(A.blocks, 'median_ms'), bstat(B.blocks, 'median_ms'));
  const vp = verdictLine('Slow frames (1% low frame time)', bstat(A.blocks, 'p99_ms'), bstat(B.blocks, 'p99_ms'));
  if (vm || vp) html += `<div class="card" style="margin-bottom:12px"><h2>Verdict</h2>${vm}${vp}<div class="dimmer small" style="margin-top:6px">Each session is cut into 30 s stretches of play; a change only counts when it is bigger than how much those stretches differ from each other. Same route and settings still matter.</div></div>`;
  else html += `<div class="card" style="margin-bottom:12px"><h2>Verdict</h2><div class="dim">Needs at least 90 s of play in both sessions (recorded with Frame Lab 2.0).</div></div>`;
  html += `<div class="grid2" style="margin-top:0"><div class="card"><h2>Overall</h2><table><tr><th></th><th class="n">before</th><th class="n">after</th><th class="n">change</th></tr>
    ${row('median frame (ms)', ka.median_ms, kb.median_ms, v => fmt.p(v, 2), true)}
    ${row('average fps', ka.avg_fps, kb.avg_fps, v => fmt.p(v, 0), false)}
    ${row('1% low fps', ka.low1_fps, kb.low1_fps, v => fmt.p(v, 0), false)}
    ${row('0.1% low fps', ka.low01_fps, kb.low01_fps, v => fmt.p(v, 0), false)}
    ${row('spikes per minute', perMin(ka), perMin(kb), v => fmt.p(v, 2), true)}
    ${row('stutter %', ka.stutter_pct, kb.stutter_pct, v => fmt.p(v, 2), true)}
    ${row('frame time spread (ms)', ka.frame_sd_ms, kb.frame_sd_ms, v => fmt.p(v, 2), true)}
    ${row('input lag (ms)', ka.input_lag_ms || null, kb.input_lag_ms || null, v => fmt.p(v, 1), true)}
    ${row('GPU bound %', ka.gpu_bound_pct >= 0 ? ka.gpu_bound_pct : null, kb.gpu_bound_pct >= 0 ? kb.gpu_bound_pct : null, v => fmt.p(v, 0), true)}
    ${row('VRAM peak (MB)', ka.vram_peak_mb || null, kb.vram_peak_mb || null, v => fmt.p(v, 0), true)}
    ${row('script time (ms/s)', (ka.redscript_ms_per_s || 0) + (ka.cet_ms_per_s || 0), (kb.redscript_ms_per_s || 0) + (kb.cet_ms_per_s || 0), v => fmt.p(v, 1), true)}
    </table><div class="dimmer small" style="margin-top:8px">Only a fair comparison when both runs cover the same route and settings.</div></div>`;
  const groups = Object.keys(GROUPS).filter(g => g !== 'other');
  const cnt = (r, g) => { const c = (r.causes || []).find(x => x.group === g); return c ? c.count : 0; };
  html += `<div class="card"><h2>Spike causes</h2><table><tr><th></th><th class="n">before</th><th class="n">after</th><th class="n">change</th></tr>
    ${groups.map(g => row('<span class="swatch" style="background:' + gcol(g) + '"></span>' + GROUPS[g].label, cnt(A, g), cnt(B, g), v => String(v), true)).join('')}</table></div></div>`;
  const mods = {};
  (A.mods || []).forEach(m => { mods[m.name] = { a: m, b: null }; });
  (B.mods || []).forEach(m => { (mods[m.name] = mods[m.name] || { a: null, b: null }).b = m; });
  const sc = (m) => m ? (m.redscript_ms_per_s || 0) + (m.cet_ms_per_s || 0) : null;
  const list = Object.entries(mods).map(([n, v]) => ({ n, a: sc(v.a), b: sc(v.b), sa: v.a ? v.a.in_spikes : 0, sb: v.b ? v.b.in_spikes : 0 }))
    .filter(x => (x.a || 0) + (x.b || 0) > 0.2 || x.sa + x.sb > 0).sort((x, y) => Math.max(y.a || 0, y.b || 0) - Math.max(x.a || 0, x.b || 0)).slice(0, 40);
  html += `<div class="card" style="margin-top:14px"><h2>Mods: script time and spikes</h2><table><tr><th>mod</th><th class="n">ms/s before</th><th class="n">ms/s after</th><th class="n">change</th><th class="n">in spikes before → after</th></tr>
    ${list.map(x => { const r = row(esc(x.n), x.a, x.b, v => v == null ? 'not there' : v.toFixed(2), true); return r.replace('</tr>', `<td class="n">${x.sa} → ${x.sb}</td></tr>`); }).join('')}</table></div>`;
  $('#cmp-body').innerHTML = html;
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
  root.innerHTML = `<div class="rep-head"><div class="title">Mods in “${esc(S.cur.meta.title)}”</div><div class="dim">${mods.length} mods with recorded cost</div>
    <div class="actions"><input id="modq" placeholder="filter" value="${esc(S.modFilter)}"></div></div>
    <div class="card"><table><tr><th class="sort" data-k="name">mod (library zip)</th><th>kind</th><th class="n sort" data-k="script">script ms/s</th><th class="n">REDscript</th><th class="n">CET</th><th class="n sort" data-k="files">files loaded</th><th class="n">load ms</th><th class="n sort" data-k="spikes">in spikes</th><th class="n">ms in spikes</th><th class="n sort" data-k="score">cost score</th></tr>
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
  if (!S.vrSub) S.vrSub = 'mods';
  const apps = S.vrSub === 'apps';
  $('#vr-apps', page).style.display = apps ? '' : 'none';
  $('#vr-mods', page).style.display = apps ? 'none' : '';
  const st = S.state;
  if (S.vrBuild == null) S.vrBuild = st.config.active || 0;
  const top = $('#vr-top', page);
  if (apps) {
    top.innerHTML = `<div class="rep-head"><div class="title">VRAM</div></div>${vramSeg()}`;
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
    <div class="actions">${r ? '<button class="btn2" id="vr-copy">Copy summary</button>' : ''}<button class="btn" id="vr-run" ${busy ? 'disabled' : ''}>${busy ? 'Scanning ...' : 'Scan build'}</button></div></div>${vramSeg()}`;
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
  html += `<div class="sec2"><h2><span class="no">01</span>Mods by ${key === 'added' ? 'what they add' : key === 'loaded' ? 'what the game loaded' : key === 'tex4k' ? '4K and 8K textures' : 'GPU data'}</h2><span class="dimmer small">click a mod for its files</span>
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
}
function vramSeg() {
  return `<div class="seg"><button data-sub="mods" class="${S.vrSub === 'mods' ? 'on' : ''}">Mods</button><button data-sub="apps" class="${S.vrSub === 'apps' ? 'on' : ''}">Programs on the graphics card</button></div>`;
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
  $$('[data-b]', root).forEach(b => b.onclick = () => { S.vrBuild = +b.dataset.b; S.vrOpen = null; renderVram(); });
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
  if (S.exBuild == null) S.exBuild = st.config.active || 0;
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
      <div class="nm">${esc(x.name)}${x.new ? '<small>new</small>' : ''}${em ? `<em class="${x.warn && x.short === em ? 'warnc' : ''}">${esc(em)}</em>` : ''}</div>
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
  $$('[data-b]', root).forEach(b => b.onclick = () => { S.exBuild = +b.dataset.b; S.ex = null; renderExperiments(); });
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
  if (S.ckBuild == null) S.ckBuild = st.config.active || 0;
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
      else if (x.undo_id && undo.has(x.undo_id)) btn = `<button class="btn2 small" data-ckundo="${esc(x.undo_id)}">Undo</button>`;
      body += `<div class="exl${cls === 'na' ? ' na' : ''}${open ? ' open' : ''}" data-ckrow="${esc(key)}"><span class="ch">${x.what ? '›' : ''}</span>
        <div class="nm">${esc(x.name)}<em class="${cls === 'chk' ? 'warnc' : ''}">${esc(x.detail)}</em></div>
        <div>${x.label ? `<span class="st ${cls}"><i></i>${esc(x.label)}</span>` : ''}</div><div class="bt">${btn}</div></div>`;
      if (open) body += `<div class="exd"><div class="w">${esc(x.what)}</div>${x.undo_id && undo.has(x.undo_id) ? '<div class="m">Fixed by Frame Lab: Undo puts back exactly what was there.</div>' : ''}</div>`;
    }
    body += '</div>';
  }
  const at = rep.at ? new Date(rep.at).toLocaleString('en-AU', { day: 'numeric', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit' }) : '';
  root.innerHTML = `<div class="rep-head"><div class="title">Checks</div>
    <div class="cf-builds">${st.config.builds.map((b, i) => `<button class="btn2 small" data-b="${i}" style="${i === S.ckBuild ? 'border-color:var(--accent);color:var(--white)' : ''}">${esc(b.name)}</button>`).join('')}</div>
    <div class="actions"><button class="btn" id="ck-run">Run checks</button></div></div>
    <div class="dim" style="max-width:900px">Things that quietly cost frames or add stutter. Frame Lab only reads here. Where something can be changed, the row says how, and Undo puts it back.
    Checked ${esc(at)} · ${flagged ? flagged + ' to look at' : 'nothing to look at'} · run again after you change something.</div>
    ${note ? `<div style="margin-top:10px" class="${note.err ? 'lose' : 'win'}">${esc(note.text)}</div>` : ''}${body}`;
  $$('[data-b]', root).forEach(b => b.onclick = () => { S.ckBuild = +b.dataset.b; S.ck = null; renderChecks(); });
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
  if (S.cfBuild == null) S.cfBuild = st.config.active || 0;
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
  let html = `<div class="rep-head"><div class="title">Conflicts</div>
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
  const pairRow = (p) => {
    const w = A[p.winner], l = A[p.loser];
    const note = p.same === p.files ? '<span class="tag g">identical copies</span>' : p.slot ? '<span class="tag g">framework slot</span>' : p.patch ? '<span class="tag g">patch, on purpose</span>' : p.order !== 'sure' ? '<span class="tag y">' + esc(p.order) + '</span>' : (p.loaded ? '<span class="tag r">used in game</span>' : '<span class="tag">check</span>');
    const files = (p.sample || []).map(f => `<div>${f.loaded ? '<span class="tag r">loaded</span> ' : ''}${f.same ? '<span class="tag g">identical</span> ' : ''}<span class="mono">${esc(f.name || f.hash)}</span></div>`).join('') + (p.files > (p.sample || []).length ? `<div class="dimmer">+ ${p.files - p.sample.length} more</div>` : '') +
      (p.same && p.same < p.files ? `<div class="dimmer">${p.same} of the ${p.files} files are identical in both</div>` : '');
    return `<details class="cf"><summary class="cf-row"><div><span class="chev">›</span><span class="win">${esc(w.label)}</span><div class="dimmer small" style="margin-left:12px">#${w.order} · ${esc(w.rel.split('/').pop())}</div></div><div class="dimmer">›</div>
      <div><span class="lose">${esc(l.label)}</span><div class="dimmer small">#${l.order} · ${esc(l.rel.split('/').pop())}</div></div>
      <div class="n">${fmt.n(p.files)}</div><div class="n">${r.session ? (p.loaded ? '<b class="lose">' + p.loaded + '</b>' : '0') : '–'}</div><div class="dim">${esc(p.kinds)}</div><div>${note}</div></summary>
      <div class="cf-files">${files}</div></details>`;
  };
  const head = `<div class="cf-row head"><div>wins (loads first)</div><div></div><div>its files are ignored</div><div class="n">files</div><div class="n">loaded in game</div><div>file types</div><div></div></div>`;
  html += `<div class="cf-sec card"><h2>Archives<span class="dim">two mods replace the same game file: the archive that loads first is used, the other one's copy is ignored</span></h2>`;
  html += real.length ? head + real.map(pairRow).join('') : '<div class="dim">No clashes between mods (apart from compatibility patches).</div>';
  if (patches.length) html += `<details class="cf" style="margin-top:10px"><summary class="dim"><span class="chev">›</span>${patches.length} harmless: compatibility patches / merges that win on purpose, framework placeholder files that mods are meant to replace, and byte-identical copies</summary>${head}${patches.map(pairRow).join('')}</details>`;
  html += `<div class="dimmer small" style="margin-top:10px">Load order: ${esc(r.order)}. To make a mod win, rename its .archive so it sorts earlier (a leading # or 0) — or remove the one you don't want. File names show once the file-name list is built (Capture tab).</div></div>`;

  // REDscript
  const sc = r.scripts || [];
  html += `<div class="cf-sec card"><h2>REDscript<span class="dim">two mods replace the same game function: only one of the replacements runs</span></h2>` +
    (sc.length ? `<table><tr><th>function</th><th>replaced by</th><th>also wrapped by (fine)</th></tr>${sc.map(x => `<tr><td class="mono">${esc(x.target)}</td><td>${x.mods.map(m => '<div class="lose">' + esc(m) + '</div>').join('')}</td><td class="dim">${esc((x.wraps || []).join(', ') || '–')}</td></tr>`).join('')}</table>`
      : `<div class="dim">None: no game function is replaced by more than one mod (${r.scripts_scanned} .reds files read).</div>`) + '</div>';

  // TweakXL
  html += `<div class="cf-sec card"><h2>TweakXL<span class="dim">the same game value set to different things: only one sticks</span></h2>` +
    (tw.length ? `<table><tr><th>value</th><th>mod</th><th>sets it to</th><th></th></tr>${tw.map(t => t.values.map((v, i) => `<tr><td class="mono">${i ? '' : esc(t.key)}</td><td>${esc(v.mod)}<div class="dimmer small">${esc(v.file)}</div></td><td class="mono">${esc(v.value)}</td><td>${i || !t.patch ? '' : '<span class="tag g">patch</span>'}</td></tr>`).join('')).join('')}</table>
      <div class="dimmer small" style="margin-top:8px">Simple "key: value" lines only; lists and appends combine and are not clashes.</div>`
      : `<div class="dim">None found (${r.tweaks_scanned} tweak files read).</div>`) + '</div>';

  // logs
  const logs = (r.logs || []).filter(l => l.errors || l.warnings), clean = (r.logs || []).length - logs.length;
  html += `<div class="cf-sec card"><h2>Logs<span class="dim">errors and warnings in the last log of each framework and plugin · ${clean} logs without any</span></h2>` +
    (logs.length ? logs.map(l => `<details class="cf"><summary class="cf-row" style="grid-template-columns:minmax(0,1fr) 90px 90px minmax(0,2fr)"><div><span class="chev">›</span><b>${esc(l.name)}</b></div>
      <div class="n">${l.errors ? '<span class="lvl-e">' + l.errors + ' errors</span>' : '<span class="dimmer">0 errors</span>'}</div><div class="n">${l.warnings ? '<span class="lvl-w">' + l.warnings + ' warn.</span>' : ''}</div>
      <div class="dimmer small">${esc(l.file)} · ${esc(new Date(l.when).toLocaleString('en-AU'))}</div></summary>
      <div class="cf-files loglines">${(l.lines || []).map(x => `<div><span class="${x.level === 'error' ? 'lvl-e' : 'lvl-w'}">${x.level === 'error' ? 'E' : 'W'}</span>${x.count > 1 ? ' <span class="dimmer">×' + x.count + '</span>' : ''} ${esc(x.text)}</div>`).join('')}</div></details>`).join('')
      : '<div class="dim">No errors or warnings.</div>') + '</div>';
  if (r.notes && r.notes.length) html += `<div class="cf-sec card dim">${r.notes.map(esc).join('<br>')}</div>`;
  root.innerHTML = html;
  bindConflicts(root);
}

function bindConflicts(root) {
  $$('[data-b]', root).forEach(b => b.onclick = () => { S.cfBuild = +b.dataset.b; renderConflicts(); });
  const run = $('#cf-run', root);
  if (run) run.onclick = async () => { try { await api('conflicts/check', { build: S.cfBuild }); setTimeout(renderConflicts, 300); } catch (e) { toast(e.message, true); } };
}

/* ------------------------------------------------------------------ settings */
function renderSettings(force) {
  const root = $('#tab-settings');
  const st = S.state;
  if (!st) return;
  if (S.settingsDirty && !force) return;
  if (!force && root.dataset.built) { updateSettingsInfo(); return; }
  const c = st.config;
  root.innerHTML = `<div class="card" style="max-width:1020px"><h2>Game builds</h2><div id="set-builds">${c.builds.map((b, i) => `<div class="brow"><input data-bn="${i}" value="${esc(b.name)}"><input data-bp="${i}" value="${esc(b.path)}"><button class="btn2 small" data-del="${i}" title="remove">✕</button></div>`).join('')}</div>
    <div style="display:flex;gap:8px;margin-top:6px"><button class="btn2 small" id="set-find">Find game installs</button><button class="btn2 small" id="set-add">Add a build</button></div>
    <div class="dimmer small" style="margin-top:6px">A build is one Cyberpunk 2077 folder (the one with bin\\x64 in it). Find game installs looks at Steam, GOG, Epic, the running game, the usual folders on every drive and copies next to them.</div>
    <h2 style="margin-top:18px">Analysis</h2><div class="form">
      <label>Spike threshold (ms)</label><input id="set-spike" type="number" min="16" max="500" value="${c.spike_ms}" style="width:110px">
      <label>Script time per mod</label><label style="color:var(--text)"><input id="set-scripts" type="checkbox" ${c.use_script_profilers ? 'checked' : ''}> time REDscript and CET mods in the parts marked with F11 (HitchRecorder)</label>
      <label>Frame generation</label><label style="color:var(--text)"><input id="set-ft" type="checkbox" ${c.track_frame_type ? 'checked' : ''}> ask PresentMon to tell real and generated frames apart</label>
      <label>Windows events</label><label style="color:var(--text)"><input id="set-we" type="checkbox" ${c.no_windows_events ? '' : 'checked'}> record which drivers and programs held the PC during the session (Windows' own event tracing, needs admin)</label>
      <label>PresentMon exe</label><input id="set-pm" value="${esc(c.presentmon || '')}" placeholder="automatic: ${esc(st.components.presentmon.path || 'not found')}">
      <label>Mod library (optional)</label><input id="set-lib" value="${esc(c.library)}" placeholder="folder with your mod zips: shows which download each file came from">
      <label>WolvenKit folder</label><input id="set-wk" value="${esc(c.wolvenkit)}">
    </div>
    <div style="margin-top:16px;display:flex;gap:10px"><button class="btn" id="set-save">Save</button><span class="dim small" id="set-msg"></span></div></div>
    <div class="card" style="max-width:1020px;margin-top:14px"><h2>Parts</h2><div id="set-info"></div></div>
    <div class="card" style="max-width:1020px;margin-top:14px"><h2>About</h2><div class="dim">Night City Frame Lab ${esc(st.version)}<br>
      PresentMon by Intel (MIT). uPlot by Leon Sorokin (MIT). go-webview2 by John Chadwick (MIT). Inter and IBM Plex Mono fonts (SIL OFL 1.1). HitchRecorder uses MinHook by Tsuda Kageyu (BSD-2); its REDscript and CET timing is our own code (mapping script functions to their files at load is an idea from jekky's redscript-dap, MIT; the CET part reads LuaJIT's data layout, MIT).
      File names come from WolvenKit's path list on this PC.</div></div>`;
  root.dataset.built = '1';
  const dirty = () => { S.settingsDirty = true; $('#set-msg').textContent = 'unsaved changes'; };
  $$('input', root).forEach(i => i.addEventListener('input', dirty));
  $('#set-add').onclick = () => { $('#set-builds').insertAdjacentHTML('beforeend', `<div class="brow"><input data-bn="n" placeholder="name"><input data-bp="n" placeholder="C:\\...\\Cyberpunk 2077"><button class="btn2 small" data-del="n">✕</button></div>`); dirty(); };
  $('#set-find').onclick = async () => {
    let r;
    try { r = await api('builds/detect', {}); } catch (x) { toast(x.message, true); return; }
    const have = new Set($$('#set-builds [data-bp]').map(i => i.value.trim().toLowerCase().replace(/[\\/]+$/, '')));
    const add = (r.found || []).filter(f => !have.has(f.path.toLowerCase().replace(/[\\/]+$/, '')));
    add.forEach(f => $('#set-builds').insertAdjacentHTML('beforeend', `<div class="brow"><input data-bn="n" value="${esc(f.name)}"><input data-bp="n" value="${esc(f.path)}"><button class="btn2 small" data-del="n">✕</button></div>`));
    if (add.length) { dirty(); toast('Found ' + add.length + ' more: check the names, then Save.'); }
    else toast((r.found || []).length ? 'No new installs: every one found is already listed.' : 'No Cyberpunk 2077 install found. Use Add a build and paste the game folder.');
  };
  $('#set-builds').onclick = (e) => { const d = e.target.closest('[data-del]'); if (d) { d.parentElement.remove(); dirty(); } };
  $('#set-save').onclick = async () => {
    const builds = $$('#set-builds .brow').map(r => ({ name: $('[data-bn]', r).value.trim(), path: $('[data-bp]', r).value.trim() })).filter(b => b.name && b.path);
    const cfg = Object.assign({}, S.state.config, {
      builds, spike_ms: +$('#set-spike').value || 50, use_script_profilers: $('#set-scripts').checked, track_frame_type: $('#set-ft').checked, no_windows_events: !$('#set-we').checked,
      presentmon: $('#set-pm').value.trim(), library: $('#set-lib').value.trim(), wolvenkit: $('#set-wk').value.trim(),
    });
    if (cfg.active >= builds.length) cfg.active = 0;
    try { await api('settings', cfg); S.settingsDirty = false; toast('Saved. Re-analyse old sessions to use a new spike threshold.'); renderSettings(true); } catch (x) { toast(x.message, true); }
  };
  updateSettingsInfo();
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
    r('WolvenKit', cp.wolvenkit.found, esc(cp.wolvenkit.path)) +
    r('Mod library', cp.library.found, esc(cp.library.path)) + '</table>';
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
