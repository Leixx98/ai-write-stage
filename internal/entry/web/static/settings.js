/* App settings: interactive import overlay and writing-rule presets. */
(() => {
  let workflowSettingsDoc = null;
  let importStatus = null;
  let importPollTimer = 0;
  let importEventSource = null;
  let importStreamSource = null;
  let importEventReconnect = 0;
  let importStreamReconnect = 0;
  const IMPORT_EVENT_LIMIT = 400;
  async function saveWorkflowSettings() {
    const previous = workflowSettingsDoc || {};
    const rules = $('writing-rules').value;
    try {
      workflowSettingsDoc = await api('/api/v2/settings/workflow', { method: 'PUT', body: JSON.stringify({ writing_rules: rules }) });
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
    const parts = [];
    if (actions.length) parts.push(actions.join('，'));
    if (warnings.length) parts.push(warnings.join('；'));
    notify(parts.length ? `设置已保存；${parts.join('；')}` : '设置已保存', 'settings-msg', warnings.length ? 'error' : 'success');
  }
  async function loadWorkflowSettingsUI() {
    try {
      workflowSettingsDoc = await api('/api/v2/settings/workflow');
      renderWritingRulePresets();
      await loadImportStatus();
    } catch (error) { notify(`设置加载失败：${error.message}`, 'settings-msg', 'error'); }
  }
  function renderWritingRulePresets() {
    if (!workflowSettingsDoc) return;
    const select = $('writing-rules-preset');
    const presets = workflowSettingsDoc.writing_rule_presets || {};
    select.innerHTML = Object.keys(presets).map((name) => `<option value="${esc(name)}" ${name === workflowSettingsDoc.active_writing_rules_preset ? 'selected' : ''}>${esc(name)}</option>`).join('');
    $('writing-rules').value = workflowSettingsDoc.writing_rules || presets[workflowSettingsDoc.active_writing_rules_preset]?.text || '';
  }
  function unfinishedImport(status = {}) {
    const state = status.state || 'idle';
    return state === 'running' || status.can_resume && !['idle', 'completed'].includes(state);
  }
  function applyImportBadge(status = {}) {
    const state = status.state || 'idle';
    const labels = { idle: '空闲', running: '导入中', awaiting_confirmation: '等待确认切分', awaiting_story_status: '等待故事状态', paused: '可恢复', completed: '已完成', failed: '失败', cancelled: '已取消' };
    const label = labels[state] || state;
    const cls = `job-status ${state === 'completed' ? 'succeeded' : state === 'failed' ? 'failed' : ''}`;
    ['import-state', 'import-dialog-state'].forEach((id) => {
      if (!$(id)) return;
      $(id).textContent = label;
      $(id).className = cls;
    });
    if ($('import-open')) $('import-open').textContent = unfinishedImport(status) ? '继续导入' : '打开导入';
    const cancelSettings = $('import-cancel-settings');
    if (cancelSettings) cancelSettings.hidden = state !== 'running';
  }
  function renderImportControls(status = {}) {
    const state = status.state || 'idle';
    const waitingConfirm = state === 'awaiting_confirmation';
    const waitingStory = state === 'awaiting_story_status';
    const running = state === 'running';
    const resuming = Boolean(status.can_resume && !waitingConfirm && !waitingStory);
    $('import-start').textContent = resuming ? '恢复导入' : '开始导入';
    $('import-start').disabled = running || waitingConfirm || waitingStory;
    $('import-confirm').hidden = !waitingConfirm;
    $('import-resegment').hidden = !waitingConfirm;
    $('import-resolve-open').hidden = !waitingStory;
    $('import-resolve-closed').hidden = !waitingStory;
    $('import-cancel').hidden = !running;
    $('import-preview').hidden = !status.preview;
    $('import-preview').textContent = status.preview || '';
    $('import-resume').textContent = status.can_resume ? status.message || '检测到未完成导入，可从当前状态继续。' : status.message || '';
    if (status.source_path && !$('import-source-path').value) $('import-source-path').value = status.source_path;
    if (status.guidance && !$('import-guidance').value) $('import-guidance').value = status.guidance;
  }
  function renderImportStatus(status = {}) {
    importStatus = status;
    applyImportBadge(status);
    if (importDialogOpen()) renderImportControls(status);
    clearTimeout(importPollTimer);
    if ((status.state || 'idle') === 'running') importPollTimer = window.setTimeout(loadImportStatus, 1200);
  }
  async function loadImportStatus() {
    try {
      renderImportStatus(await api('/api/v2/import/status'));
    } catch (error) {
      notify(`导入状态读取失败：${error.message}`, importDialogOpen() ? 'import-dialog-msg' : 'settings-msg', 'error');
    }
  }
  function importDialogOpen() {
    return !$('import-dialog')?.hidden;
  }
  function appendImportEvent(event) {
    const host = $('import-events');
    if (!host) return;
    const follow = host.scrollHeight - host.scrollTop - host.clientHeight <= 72;
    const element = document.createElement('div');
    const level = event.Level || event.level || '';
    element.className = level === 'error' ? 'event-error' : level === 'warn' ? 'event-warn' : '';
    element.innerHTML = `<span class="event-time">${esc(new Date(event.Time || event.time || Date.now()).toLocaleTimeString())}</span><strong>${esc(event.Category || event.category || 'IMPORT')}</strong> ${esc(event.Summary || event.summary || event.Detail || event.detail || '')}`;
    host.appendChild(element);
    while (host.childElementCount > IMPORT_EVENT_LIMIT) host.firstElementChild.remove();
    if (follow) host.scrollTop = host.scrollHeight;
  }
  function appendImportStream(payload = {}) {
    const view = $('import-stream');
    if (!view) return;
    if (payload.clear) {
      view.textContent = '';
      return;
    }
    const delta = payload.delta || payload.text || '';
    if (!delta) return;
    const follow = view.scrollHeight - view.scrollTop - view.clientHeight <= 72;
    view.textContent += delta;
    if (follow) view.scrollTop = view.scrollHeight;
  }
  function isImportEvent(event) {
    return String(event.Category || event.category || '').toUpperCase() === 'IMPORT';
  }
  function seedImportHistory(status = {}) {
    const host = $('import-events');
    if (!host) return;
    for (const item of status.history || []) {
      appendImportEvent({
        Time: item.time,
        Category: 'IMPORT',
        Summary: item.message || '',
        Level: item.error ? 'error' : item.level
      });
    }
  }
  async function replayImportLog() {
    const events = $('import-events');
    const stream = $('import-stream');
    if (events) events.replaceChildren();
    if (stream) stream.textContent = '';
    seedImportHistory(importStatus || {});
    try {
      const items = await api('/api/v2/replay');
      for (const item of items || []) {
        if (item.kind === 'stream_clear') appendImportStream({ clear: true });
        if (item.kind === 'stream_delta') appendImportStream(item.payload || {});
      }
    } catch (_) { /* replay is optional */ }
  }
  function connectImportEvents() {
    window.clearTimeout(importEventReconnect);
    importEventSource?.close();
    const source = new EventSource('/api/v2/events');
    importEventSource = source;
    source.onmessage = (event) => {
      try {
        const payload = JSON.parse(event.data);
        if (isImportEvent(payload)) appendImportEvent(payload);
      } catch (_) { /* keep the stream alive */ }
    };
    source.onerror = () => {
      if (importEventSource !== source) return;
      source.close();
      importEventSource = null;
      if (importDialogOpen()) importEventReconnect = window.setTimeout(connectImportEvents, 2500);
    };
  }
  function connectImportStream() {
    window.clearTimeout(importStreamReconnect);
    importStreamSource?.close();
    const source = new EventSource('/api/v2/stream');
    importStreamSource = source;
    source.onmessage = (event) => {
      try { appendImportStream(JSON.parse(event.data)); }
      catch (_) { /* keep the stream alive */ }
    };
    source.onerror = () => {
      if (importStreamSource !== source) return;
      source.close();
      importStreamSource = null;
      if (importDialogOpen()) importStreamReconnect = window.setTimeout(connectImportStream, 2500);
    };
  }
  function disconnectImportLog() {
    window.clearTimeout(importEventReconnect);
    window.clearTimeout(importStreamReconnect);
    importEventSource?.close();
    importStreamSource?.close();
    importEventSource = null;
    importStreamSource = null;
  }
  async function openImportDialog() {
    $('import-dialog').hidden = false;
    renderImportControls(importStatus || {});
    await replayImportLog();
    connectImportEvents();
    connectImportStream();
    await loadImportStatus();
  }
  function closeImportDialog() {
    $('import-dialog').hidden = true;
    disconnectImportLog();
  }
  function deepWindowValue() {
    const raw = Number($('import-deep-window').value);
    if (!Number.isFinite(raw) || raw < 0) return 20;
    return Math.floor(raw);
  }
  function importNoticeTarget() {
    return importDialogOpen() ? 'import-dialog-msg' : 'settings-msg';
  }
  async function importAction(path, body = {}) {
    try {
      renderImportStatus(await api(path, { method: 'POST', body: JSON.stringify(body) }));
      notify(path.endsWith('/cancel') ? '已取消导入。' : '导入请求已接受。', importNoticeTarget(), 'success');
    } catch (error) {
      notify(`导入操作失败：${error.message}`, importNoticeTarget(), 'error');
      await loadImportStatus();
    }
  }
  function startImport() {
    const resuming = Boolean(importStatus?.can_resume && !['awaiting_confirmation', 'awaiting_story_status'].includes(importStatus.state));
    const sourcePath = $('import-source-path').value.trim();
    if (!resuming && !sourcePath) {
      notify('请输入服务端本地文件的绝对路径。', 'import-dialog-msg', 'error');
      return;
    }
    importAction('/api/v2/import/start', {
      source_path: resuming ? '' : sourcePath,
      story_status: $('import-story-status').value,
      guidance: $('import-guidance').value,
      continue_after: $('import-continue-after').checked,
      deep_extract_chapters: deepWindowValue()
    });
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
  qs('[data-action="save-workflow"]')?.addEventListener('click', saveWorkflowSettings);
  $('import-open')?.addEventListener('click', openImportDialog);
  $('import-dialog-close')?.addEventListener('click', closeImportDialog);
  qs('[data-action="close-import"]')?.addEventListener('click', closeImportDialog);
  $('import-start')?.addEventListener('click', startImport);
  $('import-confirm')?.addEventListener('click', () => importAction('/api/v2/import/confirm'));
  $('import-resegment')?.addEventListener('click', () => importAction('/api/v2/import/resegment', { guidance: $('import-guidance').value }));
  $('import-resolve-open')?.addEventListener('click', () => importAction('/api/v2/import/resolve', { story_status: 'open' }));
  $('import-resolve-closed')?.addEventListener('click', () => importAction('/api/v2/import/resolve', { story_status: 'closed' }));
  $('import-cancel')?.addEventListener('click', () => importAction('/api/v2/import/cancel'));
  $('import-cancel-settings')?.addEventListener('click', () => importAction('/api/v2/import/cancel'));
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
  window.Settings = { load: loadWorkflowSettingsUI };
})();
