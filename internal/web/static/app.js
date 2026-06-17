'use strict';

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
    if (this.data.length > this.maxPoints) {
      this.data.shift();
    }
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

    // Draw threshold line
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

    // Fill area under line
    ctx.beginPath();
    ctx.moveTo(toX(0), toY(this.data[0]));
    for (let i = 1; i < this.data.length; i++) {
      ctx.lineTo(toX(i), toY(this.data[i]));
    }
    ctx.lineTo(toX(this.data.length - 1), h);
    ctx.lineTo(toX(0), h);
    ctx.closePath();
    ctx.fillStyle = this.fillColor;
    ctx.fill();

    // Draw line
    ctx.beginPath();
    ctx.moveTo(toX(0), toY(this.data[0]));
    for (let i = 1; i < this.data.length; i++) {
      ctx.lineTo(toX(i), toY(this.data[i]));
    }
    ctx.strokeStyle = this.color;
    ctx.lineWidth = 2;
    ctx.lineJoin = 'round';
    ctx.stroke();

    // Draw last point dot
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
  const toast = document.createElement('div');
  toast.className = `toast ${type}`;
  toast.innerHTML = `<span>${type === 'success' ? '✓' : '✗'}</span> ${message}`;
  document.body.appendChild(toast);
  setTimeout(() => {
    toast.style.animation = 'slideIn 0.3s ease reverse';
    setTimeout(() => toast.remove(), 300);
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

function progressClass(val, warn = 70, crit = 85) {
  return colorClass(val, warn, crit);
}

function fmtPct(v) {
  return typeof v === 'number' ? v.toFixed(1) + '%' : '—';
}

function fmtBytes(b) {
  if (!b && b !== 0) return '—';
  if (b > 1024 * 1024 * 1024) return (b / (1024 * 1024 * 1024)).toFixed(1) + ' GB';
  if (b > 1024 * 1024) return (b / (1024 * 1024)).toFixed(1) + ' MB';
  if (b > 1024) return (b / 1024).toFixed(1) + ' KB';
  return b + ' B';
}

function fmtLoad(v) {
  return typeof v === 'number' ? v.toFixed(2) : '—';
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

// ============================================================
// SSE connection with exponential backoff reconnect
// ============================================================
let sseSource = null;
let sseBackoff = 1000;
const sseMaxBackoff = 30000;

function connectSSE() {
  if (sseSource) {
    sseSource.close();
  }

  sseSource = new EventSource('/api/metrics/live');

  sseSource.onopen = () => {
    sseBackoff = 1000;
    const dot = document.getElementById('sse-status');
    const label = document.getElementById('sse-label');
    if (dot) { dot.style.background = 'var(--green)'; }
    if (label) label.textContent = 'Live';
  };

  sseSource.onmessage = (ev) => {
    try {
      const data = JSON.parse(ev.data);
      handleSSEMessage(data);
    } catch (e) { /* ignore parse errors */ }
  };

  sseSource.onerror = () => {
    sseSource.close();
    sseSource = null;
    const dot = document.getElementById('sse-status');
    const label = document.getElementById('sse-label');
    if (dot) { dot.style.background = 'var(--red)'; }
    if (label) label.textContent = 'Reconnecting...';
    setTimeout(connectSSE, sseBackoff);
    sseBackoff = Math.min(sseBackoff * 2, sseMaxBackoff);
  };
}

// ============================================================
// Handle SSE message and update dashboard
// ============================================================
let ramSparkline = null;
let cpuSparkline = null;

function initSparklines() {
  ramSparkline = new Sparkline(document.getElementById('sparkline-ram'), {
    color: '#58a6ff',
    fillColor: 'rgba(88,166,255,0.15)',
    max: 100,
    threshold: null, // set after config loads
  });
  cpuSparkline = new Sparkline(document.getElementById('sparkline-cpu'), {
    color: '#3fb950',
    fillColor: 'rgba(63,185,80,0.15)',
    max: 100,
    threshold: null,
  });
}

function handleSSEMessage(data) {
  // Update metric cards.
  if (data.ram_pct !== undefined) {
    updateMetricCard('m-ram', data.ram_pct, 'pb-ram', 70, 85);
    if (ramSparkline) {
      ramSparkline.push(data.ram_pct);
      document.getElementById('sparkline-ram-val').textContent = fmtPct(data.ram_pct);
      document.getElementById('sparkline-ram-val').className = 'sparkline-current ' + colorClass(data.ram_pct);
    }
  }
  if (data.cpu_pct !== undefined) {
    updateMetricCard('m-cpu', data.cpu_pct, 'pb-cpu', 60, 85);
    if (cpuSparkline) {
      cpuSparkline.push(data.cpu_pct);
      document.getElementById('sparkline-cpu-val').textContent = fmtPct(data.cpu_pct);
      document.getElementById('sparkline-cpu-val').className = 'sparkline-current ' + colorClass(data.cpu_pct, 60, 85);
    }
  }
  if (data.disk_pct !== undefined) {
    updateMetricCard('m-disk', data.disk_pct, 'pb-disk', 70, 85);
  }
  if (data.swap_pct !== undefined) {
    updateMetricCard('m-swap', data.swap_pct, 'pb-swap', 50, 80);
  }
  if (data.load_1 !== undefined) {
    const el = document.getElementById('m-load1');
    if (el) el.textContent = fmtLoad(data.load_1);
  }
  if (data.load_5 !== undefined) {
    const el = document.getElementById('m-load5');
    if (el) el.textContent = fmtLoad(data.load_5);
  }
  if (data.net_recv_kb !== undefined) {
    const el = document.getElementById('m-recv');
    if (el) el.textContent = data.net_recv_kb.toFixed(1) + ' KB/s';
  }
  if (data.net_sent_kb !== undefined) {
    const el = document.getElementById('m-sent');
    if (el) el.textContent = data.net_sent_kb.toFixed(1) + ' KB/s';
  }
  if (data.docker && Array.isArray(data.docker)) {
    renderDockerCards(data.docker);
  }
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
    const cpuColor = colorClass(c.CPUPct || 0, 50, 80);
    const memColor = colorClass(c.MemPct || 0, 60, 80);
    const isRunning = statusText.toLowerCase().startsWith('up');

    return `
    <div class="docker-card" id="docker-${c.Name}">
      <div class="docker-name">
        <span>${c.Name}</span>
        ${statusBadge}
      </div>
      <div class="docker-stats">
        <div class="docker-stat">
          <span class="docker-stat-label">CPU</span>
          <span class="docker-stat-value ${cpuColor}">${fmtPct(c.CPUPct || 0)}</span>
        </div>
        <div class="docker-stat">
          <span class="docker-stat-label">MEM</span>
          <span class="docker-stat-value ${memColor}">${fmtPct(c.MemPct || 0)}</span>
        </div>
        <div class="docker-stat">
          <span class="docker-stat-label">MEM MB</span>
          <span class="docker-stat-value">${(c.MemMB || 0).toFixed(0)} MB</span>
        </div>
      </div>
      <div class="docker-actions">
        ${isRunning
          ? `<button class="btn btn-danger" onclick="dockerAction('${c.Name}','stop')">Stop</button>`
          : `<button class="btn btn-success" onclick="dockerAction('${c.Name}','start')">Start</button>`
        }
      </div>
    </div>`;
  }).join('');
}

async function refreshDocker() {
  try {
    const resp = await fetch('/api/docker');
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    const data = await resp.json();
    renderDockerCards(data);
  } catch (e) {
    showToast('Failed to refresh Docker: ' + e.message, 'error');
  }
}

async function dockerAction(name, action) {
  try {
    const resp = await fetch(`/api/docker/${name}/${action}`, { method: 'POST' });
    if (!resp.ok) {
      const text = await resp.text();
      throw new Error(text || 'HTTP ' + resp.status);
    }
    showToast(`Container "${name}" ${action}ped successfully.`);
    setTimeout(refreshDocker, 1500);
  } catch (e) {
    showToast(`Failed to ${action} "${name}": ` + e.message, 'error');
  }
}

// ============================================================
// Graphs tab
// ============================================================
const graphTypes = ['ram', 'cpu', 'disk', 'network', 'docker', 'weekly'];
let graphsLoaded = false;

function loadGraphs() {
  graphTypes.forEach(type => {
    const container = document.getElementById('graph-' + type);
    if (!container) return;
    container.innerHTML = '<span class="spinner"></span> Loading...';
    const img = new Image();
    img.onload = () => {
      container.innerHTML = '';
      container.appendChild(img);
    };
    img.onerror = () => {
      container.innerHTML = '<span class="text-muted">Failed to load graph.</span>';
    };
    img.src = '/api/graphs/' + type + '?t=' + Date.now();
    img.alt = type + ' graph';
    img.style.width = '100%';
  });
}

function refreshGraphs() {
  loadGraphs();
}

async function testReport() {
  try {
    const resp = await fetch('/api/report/test', { method: 'POST' });
    const data = await resp.json();
    showToast(data.message || 'Test report queued!');
  } catch (e) {
    showToast('Failed to send test report: ' + e.message, 'error');
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
      tbody.innerHTML = '<tr><td colspan="5" class="no-data">No actions recorded.</td></tr>';
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
    if (tbody) tbody.innerHTML = `<tr><td colspan="5" class="no-data">Error: ${e.message}</td></tr>`;
  }
}

// ============================================================
// Config tab
// ============================================================
async function loadConfig() {
  try {
    const resp = await fetch('/api/config');
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    const cfg = await resp.json();
    const t = cfg.thresholds || {};
    const d = cfg.docker || {};
    setValue('cfg-ram-pct', t.ram_pct);
    setValue('cfg-cpu-pct', t.cpu_pct);
    setValue('cfg-cpu-sustained', t.cpu_sustained_minutes);
    setValue('cfg-disk-pct', t.disk_pct);
    setValue('cfg-ram-cooldown', t.ram_alert_cooldown_minutes);
    setValue('cfg-cpu-cooldown', t.cpu_alert_cooldown_minutes);
    setValue('cfg-disk-cooldown', t.disk_alert_cooldown_hours);
    const autoStop = document.getElementById('cfg-auto-stop');
    if (autoStop) autoStop.checked = !!d.auto_stop;
    setValue('cfg-idle-cpu', d.idle_cpu_pct);
    setValue('cfg-idle-dur', d.idle_duration_minutes);

    // Load stop order.
    const order = d.stop_order || [];
    renderStopOrderList(order);
  } catch (e) {
    showToast('Failed to load config: ' + e.message, 'error');
  }
}

function setValue(id, val) {
  const el = document.getElementById(id);
  if (el && val !== undefined && val !== null) el.value = val;
}

async function saveConfig(ev) {
  ev.preventDefault();
  const payload = {
    thresholds: {
      ram_pct: parseFloat(document.getElementById('cfg-ram-pct').value),
      cpu_pct: parseFloat(document.getElementById('cfg-cpu-pct').value),
      cpu_sustained_minutes: parseInt(document.getElementById('cfg-cpu-sustained').value),
      disk_pct: parseFloat(document.getElementById('cfg-disk-pct').value),
      ram_alert_cooldown_minutes: parseInt(document.getElementById('cfg-ram-cooldown').value),
      cpu_alert_cooldown_minutes: parseInt(document.getElementById('cfg-cpu-cooldown').value),
      disk_alert_cooldown_hours: parseInt(document.getElementById('cfg-disk-cooldown').value),
    },
    docker: {
      auto_stop: document.getElementById('cfg-auto-stop').checked,
      idle_cpu_pct: parseFloat(document.getElementById('cfg-idle-cpu').value),
      idle_duration_minutes: parseInt(document.getElementById('cfg-idle-dur').value),
    }
  };
  try {
    const resp = await fetch('/api/config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    if (!resp.ok) {
      const text = await resp.text();
      throw new Error(text || 'HTTP ' + resp.status);
    }
    showToast('Configuration saved successfully.');
  } catch (e) {
    showToast('Failed to save config: ' + e.message, 'error');
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
    list.innerHTML = '<li class="stop-order-empty">No containers in stop order.</li>';
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

function onDragLeave(ev) {
  ev.currentTarget.classList.remove('drag-over');
}

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
  if (stopOrderData.includes(name)) {
    showToast('Container already in list.', 'error');
    return;
  }
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
    showToast('Stop order saved.');
  } catch (e) {
    showToast('Failed to save stop order: ' + e.message, 'error');
  }
}

// ============================================================
// Logs tab
// ============================================================
let logRefreshTimer = null;

async function refreshLogs() {
  const container = document.getElementById('logs-container');
  const linesInput = document.getElementById('log-lines');
  const n = parseInt(linesInput.value) || 200;

  try {
    const resp = await fetch('/api/logs?lines=' + n);
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    const lines = await resp.json();
    if (!lines || lines.length === 0) {
      container.innerHTML = '<div class="log-line text-muted">No log entries.</div>';
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
    if (autoScroll && autoScroll.checked) {
      container.scrollTop = container.scrollHeight;
    }
  } catch (e) {
    if (container) container.innerHTML = `<div class="log-line error">Error: ${e.message}</div>`;
  }
}

function escapeHtml(str) {
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

// ============================================================
// Tab switching
// ============================================================
function switchTab(name) {
  document.querySelectorAll('.tab-btn').forEach(btn => {
    btn.classList.toggle('active', btn.dataset.tab === name);
  });
  document.querySelectorAll('.tab-panel').forEach(panel => {
    panel.classList.toggle('active', panel.id === 'tab-' + name);
  });

  // Load data for newly activated tabs.
  if (name === 'graphs' && !graphsLoaded) {
    graphsLoaded = true;
    loadGraphs();
  } else if (name === 'graphs') {
    loadGraphs(); // refresh on every visit
  } else if (name === 'actions') {
    refreshActions();
  } else if (name === 'config') {
    loadConfig();
  } else if (name === 'logs') {
    refreshLogs();
    if (logRefreshTimer) clearInterval(logRefreshTimer);
    logRefreshTimer = setInterval(refreshLogs, 10000);
  } else {
    if (logRefreshTimer) { clearInterval(logRefreshTimer); logRefreshTimer = null; }
  }
}

// ============================================================
// Clock and hostname
// ============================================================
function updateClock() {
  const el = document.getElementById('current-time');
  if (el) el.textContent = new Date().toUTCString().replace('GMT', 'UTC');
}

async function loadHostname() {
  try {
    const resp = await fetch('/api/metrics/latest');
    if (!resp.ok) return;
    // Just show that we're connected; the real hostname requires a separate API.
    document.getElementById('hostname').textContent = location.hostname;
  } catch (e) {
    // ignore
  }
}

// ============================================================
// SSE broadcaster poll (fallback: also poll /api/metrics/latest every 5s)
// ============================================================
async function pollLatestMetrics() {
  try {
    const resp = await fetch('/api/metrics/latest');
    if (!resp.ok) return;
    const data = await resp.json();
    // Map keys to SSE-like format.
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
// Init
// ============================================================
document.addEventListener('DOMContentLoaded', () => {
  // Tab click handlers.
  document.querySelectorAll('.tab-btn').forEach(btn => {
    btn.addEventListener('click', () => switchTab(btn.dataset.tab));
  });

  // Init sparklines.
  initSparklines();

  // Start clock.
  updateClock();
  setInterval(updateClock, 1000);

  // Load hostname.
  loadHostname();

  // Load initial dashboard data.
  refreshDocker();
  pollLatestMetrics();

  // Poll latest metrics every 5s (complement SSE).
  setInterval(pollLatestMetrics, 5000);

  // Connect SSE.
  connectSSE();
});
