/* Web workbench client. The server remains the source of truth for runtime state. */
const $ = (id) => document.getElementById(id);
const qs = (selector) => document.querySelector(selector);
const events = $('events');
let currentState = null;
const WELCOME_KEY_PREFIX = 'ainovel.web.welcome.v2.';
let welcomeWorkspaceID = '';
let welcomeStateSynced = false;
let currentWorkspaceName = '';
let workspaceItems = [];
const MAX_EVENT_ITEMS = 500;
const MAX_STREAM_ROUNDS = 32;
const MAX_STREAM_CHARS = 256 * 1024;
const STREAM_SEPARATOR = '\n\n';
const STATE_REFRESH_DELAY_MS = 150;
const IMAGE_RETRY_DELAY_MS = 3000;
const expandedChapters = new Set();
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
  if (target && target !== 'toast') {
    const inline = $(target);
    if (inline) {
      inline.textContent = '';
      inline.className = 'notice';
    }
  }
  const toast = $('toast');
  if (!toast) return;
  const text = String(message || '');
  if (!text) return;
  toast.textContent = text;
  toast.className = `toast ${kind}`.trim();
  clearTimeout(toast._timer);
  void toast.offsetWidth;
  toast.classList.add('toast-pop');
  toast._timer = setTimeout(() => {
    toast.textContent = '';
    toast.className = 'toast';
  }, 4500);
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
  reopen: '/api/v2/commands/reopen',
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
function formatExclusive(state = {}) {
  return state.Exclusive || '空闲';
}
function formatContext(state = {}) {
  const window = Number(state.ContextWindow || state.ModelContextWindow || 0);
  const tokens = Number(state.ContextTokens || 0);
  if (!window && !tokens) return '-';
  const percent = window > 0 ? Math.round(Number(state.ContextPercent) || (tokens / window * 100)) : 0;
  return window ? `${tokens}/${window}（${percent}%）` : String(tokens);
}
function formatCost(state = {}) {
  const cost = Number(state.TotalCostUSD || 0);
  if (!cost && Number(state.MissingAssistantUsage || 0) > 0) return '未统计';
  return `$${cost.toFixed(4)}`;
}

function renderState(state = {}) {
  currentState = state;
  syncWelcomeState(state);
  $('model').textContent = [state.Provider, state.ModelName, state.Style].filter(Boolean).join(' / ') || ui('modelNotConfigured');
  $('status').textContent = state.StatusLabel || ui('ready');
  $('status').className = `status ${state.IsRunning || state.Exclusive ? 'running' : ''}`;
  const busy = Boolean(state.IsRunning || state.Exclusive);
  const switcher = $('workspace-current');
  if (switcher) {
    switcher.textContent = currentWorkspaceName || '未选择工作区';
    switcher.disabled = busy;
    switcher.title = busy ? '写作或独占作业进行中，请先暂停再切换工作区' : '切换工作区';
  }
  const complete = state.Phase === 'complete';
  const novel = Boolean(state.NovelName || state.Phase);
  const completed = Number(state.CompletedCount || 0);
  $('pause').textContent = busy ? '暂停' : '继续';
  $('pause').disabled = !busy && (!state.Phase || complete);
  $('reopen-open').hidden = !complete || busy;
  $('action-other').disabled = Boolean(state.Exclusive);
  $('action-replan').disabled = !novel || complete || Boolean(state.Exclusive);
  $('action-rewrite').disabled = !novel || complete || completed < 1 || Boolean(state.Exclusive);
  const rows = [['运行状态', state.RuntimeState], ['占用', formatExclusive(state)], ['阶段', state.Phase], ['流程', state.Flow], [ui('chapter'), state.CurrentChapter], ['完成进度', `${state.CompletedCount ?? 0}/${state.TotalChapters ?? 0}`], ['字数', state.TotalWordCount], ['上下文', formatContext(state)], ['费用', formatCost(state)]];
  $('state').innerHTML = rows.map(([key, value]) => `<dt>${esc(key)}</dt><dd>${esc(value || '-')}</dd>`).join('');
  const chapters = (state.Outline || []).map((chapter) => {
    const number = Number(chapter.Chapter);
    const open = expandedChapters.has(number);
    const extras = [];
    if (chapter.Hook) extras.push(`<p class="chapter-hook">${esc(chapter.Hook)}</p>`);
    if (Array.isArray(chapter.Scenes) && chapter.Scenes.length) extras.push(`<ul class="chapter-scenes">${chapter.Scenes.map((scene) => `<li>${esc(scene)}</li>`).join('')}</ul>`);
    return `<button type="button" class="chapter-row${chapter.Chapter === state.CurrentChapter ? ' chapter-current' : ''}${open ? ' chapter-open' : ''}" data-chapter="${esc(number)}" aria-expanded="${open ? 'true' : 'false'}"><span>${ui('chapter')} ${esc(number)}</span><strong>${esc(chapter.Title || ui('untitled'))}</strong><small>${esc(chapter.CoreEvent || '')}</small>${extras.join('')}</button>`;
  }).join('');
  $('detail').innerHTML = `<h3>${ui('work')}</h3><p>${esc(state.NovelName || ui('untitled'))}</p><h3>${ui('agents')}</h3><p>${esc((state.Agents || []).map((a) => a.Name || a.Role).filter(Boolean).join(', ') || ui('none'))}</p><h3>${ui('outline')}</h3><div class="chapters">${chapters || `<p>${ui('none')}</p>`}</div><h3>${ui('premise')}</h3><p>${esc(state.Premise || ui('none'))}</p><h3>${ui('characters')}</h3><p>${esc((state.Characters || []).join(', ') || ui('none'))}</p>`;
  updateUnitImage(state);
}
async function refresh() {
  try {
    const data = await api('/api/v2/state');
    welcomeWorkspaceID = String(data.workspace_id || data.dir || '').trim();
    currentWorkspaceName = String(data.workspace || '').trim();
    renderState(data.snapshot || data);
    await renderWorkspacePickers();
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
    const kind = String(payload.kind || '');
    const delta = kind === 'tool' && payload.tool ? `\n${String(payload.tool)}\n` : String(payload.delta || payload.text || '');
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
      if (item.kind === 'stream_delta') queueStreamPayload(item.payload || {});
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
      if ((payload.type || payload.Type || payload.Category || '').toString().startsWith('image.job')) {
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
  window.requestAnimationFrame(() => $('pause')?.focus());
}
function syncWelcomeState(state = {}) {
  if (currentWorkspaceName && !welcomeStateSynced && welcomeStorageKey()) {
    welcomeStateSynced = true;
    try {
      if (localStorage.getItem(welcomeStorageKey()) === 'seen') {
        closeWelcome();
        return;
      }
    } catch (_) { /* Keep the welcome screen when browser storage is unavailable. */ }
  }
  if (!welcomeIsOpen()) return;
  const hasWorkspace = Boolean(currentWorkspaceName);
  const hasNovel = Boolean(state.NovelName || state.Phase);
  $('welcome-new').hidden = !hasWorkspace || hasNovel;
  $('welcome-existing').hidden = !hasWorkspace || !hasNovel;
  if (hasNovel) $('welcome-novel-name').textContent = state.NovelName || '未命名作品';
}
function workspaceBusy(state = currentState) {
  return Boolean(state?.IsRunning || state?.Exclusive);
}
function workspaceMeta(item) {
  const bits = [];
  if (item.novel_name) bits.push(item.novel_name);
  if (item.phase) bits.push(item.phase);
  if (item.completed) bits.push(`已完成 ${item.completed} 章`);
  return bits.join(' · ');
}
function renderWorkspaceList(target, items, selected) {
  if (!target) return;
  if (!items.length) {
    target.innerHTML = '<p class="welcome-workspace-empty">还没有工作区。新建一个后即可开始创作。</p>';
    return;
  }
  target.innerHTML = items.map((item) => {
    const name = item.name || '';
    const label = item.display || name;
    const meta = workspaceMeta(item);
    const active = selected && name === selected ? ' active' : '';
    return `<button type="button" class="${target.id === 'workspace-menu-list' ? 'workspace-menu-item' : 'welcome-workspace-item'}${active}" data-workspace="${esc(name)}"><strong>${esc(label)}</strong>${meta ? `<small>${esc(meta)}</small>` : ''}</button>`;
  }).join('');
}
async function loadWorkspaces() {
  const data = await api('/api/v2/workspaces');
  workspaceItems = Array.isArray(data?.items) ? data.items : [];
  if (data?.current) currentWorkspaceName = String(data.current);
}
async function renderWorkspacePickers() {
  try { await loadWorkspaces(); }
  catch (error) { notify(`读取工作区失败：${error.message}`, 'toast', 'error'); return; }
  renderWorkspaceList($('welcome-workspace-list'), workspaceItems, currentWorkspaceName);
  renderWorkspaceList($('workspace-menu-list'), workspaceItems, currentWorkspaceName);
  const switcher = $('workspace-current');
  if (switcher) switcher.textContent = currentWorkspaceName || '未选择工作区';
}
function resetWorkspaceViews() {
  events.innerHTML = '';
  streamRounds = [''];
  streamChars = 0;
  pendingStreamText = '';
  streamNeedsRebuild = true;
  flushStreamRender();
  connect();
  window.Galgame?.resetForWorkspace?.();
  window.GalgamePlay?.resetForWorkspace?.();
  if ($('galgame')?.classList.contains('active-view')) window.Galgame?.load();
}
async function openWorkspace(name, { notifySuccess = true } = {}) {
  const trimmed = String(name || '').trim();
  if (!trimmed) throw new Error('请先选择工作区');
  if (workspaceBusy()) throw new Error('写作或独占作业进行中，请先暂停再切换工作区');
  const data = await api('/api/v2/workspaces/open', { method: 'POST', body: JSON.stringify({ name: trimmed }) });
  currentWorkspaceName = String(data.workspace || trimmed);
  welcomeWorkspaceID = String(data.workspace_id || '').trim();
  welcomeStateSynced = false;
  resetWorkspaceViews();
  renderState(data.snapshot || {});
  await renderWorkspacePickers();
  try { await replay(); } catch (_) { /* replay is optional after a switch */ }
  if (notifySuccess) notify(`已打开工作区「${currentWorkspaceName}」。`, 'toast', 'success');
}
async function createAndOpenWorkspace(name) {
  const trimmed = String(name || '').trim();
  if (!trimmed) throw new Error('请输入工作区名称');
  await api('/api/v2/workspaces', { method: 'POST', body: JSON.stringify({ name: trimmed }) });
  await openWorkspace(trimmed);
}
async function startFromWelcome() {
  if (!currentWorkspaceName) {
    $('welcome-error').textContent = '请先选择或新建工作区。';
    $('welcome-workspace-name')?.focus();
    return;
  }
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
function closeWorkspaceMenu() {
  const menu = $('workspace-menu');
  const button = $('workspace-current');
  if (menu) menu.hidden = true;
  if (button) button.setAttribute('aria-expanded', 'false');
}
function toggleWorkspaceMenu() {
  const menu = $('workspace-menu');
  const button = $('workspace-current');
  if (!menu || !button || button.disabled) return;
  menu.hidden = !menu.hidden;
  button.setAttribute('aria-expanded', menu.hidden ? 'false' : 'true');
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
  if (name === 'image-generation') window.ImageGeneration?.load();
}
document.querySelectorAll('.tab').forEach((tab) => { tab.onclick = () => showView(tab.dataset.view); });
$('detail')?.addEventListener('click', (event) => {
  const row = event.target.closest('.chapter-row');
  if (!row || !$('detail').contains(row)) return;
  const number = Number(row.dataset.chapter);
  if (!Number.isInteger(number) || number < 1) return;
  if (expandedChapters.has(number)) expandedChapters.delete(number);
  else expandedChapters.add(number);
  const open = expandedChapters.has(number);
  row.classList.toggle('chapter-open', open);
  row.setAttribute('aria-expanded', open ? 'true' : 'false');
});

$('pause').onclick = () => command(currentState?.IsRunning || currentState?.Exclusive ? 'pause' : 'continue');
const ACTION_KINDS = {
  other: { title: '其他指令', help: '输入自由指令。尚未开书时会作为创作需求提交；写作中作为干预；暂停时作为继续说明。', textLabel: '指令', placeholder: '例如：下一章放慢节奏，先写主角回家的日常', submitLabel: '发送', number: false },
  replan: { title: '重新规划', help: '从指定章节起修订后续大纲，不会回改该章之前已写正文。', textLabel: '规划方向', placeholder: '例如：从这里起主角转入暗线，节奏放缓，先补人物关系', submitLabel: '提交规划', number: true, numberLabel: '从第几章起' },
  rewrite: { title: '重写', help: '当前不会自动回改已写正文，只会把要求交给后续规划。完结作品请用「完结后续写」。', textLabel: '重写原因', placeholder: '例如：第 3 章对话过于说明，希望改成更含蓄的冲突', submitLabel: '提交要求', number: true, numberLabel: '章节号' },
};
let currentAction = '';
function openActionModal(kind) {
  const spec = ACTION_KINDS[kind];
  if (!spec) return;
  currentAction = kind;
  $('action-title').textContent = spec.title;
  $('action-help').textContent = spec.help;
  $('action-text-label').textContent = spec.textLabel;
  $('action-text').placeholder = spec.placeholder;
  $('action-text').value = '';
  $('action-submit').textContent = spec.submitLabel;
  notify('', 'action-message');
  const wrap = $('action-number-wrap');
  wrap.hidden = !spec.number;
  if (spec.number) {
    $('action-number-label').textContent = spec.numberLabel;
    const completed = Number(currentState?.CompletedCount || 0);
    $('action-number').value = String(kind === 'replan' ? Math.max(1, completed + 1) : (currentState?.CurrentChapter || completed || 1));
  }
  $('action-modal').hidden = false;
  (spec.number ? $('action-number') : $('action-text')).focus();
}
function closeActionModal() {
  $('action-modal').hidden = true;
  notify('', 'action-message');
  currentAction = '';
}
function buildActionText(kind, number, text) {
  if (kind === 'replan') return `从第${number}章起重新规划后续大纲，不要回改第${number}章之前已写正文。新的规划方向：${text}`;
  if (kind === 'rewrite') return `请针对第${number}章调整后续规划：${text}。不要自动回改已写正文。`;
  return text;
}
async function submitActionModal() {
  const spec = ACTION_KINDS[currentAction];
  if (!spec) return;
  const text = $('action-text').value.trim();
  if (!text) {
    notify('请填写内容。', 'action-message', 'error');
    $('action-text').focus();
    return;
  }
  let number = 0;
  if (spec.number) {
    number = Number($('action-number').value);
    if (!Number.isInteger(number) || number < 1) {
      notify('请填写有效章节号。', 'action-message', 'error');
      $('action-number').focus();
      return;
    }
  }
  const payload = buildActionText(currentAction, number, text);
  const fresh = !currentState || (!currentState.NovelName && !currentState.Phase);
  if (currentAction !== 'other' && fresh) {
    notify('请先开始创作。', 'action-message', 'error');
    return;
  }
  const name = fresh ? 'start' : currentState.IsRunning ? 'steer' : 'continue';
  $('action-submit').disabled = true;
  try {
    const ok = await command(name, fresh ? { prompt: payload } : { text: payload });
    if (ok) {
      closeActionModal();
      notify(`${spec.title}已提交。`, 'toast', 'success');
    } else {
      notify('提交失败，请查看事件栏。', 'action-message', 'error');
    }
  } finally {
    $('action-submit').disabled = false;
  }
}
$('action-other')?.addEventListener('click', () => openActionModal('other'));
$('action-replan')?.addEventListener('click', () => openActionModal('replan'));
$('action-rewrite')?.addEventListener('click', () => openActionModal('rewrite'));
document.querySelectorAll('[data-action="close-action"]').forEach((element) => element.addEventListener('click', closeActionModal));
$('action-submit')?.addEventListener('click', submitActionModal);
function closeReopen() {
  $('reopen-modal').hidden = true;
  $('reopen-message').textContent = '';
}
async function submitReopen() {
  const direction = $('reopen-direction').value.trim();
  if (!direction) {
    notify('请填写续写方向。', 'reopen-message', 'error');
    $('reopen-direction').focus();
    return;
  }
  $('reopen-submit').disabled = true;
  try {
    await api(commandRoutes.reopen, { method: 'POST', body: JSON.stringify({ direction }) });
    notify('作品已重开，创作正在恢复。', 'toast', 'success');
    closeReopen();
    await refresh();
  } catch (error) {
    notify(`重开失败：${error.message}`, 'reopen-message', 'error');
  } finally {
    $('reopen-submit').disabled = false;
  }
}
$('reopen-open')?.addEventListener('click', () => {
  $('reopen-modal').hidden = false;
  $('reopen-direction').value = '';
  $('reopen-direction').focus();
});
document.querySelectorAll('[data-action="close-reopen"]').forEach((element) => element.addEventListener('click', closeReopen));
$('reopen-submit')?.addEventListener('click', submitReopen);
function closeExportMenu() {
  const menu = $('export-options');
  if (!menu || menu.hidden) return;
  menu.hidden = true;
  $('export-open')?.setAttribute('aria-expanded', 'false');
}
function toggleExportMenu() {
  const menu = $('export-options');
  const button = $('export-open');
  if (!menu || !button || button.disabled) return;
  menu.hidden = !menu.hidden;
  button.setAttribute('aria-expanded', menu.hidden ? 'false' : 'true');
}
async function exportBook(format) {
  const kind = format === 'txt' ? 'txt' : 'epub';
  const button = $('export-open');
  closeExportMenu();
  if (button) button.disabled = true;
  try {
    const response = await fetch('/api/v2/export', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ format: kind }) });
    if (!response.ok) {
      const body = await response.json().catch(() => ({}));
      throw new Error(body.msg || response.statusText || `HTTP ${response.status}`);
    }
    const blob = await response.blob();
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    const encodedName = response.headers.get('Content-Disposition')?.match(/filename\*=UTF-8''([^;]+)/i)?.[1];
    link.download = encodedName ? decodeURIComponent(encodedName.replace(/\+/g, ' ')) : (kind === 'txt' ? 'novel.txt' : 'novel.epub');
    document.body.appendChild(link);
    link.click();
    link.remove();
    URL.revokeObjectURL(url);
    notify(kind === 'txt' ? 'TXT 已导出。' : 'EPUB 已导出，可在手机阅读器中打开。', 'toast', 'success');
  } catch (error) {
    notify(`导出失败：${error.message}`, 'toast', 'error');
  } finally {
    if (button) button.disabled = false;
  }
}
$('export-open')?.addEventListener('click', (event) => {
  event.stopPropagation();
  toggleExportMenu();
});
document.querySelectorAll('[data-export]').forEach((element) => {
  element.addEventListener('click', (event) => {
    event.stopPropagation();
    exportBook(element.getAttribute('data-export'));
  });
});
document.addEventListener('click', (event) => {
  const wrap = $('export-menu');
  if (wrap && !wrap.contains(event.target)) closeExportMenu();
  const switcher = $('workspace-switcher');
  if (switcher && !switcher.contains(event.target)) closeWorkspaceMenu();
});
document.addEventListener('keydown', (event) => {
  if (event.key === 'Escape') {
    closeExportMenu();
    closeWorkspaceMenu();
  }
});
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
$('welcome-workspace-list')?.addEventListener('click', async (event) => {
  const button = event.target.closest('[data-workspace]');
  if (!button) return;
  try {
    await openWorkspace(button.dataset.workspace);
    $('welcome-error').textContent = '';
  } catch (error) {
    $('welcome-error').textContent = error.message;
    notify(error.message, 'toast', 'error');
  }
});
$('welcome-workspace-create')?.addEventListener('click', async () => {
  try {
    await createAndOpenWorkspace($('welcome-workspace-name').value);
    $('welcome-workspace-name').value = '';
    $('welcome-error').textContent = '';
  } catch (error) {
    $('welcome-error').textContent = error.message;
    notify(error.message, 'toast', 'error');
  }
});
$('welcome-workspace-name')?.addEventListener('keydown', (event) => {
  if (event.key === 'Enter') { event.preventDefault(); $('welcome-workspace-create')?.click(); }
});
$('workspace-current')?.addEventListener('click', (event) => {
  event.stopPropagation();
  toggleWorkspaceMenu();
});
$('workspace-menu-list')?.addEventListener('click', async (event) => {
  const button = event.target.closest('[data-workspace]');
  if (!button) return;
  closeWorkspaceMenu();
  try { await openWorkspace(button.dataset.workspace); }
  catch (error) { notify(error.message, 'toast', 'error'); }
});
$('workspace-menu-create')?.addEventListener('click', async () => {
  try {
    await createAndOpenWorkspace($('workspace-menu-name').value);
    $('workspace-menu-name').value = '';
    closeWorkspaceMenu();
  } catch (error) { notify(error.message, 'toast', 'error'); }
});
$('workspace-menu-name')?.addEventListener('keydown', (event) => {
  if (event.key === 'Enter') { event.preventDefault(); $('workspace-menu-create')?.click(); }
});
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
