'use strict';

// ============================================================
// Chart.js global defaults (dark theme)
// ============================================================
if (typeof Chart !== 'undefined') {
  Chart.defaults.color = '#c9d1d9';
  Chart.defaults.borderColor = '#30363d';
}

// ============================================================
// Sparkline — rolling 60-point Canvas line chart
// ============================================================
class Sparkline {
  constructor(canvas, opts) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.data = [];
    this.maxPoints = opts.maxPoints || 60;
    this.color = opts.color || '#58a6ff';
    this.fillColor = opts.fillColor || 'rgba(88,166,255,0.15)';
    this.max = opts.max || 100;
    this.thresholdColor = opts.thresholdColor || '#f85149';
    this.threshold = opts.threshold || null;
    this._resizeObserver = new ResizeObserver(() => this._resize());
    this._resizeObserver.observe(canvas.parentElement);
    this._resize();
  }

  _resize() {
    const parent = this.canvas.parentElement;
    this.canvas.width = parent.clientWidth;
    this.canvas.height = 80;
    this._draw();
  }

  push(value) {
    this.data.push(value);
    if (this.data.length > this.maxPoints) this.data.shift();
    this._draw();
  }

  _draw() {
    const ctx = this.ctx;
    const w = this.canvas.width;
    const h = this.canvas.height;
    ctx.clearRect(0, 0, w, h);
    if (this.data.length < 2) return;
    const maxVal = this.max || Math.max(...this.data, 1);
    const padTop = 6, padBottom = 4;
    const drawH = h - padTop - padBottom;
    const xStep = w / (this.maxPoints - 1);
    const xOffset = (this.maxPoints - this.data.length) * xStep;
    const toX = (i) => xOffset + i * xStep;
    const toY = (v) => padTop + drawH - (v / maxVal) * drawH;

    if (this.threshold !== null) {
      const ty = toY(this.threshold);
      ctx.beginPath();
      ctx.setLineDash([4, 3]);
      ctx.strokeStyle = this.thresholdColor;
      ctx.lineWidth = 1;
      ctx.moveTo(0, ty);
      ctx.lineTo(w, ty);
      ctx.stroke();
      ctx.setLineDash([]);
    }
    ctx.beginPath();
    ctx.moveTo(toX(0), toY(this.data[0]));
    for (let i = 1; i < this.data.length; i++) ctx.lineTo(toX(i), toY(this.data[i]));
    ctx.lineTo(toX(this.data.length - 1), h);
    ctx.lineTo(toX(0), h);
    ctx.closePath();
    ctx.fillStyle = this.fillColor;
    ctx.fill();
    ctx.beginPath();
    ctx.moveTo(toX(0), toY(this.data[0]));
    for (let i = 1; i < this.data.length; i++) ctx.lineTo(toX(i), toY(this.data[i]));
    ctx.strokeStyle = this.color;
    ctx.lineWidth = 2;
    ctx.lineJoin = 'round';
    ctx.stroke();
    const lx = toX(this.data.length - 1);
    const ly = toY(this.data[this.data.length - 1]);
    ctx.beginPath();
    ctx.arc(lx, ly, 3, 0, Math.PI * 2);
    ctx.fillStyle = this.color;
    ctx.fill();
  }
}

// ============================================================
// Toast notifications
// ============================================================
function showToast(message, type = 'success', duration = 3000) {
  const container = document.getElementById('toast-container');
  const toast = document.createElement('div');
  toast.className = `toast ${type}`;
  toast.textContent = message;
  if (container) {
    container.appendChild(toast);
  } else {
    document.body.appendChild(toast);
  }
  setTimeout(() => {
    toast.style.animation = 'slideIn 0.2s ease reverse';
    setTimeout(() => toast.remove(), 200);
  }, duration);
}

// ============================================================
// Utility helpers
// ============================================================
function colorClass(val, warn = 70, crit = 85) {
  if (val >= crit) return 'crit';
  if (val >= warn) return 'warn';
  return 'ok';
}

function fmtPct(v) {
  return typeof v === 'number' ? v.toFixed(1) + '%' : '—';
}

function formatBytes(bytes) {
  if (!bytes && bytes !== 0) return '—';
  if (bytes >= 1073741824) return (bytes / 1073741824).toFixed(2) + ' GB';
  if (bytes >= 1048576) return (bytes / 1048576).toFixed(1) + ' MB';
  if (bytes >= 1024) return (bytes / 1024).toFixed(1) + ' KB';
  return bytes + ' B';
}

function fmtLoad(v) {
  return typeof v === 'number' ? v.toFixed(2) : '—';
}

function formatDate(unix_ts) {
  return new Date(unix_ts * 1000).toLocaleString();
}

function p95(arr) {
  if (!arr || arr.length === 0) return 0;
  const sorted = [...arr].sort((a, b) => a - b);
  const idx = Math.ceil(0.95 * sorted.length) - 1;
  return sorted[Math.max(0, Math.min(idx, sorted.length - 1))];
}

function escapeHtml(str) {
  return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

function updateMetricCard(id, val, pbId, warnT = 70, critT = 85) {
  const el = document.getElementById(id);
  const pb = document.getElementById(pbId);
  if (!el) return;
  el.textContent = fmtPct(val);
  const cls = colorClass(val, warnT, critT);
  el.className = 'metric-value ' + cls;
  if (pb) {
    pb.style.width = Math.min(val, 100) + '%';
    pb.className = 'progress-fill ' + cls;
  }
}

function syncInput(rangeId, inputId) {
  const r = document.getElementById(rangeId);
  const i = document.getElementById(inputId);
  if (r && i) i.value = r.value;
}

function syncRange(inputId, rangeId) {
  const i = document.getElementById(inputId);
  const r = document.getElementById(rangeId);
  if (r && i) r.value = i.value;
}

function setValue(id, val) {
  const el = document.getElementById(id);
  if (el && val !== undefined && val !== null) el.value = val;
}

function togglePassword(inputId, btn) {
  const input = document.getElementById(inputId);
  if (!input) return;
  if (input.type === 'password') {
    input.type = 'text';
    btn.textContent = '🙈';
  } else {
    input.type = 'password';
    btn.textContent = '👁';
  }
}

// ============================================================
// SSE connection with exponential backoff
// ============================================================
let sseSource = null;
let sseBackoff = 1000;
const sseMaxBackoff = 30000;

function connectSSE() {
  if (sseSource) sseSource.close();
  sseSource = new EventSource('/api/metrics/live');

  sseSource.onopen = () => {
    sseBackoff = 1000;
    const dot = document.getElementById('sse-status');
    const label = document.getElementById('sse-label');
    if (dot) dot.style.background = 'var(--green)';
    if (label) label.textContent = 'Live';
  };

  sseSource.onmessage = (ev) => {
    try { handleSSEMessage(JSON.parse(ev.data)); } catch (e) { /* ignore */ }
  };

  sseSource.onerror = () => {
    sseSource.close();
    sseSource = null;
    const dot = document.getElementById('sse-status');
    const label = document.getElementById('sse-label');
    if (dot) dot.style.background = 'var(--red)';
    if (label) label.textContent = 'Reconnecting...';
    setTimeout(connectSSE, sseBackoff);
    sseBackoff = Math.min(sseBackoff * 2, sseMaxBackoff);
  };
}

// ============================================================
// SSE message → dashboard update
// ============================================================
let ramSparkline = null;
let cpuSparkline = null;

function initSparklines() {
  ramSparkline = new Sparkline(document.getElementById('sparkline-ram'), {
    color: '#58a6ff', fillColor: 'rgba(88,166,255,0.15)', max: 100, threshold: null,
  });
  cpuSparkline = new Sparkline(document.getElementById('sparkline-cpu'), {
    color: '#3fb950', fillColor: 'rgba(63,185,80,0.15)', max: 100, threshold: null,
  });
}

function handleSSEMessage(data) {
  if (data.ram_pct !== undefined) {
    updateMetricCard('m-ram', data.ram_pct, 'pb-ram', 70, 85);
    if (ramSparkline) {
      ramSparkline.push(data.ram_pct);
      const el = document.getElementById('sparkline-ram-val');
      if (el) { el.textContent = fmtPct(data.ram_pct); el.className = 'sparkline-current ' + colorClass(data.ram_pct); }
    }
  }
  if (data.cpu_pct !== undefined) {
    updateMetricCard('m-cpu', data.cpu_pct, 'pb-cpu', 60, 85);
    if (cpuSparkline) {
      cpuSparkline.push(data.cpu_pct);
      const el = document.getElementById('sparkline-cpu-val');
      if (el) { el.textContent = fmtPct(data.cpu_pct); el.className = 'sparkline-current ' + colorClass(data.cpu_pct, 60, 85); }
    }
  }
  if (data.disk_pct !== undefined) updateMetricCard('m-disk', data.disk_pct, 'pb-disk', 70, 85);
  if (data.swap_pct !== undefined) updateMetricCard('m-swap', data.swap_pct, 'pb-swap', 50, 80);
  if (data.load_1 !== undefined) { const el = document.getElementById('m-load1'); if (el) el.textContent = fmtLoad(data.load_1); }
  if (data.load_5 !== undefined) { const el = document.getElementById('m-load5'); if (el) el.textContent = fmtLoad(data.load_5); }
  if (data.net_recv_kb !== undefined) { const el = document.getElementById('m-recv'); if (el) el.textContent = data.net_recv_kb.toFixed(1) + ' KB/s'; }
  if (data.net_sent_kb !== undefined) { const el = document.getElementById('m-sent'); if (el) el.textContent = data.net_sent_kb.toFixed(1) + ' KB/s'; }
  if (data.docker && Array.isArray(data.docker)) renderDockerCards(data.docker);
}

// ============================================================
// Docker container management
// ============================================================
function renderDockerCards(containers) {
  const grid = document.getElementById('docker-grid');
  if (!grid) return;
  if (!containers || containers.length === 0) {
    grid.innerHTML = '<div class="no-data">No Docker containers found.</div>';
    return;
  }
  grid.innerHTML = containers.map(c => {
    const statusText = c.Status || 'unknown';
    const statusBadge = statusText.toLowerCase().startsWith('up')
      ? `<span class="status-badge running">running</span>`
      : `<span class="status-badge stopped">stopped</span>`;
    const cpuColor = colorClass(c.CPUPct || c.cpu_pct || 0, 50, 80);
    const memColor = colorClass(c.MemPct || c.mem_pct || 0, 60, 80);
    const isRunning = statusText.toLowerCase().startsWith('up');
    const name = c.Name || c.name || '?';
    const cpuVal = c.CPUPct !== undefined ? c.CPUPct : (c.cpu_pct || 0);
    const memPctVal = c.MemPct !== undefined ? c.MemPct : (c.mem_pct || 0);
    const memMBVal = c.MemMB !== undefined ? c.MemMB : (c.mem_mb || 0);
    return `<div class="docker-card" id="docker-${name}">
      <div class="docker-name"><span>${name}</span>${statusBadge}</div>
      <div class="docker-stats">
        <div class="docker-stat"><span class="docker-stat-label">CPU</span><span class="docker-stat-value ${cpuColor}">${fmtPct(cpuVal)}</span></div>
        <div class="docker-stat"><span class="docker-stat-label">MEM</span><span class="docker-stat-value ${memColor}">${fmtPct(memPctVal)}</span></div>
        <div class="docker-stat"><span class="docker-stat-label">MEM MB</span><span class="docker-stat-value">${memMBVal.toFixed(0)} MB</span></div>
      </div>
      <div class="docker-actions">
        ${isRunning
          ? `<button class="btn btn-danger" onclick="dockerAction('${name}','stop')">Stop</button>`
          : `<button class="btn btn-success" onclick="dockerAction('${name}','start')">Start</button>`}
      </div>
    </div>`;
  }).join('');
}

async function refreshDocker() {
  try {
    const resp = await fetch('/api/docker');
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    renderDockerCards(await resp.json());
  } catch (e) {
    showToast('Failed to refresh Docker: ' + e.message, 'error');
  }
}

async function dockerAction(name, action) {
  try {
    const resp = await fetch(`/api/docker/${name}/${action}`, { method: 'POST' });
    if (!resp.ok) throw new Error((await resp.text()) || 'HTTP ' + resp.status);
    showToast(`Container "${name}" ${action === 'stop' ? 'arrêté' : 'démarré'}.`);
    setTimeout(refreshDocker, 1500);
  } catch (e) {
    showToast(`Erreur ${action} "${name}": ` + e.message, 'error');
  }
}

// ============================================================
// Données tab — metric data browser
// ============================================================
let metricChartInstance = null;
let currentDataPoints = [];
let dataPage = 1;
const DATA_PAGE_SIZE = 50;
let currentPresetRange = 3600;

async function loadMetricNames() {
  try {
    const resp = await fetch('/api/metrics/names');
    if (!resp.ok) return;
    const names = await resp.json();
    const sel = document.getElementById('metric-select');
    if (!sel) return;
    const prev = sel.value;
    sel.innerHTML = '<option value="">-- Choisir une métrique --</option>';
    // Special option for action durations
    const durOpt = document.createElement('option');
    durOpt.value = '__action_durations__';
    durOpt.textContent = 'Durées d\'exécution (actions)';
    sel.appendChild(durOpt);
    (names || []).forEach(n => {
      const opt = document.createElement('option');
      opt.value = n;
      opt.textContent = n;
      sel.appendChild(opt);
    });
    if (prev && (prev === '__action_durations__' || (names && names.includes(prev)))) sel.value = prev;
  } catch (e) { /* ignore */ }
}

function setPreset(btn, range) {
  document.querySelectorAll('.preset-btn').forEach(b => b.classList.remove('active'));
  btn.classList.add('active');
  currentPresetRange = range;
  const customDiv = document.getElementById('custom-range');
  if (customDiv) customDiv.style.display = range === 'custom' ? 'flex' : 'none';
  if (range !== 'custom') onMetricOrRangeChange();
}

function getTimeRange() {
  if (currentPresetRange === 'custom') {
    const fromEl = document.getElementById('range-from');
    const toEl = document.getElementById('range-to');
    const from = fromEl && fromEl.value ? Math.floor(new Date(fromEl.value).getTime() / 1000) : Math.floor(Date.now() / 1000) - 86400;
    const to = toEl && toEl.value ? Math.floor(new Date(toEl.value).getTime() / 1000) : Math.floor(Date.now() / 1000);
    return { from, to };
  }
  const to = Math.floor(Date.now() / 1000);
  return { from: to - currentPresetRange, to };
}

function getGranularity() {
  const sel = document.querySelector('input[name="granularity"]:checked');
  return sel ? sel.value : 'auto';
}

async function onMetricOrRangeChange() {
  const name = document.getElementById('metric-select')?.value;
  if (!name) return;

  if (name === '__action_durations__') {
    const { from, to } = getTimeRange();
    const hours = Math.ceil((to - from) / 3600);
    await loadActionDurations(hours);
    return;
  }

  const { from, to } = getTimeRange();
  const granularity = getGranularity();
  try {
    const url = `/api/metrics/query?name=${encodeURIComponent(name)}&from=${from}&to=${to}&granularity=${granularity}`;
    const resp = await fetch(url);
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    currentDataPoints = (await resp.json()) || [];
    dataPage = 1;
    renderMetricChart(name, currentDataPoints);
    renderDataTable(currentDataPoints, dataPage);
    renderDataStats(currentDataPoints);
  } catch (e) {
    showToast('Erreur chargement métriques: ' + e.message, 'error');
  }
}

function renderMetricChart(name, points) {
  const canvas = document.getElementById('metric-chart');
  if (!canvas) return;
  if (metricChartInstance) {
    metricChartInstance.destroy();
    metricChartInstance = null;
  }
  if (!points || points.length === 0) return;
  const labels = points.map(p => formatDate(p.TS || p.ts || 0));
  const values = points.map(p => p.Value !== undefined ? p.Value : (p.value !== undefined ? p.value : 0));
  metricChartInstance = new Chart(canvas, {
    type: 'line',
    data: {
      labels,
      datasets: [{
        label: name,
        data: values,
        borderColor: '#58a6ff',
        backgroundColor: 'rgba(88,166,255,0.1)',
        borderWidth: 2,
        pointRadius: points.length > 200 ? 0 : 2,
        tension: 0.3,
        fill: true,
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: false,
      plugins: {
        legend: { display: false },
        tooltip: {
          mode: 'index',
          intersect: false,
          backgroundColor: '#161b22',
          borderColor: '#30363d',
          borderWidth: 1,
        }
      },
      scales: {
        x: {
          grid: { color: '#21262d' },
          ticks: { maxTicksLimit: 8, maxRotation: 0 },
        },
        y: {
          grid: { color: '#21262d' },
        }
      }
    }
  });
}

function renderDataTable(points, page) {
  const tbody = document.getElementById('data-tbody');
  if (!tbody) return;
  const total = points ? points.length : 0;
  const start = (page - 1) * DATA_PAGE_SIZE;
  const slice = (points || []).slice(start, start + DATA_PAGE_SIZE);
  if (slice.length === 0) {
    tbody.innerHTML = '<tr><td colspan="2" class="no-data">Aucune donnée.</td></tr>';
  } else {
    tbody.innerHTML = slice.map(p => {
      const ts = p.TS || p.ts || 0;
      const val = p.Value !== undefined ? p.Value : (p.value !== undefined ? p.value : 0);
      return `<tr><td class="text-mono" style="white-space:nowrap">${formatDate(ts)}</td><td>${val.toFixed(4)}</td></tr>`;
    }).join('');
  }
  renderPagination(total, page);
  const statsDiv = document.getElementById('data-stats');
  if (statsDiv) statsDiv.style.display = total > 0 ? 'flex' : 'none';
}

function renderPagination(total, page) {
  const container = document.getElementById('data-pagination');
  if (!container) return;
  const pages = Math.ceil(total / DATA_PAGE_SIZE);
  if (pages <= 1) { container.innerHTML = ''; return; }
  let html = `<button class="page-btn" onclick="goPage(${page - 1})" ${page <= 1 ? 'disabled' : ''}>←</button>`;
  const range = 2;
  for (let p = 1; p <= pages; p++) {
    if (p === 1 || p === pages || (p >= page - range && p <= page + range)) {
      html += `<button class="page-btn ${p === page ? 'active' : ''}" onclick="goPage(${p})">${p}</button>`;
    } else if (p === page - range - 1 || p === page + range + 1) {
      html += '<span class="text-muted" style="padding:0 4px">…</span>';
    }
  }
  html += `<button class="page-btn" onclick="goPage(${page + 1})" ${page >= pages ? 'disabled' : ''}>→</button>`;
  container.innerHTML = html;
}

function goPage(p) {
  dataPage = p;
  renderDataTable(currentDataPoints, dataPage);
}

function renderDataStats(points) {
  if (!points || points.length === 0) return;
  const values = points.map(p => p.Value !== undefined ? p.Value : (p.value || 0));
  const mn = Math.min(...values);
  const mx = Math.max(...values);
  const avg = values.reduce((a, b) => a + b, 0) / values.length;
  const pv = p95(values);
  const setVal = (id, v) => { const el = document.getElementById(id); if (el) el.textContent = v.toFixed(2); };
  setVal('stat-min', mn);
  setVal('stat-max', mx);
  setVal('stat-avg', avg);
  setVal('stat-p95', pv);
  const countEl = document.getElementById('stat-count');
  if (countEl) countEl.textContent = values.length;
}

function exportCSV() {
  if (!currentDataPoints || currentDataPoints.length === 0) {
    showToast('Aucune donnée à exporter.', 'error');
    return;
  }
  const lines = ['timestamp,value'];
  currentDataPoints.forEach(p => {
    const ts = p.TS || p.ts || 0;
    const val = p.Value !== undefined ? p.Value : (p.value || 0);
    lines.push(`${formatDate(ts)},${val}`);
  });
  const blob = new Blob([lines.join('\n')], { type: 'text/csv' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = 'metrics_export.csv';
  a.click();
  URL.revokeObjectURL(url);
}

// ============================================================
// Graphs tab
// ============================================================
const graphTypes = ['ram', 'cpu', 'disk', 'network', 'docker', 'weekly'];

function loadGraphs() {
  graphTypes.forEach(type => {
    const container = document.getElementById('graph-' + type);
    if (!container) return;
    container.innerHTML = '<span class="spinner"></span> Loading...';
    const img = new Image();
    img.onload = () => { container.innerHTML = ''; img.style.width = '100%'; container.appendChild(img); };
    img.onerror = () => { container.innerHTML = '<span class="text-muted">Impossible de charger le graphe.</span>'; };
    img.src = '/api/graphs/' + type + '?t=' + Date.now();
    img.alt = type + ' graph';
  });
}

function refreshGraphs() { loadGraphs(); }

async function testReport() {
  try {
    const resp = await fetch('/api/report/test', { method: 'POST' });
    const data = await resp.json();
    showToast(data.message || 'Rapport test envoyé !');
  } catch (e) {
    showToast('Erreur rapport test: ' + e.message, 'error');
  }
}

// ============================================================
// Actions tab
// ============================================================
async function refreshActions() {
  const tbody = document.getElementById('actions-tbody');
  try {
    const resp = await fetch('/api/actions');
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    const actions = await resp.json();
    if (!actions || actions.length === 0) {
      tbody.innerHTML = '<tr><td colspan="5" class="no-data">Aucune action enregistrée.</td></tr>';
      return;
    }
    tbody.innerHTML = actions.map(a => {
      const t = new Date(a.TS * 1000).toLocaleString();
      const typeLower = (a.ActionType || '').toLowerCase();
      const badgeClass = typeLower.includes('ram') ? 'badge-ram'
        : typeLower.includes('cpu') ? 'badge-cpu'
        : typeLower.includes('disk') ? 'badge-disk'
        : 'badge-docker';
      const status = a.Success
        ? '<span class="badge badge-ok">OK</span>'
        : '<span class="badge badge-fail">FAILED</span>';
      const details = (a.Details || '').substring(0, 120) + (a.Details && a.Details.length > 120 ? '…' : '');
      return `<tr>
        <td class="text-mono" style="white-space:nowrap">${t}</td>
        <td><span class="badge ${badgeClass}">${a.ActionType || '—'}</span></td>
        <td>${a.Trigger || '—'}</td>
        <td class="text-muted">${details}</td>
        <td>${status}</td>
      </tr>`;
    }).join('');
  } catch (e) {
    if (tbody) tbody.innerHTML = `<tr><td colspan="5" class="no-data">Erreur: ${e.message}</td></tr>`;
  }
}

// ============================================================
// Config tab
// ============================================================

// Collapsible sections
function toggleSection(header) {
  const body = header.nextElementSibling;
  const arrow = header.querySelector('.arrow');
  if (body) body.classList.toggle('hidden');
  if (arrow) header.classList.toggle('collapsed');
}

// Schedule windows
let scheduleWindows = [];
const DAYS_FR = ['L', 'M', 'Me', 'J', 'V', 'S', 'D'];
const DAYS_EN = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'];

function renderScheduleWindows() {
  const list = document.getElementById('schedule-windows-list');
  if (!list) return;
  if (scheduleWindows.length === 0) {
    list.innerHTML = '<div class="text-muted" style="font-size:0.85rem">Aucune plage configurée.</div>';
    return;
  }
  list.innerHTML = scheduleWindows.map((w, idx) => {
    const dayChecks = DAYS_EN.map((d, i) => {
      const checked = (w.days || []).includes(d) || (w.days || []).includes('*') ? 'checked' : '';
      return `<input type="checkbox" class="day-check" id="sw-${idx}-d-${i}" value="${d}" ${checked}>
              <label class="day-label" for="sw-${idx}-d-${i}" title="${DAYS_EN[i]}">${DAYS_FR[i]}</label>`;
    }).join('');
    return `<div class="schedule-window" data-idx="${idx}">
      <div class="day-checkboxes">${dayChecks}</div>
      <input type="time" class="form-input" value="${w.start || '07:00'}" style="width:auto" data-field="start" data-idx="${idx}">
      <span class="text-muted">→</span>
      <input type="time" class="form-input" value="${w.end || '22:00'}" style="width:auto" data-field="end" data-idx="${idx}">
      <button class="btn btn-danger" style="padding:0.25rem 0.6rem" onclick="removeScheduleWindow(${idx})">✕</button>
    </div>`;
  }).join('');
}

function addScheduleWindow() {
  scheduleWindows.push({ days: ['mon','tue','wed','thu','fri','sat','sun'], start: '07:00', end: '22:00' });
  renderScheduleWindows();
}

function removeScheduleWindow(idx) {
  scheduleWindows.splice(idx, 1);
  renderScheduleWindows();
}

function readScheduleWindows() {
  const list = document.getElementById('schedule-windows-list');
  if (!list) return scheduleWindows;
  const windows = [];
  list.querySelectorAll('.schedule-window').forEach((row, idx) => {
    const days = [];
    row.querySelectorAll('.day-check:checked').forEach(cb => days.push(cb.value));
    const startInput = row.querySelector('[data-field="start"]');
    const endInput = row.querySelector('[data-field="end"]');
    windows.push({
      days: days.length === 7 ? ['*'] : days,
      start: startInput ? startInput.value : '07:00',
      end: endInput ? endInput.value : '22:00',
    });
  });
  return windows;
}

async function loadFullConfig() {
  try {
    const resp = await fetch('/api/config/full');
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    const cfg = await resp.json();

    // Schedule
    const se = document.getElementById('cfg-schedule-enabled');
    if (se) se.checked = !!(cfg.schedule && cfg.schedule.enabled);
    setValue('cfg-schedule-tz', cfg.schedule && cfg.schedule.timezone || 'Europe/Paris');
    scheduleWindows = (cfg.schedule && cfg.schedule.windows) || [];
    renderScheduleWindows();

    // Collection
    const col = cfg.collection || {};
    setValue('cfg-coll-ram', col.ram_interval_s);
    setValue('cfg-coll-cpu', col.cpu_interval_s);
    setValue('cfg-coll-net', col.network_interval_s);
    setValue('cfg-coll-docker', col.docker_interval_s);
    setValue('cfg-coll-disk', col.disk_interval_s);
    setValue('cfg-coll-proc', col.process_interval_s);
    setValue('cfg-coll-sys', col.system_interval_s);

    // Thresholds
    const t = cfg.thresholds || {};
    setValue('cfg-ram-pct', t.ram_pct); setValue('cfg-ram-pct-range', t.ram_pct);
    setValue('cfg-cpu-pct', t.cpu_pct); setValue('cfg-cpu-pct-range', t.cpu_pct);
    setValue('cfg-cpu-sustained', t.cpu_sustained_minutes);
    setValue('cfg-disk-pct', t.disk_pct); setValue('cfg-disk-pct-range', t.disk_pct);
    setValue('cfg-ram-cooldown', t.ram_alert_cooldown_minutes);
    setValue('cfg-cpu-cooldown', t.cpu_alert_cooldown_minutes);
    setValue('cfg-disk-cooldown', t.disk_alert_cooldown_hours);

    // Docker
    const d = cfg.docker || {};
    const autoStop = document.getElementById('cfg-auto-stop');
    if (autoStop) autoStop.checked = !!d.auto_stop;
    setValue('cfg-idle-cpu', d.idle_cpu_pct);
    setValue('cfg-idle-dur', d.idle_duration_minutes);
    renderStopOrderList(d.stop_order || []);

    // Database
    const db = cfg.database || {};
    setValue('cfg-db-raw-ttl', db.raw_ttl_hours);
    setValue('cfg-db-hourly-ttl', db.hourly_ttl_days);
    setValue('cfg-db-weekly-ttl', db.weekly_ttl_weeks);
    setValue('cfg-db-max-size', db.max_size_mb);

    // Brevo / Notifications
    const br = cfg.brevo || {};
    setValue('cfg-brevo-email', br.sender_email);
    setValue('cfg-brevo-name', br.sender_name);
    const rcp = cfg.recipients || {};
    setValue('cfg-recipients', (rcp.emails || []).join(', '));
    const wk = cfg.weekly || {};
    setValue('cfg-weekly-hour', wk.hour_utc);
    setValue('cfg-weekly-weeks', wk.weeks_comparison);
    const wkGraphs = document.getElementById('cfg-weekly-graphs');
    if (wkGraphs) wkGraphs.checked = !!wk.include_graphs;

    // General
    const gen = cfg.general || {};
    setValue('cfg-log-dir', gen.log_dir);
    setValue('cfg-db-path', gen.db_path);
    setValue('cfg-top-procs', gen.top_processes_n);

    loadDBStats();
  } catch (e) {
    showToast('Erreur chargement config: ' + e.message, 'error');
  }
}

async function saveFullConfig() {
  const getVal = id => { const el = document.getElementById(id); return el ? el.value : undefined; };
  const getNum = id => { const v = parseFloat(getVal(id)); return isNaN(v) ? undefined : v; };
  const getInt = id => { const v = parseInt(getVal(id), 10); return isNaN(v) ? undefined : v; };
  const getBool = id => { const el = document.getElementById(id); return el ? el.checked : undefined; };

  const recipientsRaw = getVal('cfg-recipients') || '';
  const emails = recipientsRaw.split(',').map(s => s.trim()).filter(Boolean);

  const windows = readScheduleWindows();

  const payload = {
    schedule: {
      enabled: getBool('cfg-schedule-enabled'),
      timezone: getVal('cfg-schedule-tz'),
      windows,
    },
    collection: {
      ram_interval_s: getInt('cfg-coll-ram'),
      cpu_interval_s: getInt('cfg-coll-cpu'),
      network_interval_s: getInt('cfg-coll-net'),
      docker_interval_s: getInt('cfg-coll-docker'),
      disk_interval_s: getInt('cfg-coll-disk'),
      process_interval_s: getInt('cfg-coll-proc'),
      system_interval_s: getInt('cfg-coll-sys'),
    },
    thresholds: {
      ram_pct: getNum('cfg-ram-pct'),
      cpu_pct: getNum('cfg-cpu-pct'),
      cpu_sustained_minutes: getInt('cfg-cpu-sustained'),
      disk_pct: getNum('cfg-disk-pct'),
      ram_alert_cooldown_minutes: getInt('cfg-ram-cooldown'),
      cpu_alert_cooldown_minutes: getInt('cfg-cpu-cooldown'),
      disk_alert_cooldown_hours: getInt('cfg-disk-cooldown'),
    },
    docker: {
      auto_stop: getBool('cfg-auto-stop'),
      idle_cpu_pct: getNum('cfg-idle-cpu'),
      idle_duration_minutes: getInt('cfg-idle-dur'),
      stop_order: stopOrderData,
    },
    database: {
      raw_ttl_hours: getInt('cfg-db-raw-ttl'),
      hourly_ttl_days: getInt('cfg-db-hourly-ttl'),
      weekly_ttl_weeks: getInt('cfg-db-weekly-ttl'),
      max_size_mb: getInt('cfg-db-max-size'),
    },
    brevo: {
      api_key: getVal('cfg-brevo-key') || '',
      sender_email: getVal('cfg-brevo-email'),
      sender_name: getVal('cfg-brevo-name'),
    },
    recipients: { emails },
    weekly: {
      hour_utc: getInt('cfg-weekly-hour'),
      weeks_comparison: getInt('cfg-weekly-weeks'),
      include_graphs: getBool('cfg-weekly-graphs'),
    },
  };

  try {
    const resp = await fetch('/api/config/full', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    if (!resp.ok) throw new Error((await resp.text()) || 'HTTP ' + resp.status);
    showToast('Configuration enregistrée.');
  } catch (e) {
    showToast('Erreur sauvegarde: ' + e.message, 'error');
  }
}

// DB stats / vacuum / cleanup
async function loadDBStats() {
  try {
    const resp = await fetch('/api/db/stats');
    if (!resp.ok) return;
    const s = await resp.json();
    const set = (id, v) => { const el = document.getElementById(id); if (el) el.textContent = v; };
    set('db-stat-size', formatBytes(s.file_size_bytes));
    set('db-stat-raw', (s.raw_rows || 0).toLocaleString());
    set('db-stat-hourly', (s.hourly_rows || 0).toLocaleString());
    set('db-stat-actions', (s.action_rows || 0).toLocaleString());
    set('db-stat-weekly', (s.weekly_rows || 0).toLocaleString());
  } catch (e) { /* ignore */ }
}

async function doVacuum() {
  try {
    const resp = await fetch('/api/db/vacuum', { method: 'POST' });
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    showToast('Vacuum terminé.');
    loadDBStats();
  } catch (e) {
    showToast('Erreur vacuum: ' + e.message, 'error');
  }
}

async function doCleanup() {
  try {
    const resp = await fetch('/api/db/cleanup', { method: 'POST' });
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    showToast('Nettoyage terminé.');
    loadDBStats();
  } catch (e) {
    showToast('Erreur nettoyage: ' + e.message, 'error');
  }
}

// ============================================================
// Docker stop order drag-and-drop
// ============================================================
let stopOrderData = [];
let dragSrcIndex = null;

function renderStopOrderList(order) {
  stopOrderData = [...order];
  const list = document.getElementById('stop-order-list');
  if (!list) return;
  if (!stopOrderData || stopOrderData.length === 0) {
    list.innerHTML = '<li class="stop-order-empty">Aucun conteneur dans l\'ordre d\'arrêt.</li>';
    return;
  }
  list.innerHTML = stopOrderData.map((name, i) => `
    <li class="stop-order-item" draggable="true" data-index="${i}"
        ondragstart="onDragStart(event, ${i})"
        ondragover="onDragOver(event, ${i})"
        ondrop="onDrop(event, ${i})"
        ondragleave="onDragLeave(event)"
        ondragend="onDragEnd(event)">
      <span class="drag-handle">⠿</span>
      <span style="flex:1">${name}</span>
      <button class="btn btn-danger" style="padding:2px 8px;font-size:11px" onclick="removeFromStopOrder(${i})">✕</button>
    </li>`).join('');
}

function onDragStart(ev, index) {
  dragSrcIndex = index;
  ev.dataTransfer.effectAllowed = 'move';
  ev.currentTarget.style.opacity = '0.5';
}

function onDragEnd(ev) {
  ev.currentTarget.style.opacity = '';
  document.querySelectorAll('.stop-order-item').forEach(el => el.classList.remove('drag-over'));
}

function onDragOver(ev, index) {
  ev.preventDefault();
  ev.dataTransfer.dropEffect = 'move';
  document.querySelectorAll('.stop-order-item').forEach(el => el.classList.remove('drag-over'));
  ev.currentTarget.classList.add('drag-over');
  return false;
}

function onDragLeave(ev) { ev.currentTarget.classList.remove('drag-over'); }

function onDrop(ev, index) {
  ev.preventDefault();
  if (dragSrcIndex === null || dragSrcIndex === index) return;
  const moved = stopOrderData.splice(dragSrcIndex, 1)[0];
  stopOrderData.splice(index, 0, moved);
  renderStopOrderList(stopOrderData);
}

function addToStopOrder() {
  const input = document.getElementById('new-container-name');
  const name = input.value.trim();
  if (!name) return;
  if (stopOrderData.includes(name)) { showToast('Conteneur déjà dans la liste.', 'error'); return; }
  stopOrderData.push(name);
  renderStopOrderList(stopOrderData);
  input.value = '';
}

function removeFromStopOrder(index) {
  stopOrderData.splice(index, 1);
  renderStopOrderList(stopOrderData);
}

async function saveStopOrder() {
  try {
    const resp = await fetch('/api/docker/order', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(stopOrderData),
    });
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    showToast('Ordre d\'arrêt sauvegardé.');
  } catch (e) {
    showToast('Erreur sauvegarde ordre: ' + e.message, 'error');
  }
}

// ============================================================
// Legacy config support (old /api/config endpoint)
// ============================================================
async function loadConfig() { return loadFullConfig(); }
async function saveConfig(ev) { if (ev) ev.preventDefault(); return saveFullConfig(); }

// ============================================================
// Logs tab
// ============================================================
let logRefreshTimer = null;

async function refreshLogs() {
  const container = document.getElementById('logs-container');
  const linesInput = document.getElementById('log-lines');
  const n = parseInt((linesInput && linesInput.value) || '200', 10) || 200;
  try {
    const resp = await fetch('/api/logs?lines=' + n);
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    const lines = await resp.json();
    if (!lines || lines.length === 0) {
      container.innerHTML = '<div class="log-line text-muted">Aucun log.</div>';
      return;
    }
    container.innerHTML = lines.map(line => {
      let cls = 'log-line';
      const lower = line.toLowerCase();
      if (lower.includes('error') || lower.includes('fatal')) cls += ' error';
      else if (lower.includes('warn')) cls += ' warn';
      else if (lower.includes('info') || lower.includes('action')) cls += ' info';
      return `<div class="${cls}">${escapeHtml(line)}</div>`;
    }).join('');
    const autoScroll = document.getElementById('log-autoscroll');
    if (autoScroll && autoScroll.checked) container.scrollTop = container.scrollHeight;
  } catch (e) {
    if (container) container.innerHTML = `<div class="log-line error">Erreur: ${e.message}</div>`;
  }
}

// ============================================================
// Tab switching
// ============================================================
let metricNamesLoaded = false;
let actionsRefreshTimer = null;

function switchTab(name) {
  document.querySelectorAll('.tab-btn').forEach(btn => {
    btn.classList.toggle('active', btn.dataset.tab === name);
  });
  document.querySelectorAll('.tab-panel').forEach(panel => {
    panel.classList.toggle('active', panel.id === 'tab-' + name);
  });
  location.hash = name;

  // Clear old timers
  if (name !== 'logs' && logRefreshTimer) { clearInterval(logRefreshTimer); logRefreshTimer = null; }
  if (name !== 'actions' && actionsRefreshTimer) { clearInterval(actionsRefreshTimer); actionsRefreshTimer = null; }

  if (name === 'donnees') {
    if (!metricNamesLoaded) {
      metricNamesLoaded = true;
      loadMetricNames();
    }
  } else if (name === 'graphs') {
    loadGraphs();
  } else if (name === 'actions') {
    refreshActions();
    if (!actionsRefreshTimer) actionsRefreshTimer = setInterval(refreshActions, 60000);
  } else if (name === 'config') {
    loadFullConfig();
    loadCaps();
  } else if (name === 'logs') {
    refreshLogs();
    if (logRefreshTimer) clearInterval(logRefreshTimer);
    logRefreshTimer = setInterval(refreshLogs, 15000);
  }
}

// ============================================================
// Clock and hostname
// ============================================================
function updateClock() {
  const el = document.getElementById('current-time');
  if (el) el.textContent = new Date().toUTCString().replace('GMT', 'UTC');
}

// ============================================================
// Fallback polling (complement SSE)
// ============================================================
async function pollLatestMetrics() {
  try {
    const resp = await fetch('/api/metrics/latest');
    if (!resp.ok) return;
    const data = await resp.json();
    handleSSEMessage({
      ram_pct: data['ram.used_pct'],
      cpu_pct: data['cpu.total'],
      disk_pct: data['disk.root.used_pct'],
      swap_pct: data['system.swap_pct'],
      load_1: data['cpu.load_1'],
      load_5: data['cpu.load_5'],
      net_recv_kb: (data['net.bytes_recv_delta'] || 0) / 1024,
      net_sent_kb: (data['net.bytes_sent_delta'] || 0) / 1024,
    });
  } catch (e) { /* ignore */ }
}

// ============================================================
// Caps management
// ============================================================
let capsData = [];

async function loadCaps() {
  try {
    const resp = await fetch('/api/caps');
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    capsData = (await resp.json()) || [];
    renderCapsList();
  } catch (e) {
    showToast('Erreur chargement caps: ' + e.message, 'error');
  }
}

function addCap() {
  capsData.push({
    name: 'Nouveau Cap',
    description: '',
    metric: 'ram.used_pct',
    operator: '>',
    threshold: 80,
    cooldown_minutes: 30,
    respect_schedule: false,
    enabled: true,
    action: [],
  });
  renderCapsList();
}

function renderCapsList() {
  const container = document.getElementById('caps-list');
  if (!container) return;
  if (!capsData || capsData.length === 0) {
    container.innerHTML = '<div class="text-muted" style="font-size:0.85rem">Aucun cap configuré.</div>';
    return;
  }
  container.innerHTML = capsData.map((cap, i) => renderCap(cap, i)).join('');
}

function renderCap(cap, index) {
  const actions = (cap.action || []).map((a, ai) => renderCapAction(a, index, ai)).join('');
  return `<div class="card" style="border:1px solid #30363d;padding:1rem" data-cap-index="${index}">
    <div class="flex-row" style="justify-content:space-between;margin-bottom:0.75rem">
      <strong style="font-size:0.95rem">Cap #${index + 1}</strong>
      <button class="btn btn-danger" style="padding:2px 10px;font-size:0.8rem" onclick="removeCap(${index})">Supprimer</button>
    </div>
    <div class="form-row">
      <span class="form-label">Nom</span>
      <input type="text" class="form-input" style="width:220px" value="${escapeHtml(cap.name || '')}" onchange="capField(${index},'name',this.value)">
    </div>
    <div class="form-row">
      <span class="form-label">Description</span>
      <input type="text" class="form-input" style="width:340px" value="${escapeHtml(cap.description || '')}" onchange="capField(${index},'description',this.value)">
    </div>
    <div class="form-row">
      <span class="form-label">Métrique</span>
      <input type="text" class="form-input" style="width:220px" list="common-metrics" value="${escapeHtml(cap.metric || '')}" onchange="capField(${index},'metric',this.value)">
    </div>
    <div class="form-row">
      <span class="form-label">Opérateur</span>
      <select class="form-input" style="width:90px" onchange="capField(${index},'operator',this.value)">
        ${['>', '>=', '<', '<=', '=='].map(op => `<option value="${op}"${cap.operator===op?' selected':''}>${op}</option>`).join('')}
      </select>
    </div>
    <div class="form-row">
      <span class="form-label">Seuil</span>
      <input type="number" class="form-input" style="width:100px" step="0.1" value="${cap.threshold || 0}" onchange="capField(${index},'threshold',parseFloat(this.value))">
    </div>
    <div class="form-row">
      <span class="form-label">Cooldown (minutes)</span>
      <input type="number" class="form-input" style="width:100px" min="1" value="${cap.cooldown_minutes || 30}" onchange="capField(${index},'cooldown_minutes',parseInt(this.value,10))">
    </div>
    <div class="form-row">
      <span class="form-label">Respecter le schedule</span>
      <label class="form-toggle">
        <input type="checkbox" ${cap.respect_schedule ? 'checked' : ''} onchange="capField(${index},'respect_schedule',this.checked)">
        <span class="slider"></span>
      </label>
    </div>
    <div class="form-row">
      <span class="form-label">Activé</span>
      <label class="form-toggle">
        <input type="checkbox" ${cap.enabled ? 'checked' : ''} onchange="capField(${index},'enabled',this.checked)">
        <span class="slider"></span>
      </label>
    </div>
    <div style="margin-top:0.75rem">
      <div class="form-label" style="margin-bottom:0.5rem">Actions</div>
      <div id="cap-actions-${index}" style="display:grid;gap:0.5rem">${actions}</div>
      <button class="btn btn-success" style="margin-top:0.5rem;font-size:0.82rem" onclick="addCapAction(${index})">+ Ajouter une action</button>
    </div>
  </div>`;
}

function renderCapAction(a, capIndex, actionIndex) {
  const types = ['email', 'docker_stop', 'docker_restart', 'docker_stop_idle', 'shell', 'http_webhook', 'log_only'];
  const typeOpts = types.map(t => `<option value="${t}"${a.type===t?' selected':''}>${t}</option>`).join('');

  const showContainer = (a.type === 'docker_stop' || a.type === 'docker_restart');
  const showIdle = a.type === 'docker_stop_idle';
  const showShell = a.type === 'shell';
  const showEmail = a.type === 'email';
  const showWebhook = a.type === 'http_webhook';

  return `<div class="card" style="border:1px solid #21262d;padding:0.75rem;background:#0d1117" data-action-index="${actionIndex}">
    <div class="flex-row" style="justify-content:space-between;margin-bottom:0.5rem">
      <select class="form-input" style="width:180px" onchange="capActionField(${capIndex},${actionIndex},'type',this.value);renderCapsList()">
        ${typeOpts}
      </select>
      <button class="btn btn-danger" style="padding:1px 8px;font-size:0.78rem" onclick="removeCapAction(${capIndex},${actionIndex})">✕</button>
    </div>
    ${showContainer ? `<div class="form-row"><span class="form-label">Container</span><input type="text" class="form-input" style="width:180px" value="${escapeHtml(a.container||'')}" onchange="capActionField(${capIndex},${actionIndex},'container',this.value)"></div>` : ''}
    ${showIdle ? `
      <div class="form-row"><span class="form-label">CPU idle (%)</span><input type="number" class="form-input" style="width:90px" step="0.1" value="${a.idle_cpu_pct||0.5}" onchange="capActionField(${capIndex},${actionIndex},'idle_cpu_pct',parseFloat(this.value))"></div>
      <div class="form-row"><span class="form-label">Idle minutes</span><input type="number" class="form-input" style="width:90px" value="${a.idle_minutes||10}" onchange="capActionField(${capIndex},${actionIndex},'idle_minutes',parseInt(this.value,10))"></div>
    ` : ''}
    ${showShell ? `
      <div class="form-row"><span class="form-label">Commande</span><input type="text" class="form-input" style="width:300px" value="${escapeHtml(a.command||'')}" onchange="capActionField(${capIndex},${actionIndex},'command',this.value)"></div>
      <div class="form-row"><span class="form-label">Timeout (s)</span><input type="number" class="form-input" style="width:90px" value="${a.timeout_s||30}" onchange="capActionField(${capIndex},${actionIndex},'timeout_s',parseInt(this.value,10))"></div>
    ` : ''}
    ${showEmail ? `<div class="form-row"><span class="form-label">Sujet</span><input type="text" class="form-input" style="width:340px" value="${escapeHtml(a.subject||'')}" placeholder="{value} {metric}" onchange="capActionField(${capIndex},${actionIndex},'subject',this.value)"></div>` : ''}
    ${showWebhook ? `
      <div class="form-row"><span class="form-label">URL</span><input type="text" class="form-input" style="width:300px" value="${escapeHtml(a.url||'')}" onchange="capActionField(${capIndex},${actionIndex},'url',this.value)"></div>
      <div class="form-row"><span class="form-label">Méthode</span>
        <select class="form-input" style="width:90px" onchange="capActionField(${capIndex},${actionIndex},'method',this.value)">
          <option value="POST"${(a.method||'POST')==='POST'?' selected':''}>POST</option>
          <option value="GET"${a.method==='GET'?' selected':''}>GET</option>
        </select>
      </div>
      <div class="form-row"><span class="form-label">Body</span><input type="text" class="form-input" style="width:300px" value="${escapeHtml(a.body||'')}" onchange="capActionField(${capIndex},${actionIndex},'body',this.value)"></div>
    ` : ''}
  </div>`;
}

function capField(capIndex, field, value) {
  if (!capsData[capIndex]) return;
  capsData[capIndex][field] = value;
}

function capActionField(capIndex, actionIndex, field, value) {
  if (!capsData[capIndex] || !capsData[capIndex].action) return;
  capsData[capIndex].action[actionIndex][field] = value;
}

function removeCap(index) {
  capsData.splice(index, 1);
  renderCapsList();
}

function addCapAction(capIndex) {
  if (!capsData[capIndex]) return;
  if (!capsData[capIndex].action) capsData[capIndex].action = [];
  capsData[capIndex].action.push({ type: 'email', subject: '' });
  renderCapsList();
}

function removeCapAction(capIndex, actionIndex) {
  if (!capsData[capIndex] || !capsData[capIndex].action) return;
  capsData[capIndex].action.splice(actionIndex, 1);
  renderCapsList();
}

async function saveCaps() {
  try {
    const resp = await fetch('/api/caps', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(capsData),
    });
    if (!resp.ok) throw new Error((await resp.text()) || 'HTTP ' + resp.status);
    showToast('Caps enregistrés.');
  } catch (e) {
    showToast('Erreur sauvegarde caps: ' + e.message, 'error');
  }
}

// ============================================================
// Action durations chart (Données tab)
// ============================================================
let actionDurationsChart = null;

async function loadActionDurations(hours) {
  try {
    const resp = await fetch('/api/metrics/action-durations?hours=' + (hours || 168));
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    const points = (await resp.json()) || [];
    renderActionDurationsChart(points);
  } catch (e) {
    showToast('Erreur durées actions: ' + e.message, 'error');
  }
}

function renderActionDurationsChart(points) {
  const canvas = document.getElementById('metric-chart');
  if (!canvas) return;
  if (metricChartInstance) { metricChartInstance.destroy(); metricChartInstance = null; }
  if (actionDurationsChart) { actionDurationsChart.destroy(); actionDurationsChart = null; }
  if (!points || points.length === 0) return;
  const labels = points.map(p => formatDate(p.TS || p.ts || 0));
  const values = points.map(p => p.Value !== undefined ? p.Value : (p.value !== undefined ? p.value : 0));
  actionDurationsChart = new Chart(canvas, {
    type: 'bar',
    data: {
      labels,
      datasets: [{
        label: 'Durée (ms)',
        data: values,
        backgroundColor: 'rgba(88,166,255,0.7)',
        borderColor: '#58a6ff',
        borderWidth: 1,
      }]
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      animation: false,
      plugins: {
        legend: { display: false },
        tooltip: { mode: 'index', intersect: false, backgroundColor: '#161b22', borderColor: '#30363d', borderWidth: 1 }
      },
      scales: {
        x: { grid: { color: '#21262d' }, ticks: { maxTicksLimit: 8, maxRotation: 0 } },
        y: { grid: { color: '#21262d' }, title: { display: true, text: 'ms' } }
      }
    }
  });
}

// ============================================================
// Init
// ============================================================
document.addEventListener('DOMContentLoaded', () => {
  // Tab click handlers
  document.querySelectorAll('.tab-btn').forEach(btn => {
    btn.addEventListener('click', () => switchTab(btn.dataset.tab));
  });

  // Restore tab from hash
  const hash = location.hash.replace('#', '');
  if (hash && document.getElementById('tab-' + hash)) {
    switchTab(hash);
  }

  initSparklines();
  updateClock();
  setInterval(updateClock, 1000);
  document.getElementById('hostname').textContent = location.hostname;

  refreshDocker();
  setInterval(refreshDocker, 30000);

  pollLatestMetrics();
  setInterval(pollLatestMetrics, 5000);

  connectSSE();
});
