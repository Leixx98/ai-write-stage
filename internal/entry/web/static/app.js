/* Web workbench client. The server remains the source of truth for runtime state. */
const $ = (id) => document.getElementById(id);
const qs = (selector) => document.querySelector(selector);
const events = $('events');
let currentState = null;
let workflows = [];
let selectedWorkflow = null;
let currentJob = null;
let comfyConfig = {};
let instances = [];
let currentSchema = null;
let fieldValues = {};
let uploadedMedia = [];
let promptPresetDoc = null;
let workflowSettingsDoc = null;
let promptsDirty = false;
let bridgeConfig = null;
let bridgeSchema = null;
let bridgeJob = null;
let bridgePrompterPresets = [];
const WELCOME_KEY = 'ainovel.web.welcome.v1';
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
  $('status').className = `status ${state.IsRunning ? 'running' : ''}`;
  $('pause').textContent = state.IsRunning ? '暂停' : '继续';
  $('pause').disabled = !state.IsRunning && (!state.Phase || state.Phase === 'complete');
  const rows = [['运行状态', state.RuntimeState], ['阶段', state.Phase], ['流程', state.Flow], [ui('chapter'), state.CurrentChapter], ['完成进度', `${state.CompletedCount ?? 0}/${state.TotalChapters ?? 0}`], ['字数', state.TotalWordCount], ['上下文', `${state.ContextTokens || 0}/${state.ContextWindow || 0}`], ['费用', `$${Number(state.TotalCostUSD || 0).toFixed(4)}`]];
  $('state').innerHTML = rows.map(([key, value]) => `<dt>${esc(key)}</dt><dd>${esc(value || '-')}</dd>`).join('');
  const chapters = (state.Outline || []).map((chapter) => `<div class="chapter-row ${chapter.Chapter === state.CurrentChapter ? 'chapter-current' : ''}"><span>${ui('chapter')} ${esc(chapter.Chapter)}</span><strong>${esc(chapter.Title || ui('untitled'))}</strong><small>${esc(chapter.CoreEvent || '')}</small></div>`).join('');
  $('detail').innerHTML = `<h3>${ui('work')}</h3><p>${esc(state.NovelName || ui('untitled'))}</p><h3>${ui('agents')}</h3><p>${esc((state.Agents || []).map((a) => a.Name || a.Role).filter(Boolean).join(', ') || ui('none'))}</p><h3>${ui('outline')}</h3><div class="chapters">${chapters || `<p>${ui('none')}</p>`}</div><h3>${ui('premise')}</h3><p>${esc(state.Premise || ui('none'))}</p><h3>${ui('characters')}</h3><p>${esc((state.Characters || []).join(', ') || ui('none'))}</p>`;
  updateUnitImage(state);
}
async function refresh() {
  try {
    const data = await api('/api/v2/state').catch(() => api('/api/state'));
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
      if ((payload.type || payload.Type || payload.Category || '').toString().startsWith('comfyui.job')) renderJob(payload.data || payload.Payload || payload);
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
function closeWelcome() {
  try { localStorage.setItem(WELCOME_KEY, 'seen'); } catch (_) { /* The session can still continue without storage. */ }
  document.documentElement.classList.remove('welcome-pending');
  document.documentElement.classList.add('welcome-seen');
  window.requestAnimationFrame(() => $('prompt')?.focus());
}
function syncWelcomeState(state = {}) {
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
  if (name === 'comfyui') loadComfyUI();
  if (name === 'api-settings') loadModels();
  if (name === 'prompts') loadPrompts();
  if (name === 'app-settings') loadWorkflowSettingsUI();
}
document.querySelectorAll('.tab').forEach((tab) => { tab.onclick = () => showView(tab.dataset.view); });

async function loadModels() {
  try {
    const data = await api('/api/v2/settings/models');
    $('provider').value = data.DefaultProvider || data.default_provider || '';
    $('model-name').value = data.DefaultModel || data.default_model || '';
    $('role-models').innerHTML = '<p>角色模型覆盖配置读取自项目配置文件。</p>';
  } catch (error) { notify(`模型配置加载失败：${error.message}`, 'models-msg', 'error'); }
}
async function saveWorkflowSettings() {
  const previous = workflowSettingsDoc || {};
  const source = $('import-source').value.trim();
  const reference = $('imitate-reference').value.trim();
  const rules = $('writing-rules').value;
  try {
    workflowSettingsDoc = await api('/api/v2/settings/workflow', { method: 'PUT', body: JSON.stringify({ import_source: source, imitate_reference: reference, writing_rules: rules }) });
    renderWritingRulePresets();
  } catch (error) {
    notify(`设置保存失败：${error.message}`, 'settings-msg', 'error');
    return;
  }
  const actions = [];
  const warnings = [];
  if (rules.trim() && rules !== String(previous.writing_rules || '')) {
    try {
      await api(commandRoutes.writingRules, { method: 'POST', body: JSON.stringify({ preferences: rules }) });
      actions.push('写作要求已生效');
    } catch (error) {
      warnings.push(`写作要求未生效：${error.message}`);
    }
  }
  const importChanged = Boolean(source && source !== String(previous.import_source || '').trim());
  const imitateChanged = Boolean(reference && reference !== String(previous.imitate_reference || '').trim());
  let exclusiveStarted = false;
  if (importChanged) {
    try {
      await api(commandRoutes.import, { method: 'POST', body: JSON.stringify({ source }) });
      actions.push('导入已启动');
      exclusiveStarted = true;
    } catch (error) {
      warnings.push(`导入未启动：${error.message}`);
    }
  }
  if (imitateChanged) {
    if (exclusiveStarted) {
      warnings.push('仿写未启动：导入进行中，两者互斥，请完成后再保存仿写路径');
    } else {
      try {
        await api(commandRoutes.imitate, { method: 'POST', body: JSON.stringify({ reference }) });
        actions.push('仿写已启动');
      } catch (error) {
        warnings.push(`仿写未启动：${error.message}`);
      }
    }
  }
  const parts = [];
  if (actions.length) parts.push(actions.join('，'));
  if (warnings.length) parts.push(warnings.join('；'));
  notify(parts.length ? `设置已保存；${parts.join('；')}` : '设置已保存', 'settings-msg', warnings.length ? 'error' : 'success');
}
async function loadWorkflowSettingsUI() {
  try {
    workflowSettingsDoc = await api('/api/v2/settings/workflow');
    $('import-source').value = workflowSettingsDoc.import_source || '';
    $('imitate-reference').value = workflowSettingsDoc.imitate_reference || '';
    renderWritingRulePresets();
  } catch (error) { notify(`设置加载失败：${error.message}`, 'settings-msg', 'error'); }
}
function renderWritingRulePresets() {
  if (!workflowSettingsDoc) return;
  const select = $('writing-rules-preset');
  const presets = workflowSettingsDoc.writing_rule_presets || {};
  select.innerHTML = Object.keys(presets).map((name) => `<option value="${esc(name)}" ${name === workflowSettingsDoc.active_writing_rules_preset ? 'selected' : ''}>${esc(name)}</option>`).join('');
  $('writing-rules').value = workflowSettingsDoc.writing_rules || presets[workflowSettingsDoc.active_writing_rules_preset]?.text || '';
}
async function writingRuleAction(action, name, overwrite = false) {
  const previous = workflowSettingsDoc || {};
  try {
    const text = $('writing-rules').value;
    workflowSettingsDoc = await api('/api/v2/settings/workflow', { method: 'PUT', body: JSON.stringify({ action, name, text, overwrite }) });
    renderWritingRulePresets();
  } catch (error) {
    notify(`写作要求预设操作失败：${error.message}`, 'settings-msg', 'error');
    return;
  }
  const applied = String(workflowSettingsDoc.writing_rules || '').trim();
  if (applied && applied !== String(previous.writing_rules || '')) {
    try {
      await api(commandRoutes.writingRules, { method: 'POST', body: JSON.stringify({ preferences: applied }) });
      notify('写作要求预设已更新并生效', 'settings-msg', 'success');
      return;
    } catch (error) {
      notify(`写作要求预设已保存，但未生效：${error.message}`, 'settings-msg', 'error');
      return;
    }
  }
  notify('写作要求预设已更新', 'settings-msg', 'success');
}
async function loadPrompts() {
  try {
    promptPresetDoc = await api('/api/v2/settings/prompts');
    const prompts = promptPresetDoc.prompts || {};
    const labels = { architect: '架构师', chapter_planner: '章节规划师', writer: '写作者', editor: '编辑' };
    $('prompt-editors').innerHTML = Object.keys(labels).map((role) => `<label>${labels[role]}<textarea data-prompt="${role}" rows="7">${esc(prompts[role] || '')}</textarea></label>`).join('');
    $('prompt-editors').querySelectorAll('textarea').forEach((field) => field.addEventListener('input', () => { promptsDirty = true; }));
    renderPromptPresetOptions();
    promptsDirty = false;
  } catch (error) { notify(`提示词加载失败：${error.message}`, 'prompts-msg', 'error'); }
}
function renderPromptPresetOptions() {
  if (!promptPresetDoc) return;
  const select = $('prompt-preset');
  select.innerHTML = Object.keys(promptPresetDoc.presets || {}).map((name) => `<option value="${esc(name)}" ${name === promptPresetDoc.active_preset ? 'selected' : ''}>${esc(name)}</option>`).join('');
}
function readPrompts() {
  const prompts = {};
  document.querySelectorAll('[data-prompt]').forEach((field) => { prompts[field.dataset.prompt] = field.value; });
  return prompts;
}
async function savePrompts(name = promptPresetDoc?.active_preset, action = 'save', overwrite = true) {
  try { promptPresetDoc = await api('/api/v2/settings/prompts', { method: 'PUT', body: JSON.stringify({ action, name, source: promptPresetDoc?.active_preset, prompts: readPrompts(), overwrite }) }); renderPromptPresetOptions(); promptsDirty = false; notify('提示词已保存', 'prompts-msg', 'success'); return true; }
  catch (error) { notify(`提示词保存失败：${error.message}`, 'prompts-msg', 'error'); }
  return false;
}
async function activatePromptPreset(name) {
  try {
    promptPresetDoc = await api('/api/v2/settings/prompts', { method: 'PUT', body: JSON.stringify({ action: 'activate', name }) });
    await loadPrompts();
  } catch (error) { notify(`提示词组合切换失败：${error.message}`, 'prompts-msg', 'error'); renderPromptPresetOptions(); }
}

function formConfig(config = {}) {
  comfyConfig = { ...config };
  $('comfy-url').value = config.base_url || '';
  $('comfy-timeout').value = config.timeout_ms ?? 600000;
  $('comfy-poll').value = config.poll_interval_ms ?? 1000;
  $('comfy-client-id').value = config.client_id || 'ainovel-web';
  $('comfy-max-bytes').value = config.max_response_bytes ?? 52428800;
  $('comfy-workflow-id').value = config.workflow_id || '';
  $('comfy-enabled').checked = !!config.enabled;
  $('comfy-strict').checked = config.strict !== false;
}
async function loadComfyUI() { try { formConfig(await api('/api/v2/comfyui/config')); await loadInstances(); await loadWorkflows(); } catch (error) { notify(`ComfyUI 配置加载失败：${error.message}`, 'comfy-msg', 'error'); } }
async function saveComfyUI() {
  const config = { enabled: $('comfy-enabled').checked, base_url: $('comfy-url').value.trim(), timeout_ms: Number($('comfy-timeout').value), poll_interval_ms: Number($('comfy-poll').value), client_id: $('comfy-client-id').value.trim(), max_response_bytes: Number($('comfy-max-bytes').value), strict: $('comfy-strict').checked, workflow_id: $('comfy-workflow-id').value.trim() };
  try { formConfig(await api('/api/v2/comfyui/config', { method: 'PUT', body: JSON.stringify(config) })); notify('连接设置已保存', 'comfy-msg', 'success'); }
  catch (error) { notify(`保存失败：${error.message}`, 'comfy-msg', 'error'); }
}
async function testConnection() {
  const draft = { enabled: $('comfy-enabled').checked, base_url: $('comfy-url').value.trim(), timeout_ms: Number($('comfy-timeout').value), poll_interval_ms: Number($('comfy-poll').value), client_id: $('comfy-client-id').value.trim(), max_response_bytes: Number($('comfy-max-bytes').value), strict: $('comfy-strict').checked, workflow_id: $('comfy-workflow-id').value.trim() };
  try {
    await api('/api/v2/comfyui/test-connection', { method: 'POST', body: JSON.stringify(draft) });
    notify('连接成功，配置尚未保存', 'comfy-msg', 'success');
  } catch (error) {
    const details = error.body?.data || {};
    const suffix = [details.phase && `phase: ${details.phase}`, details.retryable !== undefined && `retryable: ${details.retryable}`].filter(Boolean).join('; ');
    notify(`连接失败：${error.message}${suffix ? `（${suffix}）` : ''}`, 'comfy-msg', 'error');
  }
}
async function loadInstances() {
  try {
    const data = await api('/api/v2/comfyui/instances');
    const settings = data.settings || data;
    instances = data.instances || settings.instances || [];
    if (!Array.isArray(instances)) instances = [];
    if (settings.strategy) $('instance-strategy').value = settings.strategy;
    const defaultID = settings.default_instance_id || data.default_instance_id;
    $('instances-list').innerHTML = instances.length ? instances.map((item) => `<div class="instance-card ${item.health === 'healthy' ? 'healthy' : item.health === 'unhealthy' ? 'unhealthy' : ''}" data-instance-id="${esc(item.id)}"><label><input type="radio" name="default-instance" value="${esc(item.id)}" ${item.id === defaultID ? 'checked' : ''}><strong>${esc(item.name || item.id)}</strong></label><span>${esc(item.base_url || '')}</span><small>${esc(item.health || 'unknown')} · priority ${esc(item.priority ?? 0)} · queue ${esc(item.queue_length ?? '-')}</small></div>`).join('') : '<span class="muted">No instances loaded; the legacy Base URL is still available below.</span>';
  } catch (error) { notify(`Instance list unavailable: ${error.message}`, 'instances-msg', 'error'); }
}
async function refreshInstances() { try { await Promise.all(instances.map((item) => api(`/api/v2/comfyui/instances/${encodeURIComponent(item.id)}/test`, { method: 'POST', body: '{}' }))); await loadInstances(); notify('Instance health refreshed', 'instances-msg', 'success'); } catch (error) { notify(`Health refresh failed: ${error.message}`, 'instances-msg', 'error'); } }
async function saveInstances() { const selected = document.querySelector('input[name="default-instance"]:checked'); try { await api('/api/v2/comfyui/instances', { method: 'PUT', body: JSON.stringify({ settings: { default_instance_id: selected?.value || '', strategy: $('instance-strategy').value }, instances }) }); notify('Instances saved', 'instances-msg', 'success'); } catch (error) { notify(`Instance save failed: ${error.message}`, 'instances-msg', 'error'); } }
function workflowAPI(workflow) { return workflow.api_json || workflow.workflow || {}; }
function renderNodes(workflow) { const apiJSON = workflowAPI(workflow); const ids = Object.keys(apiJSON); $('workflow-node-list').innerHTML = ids.map((id) => { const node = apiJSON[id] || {}; return `<option value="${esc(id)}">${esc(id)} · ${esc(node.class_type || 'node')}</option>`; }).join(''); if (ids[0]) renderNodeInputs(ids[0]); else $('node-inputs').innerHTML = '<span class="muted">No nodes</span>'; }
function renderNodeInputs(id) { const node = workflowAPI(selectedWorkflow)[id] || {}; const inputs = node.inputs || {}; const rows = Object.keys(inputs).map((key) => `<div class="node-input-row"><code>${esc(key)}</code><span>${esc(typeof inputs[key] === 'object' ? JSON.stringify(inputs[key]) : inputs[key])}</span></div>`).join(''); $('node-inputs').innerHTML = `<div class="node-type">${esc(node.class_type || '')}</div>${rows || '<span class="muted">No inputs</span>'}`; }
function inferField(binding, value) { const stableKey = binding.id || binding.key || `${binding.node_id || ''}::${binding.input || ''}`; const name = String(stableKey).toLowerCase(); const type = binding.value_type || binding.type || (typeof value === 'number' ? 'number' : typeof value === 'boolean' ? 'boolean' : 'string'); let control = binding.control; if (!control) control = name.includes('prompt') || name.includes('text') ? 'textarea' : name.includes('seed') || name.includes('steps') || name.includes('width') || name.includes('height') ? 'number' : 'text'; return {...binding, id:stableKey, key:stableKey, control, value_type:type, default:binding.default ?? value ?? ''}; }
function renderDynamicFields(schema, defaults = {}) { currentSchema = schema; const rawFields = schema?.fields || schema || []; const fields = Array.isArray(rawFields) ? rawFields.map((field) => { const normalized = inferField(field, defaults[field.id || field.key]); return {...normalized, default:defaults[normalized.id] ?? normalized.default}; }) : []; fieldValues = {...defaults}; $('dynamic-fields').innerHTML = fields.length ? fields.map((field) => { const id = `field-${field.id.replace(/[^a-zA-Z0-9_-]/g, '-')}`; const value = fieldValues[field.id] ?? field.default ?? ''; let control = `<input id="${esc(id)}" data-field-key="${esc(field.id)}" value="${esc(value)}">`; if (field.control === 'textarea') control = `<textarea id="${esc(id)}" data-field-key="${esc(field.id)}" rows="3">${esc(value)}</textarea>`; if (field.control === 'number' || field.control === 'slider') control = `<input id="${esc(id)}" data-field-key="${esc(field.id)}" type="${field.control === 'slider' ? 'range' : 'number'}" value="${esc(value)}" ${field.min != null ? `min="${field.min}"` : ''} ${field.max != null ? `max="${field.max}"` : ''} ${field.step != null ? `step="${field.step}"` : ''}>`; if (field.control === 'boolean') control = `<input id="${esc(id)}" data-field-key="${esc(field.id)}" type="checkbox" ${value ? 'checked' : ''}>`; if (field.control === 'dropdown') control = `<select id="${esc(id)}" data-field-key="${esc(field.id)}">${(field.options || []).map((option) => `<option ${String(option) === String(value) ? 'selected' : ''}>${esc(option)}</option>`).join('')}</select>`; if (field.control === 'image' || field.control === 'video' || field.control === 'audio' || field.control === 'file') control = `<input id="${esc(id)}" data-field-key="${esc(field.id)}" type="file" accept="${field.control}/*">`; return `<label class="dynamic-field"><span>${esc(field.label || field.id)} <small>${esc(field.source || 'inferred')}</small></span>${control}</label>`; }).join('') : '<span class="muted">No dynamic fields inferred</span>'; }
function renderDefaultsEditor(defaults = {}) { let host = $('defaults-form'); if (!host) { host = document.createElement('div'); host.id = 'defaults-form'; host.className = 'defaults-form'; $('workflow-defaults').parentNode.insertBefore(host, $('workflow-defaults')); } const entries = Object.entries(defaults); host.innerHTML = entries.length ? entries.map(([key, value]) => `<label class="default-field"><span>${esc(key)}</span><input data-default-key="${esc(key)}" value="${esc(value)}"></label>`).join('') : '<span class="muted">No defaults configured</span>'; }
function readDefaultsEditor() { const fields = document.querySelectorAll('[data-default-key]'); if (!fields.length) return parseJSON('workflow-defaults'); const values = {}; fields.forEach((field) => { const value = field.value; values[field.dataset.defaultKey] = value !== '' && !Number.isNaN(Number(value)) ? Number(value) : value; }); $('workflow-defaults').value = JSON.stringify(values, null, 2); return values; }
function readDynamicFields() { const values = {...fieldValues}; document.querySelectorAll('[data-field-key]').forEach((field) => { values[field.dataset.fieldKey] = field.type === 'checkbox' ? field.checked : field.type === 'number' || field.type === 'range' ? Number(field.value) : field.value; }); return values; }
function workflowForm(workflow = {}) { selectedWorkflow = workflow; const defaults = workflow.defaults || workflow.config?.defaults || {}; $('workflow-name').value = workflow.name || ''; $('workflow-json').value = JSON.stringify(workflowAPI(workflow), null, 2); $('workflow-defaults').value = JSON.stringify(defaults, null, 2); $('workflow-output').value = JSON.stringify(workflow.output || workflow.outputs || workflow.config?.outputs || {}, null, 2); renderDefaultsEditor(defaults); renderBindings(workflow.bindings || workflow.config?.bindings || []); renderNodes(workflow); renderDynamicFields(workflow.config || workflow.schema || [], defaults); }
function renderBindings(bindings) { $('bindings-body').innerHTML = bindings.map((binding, index) => `<tr data-binding-row="${index}"><td><input data-bind="key" value="${esc(binding.key)}"></td><td><input data-bind="node_id" value="${esc(binding.node_id)}"></td><td><input data-bind="path" value="${esc(binding.path)}"></td><td><select data-bind="control"><option ${binding.control === 'textarea' ? 'selected' : ''}>textarea</option><option ${binding.control === 'text' ? 'selected' : ''}>text</option><option ${binding.control === 'number' ? 'selected' : ''}>number</option><option ${binding.control === 'slider' ? 'selected' : ''}>slider</option><option ${binding.control === 'boolean' ? 'selected' : ''}>boolean</option><option ${binding.control === 'dropdown' ? 'selected' : ''}>dropdown</option><option ${binding.control === 'image' ? 'selected' : ''}>image</option></select></td><td><select data-bind="type"><option ${binding.type === 'string' || binding.value_type === 'string' ? 'selected' : ''}>string</option><option ${binding.type === 'integer' || binding.value_type === 'integer' ? 'selected' : ''}>integer</option><option ${binding.type === 'number' || binding.value_type === 'number' ? 'selected' : ''}>number</option><option ${binding.type === 'boolean' || binding.value_type === 'boolean' ? 'selected' : ''}>boolean</option></select></td><td><input data-bind="required" type="checkbox" ${binding.required ? 'checked' : ''}></td><td><button class="icon-button" data-remove-binding="${index}" title="Remove">x</button></td></tr>`).join(''); }
function readBindings() { return [...document.querySelectorAll('[data-binding-row]')].map((row) => { const get = (key) => row.querySelector(`[data-bind="${key}"]`); return { key: get('key').value, node_id: get('node_id').value, path: get('path').value, control: get('control').value, type: get('type').value, value_type:get('type').value, required: get('required').checked }; }); }
async function loadWorkflows() { try { const data = await api('/api/v2/comfyui/workflows') || []; workflows = data.workflows || data || []; if (!Array.isArray(workflows)) workflows = []; $('workflow-list').innerHTML = workflows.map((w) => `<option value="${esc(w.id)}">${esc(w.name || w.id)}</option>`).join(''); if (workflows[0]) await selectWorkflow(workflows[0].id); } catch (error) { notify(`Workflow load failed: ${error.message}`, 'workflow-msg', 'error'); } }
async function selectWorkflow(id) { try { const workflow = await api(`/api/v2/comfyui/workflows/${encodeURIComponent(id)}`); workflowForm(workflow); try { const schema = await api(`/api/v2/comfyui/workflows/${encodeURIComponent(id)}/schema`); renderDynamicFields(schema, workflow.defaults || {}); } catch (_) {} } catch (error) { notify(error.message, 'workflow-msg', 'error'); } }
function newWorkflow() { workflowForm({ id: '', name: 'unit image', workflow: {}, bindings: [], defaults: {}, output: {} }); }
async function importWorkflow(file) { try { const raw = JSON.parse(await file.text()); workflowForm({ id:'', name:file.name.replace(/\.json$/i, ''), workflow:raw, bindings:[], defaults:{}, output:{} }); notify('API workflow JSON loaded; inspect nodes before saving', 'workflow-msg', 'success'); } catch (error) { notify(`JSON import failed: ${error.message}`, 'workflow-msg', 'error'); } }
function parseJSON(id) { try { return JSON.parse($(id).value || '{}'); } catch (_) { throw new Error(`${id} is not valid JSON`); } }
async function validateWorkflow() { notify('Validation is performed when saving a workflow', 'workflow-msg'); }
async function saveWorkflowDefinition() {
  try {
    const apiJSON = parseJSON('workflow-json');
    const defaults = readDefaultsEditor();
    const bindings = readBindings();
    const schemaFields = currentSchema?.fields || currentSchema || [];
    const fields = (Array.isArray(schemaFields) ? schemaFields : []).map((field) => inferField(field, defaults[field.id || field.key]));
    const body = { format: 'comfyui_api_v1', name: $('workflow-name').value.trim(), api_json: apiJSON, workflow: apiJSON, config: { format: 'ainovel_workflow_config_v1', fields, bindings, defaults, outputs: [readOutputSelector()] }, bindings, defaults, output: readOutputSelector(), instance_id: document.querySelector('input[name="default-instance"]:checked')?.value || '' };
    const id = selectedWorkflow?.id;
    await api(id ? `/api/v2/comfyui/workflows/${encodeURIComponent(id)}` : '/api/v2/comfyui/workflows/import', { method: id ? 'PUT' : 'POST', body: JSON.stringify(body) });
    notify('Workflow saved', 'workflow-msg', 'success');
    await loadWorkflows();
  } catch (error) { notify(`Workflow save failed: ${error.message}`, 'workflow-msg', 'error'); }
}
function readOutputSelector() { let value = parseJSON('workflow-output'); if (Array.isArray(value)) value = value[Number($('output-index').value || 0)] || value[0] || {}; if (value && Array.isArray(value.outputs)) value = value.outputs[Number($('output-index').value || 0)] || value.outputs[0] || {}; return value || {}; }
async function uploadMedia() { const file = $('input-media').files[0]; if (!file) return notify('Choose an input media file first', 'comfy-msg', 'error'); try { const form = new FormData(); form.append('file', file); form.append('purpose', 'workflow-input'); const response = await fetch('/api/v2/comfyui/media/upload', { method:'POST', body:form }); const body = await response.json().catch(() => ({})); if (!response.ok || (body.code !== undefined && body.code !== 0)) throw new Error(body.msg || response.statusText); const raw = body.data || body; const media = { source:raw.source || 'upload', storage_key:raw.storage_key || raw.storageKey || '', instance_id:raw.instance_id || raw.instanceID || '', upload_name:raw.upload_name || raw.uploadName || file.name, mime:raw.mime || file.type, size:raw.size || file.size, sha256:raw.sha256 || '' }; uploadedMedia.push(media); $('media-list').innerHTML = uploadedMedia.map((item) => `<div class="media-chip">${esc(item.upload_name || item.storage_key)} <small>${esc(item.mime || '')}</small></div>`).join(''); notify('Input media uploaded', 'comfy-msg', 'success'); } catch (error) { notify(`Media upload failed: ${error.message}`, 'comfy-msg', 'error'); } }
async function testJob() { const values = readDynamicFields(); const body = { workflow_id: selectedWorkflow?.id, instance_id: document.querySelector('input[name="default-instance"]:checked')?.value || null, prompt: values.positive_prompt || $('prompt').value || '', negative_prompt: values.negative_prompt || '', parameters: values, inputs: uploadedMedia, mode:'test' }; try { currentJob = await api(`/api/v2/comfyui/workflows/${encodeURIComponent(selectedWorkflow?.id || '')}/run`, { method:'POST', body:JSON.stringify(body) }); renderJob(currentJob); } catch (error) { if (error.status === 404) { try { currentJob = await api('/api/v2/comfyui/jobs/test', { method:'POST', body:JSON.stringify(body) }); renderJob(currentJob); } catch (fallback) { notify(`Job submit failed: ${fallback.message}`, 'comfy-msg', 'error'); } } else notify(`Job submit failed: ${error.message}`, 'comfy-msg', 'error'); } }
function renderJob(job = {}) { if (!job.job_id && !job.id) return; currentJob = { ...currentJob, ...job }; const id = currentJob.job_id || currentJob.id; const status = currentJob.status || currentJob.status_alias || 'pending'; $('job-status').textContent = `${id} / ${status}`; $('job-phase').textContent = [currentJob.instance_id, currentJob.phase || '', currentJob.progress != null ? `${Math.round(currentJob.progress * 100)}%` : '', currentJob.attempt ? `attempt ${currentJob.attempt}` : ''].filter(Boolean).join(' · '); $('job-json').textContent = JSON.stringify(currentJob, null, 2); $('job-logs').textContent = (currentJob.logs || currentJob.log || []).map((line) => typeof line === 'string' ? line : JSON.stringify(line)).join('\n'); renderOutputs(currentJob.outputs || currentJob.output, id); if (!['succeeded','completed','failed','timeout','cancelled'].includes(status)) { clearTimeout(renderJob.pollTimer); renderJob.pollTimer = setTimeout(() => refreshJob(id), 1500); } }
function renderOutputs(outputs, jobID = '') { const list = Array.isArray(outputs) ? outputs : outputs ? [outputs] : []; $('job-outputs').innerHTML = list.length ? list.map((output, index) => { const url = output.url || output.preview_url || (jobID ? `/api/v2/comfyui/jobs/${encodeURIComponent(jobID)}/outputs/${index}` : ''); if (output.kind === 'image' || output.mime?.startsWith('image/')) return `<figure class="output-item"><img src="${esc(url)}" alt="output ${index}" loading="lazy"><figcaption>image · ${esc(output.mime || '')} · #${index}</figcaption></figure>`; return `<div class="output-item output-text"><strong>${esc(output.kind || 'file')} · #${index}</strong><pre>${esc(output.preview || output.filename || JSON.stringify(output))}</pre></div>`; }).join('') : '<span class="muted">No outputs yet</span>'; }
async function refreshJob(id) { try { renderJob(await api(`/api/v2/comfyui/jobs/${encodeURIComponent(id)}`)); } catch (_) {} }
async function previewOutputs() { const id = currentJob?.job_id || currentJob?.id; if (!id) return notify('Run a job to preview outputs', 'comfy-msg', 'error'); try { const data = await api(`/api/v2/comfyui/jobs/${encodeURIComponent(id)}/outputs`); renderOutputs(data.outputs || data, id); } catch (error) { notify(`Output preview failed: ${error.message}`, 'comfy-msg', 'error'); } }
async function jobAction(action) { if (!currentJob?.job_id && !currentJob?.id) return notify('No active job', 'comfy-msg', 'error'); const id = currentJob.job_id || currentJob.id; try { renderJob(await api(`/api/v2/comfyui/jobs/${encodeURIComponent(id)}/${action}`, { method: 'POST', body: '{}' })); } catch (error) { notify(`${action} failed: ${error.message}`, 'comfy-msg', 'error'); } }

qs('[data-action="save-comfyui"]')?.addEventListener('click', saveComfyUI);
qs('[data-action="save-workflow"]')?.addEventListener('click', saveWorkflowSettings);
qs('[data-action="save-prompts"]')?.addEventListener('click', savePrompts);
qs('[data-action="save-prompts-as"]')?.addEventListener('click', async () => { const name = window.prompt('请输入新的提示词组合名称'); if (name?.trim()) await savePrompts(name.trim(), 'save_as', false); });
qs('[data-action="default-prompts"]')?.addEventListener('click', () => activatePromptPreset('默认配置'));
qs('[data-action="save-writing-rules"]')?.addEventListener('click', () => writingRuleAction('save_writing_rules', $('writing-rules-preset')?.value || '默认要求', true));
qs('[data-action="save-writing-rules-as"]')?.addEventListener('click', async () => { const name = window.prompt('请输入新的写作要求预设名称'); if (name?.trim()) await writingRuleAction('save_writing_rules_as', name.trim(), false); });
qs('[data-action="default-writing-rules"]')?.addEventListener('click', () => writingRuleAction('activate_writing_rules', '默认要求', true));
$('writing-rules-preset')?.addEventListener('change', async (event) => {
  const name = event.target.value;
  if (workflowSettingsDoc?.writing_rules && workflowSettingsDoc.writing_rule_presets?.[workflowSettingsDoc.active_writing_rules_preset]?.text !== $('writing-rules').value && !window.confirm('当前写作要求尚未保存，是否放弃修改并切换？')) {
    renderWritingRulePresets();
    return;
  }
  try {
    workflowSettingsDoc = await api('/api/v2/settings/workflow', { method: 'PUT', body: JSON.stringify({ action: 'activate_writing_rules', name }) });
    renderWritingRulePresets();
    await api(commandRoutes.writingRules, { method: 'POST', body: JSON.stringify({ preferences: workflowSettingsDoc.writing_rules }) });
  } catch (error) {
    notify(`写作要求预设切换失败：${error.message}`, 'settings-msg', 'error');
    renderWritingRulePresets();
  }
});
qs('[data-action="test-connection"]')?.addEventListener('click', testConnection);
qs('[data-action="refresh-instances"]')?.addEventListener('click', refreshInstances);
qs('[data-action="save-instances"]')?.addEventListener('click', saveInstances);
qs('[data-action="new-workflow"]')?.addEventListener('click', newWorkflow);
qs('[data-action="import-workflow"]')?.addEventListener('click', () => $('workflow-file')?.click());
if ($('workflow-file')) $('workflow-file').addEventListener('change', (event) => event.target.files[0] && importWorkflow(event.target.files[0]));
if ($('workflow-list')) $('workflow-list').addEventListener('change', (event) => selectWorkflow(event.target.value));
if ($('workflow-node-list')) $('workflow-node-list').addEventListener('change', (event) => renderNodeInputs(event.target.value));
qs('[data-action="validate-workflow"]')?.addEventListener('click', validateWorkflow);
qs('[data-action="add-binding"]')?.addEventListener('click', () => { const bindings = readBindings(); bindings.push({ key: 'positive_prompt', node_id: '', path: 'inputs.text', type: 'string', required: false }); renderBindings(bindings); });
qs('[data-action="upload-media"]')?.addEventListener('click', uploadMedia);
qs('[data-action="preview-output"]')?.addEventListener('click', previewOutputs);
if ($('bindings-body')) $('bindings-body').addEventListener('click', (event) => { const index = event.target.dataset.removeBinding; if (index !== undefined) { const bindings = readBindings(); bindings.splice(Number(index), 1); renderBindings(bindings); } });
$('dynamic-fields').oninput = (event) => { if (event.target.dataset.fieldKey) fieldValues[event.target.dataset.fieldKey] = event.target.type === 'checkbox' ? event.target.checked : event.target.value; };
$('send').onclick = () => { const text = $('prompt').value.trim(); if (!text) return; const fresh = !currentState || (!currentState.NovelName && !currentState.Phase); const name = fresh ? 'start' : currentState.IsRunning ? 'steer' : 'continue'; command(name, fresh ? { prompt: text } : { text }); $('prompt').value = ''; };
$('pause').onclick = () => command(currentState?.IsRunning ? 'pause' : 'continue');
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
// Load prompt bundles at startup so the writing-prompts view is ready when opened.
// before the user opens the writing-prompts view.
loadPrompts();

// The prompt preset selector lives inside the writing-prompts view.
$('prompt-preset')?.addEventListener('change', async (event) => {
  const name = event.target.value;
  if (promptsDirty) {
    const choice = window.prompt('当前提示词有未保存修改。输入 save 保存后切换，输入 discard 放弃修改，其他内容取消切换。', 'save');
    if (choice === 'save' && !(await savePrompts())) { renderPromptPresetOptions(); return; }
    if (choice !== 'save' && choice !== 'discard') { renderPromptPresetOptions(); return; }
  }
  await activatePromptPreset(name);
});

/* Canvas-first ComfyUI editor. This override is intentionally kept at the end of
   the legacy client so old API routes remain available while the visible UI uses
   a graph, node inspector and test inspector. */
let canvasMode = 'workflow';
let canvasView = { x: 0, y: 0, k: 1 };
let canvasPositions = {};
let canvasPointer = null;
let nodeEditorID = '';
let canvasLoaded = false;
function workflowAPI(workflow) { return workflow?.api_json || workflow?.workflow || {}; }
function inferCanvasField(nodeID, input, value) { const id = /^[A-Za-z_][A-Za-z0-9_.-]{0,63}$/.test(input) ? input : `field_${nodeID}_${String(input).replace(/[^A-Za-z0-9_.-]/g, '_')}`; const lower = id.toLowerCase(); const kind = typeof value === 'number' ? 'number' : typeof value === 'boolean' ? 'boolean' : 'string'; let control = lower.includes('prompt') || lower.includes('text') ? 'textarea' : lower.includes('seed') || lower.includes('steps') || lower.includes('width') || lower.includes('height') ? 'number' : kind === 'boolean' ? 'boolean' : 'text'; return { id, node_id: nodeID, input, label: input, control, value_type: kind, default: value, source: kind === 'string' && (control === 'text' || control === 'textarea') ? 'prompter' : 'default', editable: true }; }
function canvasFields(workflow = selectedWorkflow) { const cfg = workflow?.config || workflow?.schema || {}; const fields = Array.isArray(cfg) ? cfg : (cfg.fields || workflow?.fields || []); return fields.map((f) => ({ ...f, id: f.id || `${f.node_id || f.node || ''}::${f.input || f.key || ''}`, node_id: f.node_id || f.node || '', input: f.input || f.key || '', label: f.label || f.name || f.input || f.key || '', control: f.control || 'text', value_type: f.value_type || f.type || 'string' })); }
function canvasKey(id) { return `ainovel-comfy-canvas:${id || 'draft'}`; }
function restoreCanvas(id) {
  try {
    const v = JSON.parse(localStorage.getItem(canvasKey(id)) || '{}');
    canvasView = { x: 0, y: 0, k: 1, ...(v.viewport || {}) };
    // Persisted canvas documents store nodes as an array; the renderer uses
    // source node IDs as keys for fast position lookup.
    if (Array.isArray(v.nodes)) {
      canvasPositions = {};
      v.nodes.forEach((node) => {
        const key = node.source_node_id || node.id?.replace(/^node-/, '');
        if (key != null) canvasPositions[String(key)] = { x: Number(node.x) || 40, y: Number(node.y) || 40 };
      });
    } else {
      canvasPositions = v.nodes && typeof v.nodes === 'object' ? v.nodes : {};
    }
  } catch (_) {
    canvasView = { x: 0, y: 0, k: 1 };
    canvasPositions = {};
  }
}
async function persistCanvas() { if (!selectedWorkflow?.id) return; const list = nodeList(); const fields = canvasFields().map((f) => ({ id: f.id, node_id: f.node_id, input: f.input, name: f.label || f.name || f.input, control: f.control, value_type: f.value_type || 'string', default: f.default, min: f.min, max: f.max, step: f.step, options: f.options || [], required: !!f.required, random_enabled: !!f.random_enabled, exposed: true, source: f.source || 'canvas' })); const nodes = list.map((n) => ({ id: `node-${n.id}`, kind: 'workflow', source_node_id: String(n.id), class_type: n.class_type || '', label: n.class_type || '', x: canvasPositions[n.id]?.x || 40, y: canvasPositions[n.id]?.y || 40, width: 176, height: 70, exposed_field_ids: fields.filter((f) => String(f.node_id) === String(n.id)).map((f) => f.id) })); const edges = []; list.forEach((n) => Object.entries(n.inputs || {}).forEach(([key, value]) => { if (Array.isArray(value) && value.length) edges.push({ id: `edge-${value[0]}-${n.id}-${key}`, source: `node-${value[0]}`, source_handle: `output-${value[1]}`, target: `node-${n.id}`, target_handle: key, kind: 'workflow', label: key }); })); const payload = { format: 'ainovel_comfy_canvas_v1', version: 1, id: selectedWorkflow.id, workflow_id: selectedWorkflow.id, title: selectedWorkflow.name || '', viewport: { x: canvasView.x, y: canvasView.y, scale: canvasView.k, min_scale: .2, max_scale: 3 }, nodes, edges, fields, mini_test_cards: [] }; localStorage.setItem(canvasKey(selectedWorkflow.id), JSON.stringify(payload)); try { await api(`/api/v2/comfyui/workflows/${encodeURIComponent(selectedWorkflow.id)}/canvas`, { method: 'PUT', body: JSON.stringify(payload) }); } catch (_) {} }
function formConfig(config = {}) { comfyConfig = { ...config }; const map = { 'comfy-url': 'base_url', 'comfy-timeout': 'timeout_ms', 'comfy-poll': 'poll_interval_ms', 'comfy-client-id': 'client_id', 'comfy-max-bytes': 'max_response_bytes', 'comfy-workflow-id': 'workflow_id' }; Object.entries(map).forEach(([id, key]) => { if ($(id)) $(id).value = config[key] ?? ''; }); if ($('comfy-enabled')) $('comfy-enabled').checked = !!config.enabled; if ($('comfy-strict')) $('comfy-strict').checked = config.strict !== false; }
async function loadComfyUI() { if (canvasLoaded) return; canvasLoaded = true; try { formConfig(await api('/api/v2/comfyui/config')); await loadInstances(); await loadWorkflows(); await loadBridgeConfig(); } catch (error) { canvasLoaded = false; notify(`ComfyUI 配置加载失败：${error.message}`, 'comfy-msg', 'error'); } }
async function saveComfyUI() { const c = { enabled: $('comfy-enabled')?.checked ?? true, base_url: $('comfy-url')?.value.trim() || '', timeout_ms: Number($('comfy-timeout')?.value || 600000), poll_interval_ms: Number($('comfy-poll')?.value || 1000), client_id: $('comfy-client-id')?.value.trim() || 'ainovel-web', max_response_bytes: Number($('comfy-max-bytes')?.value || 52428800), strict: $('comfy-strict')?.checked !== false, workflow_id: $('comfy-workflow-id')?.value.trim() || '' }; try { formConfig(await api('/api/v2/comfyui/config', { method: 'PUT', body: JSON.stringify(c) })); notify('Connection settings saved', 'comfy-msg', 'success'); } catch (error) { notify(`Save failed: ${error.message}`, 'comfy-msg', 'error'); } }
function workflowFieldCount(w) {
  if (selectedWorkflow?.id && w?.id === selectedWorkflow.id) return canvasFields(selectedWorkflow).length;
  const configFields = w?.config?.fields || w?.schema?.fields || w?.fields;
  if (Array.isArray(configFields)) return configFields.length;
  return Number.isFinite(Number(w?.field_count)) && Number(w.field_count) > 0 ? Number(w.field_count) : 0;
}
function renderWorkflowList() { const host = $('workflow-list'); if (!host) return; const query = ($('workflow-search')?.value || '').toLowerCase(); host.innerHTML = workflows.filter((w) => !query || String(w.name || w.id).toLowerCase().includes(query)).map((w) => `<button class="workflow-item ${selectedWorkflow?.id === w.id ? 'active' : ''}" data-workflow-id="${esc(w.id)}"><strong>${esc(w.name || w.id)}</strong><small>${workflowFieldCount(w)} 个可配置字段</small></button>`).join('') || '<span class="muted">暂无工作流</span>'; }
async function loadWorkflows(preferredID = '') { try { const data = await api('/api/v2/comfyui/workflows') || []; workflows = data.workflows || data || []; if (!Array.isArray(workflows)) workflows = []; renderWorkflowList(); refreshBridgeWorkflowOptions(); const target = preferredID && workflows.some((w) => w.id === preferredID) ? preferredID : (selectedWorkflow?.id && workflows.some((w) => w.id === selectedWorkflow.id) ? selectedWorkflow.id : workflows[0]?.id); if (target) await selectWorkflow(target); } catch (error) { notify(`工作流加载失败：${error.message}`, 'workflow-msg', 'error'); } }
function nodeList() { return Object.entries(workflowAPI(selectedWorkflow)).map(([id, node]) => ({ id, ...(node || {}) })); }
function normalizeWorkflowResponse(data) { if (data?.workflow && data.workflow.id) return { ...data.workflow, config: data.config || data.workflow.config, canvas: data.canvas }; return data || {}; }
function graphLayout() { const list = nodeList(); const incoming = {}; list.forEach((n) => { incoming[n.id] = []; }); list.forEach((n) => Object.values(n.inputs || {}).forEach((v) => { if (Array.isArray(v) && v.length && incoming[v[0]]) incoming[n.id].push(String(v[0])); })); const depth = {}; const visit = (id, stack = new Set()) => { if (depth[id] != null) return depth[id]; if (stack.has(id)) return 0; stack.add(id); depth[id] = Math.max(0, ...(incoming[id] || []).map((x) => visit(x, stack) + 1)); return depth[id]; }; list.forEach((n) => visit(n.id)); const rows = {}; list.forEach((n) => { const d = depth[n.id] || 0; (rows[d] ||= []).push(n); }); list.forEach((n) => { if (!canvasPositions[n.id]) { const d = depth[n.id] || 0; const row = rows[d].indexOf(n); canvasPositions[n.id] = { x: 50 + d * 240, y: 40 + row * 110 }; } }); return { list, depth }; }
function renderCanvas() { const svg = $('workflow-canvas'); if (!svg) return; const empty = $('canvas-empty'); const { list } = graphLayout(); if (empty) empty.hidden = list.length > 0; const edgeHost = $('graph-edges'); const nodeHost = $('graph-nodes'); if (!list.length) { edgeHost.innerHTML = ''; nodeHost.innerHTML = ''; return; } const edgeParts = []; list.forEach((n) => Object.values(n.inputs || {}).forEach((v) => { if (!Array.isArray(v) || !v.length || !canvasPositions[v[0]]) return; const a = canvasPositions[v[0]], b = canvasPositions[n.id]; edgeParts.push(`<path class="graph-edge" d="M ${a.x + 176} ${a.y + 35} C ${a.x + 210} ${a.y + 35}, ${b.x - 34} ${b.y + 35}, ${b.x} ${b.y + 35}"/>`); })); edgeHost.innerHTML = edgeParts.join(''); const fields = canvasFields(); nodeHost.innerHTML = list.map((n) => { const p = canvasPositions[n.id]; const exposed = fields.filter((f) => String(f.node_id) === String(n.id)).length; const title = String(n.class_type || 'Node').replace(/_/g, ' '); return `<g class="graph-node ${exposed ? 'has-exposed' : ''}" data-node-id="${esc(n.id)}" transform="translate(${p.x},${p.y})"><rect width="176" height="70"></rect><text x="10" y="22">${esc(title.slice(0, 24))}</text><text class="node-class" x="10" y="40">#${esc(n.id)}</text>${exposed ? `<text class="node-pill" x="10" y="58">已选 ${exposed} 个字段</text>` : '<text class="node-class" x="10" y="58">点击选择可配置输入</text>'}</g>`; }).join(''); const vp = $('graph-viewport'); vp.setAttribute('transform', `translate(${canvasView.x},${canvasView.y}) scale(${canvasView.k})`); if ($('canvas-zoom-label')) $('canvas-zoom-label').textContent = `${Math.round(canvasView.k * 100)}%`; }
function fitCanvas() { const { list } = graphLayout(); if (!list.length) return; const xs = list.map((n) => canvasPositions[n.id].x), ys = list.map((n) => canvasPositions[n.id].y); const svg = $('workflow-canvas'); const w = svg.clientWidth || 800, h = svg.clientHeight || 600; const gw = Math.max(...xs) - Math.min(...xs) + 230, gh = Math.max(...ys) - Math.min(...ys) + 120; canvasView.k = Math.max(.35, Math.min(1.5, Math.min(w / gw, h / gh))); canvasView.x = (w - gw * canvasView.k) / 2 - Math.min(...xs) * canvasView.k; canvasView.y = (h - gh * canvasView.k) / 2 - Math.min(...ys) * canvasView.k; renderCanvas(); persistCanvas(); }
  function renderNodeEditor(nodeID) { const node = workflowAPI(selectedWorkflow)[nodeID] || {}; const fields = canvasFields(); const existing = new Map(fields.filter((f) => String(f.node_id) === String(nodeID)).map((f) => [f.input, f])); const controls = { text: '文本', textarea: '多行文本', number: '数字', slider: '滑块', dropdown: '下拉框', boolean: '开关', image: '图片' }; const sourceLabels = { prompter: '由提示词模型生成', default: '使用工作流默认值', runtime: '仅运行时输入' }; const rows = Object.entries(node.inputs || {}).filter(([, value]) => !Array.isArray(value)).map(([input, value]) => { const f = existing.get(input) || inferCanvasField(nodeID, input, value); const opts = Object.keys(controls).map((x) => `<option value="${x}" ${f.control === x ? 'selected' : ''}>${controls[x]}</option>`).join(''); const source = normalizeFieldSource(f); const sourceOptions = Object.entries(sourceLabels).map(([key, label]) => `<option value="${key}" ${source === key ? 'selected' : ''}>${label}</option>`).join(''); return `<div class="node-field-row ${existing.has(input) ? 'exposed' : ''}" data-field-input="${esc(input)}" data-field-value-type="${esc(f.value_type || typeof value)}"><div class="node-field-head"><input type="checkbox" data-field-expose ${existing.has(input) ? 'checked' : ''}><code>${esc(input)}</code></div><label class="field-config-label">JSON 字段键<input data-field-id placeholder="例如 text" value="${esc(f.id)}"></label><label class="field-config-label">字段来源<select data-field-source>${sourceOptions}</select></label><input data-field-label placeholder="字段说明" value="${esc(f.label || input)}"><select data-field-control>${opts}</select><input data-field-default placeholder="默认值" value="${esc(f.default ?? value)}"><div class="node-field-extra"><label>最小值<input data-field-min value="${esc(f.min ?? '')}"></label><label>最大值<input data-field-max value="${esc(f.max ?? '')}"></label><label>步长<input data-field-step value="${esc(f.step ?? '')}"></label></div></div>`; }).join(''); const host = $('node-field-editor'); const modal = $('modal-field-editor'); if (host) host.innerHTML = '<span class="muted">字段配置已移至弹窗</span>'; if (modal) modal.innerHTML = rows || '<span class="muted">此节点没有可编辑输入</span>'; }
function openNodePopup(nodeID) { nodeEditorID = String(nodeID); const node = workflowAPI(selectedWorkflow)[nodeID] || {}; $('inspector-node-title').textContent = `${node.class_type || 'Node'} #${nodeID}`; $('node-inspector-empty').hidden = true; $('node-inspector-body').hidden = false; $('modal-node-title').textContent = `${node.class_type || 'Node'} #${nodeID}`; $('modal-node-subtitle').textContent = '选择要在运行面板中编辑的字段'; renderNodeEditor(nodeID); $('node-modal').hidden = false; }
function closeNodePopup() { $('node-modal').hidden = true; nodeEditorID = ''; }
  function readFieldEditor(host, nodeID = nodeEditorID) { return [...(host || document).querySelectorAll('.node-field-row')].filter((row) => row.querySelector('[data-field-expose]')?.checked).map((row) => { const val = row.querySelector('[data-field-default]')?.value || ''; const valueType = row.dataset.fieldValueType === 'boolean' ? 'boolean' : (val !== '' && !Number.isNaN(Number(val)) ? 'number' : 'string'); const cast = valueType === 'number' ? Number(val) : valueType === 'boolean' ? val === 'true' : val; return { id: row.querySelector('[data-field-id]')?.value.trim() || '', node_id: String(nodeID), input: row.dataset.fieldInput, label: row.querySelector('[data-field-label]')?.value || row.dataset.fieldInput, control: row.querySelector('[data-field-control]')?.value || 'text', value_type: valueType, default: cast, min: row.querySelector('[data-field-min]')?.value || undefined, max: row.querySelector('[data-field-max]')?.value || undefined, step: row.querySelector('[data-field-step]')?.value || undefined, editable: true, source: row.querySelector('[data-field-source]')?.value || 'default' }; }); }
  async function saveNodeFields() { if (!selectedWorkflow || !nodeEditorID) return; const source = $('modal-field-editor'); const notes = Object.fromEntries(canvasFields().map((f) => [f.id, f.note || ''])); const keep = canvasFields().filter((f) => String(f.node_id) !== String(nodeEditorID)); const added = readFieldEditor(source, nodeEditorID).map((f) => ({ ...f, note: notes[f.id] || '' })); const fields = keep.concat(added); const fieldError = validateCanvasFieldIDs(fields); if (fieldError) return notify(fieldError, 'comfy-msg', 'error'); selectedWorkflow.config = { ...(selectedWorkflow.config || {}), format: 'ainovel_workflow_config_v1', version: 1, fields, bindings: fields.map((f) => ({ key: f.id, node_id: f.node_id, path: `inputs.${f.input}`, type: f.value_type, required: false })), defaults: Object.fromEntries(fields.map((f) => [f.id, f.default])) }; selectedWorkflow.bindings = selectedWorkflow.config.bindings; selectedWorkflow.defaults = selectedWorkflow.config.defaults; renderCanvas(); renderDynamicFields(selectedWorkflow.config, selectedWorkflow.defaults); const savedID = selectedWorkflow.id; closeNodePopup(); const saved = await saveWorkflowDefinition(true); if (!saved) return; if (savedID) { renderWorkflowList(); await loadPromptSchema(savedID, { silent: true }); } notify('节点字段已更新', 'comfy-msg', 'success'); }
  function renderDynamicFields(schema, defaults = {}) { const fields = canvasFields({ config: schema }); fieldValues = { ...defaults }; const host = $('dynamic-fields'); if (!host) return; const sourceLabels = { prompter: '提示词模型', default: '默认值', runtime: '运行时' }; host.innerHTML = fields.length ? fields.map((f) => { const value = fieldValues[f.id] ?? f.default ?? ''; const id = `field-${f.id.replace(/[^a-zA-Z0-9_-]/g, '-')}`; let control = `<input id="${id}" data-field-key="${esc(f.id)}" value="${esc(value)}">`; if (f.control === 'textarea') control = `<textarea id="${id}" data-field-key="${esc(f.id)}" rows="3">${esc(value)}</textarea>`; if (f.control === 'number' || f.control === 'slider') control = `<input id="${id}" data-field-key="${esc(f.id)}" type="${f.control === 'slider' ? 'range' : 'number'}" value="${esc(value)}" min="${esc(f.min ?? '')}" max="${esc(f.max ?? '')}" step="${esc(f.step ?? '')}">`; if (f.control === 'boolean') control = `<input id="${id}" data-field-key="${esc(f.id)}" type="checkbox" ${value ? 'checked' : ''}>`; if (f.control === 'dropdown') control = `<select id="${id}" data-field-key="${esc(f.id)}">${(f.options || []).map((o) => `<option ${String(o) === String(value) ? 'selected' : ''}>${esc(o)}</option>`).join('')}</select>`; return `<label class="dynamic-field"><span>${esc(f.label || f.input)}<small>${esc(sourceLabels[normalizeFieldSource(f)])} · #${esc(f.node_id)}.${esc(f.input)}</small></span>${control}</label>`; }).join('') : '<span class="muted">请先在节点中勾选字段，字段会显示在这里。</span>'; }
function readDynamicFields() { const values = { ...fieldValues }; document.querySelectorAll('#dynamic-fields [data-field-key]').forEach((f) => { values[f.dataset.fieldKey] = f.type === 'checkbox' ? f.checked : (f.type === 'number' || f.type === 'range' ? Number(f.value) : f.value); }); return values; }
async function selectWorkflow(id) {
  try {
    const template = $('bridge-prompter-template');
    if (template) { template.dataset.loaded = 'false'; template.dataset.dirty = 'false'; }
    selectedWorkflow = normalizeWorkflowResponse(await api(`/api/v2/comfyui/workflows/${encodeURIComponent(id)}`));
    restoreCanvas(id);
    let canvasDocumentLoaded = false;
    try {
      const canvas = await api(`/api/v2/comfyui/workflows/${encodeURIComponent(id)}/canvas`);
      canvasDocumentLoaded = !!canvas;
      if (canvas?.viewport) canvasView = { x: canvas.viewport.x || 0, y: canvas.viewport.y || 0, k: canvas.viewport.scale || 1 };
      if (Array.isArray(canvas?.nodes)) canvas.nodes.forEach((n) => {
        if (n.source_node_id) canvasPositions[n.source_node_id] = { x: n.x || 40, y: n.y || 40 };
      });
      // An empty fields array is meaningful: it means the user explicitly
      // un-exposed every field and must replace the workflow config.
      if (canvas?.prompter_template != null) selectedWorkflow.prompter_template = canvas.prompter_template || '';
      if (canvas?.prompter_preset != null) selectedWorkflow.prompter_preset = canvas.prompter_preset || '';
      if (Array.isArray(canvas?.fields)) {
        const fields = canvas.fields.map((f) => ({ ...f, label: f.name || f.label || f.input, id: f.id || `${f.node_id}::${f.input}` }));
        selectedWorkflow.config = {
          ...(selectedWorkflow.config || {}),
          fields,
          bindings: fields.map((f) => ({ key: f.id, node_id: f.node_id, path: `inputs.${f.input}`, type: f.value_type || 'string', required: !!f.required })),
          defaults: Object.fromEntries(fields.map((f) => [f.id, f.default])),
        };
        selectedWorkflow.defaults = selectedWorkflow.config.defaults;
        selectedWorkflow.bindings = selectedWorkflow.config.bindings;
      }
    } catch (_) {}
    try {
      const schema = await api(`/api/v2/comfyui/workflows/${encodeURIComponent(id)}/schema`);
      if (schema?.fields && !canvasDocumentLoaded && !canvasFields().length) selectedWorkflow.config = { ...(selectedWorkflow.config || {}), fields: schema.fields };
    } catch (_) {}
    renderWorkflowList();
    refreshBridgeWorkflowOptions();
    if ($('bridge-workflow-id') && workflows.some((workflow) => workflow.id === id)) $('bridge-workflow-id').value = id;
    renderCanvas();
    renderDynamicFields(selectedWorkflow.config || {}, selectedWorkflow.defaults || {});
    if (!Object.keys(canvasPositions).length) fitCanvas(); else renderCanvas();
    await loadPromptSchema(id, { silent: true });
  } catch (error) {
    notify(error.message, 'workflow-msg', 'error');
  }
}
function newWorkflow() { selectedWorkflow = { id: '', name: 'unit image', workflow: {}, config: { fields: [], bindings: [], defaults: {} }, defaults: {}, bindings: [] }; canvasPositions = {}; renderWorkflowList(); renderCanvas(); }
async function importWorkflow(file) { try { const raw = JSON.parse(await file.text()); selectedWorkflow = { id: '', name: file.name.replace(/\.json$/i, ''), workflow: raw, config: { fields: [], bindings: [], defaults: {} }, defaults: {}, bindings: [] }; canvasPositions = {}; renderCanvas(); fitCanvas(); notify('工作流已导入。请点击节点暴露字段，然后保存。', 'workflow-msg', 'success'); } catch (error) { notify(`JSON 导入失败：${error.message}`, 'workflow-msg', 'error'); } }
async function saveWorkflowDefinition(silent = false) { if (!selectedWorkflow) return; const apiJSON = workflowAPI(selectedWorkflow); if (!Object.keys(apiJSON).length) return notify('请先导入 ComfyUI API JSON', 'workflow-msg', 'error'); const fields = canvasFields(); const config = { format: 'ainovel_workflow_config_v1', version: 1, fields, bindings: fields.map((f) => ({ key: f.id, node_id: f.node_id, path: `inputs.${f.input}`, type: f.value_type, required: false })), defaults: Object.fromEntries(fields.map((f) => [f.id, f.default])), outputs: selectedWorkflow.config?.outputs || [] }; const body = { format: 'comfyui_api_v1', id: selectedWorkflow.id, name: selectedWorkflow.name || 'workflow', api_json: apiJSON, workflow: apiJSON, config, bindings: config.bindings, defaults: config.defaults, output: selectedWorkflow.output || {}, instance_id: document.querySelector('input[name="default-instance"]:checked')?.value || '' }; try { const saved = normalizeWorkflowResponse(await api(selectedWorkflow.id ? `/api/v2/comfyui/workflows/${encodeURIComponent(selectedWorkflow.id)}` : '/api/v2/comfyui/workflows/import', { method: selectedWorkflow.id ? 'PUT' : 'POST', body: JSON.stringify(body) })); const savedID = saved.id || selectedWorkflow.id; selectedWorkflow = saved; await loadWorkflows(savedID); await persistCanvas(); if (!silent) notify('工作流已保存', 'workflow-msg', 'success'); } catch (error) { notify(`工作流保存失败：${error.message}`, 'workflow-msg', 'error'); } }
async function testJob() { if (!selectedWorkflow?.id) return notify('请先保存工作流再测试', 'comfy-msg', 'error'); const values = readDynamicFields(); const body = { workflow_id: selectedWorkflow.id, instance_id: document.querySelector('input[name="default-instance"]:checked')?.value || null, prompt: values.text || values.positive_prompt || '', negative_prompt: values.negative_prompt || '', parameters: values, field_values: values, mini_test_values: {}, inputs: uploadedMedia, mode: 'test' }; try { currentJob = await api(`/api/v2/comfyui/workflows/${encodeURIComponent(selectedWorkflow.id)}/run`, { method: 'POST', body: JSON.stringify(body) }); renderJob(currentJob); showInspector('run'); } catch (error) { notify(`提交任务失败：${error.message}`, 'comfy-msg', 'error'); } }
function outputList(job = {}) { const outputs = Array.isArray(job.outputs) && job.outputs.length ? job.outputs : (Array.isArray(job.output) ? job.output : job.output ? [job.output] : []); return outputs; }
function renderJob(job = {}) { if (!job.job_id && !job.id) return; currentJob = { ...currentJob, ...job }; const id = currentJob.job_id || currentJob.id; const status = currentJob.status || 'pending'; const statusLabels = { pending: '等待中', submitting: '提交中', queued: '排队中', running: '运行中', succeeded: '已完成', completed: '已完成', failed: '失败', timeout: '超时', cancelled: '已取消' }; if ($('job-status')) { $('job-status').textContent = statusLabels[status] || status; $('job-status').className = `job-status ${status}`; } if ($('job-phase')) $('job-phase').textContent = [currentJob.phase || '', currentJob.progress != null ? `${Math.round(currentJob.progress * 100)}%` : ''].filter(Boolean).join(' · '); renderOutputs(outputList(currentJob), id); if (!['succeeded', 'completed', 'failed', 'timeout', 'cancelled'].includes(status)) { clearTimeout(renderJob.pollTimer); renderJob.pollTimer = setTimeout(() => refreshJob(id), 1500); } }
function renderOutputs(outputs, jobID = '') { const list = Array.isArray(outputs) ? outputs : outputs ? [outputs] : []; const host = $('job-outputs'); if (!host) return; host.innerHTML = list.length ? list.map((o, i) => { const url = o.url || o.preview_url || o.image_url || (jobID ? `/api/v2/comfyui/jobs/${encodeURIComponent(jobID)}/outputs/${i}` : ''); const mime = o.mime || o.content_type || ''; const isImage = o.kind === 'image' || o.previewable || mime.startsWith('image/') || /\.(png|jpe?g|webp|gif)$/i.test(String(o.filename || '')); return isImage ? `<figure class="output-item"><img src="${esc(url)}" alt="${ui('latestOutput')} ${i + 1}" loading="lazy" data-lightbox-src="${esc(url)}"><figcaption>${ui('image')} · ${esc(mime)}</figcaption></figure>` : `<div class="output-item output-text"><strong>${esc(o.kind || ui('file'))} · #${i + 1}</strong><pre>${esc(o.preview || o.filename || JSON.stringify(o))}</pre></div>`; }).join('') : `<span class="muted">${ui('noOutputs')}</span>`; }
async function refreshJob(id) { try { renderJob(await api(`/api/v2/comfyui/jobs/${encodeURIComponent(id)}`)); } catch (_) {} }
function showInspector(tab) { document.querySelectorAll('.inspector-tab').forEach((b) => b.classList.toggle('active', b.dataset.inspectorTab === tab)); $('node-inspector').hidden = tab !== 'node'; $('run-inspector').hidden = tab !== 'run'; if ($('bridge-inspector')) $('bridge-inspector').hidden = tab !== 'bridge'; if (tab === 'bridge') { setBridgeUnitDefaults(); loadPromptSchema($('bridge-workflow-id')?.value || selectedWorkflow?.id, { silent: true }); loadUnitImageJob(); } }
function saveCanvasPosition(id, x, y) { canvasPositions[id] = { x, y }; renderCanvas(); persistCanvas(); }
const canvasSVG = $('workflow-canvas');
if (canvasSVG) { canvasSVG.addEventListener('wheel', (e) => { e.preventDefault(); const factor = e.deltaY < 0 ? 1.1 : .9; canvasView.k = Math.max(.3, Math.min(2.5, canvasView.k * factor)); renderCanvas(); }, { passive: false }); canvasSVG.addEventListener('pointerdown', (e) => { const node = e.target.closest('.graph-node'); const rect = canvasSVG.getBoundingClientRect(); canvasPointer = { startX: e.clientX, startY: e.clientY, x: canvasView.x, y: canvasView.y, node: node?.dataset.nodeId || '', nodeStart: node ? { ...canvasPositions[node.dataset.nodeId] } : null, rect }; canvasSVG.setPointerCapture(e.pointerId); }); canvasSVG.addEventListener('pointermove', (e) => { if (!canvasPointer) return; const dx = e.clientX - canvasPointer.startX, dy = e.clientY - canvasPointer.startY; if (canvasPointer.node) { canvasPositions[canvasPointer.node] = { x: canvasPointer.nodeStart.x + dx / canvasView.k, y: canvasPointer.nodeStart.y + dy / canvasView.k }; } else { canvasView.x = canvasPointer.x + dx; canvasView.y = canvasPointer.y + dy; } renderCanvas(); }); canvasSVG.addEventListener('pointerup', (e) => { if (!canvasPointer) return; const p = canvasPointer; canvasPointer = null; const moved = Math.abs(e.clientX - p.startX) + Math.abs(e.clientY - p.startY) > 4; if (p.node && !moved) openNodePopup(p.node); else persistCanvas(); }); }
document.querySelector('[data-action="close-node"]')?.addEventListener('click', closeNodePopup);
document.querySelectorAll('[data-action="close-node"]').forEach((el) => el.addEventListener('click', closeNodePopup));
document.querySelectorAll('[data-action="save-node-fields"]').forEach((el) => el.addEventListener('click', saveNodeFields));
document.querySelector('[data-action="save-workflow-definition"]')?.addEventListener('click', () => saveWorkflowDefinition());
document.querySelector('[data-action="test-job"]')?.addEventListener('click', () => testJob());
document.querySelector('[data-action="cancel-job"]')?.addEventListener('click', () => jobAction('cancel'));
document.querySelector('[data-action="retry-job"]')?.addEventListener('click', () => jobAction('retry'));
document.querySelector('[data-action="new-workflow"]')?.addEventListener('click', newWorkflow);
document.querySelector('[data-action="import-workflow"]')?.addEventListener('click', () => $('workflow-file')?.click());
$('workflow-file')?.addEventListener('change', (e) => e.target.files[0] && importWorkflow(e.target.files[0]));
$('workflow-list')?.addEventListener('click', (e) => { const id = e.target.closest('[data-workflow-id]')?.dataset.workflowId; if (id) selectWorkflow(id); });
$('workflow-search')?.addEventListener('input', renderWorkflowList);
document.querySelectorAll('[data-inspector-tab]').forEach((b) => b.addEventListener('click', () => showInspector(b.dataset.inspectorTab)));
document.querySelectorAll('[data-canvas-mode]').forEach((b) => b.addEventListener('click', () => { canvasMode = b.dataset.canvasMode; document.querySelectorAll('[data-canvas-mode]').forEach((x) => x.classList.toggle('active', x === b)); $('workflow-canvas').hidden = canvasMode !== 'workflow'; $('test-canvas').hidden = canvasMode !== 'test'; if (canvasMode === 'test') showInspector('run'); }));
document.querySelector('[data-canvas-tool="zoom-in"]')?.addEventListener('click', () => { canvasView.k = Math.min(2.5, canvasView.k * 1.15); renderCanvas(); });
document.querySelector('[data-canvas-tool="zoom-out"]')?.addEventListener('click', () => { canvasView.k = Math.max(.3, canvasView.k / 1.15); renderCanvas(); });
document.querySelector('[data-canvas-tool="fit"]')?.addEventListener('click', fitCanvas);
document.querySelector('[data-canvas-tool="fullscreen"]')?.addEventListener('click', () => $('canvas-stage')?.requestFullscreen?.());
if ($('job-outputs')) $('job-outputs').addEventListener('click', (e) => { const src = e.target.dataset.lightboxSrc; if (src) window.open(src, '_blank', 'noopener'); });

/* Mini test canvas mirrors Infinite Canvas' quick-run cards without duplicating
   workflow JSON. Values are transient input to the same run endpoint. */
let testCanvasPrompt = '';
let testCanvasView = { x: 0, y: 0, k: 1 };
let testCanvasPointer = null;
function translateStaticUI() {
  const text = new Map([
    ['API', '模型 API'], ['Settings', '设置'], ['Writing prompts', '写作提示词'], ['ComfyUI Canvas', 'ComfyUI 画布'], ['ComfyUI', 'ComfyUI'], ['Workflow', '工作流'], ['Test canvas', '测试画布'],
    ['Workflows', '工作流'], ['New workflow', '新建工作流'], ['Import API JSON', '导入 API JSON'], ['Import', '导入'], ['Search workflows', '搜索工作流'], ['No workflows loaded', '暂无工作流'],
    ['Connection', '连接状态'], ['Unknown', '未知'], ['Least queue', '最短队列'], ['Fit', '适配'], ['Fullscreen', '全屏'],
    ['Validate workflow', '校验工作流'], ['Export JSON', '导出 JSON'], ['Node', '节点'], ['Run', '运行'],
    ['Click a node on the canvas to configure exposed fields.', '点击画布节点配置可编辑字段。'], ['Close', '关闭'], ['Apply fields', '应用字段'],
    ['Run test', '运行测试'], ['Idle', '空闲'], ['Cancel', '取消'], ['Retry', '重试'],
    ['Expose fields from nodes to edit them here.', '请先在节点中勾选字段，字段会显示在这里。'], ['Results appear here', '结果将在这里显示'],
    ['Import or select a workflow to begin', '请导入或选择工作流'], ['Workflow graph', '工作流图'], ['Fit graph', '适配画布'], ['Advanced connection settings', '高级连接设置'], ['Base URL', '服务地址'], ['Timeout', '超时时间'], ['Poll interval', '轮询间隔'],
    ['Client ID', '客户端 ID'], ['Max response bytes', '最大响应字节数'], ['Default workflow ID', '默认工作流 ID'], ['Enabled', '启用'], ['Strict mode', '严格模式'],
  ]);
  document.querySelectorAll('body *').forEach((el) => {
    if (el.children.length === 0 && text.has(el.textContent.trim())) el.textContent = text.get(el.textContent.trim());
  });
  document.querySelectorAll('[placeholder]').forEach((el) => { if (text.has(el.getAttribute('placeholder'))) el.setAttribute('placeholder', text.get(el.getAttribute('placeholder'))); });
  document.querySelectorAll('[title]').forEach((el) => { if (text.has(el.getAttribute('title'))) el.setAttribute('title', text.get(el.getAttribute('title'))); });
}
function renderTestCards() {
  const host = $('test-cards'); if (!host) return;
  const output = outputList(currentJob || {}).find((o) => o.kind === 'image' || o.previewable || o.mime?.startsWith('image/') || /\.(png|jpe?g|webp|gif)$/i.test(String(o.filename || '')));
  const outputURL = output ? (output.url || output.preview_url || output.image_url || ((currentJob?.job_id || currentJob?.id) ? `/api/v2/comfyui/jobs/${encodeURIComponent(currentJob.job_id || currentJob.id)}/outputs/0` : '')) : '';
  host.style.transform = `translate(${testCanvasView.x}px,${testCanvasView.y}px) scale(${testCanvasView.k})`;
  host.innerHTML = `<article class="test-card" data-test-card="prompt" style="left:40px;top:60px"><h3>${ui('prompt')}</h3><textarea id="test-canvas-prompt" rows="5" placeholder="描述要生成的图片..."></textarea><small class="muted">写入 text</small></article><article class="test-card comfy" data-test-card="comfy" style="left:330px;top:110px"><h3>${ui('comfyui')}</h3><div class="muted">${esc(selectedWorkflow?.name || ui('noWorkflow'))}</div><button data-action="test-canvas-run">${ui('runTest')}</button><span class="job-status">${esc(currentJob?.status || ui('idle'))}</span></article><article class="test-card output" data-test-card="output" style="left:650px;top:75px"><h3>${ui('output')}</h3>${outputURL ? `<img src="${esc(outputURL)}" alt="${ui('latestOutput')}" data-lightbox-src="${esc(outputURL)}">` : `<div class="muted">${esc(currentJob ? `任务 ${currentJob.status || 'pending'}` : '运行后预览图片')}</div>`}</article>`;
  const prompt = $('test-canvas-prompt'); if (prompt) { prompt.value = testCanvasPrompt; prompt.addEventListener('input', (e) => { testCanvasPrompt = e.target.value; }); }
  host.querySelector('[data-action="test-canvas-run"]')?.addEventListener('click', () => testJob());
  host.querySelectorAll('[data-lightbox-src]').forEach((img) => img.addEventListener('click', () => window.open(img.dataset.lightboxSrc, '_blank', 'noopener')));
}
function syncTestCanvasView() { const host = $('test-cards'); if (host) host.style.transform = `translate(${testCanvasView.x}px,${testCanvasView.y}px) scale(${testCanvasView.k})`; }
document.querySelectorAll('[data-canvas-mode]').forEach((b) => b.addEventListener('click', () => { if (b.dataset.canvasMode === 'test') renderTestCards(); }));
if ($('test-canvas')) {
  $('test-canvas').addEventListener('wheel', (e) => { e.preventDefault(); testCanvasView.k = Math.max(.45, Math.min(2, testCanvasView.k * (e.deltaY < 0 ? 1.1 : .9))); syncTestCanvasView(); }, { passive: false });
  $('test-canvas').addEventListener('pointerdown', (e) => { if (e.target.closest('button,textarea,input,select,img')) return; testCanvasPointer = { x: e.clientX, y: e.clientY, ox: testCanvasView.x, oy: testCanvasView.y }; $('test-canvas').setPointerCapture(e.pointerId); });
  $('test-canvas').addEventListener('pointermove', (e) => { if (!testCanvasPointer) return; testCanvasView.x = testCanvasPointer.ox + e.clientX - testCanvasPointer.x; testCanvasView.y = testCanvasPointer.oy + e.clientY - testCanvasPointer.y; syncTestCanvasView(); });
  $('test-canvas').addEventListener('pointerup', () => { testCanvasPointer = null; });
}
const originalTestJob = testJob;
testJob = async function testJobWithCanvas() { const prompt = testCanvasPrompt.trim(); if (prompt) { const originalRead = readDynamicFields; const values = originalRead(); values.text = prompt; fieldValues = values; } await originalTestJob(); renderTestCards(); };
const originalRenderJob = renderJob;
renderJob = function renderJobWithCanvas(job) { if (job?.unit_id || job?.trigger === 'unit') renderBridgeJob(job); else { originalRenderJob(job); renderTestCards(); } };
translateStaticUI();

function normalizeFieldSource(field = {}) {
  const source = String(field.source || '').toLowerCase();
  if (['prompter', 'default', 'runtime'].includes(source)) return source;
  const type = field.value_type || field.type || 'string';
  const control = field.control || 'text';
  return type === 'string' && (control === 'text' || control === 'textarea') ? 'prompter' : 'default';
}

function validateCanvasFieldIDs(fields) {
  const seen = new Set();
  for (const field of fields) {
    if (!/^[A-Za-z_][A-Za-z0-9_.-]{0,63}$/.test(field.id || '')) return `JSON 字段键“${field.id || '(空)'}”格式无效，请使用字母或下划线开头，最多 64 个字符。`;
    if (seen.has(field.id)) return `JSON 字段键“${field.id}”重复，请为每个字段设置唯一键。`;
    seen.add(field.id);
  }
  return '';
}

function refreshBridgeWorkflowOptions() {
  const select = $('bridge-workflow-id');
  if (!select) return;
  const current = select.value || bridgeConfig?.workflow_id || selectedWorkflow?.id || '';
  select.innerHTML = `<option value="">请选择工作流</option>${workflows.map((workflow) => `<option value="${esc(workflow.id)}">${esc(workflow.name || workflow.id)}</option>`).join('')}`;
  if (workflows.some((workflow) => workflow.id === current)) select.value = current;
}

function renderBridgeConfig(config = {}) {
  bridgeConfig = { ...config };
  refreshBridgeWorkflowOptions();
  $('bridge-enabled').checked = !!config.enabled;
  $('bridge-auto-generate').checked = !!config.auto_generate;
  $('bridge-workflow-id').value = config.workflow_id || selectedWorkflow?.id || '';
  $('bridge-strict').checked = config.strict !== false;
  $('bridge-parser-strict').checked = config.strict !== false;
  $('bridge-prompter-timeout').value = config.prompter_timeout_ms ?? 120000;
  $('bridge-previous-tail').value = config.previous_tail_chars ?? 1200;
  $('bridge-state').textContent = config.enabled ? '已启用' : '未启用';
  $('bridge-state').className = `job-status ${config.enabled ? 'succeeded' : ''}`;
}

async function loadBridgeConfig() {
  try {
    renderBridgeConfig(await api('/api/v2/comfyui/bridge'));
    const workflowID = bridgeConfig?.workflow_id || selectedWorkflow?.id;
    if (workflowID && selectedWorkflow?.id !== workflowID && workflows.some((workflow) => workflow.id === workflowID)) await selectWorkflow(workflowID);
    if (workflowID) await loadPromptSchema(workflowID, { silent: true });
    setBridgeUnitDefaults();
  } catch (error) {
    $('bridge-state').textContent = '加载失败';
    $('bridge-state').className = 'job-status failed';
    notify(`桥接设置加载失败：${error.message}`, 'bridge-msg', 'error');
  }
}

function readBridgeConfig() {
  return {
    enabled: $('bridge-enabled').checked,
    auto_generate: $('bridge-auto-generate').checked,
    workflow_id: $('bridge-workflow-id').value,
    strict: $('bridge-strict').checked,
    prompter_timeout_ms: Number($('bridge-prompter-timeout').value),
    previous_tail_chars: Number($('bridge-previous-tail').value),
  };
}

async function saveBridgeConfig() {
  const draft = readBridgeConfig();
  if (!draft.workflow_id) return notify('请选择用于小说单元图片的工作流。', 'bridge-msg', 'error');
  if (draft.prompter_timeout_ms < 1000 || draft.prompter_timeout_ms > 600000) return notify('提示词生成超时必须在 1000 到 600000 毫秒之间。', 'bridge-msg', 'error');
  if (draft.previous_tail_chars < 0 || draft.previous_tail_chars > 8000) return notify('上一单元结尾字符数必须在 0 到 8000 之间。', 'bridge-msg', 'error');
  try {
    renderBridgeConfig(await api('/api/v2/comfyui/bridge', { method: 'PUT', body: JSON.stringify(draft) }));
    applyPrompterEditorToWorkflow();
    await persistCanvas();
    await loadPromptSchema(draft.workflow_id, { silent: false });
    notify('小说图片桥接设置已保存。', 'bridge-msg', 'success');
  } catch (error) {
    notify(`桥接设置保存失败：${error.message}`, 'bridge-msg', 'error');
    renderBridgeValidationErrors(error, 'bridge-parser-result');
  }
}

function schemaSample(fields = []) {
  return Object.fromEntries(fields.map((field) => {
    const type = field.value_type || bridgeSchema?.schema?.properties?.[field.id]?.type;
    if (type === 'integer' || type === 'number') return [field.id, 0];
    if (type === 'boolean') return [field.id, false];
    return [field.id, field.id.includes('negative') ? '低质量，模糊' : '电影感场景描述'];
  }));
}

function applyPrompterEditorToWorkflow() {
  if (!selectedWorkflow) return;
  const template = $('bridge-prompter-template');
  if (template?.dataset.loaded === 'true') selectedWorkflow.prompter_template = template.value;
  const preset = $('bridge-prompter-preset');
  if (preset?.value) selectedWorkflow.prompter_preset = preset.value;
  const notesHost = $('bridge-field-notes');
  if (!notesHost) return;
  const inputs = notesHost.querySelectorAll('[data-field-note]');
  if (!inputs.length) return;
  const notes = {};
  inputs.forEach((el) => { notes[el.dataset.fieldNote] = el.value; });
  if (selectedWorkflow.config?.fields) {
    selectedWorkflow.config.fields = selectedWorkflow.config.fields.map((f) => ({ ...f, note: notes[f.id] ?? f.note ?? '' }));
  }
}

function renderPrompterPresets(data = null) {
  const select = $('bridge-prompter-preset');
  if (!select) return;
  if (!qs('[data-action="save-prompter-preset"]')) {
    const label = select.closest('label');
    const toolbar = document.createElement('div');
    toolbar.className = 'preset-toolbar bridge-prompter-toolbar';
    label?.parentNode?.insertBefore(toolbar, label);
    if (label) {
      label.classList.add('preset-label');
      label.htmlFor = select.id;
      toolbar.appendChild(label);
      toolbar.appendChild(select);
    }
    toolbar.insertAdjacentHTML('beforeend', '<button type="button" class="small" data-action="save-prompter-preset">覆盖保存</button><button type="button" class="small" data-action="save-prompter-preset-as">另存为</button>');
    qs('[data-action="save-prompter-preset"]')?.addEventListener('click', () => savePrompterPreset($('bridge-prompter-preset')?.value, 'save', true));
    qs('[data-action="save-prompter-preset-as"]')?.addEventListener('click', async () => { const name = window.prompt('请输入新的生图提示词预设名称'); if (name?.trim()) await savePrompterPreset(name.trim(), 'save_as', false); });
  }
  if (Array.isArray(data?.presets) && data.presets.length) bridgePrompterPresets = data.presets;
  const current = selectedWorkflow?.prompter_preset || data?.preset || 'natural';
  select.innerHTML = bridgePrompterPresets.map((preset) => `<option value="${esc(preset.id)}" ${preset.id === current ? 'selected' : ''}>${esc(preset.label)}</option>`).join('');
  if (![...select.options].some((option) => option.value === current) && current) {
    select.insertAdjacentHTML('beforeend', `<option value="${esc(current)}" selected>${esc(current)}</option>`);
  }
  const selected = bridgePrompterPresets.find((preset) => preset.id === select.value);
  const help = $('bridge-prompter-preset-help');
  if (help) help.textContent = selected?.description || '';
}

function applyPrompterPreset(id) {
  const preset = bridgePrompterPresets.find((item) => item.id === id);
  if (!preset) return;
  if (selectedWorkflow) {
    selectedWorkflow.prompter_preset = id;
    selectedWorkflow.prompter_template = preset.template || '';
  }
  const template = $('bridge-prompter-template');
  if (template) {
    template.value = preset.template || '';
    template.dataset.dirty = 'true';
    template.dataset.loaded = 'true';
  }
  const help = $('bridge-prompter-preset-help');
  if (help) help.textContent = preset.description || '';
}

async function savePrompterPreset(name, action = 'save', overwrite = true) {
  const workflowID = $('bridge-workflow-id')?.value || selectedWorkflow?.id;
  const template = $('bridge-prompter-template')?.value || '';
  if (!workflowID) return notify('请先选择已保存的工作流。', 'bridge-msg', 'error');
  if (!name?.trim()) return notify('生图提示词预设名称不能为空。', 'bridge-msg', 'error');
  if (!template.trim()) return notify('提示词模板不能为空。', 'bridge-msg', 'error');
  try {
    const data = await api('/api/v2/comfyui/prompter-presets', { method: 'PUT', body: JSON.stringify({ action, name: name.trim(), workflow_id: workflowID, template, overwrite }) });
    if (selectedWorkflow) {
      selectedWorkflow.prompter_preset = data.preset || name.trim();
      selectedWorkflow.prompter_template = data.template || template;
    }
    const editor = $('bridge-prompter-template');
    if (editor) editor.dataset.dirty = 'false';
    renderPromptSchema(data);
    notify('生图提示词已保存并应用到当前工作流。', 'bridge-msg', 'success');
  } catch (error) {
    notify(`生图提示词保存失败：${error.message}`, 'bridge-msg', 'error');
  }
}

function renderPromptSchema(data = null, error = null) {
  bridgeSchema = data;
  const fields = data?.fields || [];
  const jsonHost = $('bridge-field-json');
  const notesHost = $('bridge-field-notes');
  const template = $('bridge-prompter-template');
  if (error || !data) {
    if (jsonHost) jsonHost.textContent = '{}';
    if (notesHost) notesHost.innerHTML = `<span class="muted">${esc(error || '请先选择已保存的工作流。')}</span>`;
    return;
  }
  renderPrompterPresets(data);
  if (template && template.dataset.dirty !== 'true') {
    template.value = data.template || data.default_template || '';
    template.dataset.loaded = 'true';
    if (data.preset && selectedWorkflow && !selectedWorkflow.prompter_preset) selectedWorkflow.prompter_preset = data.preset;
  } else if (template) {
    template.dataset.loaded = 'true';
  }
  const existingNotes = {};
  notesHost?.querySelectorAll('[data-field-note]').forEach((el) => { existingNotes[el.dataset.fieldNote] = el.value; });
  if (jsonHost) jsonHost.textContent = JSON.stringify(data.field_json || {}, null, 2);
  if (notesHost) {
    notesHost.innerHTML = fields.length ? fields.map((field) => {
      const note = existingNotes[field.id] ?? field.note ?? '';
      const type = field.value_type || 'string';
      return `<label class="field-note"><code>${esc(field.id)}</code><small>#${esc(field.node_id)} · ${esc(field.input)} · ${esc(type)}</small><textarea data-field-note="${esc(field.id)}" rows="2" placeholder="填写该字段的含义、约束或示例">${esc(note)}</textarea></label>`;
    }).join('') : '<span class="muted">请先在节点中勾选字段。</span>';
  }
  const raw = $('bridge-parser-raw');
  if (raw && raw.dataset.schemaHash !== (data.schema_hash || '')) {
    raw.dataset.schemaHash = data.schema_hash || '';
    raw.dataset.edited = 'false';
  }
  if (raw && raw.dataset.edited !== 'true') raw.value = JSON.stringify(data.field_json || schemaSample(fields), null, 2);
}

async function loadPromptSchema(workflowID, options = {}) {
  if (!workflowID) {
    renderPromptSchema(null, '请先选择工作流。');
    return null;
  }
  try {
    const data = await api(`/api/v2/comfyui/workflows/${encodeURIComponent(workflowID)}/prompt-schema`);
    renderPromptSchema(data);
    return data;
  } catch (error) {
    renderPromptSchema(null, error.message);
    if (!options.silent) notify(`提示词字段加载失败：${error.message}`, 'bridge-msg', 'error');
    return null;
  }
}

function renderBridgeValidationErrors(error, targetID = 'bridge-parser-result') {
  const target = $(targetID);
  if (!target) return;
  const data = error?.body?.data || {};
  const errors = Array.isArray(data.errors) ? data.errors : [];
  target.className = 'parser-result error';
  target.innerHTML = `<strong>${esc(error?.message || '校验失败')}</strong>${error?.body?.code ? `<small>错误码：${esc(error.body.code)}</small>` : ''}${data.stage ? `<small>阶段：${esc(data.stage)}</small>` : ''}${errors.length ? `<ul>${errors.map((item) => `<li>${item.field ? `<code>${esc(item.field)}</code> ` : ''}${esc(item.message || item.rule || String(item))}</li>`).join('')}</ul>` : ''}`;
}

async function parsePrompterJSON() {
  const workflowID = $('bridge-workflow-id').value || selectedWorkflow?.id;
  if (!workflowID) return notify('请先选择桥接工作流。', 'bridge-parser-result', 'error');
  const body = { workflow_id: workflowID, raw: $('bridge-parser-raw').value, strict: $('bridge-parser-strict').checked };
  try {
    const result = await api('/api/v2/comfyui/prompter/parse', { method: 'POST', body: JSON.stringify(body) });
    $('bridge-parser-result').className = 'parser-result success';
    $('bridge-parser-result').innerHTML = `<strong>JSON 合法，字段可以绑定</strong><pre>${esc(JSON.stringify(result.values || {}, null, 2))}</pre>${(result.warnings || []).length ? `<small>${esc(result.warnings.join('；'))}</small>` : ''}`;
  } catch (error) {
    renderBridgeValidationErrors(error);
  }
}

function setBridgeUnitDefaults() {
  if (!$('bridge-unit-chapter').value && currentState?.CurrentChapter) $('bridge-unit-chapter').value = currentState.CurrentChapter;
  if (!$('bridge-unit-ordinal').value && currentState?.CurrentUnit) $('bridge-unit-ordinal').value = currentState.CurrentUnit;
}

async function loadUnitImageJob() {
  const chapter = Number($('bridge-unit-chapter').value);
  const ordinal = Number($('bridge-unit-ordinal').value);
  if (!Number.isInteger(chapter) || chapter < 1 || !Number.isInteger(ordinal) || ordinal < 1) return;
  try {
    renderBridgeJob(await api(`/api/v2/units/${chapter}/${ordinal}/image-job`));
  } catch (error) {
    if (error.status !== 404) notify(`已有任务读取失败：${error.message}`, 'bridge-job-error', 'error');
  }
}

const BRIDGE_TERMINAL_STATES = new Set(['succeeded', 'completed', 'failed', 'timeout', 'cancelled']);
function renderBridgeJob(job = {}) {
  if (!job.job_id && !job.id) return;
  bridgeJob = { ...bridgeJob, ...job };
  const id = bridgeJob.job_id || bridgeJob.id;
  const status = bridgeJob.status || 'pending';
  const labels = { pending: '等待中', prompting: '生成提示词', validating: '校验 JSON', binding: '绑定字段', submitting: '提交中', queued: '排队中', running: '生成中', succeeded: '已完成', completed: '已完成', failed: '失败', timeout: '超时', cancelled: '已取消' };
  $('bridge-job-status').textContent = labels[status] || status;
  $('bridge-job-status').className = `job-status ${status}`;
  $('bridge-job-stage').textContent = [bridgeJob.stage || bridgeJob.phase, bridgeJob.attempt ? `第 ${bridgeJob.attempt} 次` : '', bridgeJob.schema_hash || ''].filter(Boolean).join(' · ');
  const error = bridgeJob.error;
  $('bridge-job-error').textContent = typeof error === 'string' ? error : error?.message || '';
  $('bridge-job-error').className = `notice ${error ? 'error' : ''}`;
  $('bridge-prompt-values').textContent = JSON.stringify(bridgeJob.prompt_values || {}, null, 2);
  const chapter = bridgeJob.chapter || Number($('bridge-unit-chapter').value);
  const ordinal = bridgeJob.ordinal || Number($('bridge-unit-ordinal').value);
  const output = Array.isArray(bridgeJob.outputs) ? bridgeJob.outputs[0] : bridgeJob.output;
  const outputURL = output?.url || output?.preview_url || (status === 'completed' || status === 'succeeded' ? `/api/v2/units/${encodeURIComponent(chapter)}/${encodeURIComponent(ordinal)}/image?v=${encodeURIComponent(id)}` : '');
  $('bridge-job-output').innerHTML = outputURL ? `<img src="${esc(outputURL)}" alt="单元生成图片" data-lightbox-src="${esc(outputURL)}"><small>${esc(bridgeJob.unit_id || `第 ${chapter} 章 · 单元 ${ordinal}`)}</small>` : '<span class="muted">生成完成后在此显示图片。</span>';
  const image = $('bridge-job-output').querySelector('img');
  if (image) image.onerror = () => { $('bridge-job-output').innerHTML = '<span class="notice error">图片文件无法显示，请检查任务输出和媒体接口。</span>'; };
  const terminal = BRIDGE_TERMINAL_STATES.has(status);
  qs('[data-action="retry-unit-image"]').disabled = !terminal || status === 'completed' || status === 'succeeded';
  qs('[data-action="regenerate-unit-prompt"]').disabled = !terminal || status === 'completed' || status === 'succeeded';
  qs('[data-action="cancel-unit-image"]').disabled = terminal;
  clearTimeout(renderBridgeJob.pollTimer);
  if (!terminal) renderBridgeJob.pollTimer = setTimeout(() => refreshBridgeJob(id), 1500);
}

async function generateUnitImage() {
  const chapter = Number($('bridge-unit-chapter').value);
  const ordinal = Number($('bridge-unit-ordinal').value);
  if (!Number.isInteger(chapter) || chapter < 1 || !Number.isInteger(ordinal) || ordinal < 1) return notify('请输入有效的章节和单元序号。', 'bridge-job-error', 'error');
  const body = { workflow_id: $('bridge-workflow-id').value || selectedWorkflow?.id || '', force: $('bridge-unit-force').checked };
  try {
    $('bridge-job-error').textContent = '';
    renderBridgeJob(await api(`/api/v2/units/${chapter}/${ordinal}/image/generate`, { method: 'POST', body: JSON.stringify(body) }));
  } catch (error) {
    renderBridgeValidationErrors(error, 'bridge-job-error');
  }
}

async function refreshBridgeJob(id) {
  try { renderBridgeJob(await api(`/api/v2/comfyui/jobs/${encodeURIComponent(id)}`)); }
  catch (error) { notify(`图片任务状态读取失败：${error.message}`, 'bridge-job-error', 'error'); }
}

async function bridgeJobAction(action, regeneratePrompt = false) {
  const id = bridgeJob?.job_id || bridgeJob?.id;
  if (!id) return notify('当前没有可操作的单元图片任务。', 'bridge-job-error', 'error');
  try {
    renderBridgeJob(await api(`/api/v2/comfyui/jobs/${encodeURIComponent(id)}/${action}`, { method: 'POST', body: JSON.stringify(action === 'retry' ? { regenerate_prompt: regeneratePrompt } : {}) }));
  } catch (error) {
    renderBridgeValidationErrors(error, 'bridge-job-error');
  }
}

qs('[data-action="open-bridge"]')?.addEventListener('click', () => showInspector('bridge'));
qs('[data-action="save-bridge"]')?.addEventListener('click', saveBridgeConfig);
qs('[data-action="refresh-prompter-prompt"]')?.addEventListener('click', () => loadPromptSchema($('bridge-workflow-id').value || selectedWorkflow?.id, { silent: false }));
qs('[data-action="parse-prompter-json"]')?.addEventListener('click', parsePrompterJSON);
qs('[data-action="generate-unit-image"]')?.addEventListener('click', generateUnitImage);
qs('[data-action="load-unit-image-job"]')?.addEventListener('click', loadUnitImageJob);
qs('[data-action="retry-unit-image"]')?.addEventListener('click', () => bridgeJobAction('retry', false));
qs('[data-action="regenerate-unit-prompt"]')?.addEventListener('click', () => bridgeJobAction('retry', true));
qs('[data-action="cancel-unit-image"]')?.addEventListener('click', () => bridgeJobAction('cancel'));
$('bridge-prompter-template')?.addEventListener('input', (event) => { event.target.dataset.dirty = 'true'; event.target.dataset.loaded = 'true'; });
$('bridge-prompter-preset')?.addEventListener('change', (event) => applyPrompterPreset(event.target.value));
$('bridge-workflow-id')?.addEventListener('change', async (event) => {
  const workflowID = event.target.value;
  if (workflowID && workflowID !== selectedWorkflow?.id) await selectWorkflow(workflowID);
  await loadPromptSchema(workflowID, { silent: true });
});
$('bridge-parser-raw')?.addEventListener('input', (event) => { event.target.dataset.edited = 'true'; });
$('bridge-job-output')?.addEventListener('click', (event) => { const src = event.target.dataset.lightboxSrc; if (src) window.open(src, '_blank', 'noopener'); });

// Save the API workflow and canvas projection in order. Reloading the workflow
// before the canvas PUT can resurrect stale exposed fields from disk.
async function saveWorkflowDefinition(silent = false) {
  if (!selectedWorkflow) return false;
  const apiJSON = workflowAPI(selectedWorkflow);
  if (!Object.keys(apiJSON).length) {
    notify('请先导入 ComfyUI API JSON', 'workflow-msg', 'error');
    return false;
  }
  const fields = canvasFields();
  const fieldError = validateCanvasFieldIDs(fields);
  if (fieldError) {
    notify(fieldError, 'workflow-msg', 'error');
    return false;
  }
  const config = {
    format: 'ainovel_workflow_config_v1',
    version: 1,
    fields,
    bindings: fields.map((f) => ({ key: f.id, node_id: f.node_id, path: `inputs.${f.input}`, type: f.value_type || 'string', required: false })),
    defaults: Object.fromEntries(fields.map((f) => [f.id, f.default])),
    outputs: selectedWorkflow.config?.outputs || [],
  };
  const body = {
    format: 'comfyui_api_v1',
    id: selectedWorkflow.id,
    name: selectedWorkflow.name || 'workflow',
    api_json: apiJSON,
    workflow: apiJSON,
    config,
    bindings: config.bindings,
    defaults: config.defaults,
    output: selectedWorkflow.output || {},
    enabled: true,
    instance_id: document.querySelector('input[name="default-instance"]:checked')?.value || '',
  };
  try {
    const saved = normalizeWorkflowResponse(await api(selectedWorkflow.id ? `/api/v2/comfyui/workflows/${encodeURIComponent(selectedWorkflow.id)}` : '/api/v2/comfyui/workflows/import', {
      method: selectedWorkflow.id ? 'PUT' : 'POST',
      body: JSON.stringify(body),
    }));
    const savedID = saved.id || selectedWorkflow.id;
    selectedWorkflow = { ...saved, config, bindings: config.bindings, defaults: config.defaults, workflow: apiJSON, api_json: apiJSON, id: savedID, prompter_template: selectedWorkflow.prompter_template || '', prompter_preset: selectedWorkflow.prompter_preset || '' };
    // This must happen before loadWorkflows/selectWorkflow reads the canvas.
    if (!await persistCanvas()) return false;
    await loadWorkflows(savedID);
    if (!silent) notify('工作流已保存', 'workflow-msg', 'success');
    return true;
  } catch (error) {
    notify(`工作流保存失败：${error.message}`, 'workflow-msg', 'error');
    return false;
  }
}

// Canvas fields with exposed:false are legacy tombstones and must not appear
// in the run inspector or be written back to the canonical document.
function canvasFields(workflow = selectedWorkflow) {
  const cfg = workflow?.config || workflow?.schema || {};
  const fields = Array.isArray(cfg) ? cfg : (cfg.fields || workflow?.fields || []);
  return fields.filter((f) => f && f.exposed !== false).map((f) => ({
    ...f,
    id: f.id || `${f.node_id || f.node || ''}::${f.input || f.key || ''}`,
    node_id: f.node_id || f.node || '',
    input: f.input || f.key || '',
    label: f.label || f.name || f.input || f.key || '',
    control: f.control || 'text',
    value_type: f.value_type || f.type || 'string',
    source: normalizeFieldSource(f),
    note: f.note || '',
  }));
}

async function persistCanvas() {
  if (!selectedWorkflow?.id) return true;
  applyPrompterEditorToWorkflow();
  const fields = canvasFields().map((f) => ({ id: f.id, node_id: f.node_id, input: f.input, name: f.label || f.name || f.input, control: f.control, value_type: f.value_type || 'string', default: f.default, min: f.min, max: f.max, step: f.step, options: f.options || [], required: !!f.required, random_enabled: !!f.random_enabled, exposed: true, source: normalizeFieldSource(f), note: f.note || '' }));
  const list = nodeList();
  const nodes = list.map((n) => ({ id: `node-${n.id}`, kind: 'workflow', source_node_id: String(n.id), class_type: n.class_type || '', label: n.class_type || '', x: canvasPositions[n.id]?.x || 40, y: canvasPositions[n.id]?.y || 40, width: 176, height: 70, exposed_field_ids: fields.filter((f) => String(f.node_id) === String(n.id)).map((f) => f.id) }));
  const edges = [];
  list.forEach((n) => Object.entries(n.inputs || {}).forEach(([key, value]) => { if (Array.isArray(value) && value.length) edges.push({ id: `edge-${value[0]}-${n.id}-${key}`, source: `node-${value[0]}`, source_handle: `output-${value[1]}`, target: `node-${n.id}`, target_handle: key, kind: 'workflow', label: key }); }));
  const payload = { format: 'ainovel_comfy_canvas_v1', version: 1, id: selectedWorkflow.id, workflow_id: selectedWorkflow.id, title: selectedWorkflow.name || '', viewport: { x: canvasView.x, y: canvasView.y, scale: canvasView.k, min_scale: .2, max_scale: 3 }, nodes, edges, fields, prompter_preset: selectedWorkflow.prompter_preset || '', prompter_template: selectedWorkflow.prompter_template || '', mini_test_cards: [] };
  localStorage.setItem(canvasKey(selectedWorkflow.id), JSON.stringify(payload));
  try {
    await api(`/api/v2/comfyui/workflows/${encodeURIComponent(selectedWorkflow.id)}/canvas`, { method: 'PUT', body: JSON.stringify(payload) });
    return true;
  } catch (error) {
    notify(`画布保存失败：${error.message}`, 'workflow-msg', 'error');
    return false;
  }
}
