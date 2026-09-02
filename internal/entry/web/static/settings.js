/* App settings: interactive import and writing-rule presets. */
(() => {
  let workflowSettingsDoc = null;
  let importStatus = null;
  let importPollTimer = 0;
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
  function renderImportStatus(status = {}) {
    importStatus = status;
    const state = status.state || 'idle';
    const labels = { idle: '空闲', running: '导入中', awaiting_confirmation: '等待确认切分', awaiting_story_status: '等待故事状态', paused: '可恢复', completed: '已完成', failed: '失败', cancelled: '已取消' };
    $('import-state').textContent = labels[state] || state;
    $('import-state').className = `job-status ${state === 'completed' ? 'succeeded' : state === 'failed' ? 'failed' : ''}`;
    $('import-start').textContent = status.can_resume && !['awaiting_confirmation', 'awaiting_story_status'].includes(state) ? '恢复导入' : '开始导入';
    $('import-start').disabled = state === 'running' || ['awaiting_confirmation', 'awaiting_story_status'].includes(state);
    $('import-confirm').hidden = state !== 'awaiting_confirmation';
    $('import-resegment').hidden = state !== 'awaiting_confirmation';
    $('import-resolve-open').hidden = state !== 'awaiting_story_status';
    $('import-resolve-closed').hidden = state !== 'awaiting_story_status';
    $('import-cancel').hidden = state !== 'running';
    $('import-preview').hidden = !status.preview;
    $('import-preview').textContent = status.preview || '';
    $('import-resume').textContent = status.can_resume ? status.message || '检测到未完成导入，可从当前状态继续。' : status.message || '';
    if (status.source_path && !$('import-source-path').value) $('import-source-path').value = status.source_path;
    if (status.guidance && !$('import-guidance').value) $('import-guidance').value = status.guidance;
    const history = status.history || [];
    $('import-history').innerHTML = history.length ? history.map((item) => `<div class="import-history-item ${item.error ? 'error' : ''}"><time>${esc(new Date(item.time || Date.now()).toLocaleTimeString())}</time><strong>${esc(item.stage || 'progress')}</strong><span>${item.total ? `${esc(item.current)}/${esc(item.total)} ` : ''}${esc(item.message || '')}</span>${item.error ? `<small>${esc(item.error)}</small>` : ''}</div>`).join('') : '<span class="muted">暂无导入进度</span>';
    clearTimeout(importPollTimer);
    if (state === 'running') importPollTimer = window.setTimeout(loadImportStatus, 1200);
  }
  async function loadImportStatus() {
    try {
      renderImportStatus(await api('/api/v2/import/status'));
    } catch (error) {
      notify(`导入状态读取失败：${error.message}`, 'settings-msg', 'error');
    }
  }
  async function importAction(path, body = {}) {
    try {
      renderImportStatus(await api(path, { method: 'POST', body: JSON.stringify(body) }));
      notify('导入请求已接受。', 'settings-msg', 'success');
    } catch (error) {
      notify(`导入操作失败：${error.message}`, 'settings-msg', 'error');
      await loadImportStatus();
    }
  }
  function startImport() {
    const resuming = Boolean(importStatus?.can_resume && !['awaiting_confirmation', 'awaiting_story_status'].includes(importStatus.state));
    const sourcePath = $('import-source-path').value.trim();
    if (!resuming && !sourcePath) {
      notify('请输入服务端本地文件的绝对路径。', 'settings-msg', 'error');
      return;
    }
    importAction('/api/v2/import/start', { source_path: resuming ? '' : sourcePath, story_status: $('import-story-status').value, guidance: $('import-guidance').value, continue_after: $('import-continue-after').checked });
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
  $('import-refresh')?.addEventListener('click', loadImportStatus);
  $('import-start')?.addEventListener('click', startImport);
  $('import-confirm')?.addEventListener('click', () => importAction('/api/v2/import/confirm'));
  $('import-resegment')?.addEventListener('click', () => importAction('/api/v2/import/resegment', { guidance: $('import-guidance').value }));
  $('import-resolve-open')?.addEventListener('click', () => importAction('/api/v2/import/resolve', { story_status: 'open' }));
  $('import-resolve-closed')?.addEventListener('click', () => importAction('/api/v2/import/resolve', { story_status: 'closed' }));
  $('import-cancel')?.addEventListener('click', () => importAction('/api/v2/import/cancel'));
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
