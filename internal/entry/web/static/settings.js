/* App settings: import, imitate, and writing-rule presets. */
(() => {
  let workflowSettingsDoc = null;
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
  qs('[data-action="save-workflow"]')?.addEventListener('click', saveWorkflowSettings);
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
