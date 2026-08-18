/* Web workbench client. The server remains the source of truth for runtime state. */
const $ = (id) => document.getElementById(id);
const qs = (selector) => document.querySelector(selector);
const events = $('events');
let currentState = null;
const WELCOME_KEY_PREFIX = 'ainovel.web.welcome.v2.';
let welcomeWorkspaceID = '';
let welcomeStateSynced = false;
const MAX_EVENT_ITEMS = 500;
const MAX_STREAM_ROUNDS = 32;
const MAX_STREAM_CHARS = 256 * 1024;
const STREAM_SEPARATOR = '\n\n';
const STATE_REFRESH_DELAY_MS = 150;
const IMAGE_RETRY_DELAY_MS = 3000;
const streamView = $('stream');
let streamRounds = [''];
let streamChars = 0;
let pendingStreamText = '';
let streamNeedsRebuild = false;
let streamRenderFrame = 0;
let streamAutoFollow = true;
let stateRefreshTimer = 0;
let stateRefreshInFlight = false;
let stateRefreshQueued = false;
let eventSource = null;
let streamSource = null;
let eventReconnectTimer = 0;
let streamReconnectTimer = 0;

function esc(value) {
  return String(value ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}
function notify(message, target = 'toast', kind = '') {
  const el = $(target);
  if (!el) return;
  el.textContent = message || '';
  el.className = target === 'toast' ? `toast ${kind}` : `notice ${kind}`;
  if (target === 'toast' && message) {
    clearTimeout(el._timer);
    el._timer = setTimeout(() => { el.textContent = ''; }, 4500);
  }
}
async function api(path, options = {}) {
  const response = await fetch(path, { ...options, headers: { 'Content-Type': 'application/json', ...(options.headers || {}) } });
  const body = await response.json().catch(() => ({}));
  if (!response.ok || (body.code !== undefined && body.code !== 0)) {
    const error = new Error(body.msg || body.error || response.statusText || `HTTP ${response.status}`);
    error.status = response.status;
    error.body = body;
    throw error;
  }
  return body.data !== undefined ? body.data : body;
}
const commandRoutes = {
  start: '/api/v2/commands/start',
  continue: '/api/v2/commands/continue',
  steer: '/api/v2/commands/steer',
  import: '/api/v2/commands/import',
  imitate: '/api/v2/commands/imitate',
  writingRules: '/api/v2/commands/writing-rules',
  pause: '/api/v2/commands/pause',
  abort: '/api/v2/commands/abort',
};
const UI_TEXT = {
  modelNotConfigured: '模型未配置', ready: '就绪', status: '状态', events: '事件',
  streamingOutput: '流式输出', details: '详情', unitImagePreview: '单元图片预览',
  chapter: '章节', untitled: '未命名', none: '暂无', work: '作品', agents: '智能体',
  outline: '大纲', premise: '故事前提', characters: '角色', event: '事件', error: '错误',
  resultsHere: '结果将在这里显示', noOutputs: '暂无输出', output: '输出', image: '图片',
  file: '文件', runTest: '运行测试', idle: '空闲', run: '运行', cancel: '取消', retry: '重试',
  noWorkflow: '未选择工作流', prompt: '提示词', comfyui: 'ComfyUI', latestOutput: '最新输出',
};
function ui(key, fallback = key) { return UI_TEXT[key] || fallback; }

function renderState(state = {}) {
  currentState = state;
  syncWelcomeState(state);
  $('model').textContent = [state.Provider, state.ModelName, state.Style].filter(Boolean).join(' / ') || ui('modelNotConfigured');
  $('status').textContent = state.StatusLabel || ui('ready');
  $('status').className = `status ${state.IsRunning || state.Exclusive ? 'running' : ''}`;
  $('pause').textContent = (state.IsRunning || state.Exclusive) ? '暂停' : '继续';
  $('pause').disabled = !state.IsRunning && !state.Exclusive && (!state.Phase || state.Phase === 'complete');
  const rows = [['运行状态', state.RuntimeState], ['占用', state.Exclusive], ['阶段', state.Phase], ['流程', state.Flow], [ui('chapter'), state.CurrentChapter], ['完成进度', `${state.CompletedCount ?? 0}/${state.TotalChapters ?? 0}`], ['字数', state.TotalWordCount], ['上下文', `${state.ContextTokens || 0}/${state.ContextWindow || 0}`], ['费用', `$${Number(state.TotalCostUSD || 0).toFixed(4)}`]];
  $('state').innerHTML = rows.map(([key, value]) => `<dt>${esc(key)}</dt><dd>${esc(value || '-')}</dd>`).join('');
  const chapters = (state.Outline || []).map((chapter) => `<div class="chapter-row ${chapter.Chapter === state.CurrentChapter ? 'chapter-current' : ''}"><span>${ui('chapter')} ${esc(chapter.Chapter)}</span><strong>${esc(chapter.Title || ui('untitled'))}</strong><small>${esc(chapter.CoreEvent || '')}</small></div>`).join('');
  $('detail').innerHTML = `<h3>${ui('work')}</h3><p>${esc(state.NovelName || ui('untitled'))}</p><h3>${ui('agents')}</h3><p>${esc((state.Agents || []).map((a) => a.Name || a.Role).filter(Boolean).join(', ') || ui('none'))}</p><h3>${ui('outline')}</h3><div class="chapters">${chapters || `<p>${ui('none')}</p>`}</div><h3>${ui('premise')}</h3><p>${esc(state.Premise || ui('none'))}</p><h3>${ui('characters')}</h3><p>${esc((state.Characters || []).join(', ') || ui('none'))}</p>`;
  updateUnitImage(state);
}
async function refresh() {
  try {
    const data = await api('/api/v2/state');
    welcomeWorkspaceID = String(data.workspace_id || data.dir || '').trim();
    renderState(data.snapshot || data);
  } catch (error) { notify(`状态刷新失败：${error.message}`, 'toast', 'error'); }
}
function scheduleRefresh() {
  stateRefreshQueued = true;
  if (stateRefreshTimer || stateRefreshInFlight) return;
  stateRefreshTimer = window.setTimeout(runScheduledRefresh, STATE_REFRESH_DELAY_MS);
}
async function runScheduledRefresh() {
  stateRefreshTimer = 0;
  if (!stateRefreshQueued || stateRefreshInFlight) return;
  stateRefreshQueued = false;
  stateRefreshInFlight = true;
  try { await refresh(); }
  finally {
    stateRefreshInFlight = false;
    if (stateRefreshQueued) scheduleRefresh();
  }
}
function isNearBottom(element, threshold = 72) {
  return element.scrollHeight - element.scrollTop - element.clientHeight <= threshold;
}
function appendEvent(event, scroll = true) {
  const follow = scroll && isNearBottom(events);
  const element = document.createElement('div');
  element.className = event.Level === 'error' ? 'event-error' : event.Level === 'warn' ? 'event-warn' : '';
  element.innerHTML = `<span class="event-time">${esc(new Date(event.Time || event.time || Date.now()).toLocaleTimeString())}</span><strong>${esc(event.Category || event.category || 'EVENT')}</strong> ${esc(event.Summary || event.summary || event.Detail || event.detail || '')}`;
  events.appendChild(element);
  while (events.childElementCount > MAX_EVENT_ITEMS) events.firstElementChild.remove();
  if (follow) events.scrollTop = events.scrollHeight;
}
function trimStreamHistory() {
  while (streamRounds.length > MAX_STREAM_ROUNDS) {
    streamChars -= streamRounds.shift().length;
    streamNeedsRebuild = true;
  }
  while (streamChars > MAX_STREAM_CHARS && streamRounds.length > 1) {
    streamChars -= streamRounds.shift().length;
    streamNeedsRebuild = true;
  }
  if (streamChars > MAX_STREAM_CHARS) {
    const last = streamRounds[0];
    streamRounds[0] = last.slice(-MAX_STREAM_CHARS);
    streamChars = streamRounds[0].length;
    streamNeedsRebuild = true;
  }
}
function queueStreamPayload(payload = {}) {
  if (payload.clear) {
    streamRounds.push('');
    pendingStreamText += STREAM_SEPARATOR;
  } else {
    const delta = String(payload.delta || '');
    if (!delta) return;
    streamRounds[streamRounds.length - 1] += delta;
    streamChars += delta.length;
    pendingStreamText += delta;
  }
  trimStreamHistory();
  if (!streamRenderFrame) {
    streamRenderFrame = window.requestAnimationFrame(() => {
      streamRenderFrame = 0;
      renderPendingStream();
    });
  }
}
function renderPendingStream() {
  if (!pendingStreamText && !streamNeedsRebuild) return;
  if (streamNeedsRebuild) {
    streamView.textContent = streamRounds.join(STREAM_SEPARATOR);
  } else if (pendingStreamText) {
    let textNode = streamView.firstChild;
    if (!textNode) {
      textNode = document.createTextNode('');
      streamView.appendChild(textNode);
    }
    if (textNode.nodeType === Node.TEXT_NODE && !textNode.nextSibling) textNode.appendData(pendingStreamText);
    else streamView.textContent = streamRounds.join(STREAM_SEPARATOR);
  }
  pendingStreamText = '';
  streamNeedsRebuild = false;
  if (streamAutoFollow) streamView.scrollTop = streamView.scrollHeight;
}
function flushStreamRender() {
  if (streamRenderFrame) window.cancelAnimationFrame(streamRenderFrame);
  streamRenderFrame = 0;
  renderPendingStream();
}
async function replay() {
  try {
    const items = await api('/api/v2/replay');
    for (const item of items || []) {
      if (item.kind === 'ui_event') appendEvent({ Time: item.time, Category: item.category, Summary: item.summary }, false);
      if (item.kind === 'stream_clear') queueStreamPayload({ clear: true });
      if (item.kind === 'stream_delta') queueStreamPayload({ delta: item.payload?.delta || '' });
    }
    events.scrollTop = events.scrollHeight;
    flushStreamRender();
  } catch (_) { /* replay is optional */ }
}
function connectEvents() {
  window.clearTimeout(eventReconnectTimer);
  eventSource?.close();
  const source = new EventSource('/api/v2/events');
  eventSource = source;
  source.onmessage = (event) => {
    try {
      const payload = JSON.parse(event.data);
      appendEvent(payload);
      if ((payload.type || payload.Type || payload.Category || '').toString().startsWith('comfyui.job')) {
        window.ComfyUI?.onJobEvent(payload.data || payload.Payload || payload);
      }
    } catch (_) { /* Ignore malformed events and keep the stream alive. */ }
    scheduleRefresh();
  };
  source.onerror = () => {
    if (eventSource !== source) return;
    source.close();
    eventSource = null;
    eventReconnectTimer = window.setTimeout(connectEvents, 2500);
  };
}
function connectStream() {
  window.clearTimeout(streamReconnectTimer);
  streamSource?.close();
  const source = new EventSource('/api/v2/stream');
  streamSource = source;
  source.onmessage = (event) => {
    try { queueStreamPayload(JSON.parse(event.data)); }
    catch (_) { /* Ignore malformed deltas and keep the stream alive. */ }
  };
  source.onerror = () => {
    if (streamSource !== source) return;
    source.close();
    streamSource = null;
    streamReconnectTimer = window.setTimeout(connectStream, 2500);
  };
}
function connect() {
  connectEvents();
  connectStream();
}
async function command(name, body = {}) {
  const path = commandRoutes[name];
  if (!path) throw new Error(`Unknown command: ${name}`);
  try { await api(path, { method: 'POST', body: JSON.stringify(body) }); await refresh(); return true; }
  catch (error) { appendEvent({ Time: Date.now(), Category: 'ERROR', Level: 'error', Summary: error.message }); return false; }
}
function welcomeIsOpen() { return document.documentElement.classList.contains('welcome-pending'); }
function welcomeStorageKey() { return welcomeWorkspaceID ? `${WELCOME_KEY_PREFIX}${welcomeWorkspaceID}` : ''; }
function closeWelcome() {
  const key = welcomeStorageKey();
  try { if (key) localStorage.setItem(key, 'seen'); } catch (_) { /* The session can still continue without storage. */ }
  document.documentElement.classList.remove('welcome-pending');
  document.documentElement.classList.add('welcome-seen');
  window.requestAnimationFrame(() => $('prompt')?.focus());
}
function syncWelcomeState(state = {}) {
  if (!welcomeStateSynced && welcomeStorageKey()) {
    welcomeStateSynced = true;
    try {
      if (localStorage.getItem(welcomeStorageKey()) === 'seen') {
        closeWelcome();
        return;
      }
    } catch (_) { /* Keep the welcome screen when browser storage is unavailable. */ }
  }
  if (!welcomeIsOpen()) return;
  const hasNovel = Boolean(state.NovelName || state.Phase);
  $('welcome-new').hidden = hasNovel;
  $('welcome-existing').hidden = !hasNovel;
  if (hasNovel) $('welcome-novel-name').textContent = state.NovelName || '未命名作品';
}
async function startFromWelcome() {
  const input = $('welcome-prompt');
  const text = input.value.trim();
  if (!text) {
    $('welcome-error').textContent = '请先输入小说需求。';
    input.focus();
    return;
  }
  $('welcome-error').textContent = '';
  $('welcome-start').disabled = true;
  $('welcome-start').textContent = '正在启动...';
  const started = await command('start', { prompt: text });
  $('welcome-start').disabled = false;
  $('welcome-start').textContent = '开始创作';
  if (started) closeWelcome();
  else $('welcome-error').textContent = '启动失败，请检查工作台事件中的错误信息后重试。';
}
function updateUnitImage(state) {
  const image = $('unit-image');
  const placeholder = $('image-placeholder');
  if (!state.CurrentChapter || !state.CurrentUnit) {
    image.hidden = true;
    placeholder.hidden = false;
    return;
  }
  const src = `/api/v2/units/${encodeURIComponent(state.CurrentChapter)}/${encodeURIComponent(state.CurrentUnit)}/image`;
  const now = Date.now();
  const sameUnit = image.dataset.unitSrc === src;
  const lastAttempt = Number(image.dataset.lastAttempt || 0);
  if (sameUnit && (!image.hidden || now - lastAttempt < IMAGE_RETRY_DELAY_MS)) return;
  if (!sameUnit) {
    image.hidden = true;
    placeholder.hidden = false;
  }
  image.dataset.unitSrc = src;
  image.dataset.lastAttempt = String(now);
  image.onload = () => { image.hidden = false; $('image-placeholder').hidden = true; };
  image.onerror = () => { image.hidden = true; $('image-placeholder').hidden = false; };
  image.src = src;
}
function showView(name) {
  document.querySelectorAll('.view').forEach((view) => view.classList.toggle('active-view', view.id === name));
  document.querySelectorAll('.tab').forEach((tab) => tab.classList.toggle('active', tab.dataset.view === name));
  if (name === 'galgame') window.Galgame?.load();
  if (name === 'api-settings') window.ModelSettings?.load();
  if (name === 'app-settings') window.Settings?.load();
  if (name === 'prompts') window.Prompts?.load();
  if (name === 'comfyui') window.ComfyUI?.load();
}
document.querySelectorAll('.tab').forEach((tab) => { tab.onclick = () => showView(tab.dataset.view); });

$('send').onclick = () => { const text = $('prompt').value.trim(); if (!text) return; const fresh = !currentState || (!currentState.NovelName && !currentState.Phase); const name = fresh ? 'start' : currentState.IsRunning ? 'steer' : 'continue'; command(name, fresh ? { prompt: text } : { text }); $('prompt').value = ''; };
$('pause').onclick = () => command(currentState?.IsRunning ? 'pause' : 'continue');
async function exportBookEPUB() {
  const button = qs('[data-action="export-book"]');
  if (button) button.disabled = true;
  try {
    const response = await fetch('/api/v2/export', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' });
    if (!response.ok) {
      const body = await response.json().catch(() => ({}));
      throw new Error(body.msg || response.statusText || `HTTP ${response.status}`);
    }
    const blob = await response.blob();
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    const encodedName = response.headers.get('Content-Disposition')?.match(/filename\*=UTF-8''([^;]+)/i)?.[1];
    link.download = encodedName ? decodeURIComponent(encodedName.replace(/\+/g, ' ')) : 'novel.epub';
    document.body.appendChild(link);
    link.click();
    link.remove();
    URL.revokeObjectURL(url);
    notify('EPUB 已导出，可在手机阅读器中打开。', 'toast', 'success');
  } catch (error) {
    notify(`导出失败：${error.message}`, 'toast', 'error');
  } finally {
    if (button) button.disabled = false;
  }
}
const controls = qs('.controls');
if (controls && !qs('[data-action="export-book"]')) {
  const button = document.createElement('button');
  button.type = 'button';
  button.className = 'secondary';
  button.dataset.action = 'export-book';
  button.textContent = '导出 EPUB';
  controls.insertBefore(button, qs('#reader-open') || null);
  button.addEventListener('click', exportBookEPUB);
}
$('prompt').addEventListener('keydown', (event) => { if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); $('send').click(); } });
streamView.addEventListener('scroll', () => { streamAutoFollow = isNearBottom(streamView); }, { passive: true });
window.addEventListener('beforeunload', () => {
  eventSource?.close();
  streamSource?.close();
  window.clearTimeout(eventReconnectTimer);
  window.clearTimeout(streamReconnectTimer);
});
$('welcome-skip')?.addEventListener('click', closeWelcome);
$('welcome-enter')?.addEventListener('click', closeWelcome);
$('welcome-start')?.addEventListener('click', startFromWelcome);
$('welcome-prompt')?.addEventListener('keydown', (event) => {
  if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); startFromWelcome(); }
});
document.querySelectorAll('[data-welcome-example]').forEach((button) => {
  button.addEventListener('click', () => {
    $('welcome-prompt').value = button.dataset.welcomeExample || '';
    $('welcome-error').textContent = '';
    $('welcome-prompt').focus();
  });
});
if (welcomeIsOpen()) window.requestAnimationFrame(() => $('welcome-prompt')?.focus());

refresh();
replay();
connect();
