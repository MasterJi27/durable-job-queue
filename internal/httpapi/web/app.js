'use strict';

const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => [...document.querySelectorAll(selector)];
const icon = (name, extra = '') => `<svg class="icon ${extra}" aria-hidden="true"><use href="#${name}-icon"/></svg>`;
const esc = (value) => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const number = (n) => new Intl.NumberFormat().format(n);
const statuses = ['pending', 'running', 'succeeded', 'dead'];
const labels = {pending:'Pending', running:'Running', succeeded:'Succeeded', dead:'Dead letter'};
const colors = {pending:'#dfc28d', running:'#90a8e4', succeeded:'#8ec0a8', dead:'#dda0a6'};
const state = {page:'overview', queue:'default', status:'', search:'', offset:0, data:null, controller:null, revision:0, selected:null, detailRevision:0, resultCache:new Map(), simulation:null};
let toastTimer, searchTimer;

function toast(message) { $('#toast').textContent = message; $('#toast').hidden = false; clearTimeout(toastTimer); toastTimer = setTimeout(() => { $('#toast').hidden = true; }, 4000); }
async function api(path, options = {}) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 12000);
  try {
    const response = await fetch(path, {...options, signal:options.signal || controller.signal, headers:{'Content-Type':'application/json', ...options.headers}});
    const data = await response.json();
    if (!response.ok) throw new Error(data.error || `Request failed (${response.status})`);
    return data;
  } finally { clearTimeout(timer); }
}
function ago(value) {
  const age = Math.max(0, Math.floor((Date.now() - Date.parse(value)) / 1000));
  if (!Number.isFinite(age)) return '—';
  if (age < 5) return 'Just now';
  if (age < 60) return `${age}s ago`;
  if (age < 3600) return `${Math.floor(age / 60)}m ago`;
  if (age < 86400) return `${Math.floor(age / 3600)}h ago`;
  return `${Math.floor(age / 86400)}d ago`;
}
function date(value) { return value && !value.startsWith('0001-') ? new Date(value).toLocaleString() : '—'; }
function badge(status) { return statuses.includes(status) ? `<span class="badge ${status}">${labels[status]}</span>` : ''; }
function key() { return `job-${Date.now().toString(36)}-${crypto.randomUUID().slice(0, 6)}`; }
function setOnline(online, message = '') {
  $('#sidebar-status').textContent = online ? 'Database connected' : 'Connection unavailable';
  $('#sidebar-dot').className = `connection-dot ${online ? 'online' : 'offline'}`;
  $('#connection-banner').hidden = online;
  $('#connection-banner').textContent = online ? '' : `Could not refresh the queue. ${message} ${state.data ? 'The last loaded data is shown.' : 'Start the database and API, then refresh.'}`;
  if (!online) $('#last-updated').textContent = 'Updates paused';
}

function renderStats(stats) {
  const descriptions = {pending:'Waiting for an eligible worker', running:'Leased to a worker', succeeded:'Result committed to storage', dead:'Retry budget exhausted'};
  const icons = {pending:'clock',running:'play',succeeded:'check',dead:'error'};
  $('#metrics').innerHTML = statuses.map(s => `<button class="metric ${s}" data-metric="${s}" aria-label="Show ${labels[s].toLowerCase()} jobs"><span class="metric-top">${labels[s]}<span class="metric-icon">${icon(icons[s])}</span></span><strong class="metric-value">${stats ? number(stats[s]) : '—'}</strong><span class="metric-description">${descriptions[s]}</span></button>`).join('');
  $('#nav-total').textContent = stats ? number(stats.total) : '—';
  $('#total-caption').textContent = stats ? `${number(stats.total)} jobs · ${state.queue}` : 'Waiting for data';
  let x = 0;
  const segments = stats ? statuses.map(s => { const width = stats.total ? stats[s] / stats.total * 1000 : 0; const r = `<rect x="${x}" width="${width}" height="12" fill="${colors[s]}"/>`; x += width; return r; }).join('') : '';
  $('#distribution').innerHTML = `<svg class="distribution-bar" viewBox="0 0 1000 12" preserveAspectRatio="none" role="img" aria-label="Queue status distribution">${segments}</svg><div class="distribution-legend">${statuses.map(s => `<span><i class="legend-dot ${s}"></i>${labels[s]}<b>${stats ? number(stats[s]) : '—'}</b></span>`).join('')}</div>`;
}
function renderTabs() {
  $('#status-tabs').innerHTML = ['', ...statuses].map(s => `<button data-status="${s}" class="${state.status === s ? 'active' : ''}" aria-pressed="${state.status === s}">${s ? labels[s] : 'All jobs'}</button>`).join('');
}
function renderJobs(data) {
  $('#table-count').textContent = number(data.filtered);
  $('#job-rows').innerHTML = data.jobs.map(j => {
    const bars = Array.from({length:Math.min(j.max_attempts, 8)}, (_, i) => `<i class="${i < j.attempts ? (j.attempts > 1 ? 'filled retried' : 'filled') : ''}"></i>`).join('');
    return `<tr><td><div class="job-identity"><span class="job-icon">${icon(j.kind === 'sha256' ? 'code' : 'jobs')}</span><div><button class="job-key" data-inspect="${esc(j.id)}" title="${esc(j.idempotency_key)}">${esc(j.idempotency_key)}</button><span class="job-id">${esc(j.id.slice(0, 12))}</span></div></div></td><td>${badge(j.status)}</td><td><span class="handler-tag">${esc(j.kind)}</span></td><td><span class="attempts"><span class="attempt-track" aria-hidden="true">${bars}</span><span>${j.attempts} <span class="subtle">/ ${j.max_attempts}</span></span></span></td><td title="${esc(date(j.created_at))}">${ago(j.created_at)}</td><td><button class="icon-button" data-inspect="${esc(j.id)}" aria-label="Inspect ${esc(j.idempotency_key)}">${icon('arrow')}</button></td></tr>`;
  }).join('');
  const empty = data.jobs.length === 0;
  const trulyEmpty = data.stats.total === 0;
  $('#empty-state').hidden = !empty;
  $('#empty-title').textContent = trulyEmpty ? 'A clear queue. A fresh start.' : 'No jobs match this view.';
  $('#empty-description').textContent = trulyEmpty ? 'Submit your first job or add a small batch to see the queue in action.' : 'Try another status, a different search, or the previous page.';
  $('#samples-button').hidden = !trulyEmpty;
  $('#page-range').textContent = data.filtered ? `${empty ? 0 : data.offset + 1}–${Math.min(data.offset + data.jobs.length, data.filtered)} of ${number(data.filtered)} jobs` : '0 jobs';
  $('#pagination').hidden = data.filtered <= data.limit && state.offset === 0;
  $('#previous-page').disabled = state.offset === 0;
  $('#next-page').disabled = data.offset + data.jobs.length >= data.filtered;
  $('#last-updated').innerHTML = '<span class="small-dot"></span> Live · updates every 2s';
}
async function loadDashboard(force = false) {
  if (state.page === 'simulation') return;
  if (state.controller && !force) return;
  state.controller?.abort();
  const controller = new AbortController(); state.controller = controller;
  const revision = ++state.revision;
  const timeout = setTimeout(() => controller.abort(), 10000);
  $('#refresh-button .icon').classList.add('spinning');
  const query = new URLSearchParams({queue:state.queue, status:state.status, search:state.search, offset:state.offset, limit:state.page === 'overview' ? 8 : 20});
  try {
    const data = await api(`/v1/overview?${query}`, {signal:controller.signal});
    if (revision !== state.revision || state.page === 'simulation') return;
    state.data = data; setOnline(true); renderStats(data.stats); renderJobs(data);
  } catch (error) { if (revision === state.revision) setOnline(false, error.name === 'AbortError' ? 'The request timed out.' : error.message); }
  finally { clearTimeout(timeout); if (revision === state.revision) { state.controller = null; $('#refresh-button .icon').classList.remove('spinning'); } }
}
function navigate() {
  const page = location.hash.slice(1);
  state.page = ['overview', 'jobs', 'simulation'].includes(page) ? page : 'overview';
  state.controller?.abort(); state.controller = null; state.revision++;
  state.offset = 0;
  if (state.page === 'overview') { state.status = ''; state.search = ''; $('#job-search').value = ''; }
  $$('.nav-link').forEach(a => { a.classList.toggle('active', a.dataset.nav === state.page); if (a.dataset.nav === state.page) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current'); });
  $('#breadcrumb').textContent = {overview:'Overview', jobs:'All jobs', simulation:'Simulation lab'}[state.page];
  $('#queue-view').hidden = state.page === 'simulation'; $('#simulation-view').hidden = state.page !== 'simulation';
  $('#page-title').textContent = state.page === 'jobs' ? 'Every job. Every detail.' : 'Your queue, in focus.';
  $('#page-description').textContent = state.page === 'jobs' ? `Search, filter, and inspect work in the ${state.queue} queue.` : 'Every job accounted for. Every attempt visible.';
  $('#overview-insights').hidden = state.page !== 'overview'; $('#view-all').hidden = state.page !== 'overview';
  $('#table-title').firstChild.textContent = state.page === 'jobs' ? 'All jobs ' : 'Recent jobs ';
  $('#table-caption').textContent = state.page === 'jobs' ? 'Live status, attempts, and committed results.' : 'The latest work in your queue.';
  $('#sidebar').classList.remove('open'); $('#menu-button').setAttribute('aria-expanded', 'false');
  renderTabs();
  if (state.page === 'simulation') { api('/healthz').then(() => setOnline(true)).catch(e => setOnline(false, e.message)); }
  else loadDashboard(true);
}

function preset(name) {
  const payloads = {echo:{message:'Hello, durable queue.'}, retry:{message:'Recover and try again.',demo_fail_until:2}, slow:{message:'A little work takes time.',demo_sleep_ms:10000}, fail:{message:'Demonstrate dead-letter retention.',demo_fail_until:100}};
  $('#create-payload').value = JSON.stringify(payloads[name], null, 2);
  $('#create-kind').value = 'echo'; $('#job-form').elements.attempts.value = name === 'fail' ? 2 : 5;
  $$('#create-dialog [data-preset]').forEach(b => b.classList.toggle('selected', b.dataset.preset === name));
}
function newJob() {
  $('#job-form').reset(); $('#create-queue').value = state.queue; $('#create-key').value = key(); preset('echo');
  $('#job-error').hidden = true; $('#create-dialog').showModal();
}
$('#job-form').addEventListener('submit', async event => {
  event.preventDefault(); const form = event.currentTarget;
  $('#job-error').hidden = true;
  let payload;
  try { payload = JSON.parse(form.elements.payload.value); } catch { $('#job-error').textContent = 'The payload is not valid JSON. Check commas, quotes, and brackets.'; $('#job-error').hidden = false; return; }
  const body = {queue:form.elements.queue.value.trim(), kind:form.elements.kind.value, idempotency_key:form.elements.key.value.trim(), payload, max_attempts:Number(form.elements.attempts.value), delay_ms:Number(form.elements.delay.value) * 1000};
  const button = $('#submit-job'); button.disabled = true; button.textContent = 'Enqueueing…';
  try {
    const job = await api('/v1/jobs', {method:'POST',body:JSON.stringify(body)});
    $('#create-dialog').close();
    if (state.queue !== job.queue) { state.queue = job.queue; $('#queue-name').value = job.queue; state.data = null; renderStats(null); }
    state.status = ''; state.search = ''; state.offset = 0; $('#job-search').value = ''; renderTabs();
    if (state.page === 'simulation') location.hash = 'jobs'; else await loadDashboard(true);
    toast(`Job accepted: ${job.idempotency_key}`);
  } catch (error) { $('#job-error').textContent = error.message; $('#job-error').hidden = false; }
  finally { button.disabled = false; button.innerHTML = `${icon('plus')}Enqueue job`; }
});
$('#samples-button').addEventListener('click', async () => {
  const button = $('#samples-button'); button.disabled = true;
  const suffix = Date.now().toString(36); let created = 0;
  const samples = [
    {key:'welcome',kind:'echo',payload:{message:'Your first durable result.'},attempts:5,delay:0},
    {key:'report-digest',kind:'sha256',payload:{report:'quarterly',version:1},attempts:5,delay:0},
    {key:'retry-example',kind:'echo',payload:{demo_fail_until:2,message:'Recover from transient failures.'},attempts:5,delay:0},
    {key:'long-running',kind:'echo',payload:{demo_sleep_ms:15000,message:'Heartbeats keep this lease alive.'},attempts:5,delay:0},
    {key:'dead-letter',kind:'echo',payload:{demo_fail_until:100},attempts:2,delay:0},
    {key:'scheduled-report',kind:'sha256',payload:{report:'scheduled'},attempts:5,delay:60000}
  ];
  try {
    for (const sample of samples) { await api('/v1/jobs', {method:'POST',body:JSON.stringify({queue:state.queue,kind:sample.kind,idempotency_key:`${sample.key}-${suffix}`,payload:sample.payload,max_attempts:sample.attempts,delay_ms:sample.delay})}); created++; }
    toast('Six real jobs submitted. Watch the workers take over.');
  } catch (error) { toast(`${created} jobs submitted. ${error.message}`); }
  finally { button.disabled = false; loadDashboard(true); }
});

function meta(label, value, wide = false) { return `<div class="${wide ? 'wide' : ''}"><dt>${label}</dt><dd>${value}</dd></div>`; }
async function loadDetail(id, opening = false) {
  if (opening) { state.selected = id; $('#detail-title').textContent = 'Job details'; $('#detail-body').innerHTML = '<p class="detail-empty">Loading job details…</p>'; $('#detail-dialog').showModal(); }
  const revision = ++state.detailRevision;
  try {
    const job = await api(`/v1/jobs/${encodeURIComponent(id)}`);
    let result = null, resultError = '';
    if (job.status === 'succeeded') {
      result = state.resultCache.get(id);
      if (!result) { try { result = await api(`/v1/jobs/${encodeURIComponent(id)}/result`); state.resultCache.set(id, result); } catch (error) { resultError = error.message; } }
    }
    if (revision !== state.detailRevision || state.selected !== id || !$('#detail-dialog').open) return;
    $('#detail-title').textContent = job.idempotency_key;
    const leaseRemaining = Math.max(0, Math.ceil((Date.parse(job.lease_until) - Date.now()) / 1000));
    $('#detail-body').innerHTML = `<div class="detail-status">${badge(job.status)}<span class="subtle">Updated ${ago(job.updated_at)}</span></div><dl class="detail-meta">${meta('Job ID', `<span class="copy-id"><code>${esc(job.id)}</code><button class="icon-button" data-copy="id" aria-label="Copy job ID">${icon('copy')}</button></span>`,true)}${meta('Queue',esc(job.queue))}${meta('Handler',`<span class="handler-tag">${esc(job.kind)}</span>`)}${meta('Attempts',`${job.attempts} of ${job.max_attempts}`)}${meta('Fencing version',String(job.lease_version))}${meta('Created',esc(date(job.created_at)))}${meta('Available from',esc(date(job.available_at)))}${job.status === 'running' ? meta('Lease deadline',`${esc(date(job.lease_until))} · ${leaseRemaining ? `${leaseRemaining}s remaining` : 'expired'}`,true) : ''}${job.owner ? meta('Last worker',`<code>${esc(job.owner)}</code>`,true) : ''}</dl>${job.last_error ? `<div class="error-detail"><strong>Last attempt error</strong><p>${esc(job.last_error)}</p></div>` : ''}<section class="code-section"><div class="code-heading"><h3>Payload</h3><button class="icon-button" data-copy="payload" aria-label="Copy payload">${icon('copy')}</button></div><pre id="detail-payload"></pre></section><section class="code-section"><div class="code-heading"><h3>Committed result</h3>${result ? `<button class="icon-button" data-copy="result" aria-label="Copy committed result">${icon('copy')}</button>` : ''}</div>${result ? '<pre id="detail-result"></pre>' : `<p class="detail-empty">${esc(resultError || (job.status === 'dead' ? 'This job exhausted its retry budget. No result was committed.' : 'The result appears here once a worker completes this job.'))}</p>`}</section>${result ? `<p class="field-hint">Committed ${esc(date(result.committed_at))}</p>` : ''}<p class="detail-note">Processing attempts may repeat after a crash. A successful job has one committed database result.</p>`;
    $('#detail-payload').textContent = JSON.stringify(job.payload, null, 2);
    if (result) $('#detail-result').textContent = JSON.stringify(result.value, null, 2);
  } catch (error) { if (revision === state.detailRevision) $('#detail-body').innerHTML = `<p class="form-error">${esc(error.message)}</p>`; }
}
$('#detail-dialog').addEventListener('close', () => { state.selected = null; state.detailRevision++; });

function renderSimulation(report) {
  $('#simulation-placeholder').hidden = true; $('#simulation-results').hidden = false;
  const successWidth = report.accepted ? report.succeeded / report.accepted * 1000 : 0;
  const counts = [['Worker crashes',report.crashes],['Duplicate deliveries',report.duplicate_deliveries],['Duplicate enqueue requests',report.duplicate_enqueues],['Lost acknowledgements',report.lost_acknowledgements],['Database clock jumps',report.clock_jumps],['Stale leases rejected',report.stale_rejections]];
  $('#simulation-results').innerHTML = `<div class="sim-success-header"><div class="success-icon">${icon('check')}</div><div><h2>All invariants passed.</h2><p>Seed ${report.seed} · ${number(report.steps)} simulated events</p></div><button class="button secondary small" id="export-simulation" aria-label="Export simulation results">${icon('download')}<span>Export</span></button></div><div class="sim-results-content"><div class="sim-scoreboard"><div><strong>${number(report.invariant_checks)}</strong><span>Invariant checks</span></div><div><strong>${number(report.accepted)}</strong><span>Jobs accounted for</span></div><div><strong>${number(report.claims)}</strong><span>Processing attempts</span></div></div><div class="sim-accounting"><svg class="distribution-bar" viewBox="0 0 1000 12" preserveAspectRatio="none" role="img" aria-label="${report.succeeded} succeeded, ${report.dead} dead-letter jobs"><rect width="${successWidth}" height="12" fill="${colors.succeeded}"/><rect x="${successWidth}" width="${1000-successWidth}" height="12" fill="${colors.dead}"/></svg><div class="distribution-legend"><span><i class="legend-dot succeeded"></i>Succeeded<b>${report.succeeded}</b></span><span><i class="legend-dot dead"></i>Dead letter<b>${report.dead}</b></span></div><p>Every accepted job reached a terminal state. Dead-letter jobs remain accounted for; they are not successful executions.</p></div><h3>FAULTS & RECOVERY</h3>${counts.map(([label,n]) => `<div class="fault-stat"><span>${label}</span><strong>${number(n)}</strong></div>`).join('')}</div><div class="sim-invariants"><span>${icon('check')}Job conservation</span><span>${icon('check')}Result consistency</span><span>${icon('check')}Lease fencing</span></div>`;
}
$('#simulation-form').addEventListener('submit', async event => {
  event.preventDefault(); const form = new FormData(event.currentTarget); const body = Object.fromEntries([...form].map(([k,v]) => [k,Number(v)]));
  $('#simulation-error').hidden = true; const button = $('#run-simulation'); button.disabled = true; button.innerHTML = `${icon('refresh','spinning')}Running schedule…`;
  try { const report = await api('/v1/simulations', {method:'POST',body:JSON.stringify(body)}); state.simulation = {...report, configuration:body}; renderSimulation(report); toast(`Seed ${report.seed} passed ${number(report.invariant_checks)} invariant checks.`); }
  catch (error) { $('#simulation-error').textContent = error.message; $('#simulation-error').hidden = false; }
  finally { button.disabled = false; button.innerHTML = `${icon('play')}Run simulation`; }
});
function exportSimulation() {
  if (!state.simulation) return;
  const url = URL.createObjectURL(new Blob([JSON.stringify(state.simulation,null,2)], {type:'application/json'}));
  const a = document.createElement('a'); a.href = url; a.download = `durable-simulation-${state.simulation.seed}.json`; a.click(); setTimeout(() => URL.revokeObjectURL(url),1000);
}

document.addEventListener('click', async event => {
  const newButton = event.target.closest('[data-new-job]'); if (newButton) newJob();
  const close = event.target.closest('[data-close]'); if (close) document.getElementById(close.dataset.close).close();
  const presetButton = event.target.closest('[data-preset]'); if (presetButton) preset(presetButton.dataset.preset);
  const inspect = event.target.closest('[data-inspect]'); if (inspect) loadDetail(inspect.dataset.inspect,true);
  const status = event.target.closest('[data-status]'); if (status) { state.status = status.dataset.status; state.offset = 0; renderTabs(); loadDashboard(true); }
  const metric = event.target.closest('[data-metric]'); if (metric) { state.status = metric.dataset.metric; state.offset = 0; if (state.page !== 'jobs') location.hash = 'jobs'; else {renderTabs();loadDashboard(true);} }
  const copy = event.target.closest('[data-copy]');
  if (copy) { const text = copy.dataset.copy === 'id' ? state.selected : $(`#detail-${copy.dataset.copy}`).textContent; try { await navigator.clipboard.writeText(text); toast('Copied to clipboard.'); } catch { toast('Clipboard access is unavailable in this browser.'); } }
  if (event.target.closest('#export-simulation')) exportSimulation();
});
$('#generate-key').addEventListener('click', () => { $('#create-key').value = key(); });
$('#about-button').addEventListener('click', () => $('#about-dialog').showModal());
$('#refresh-button').addEventListener('click', () => loadDashboard(true));
$('#queue-form').addEventListener('submit', event => {
  event.preventDefault(); const name = $('#queue-name').value.trim(); if (!name) return;
  state.queue = name; state.offset = 0; state.status = ''; state.search = ''; state.data = null; $('#job-search').value = ''; renderStats(null);
  if (state.page === 'simulation') location.hash = 'overview'; else navigate();
});
$('#job-search').addEventListener('input', () => { clearTimeout(searchTimer); searchTimer = setTimeout(() => { state.search = $('#job-search').value; state.offset = 0; loadDashboard(true); },250); });
$('#previous-page').addEventListener('click', () => { state.offset = Math.max(0,state.offset - (state.page === 'overview' ? 8 : 20)); loadDashboard(true); });
$('#next-page').addEventListener('click', () => { state.offset += state.page === 'overview' ? 8 : 20; loadDashboard(true); });
$('#menu-button').addEventListener('click', () => { const open = $('#sidebar').classList.toggle('open'); $('#menu-button').setAttribute('aria-expanded', String(open)); });
document.addEventListener('keydown', event => { if (event.key === '/' && !event.target.matches('input,textarea,select') && !document.querySelector('dialog[open]') && state.page !== 'simulation') { event.preventDefault(); $('#job-search').focus(); } });
window.addEventListener('hashchange', navigate);
document.addEventListener('visibilitychange', () => { if (!document.hidden) loadDashboard(true); });
renderStats(null); renderTabs(); $('#job-rows').innerHTML = '<tr class="loading-row"><td colspan="6">Connecting to your queue…</td></tr>'; navigate();
setInterval(() => {
  if (document.hidden || document.activeElement?.closest('#job-rows')) return;
  loadDashboard();
  if (state.selected && $('#detail-dialog').open && !document.activeElement?.closest('#detail-body')) loadDetail(state.selected);
},2000);
