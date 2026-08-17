/* Writing-prompt presets for architect / planner / writer / editor. */
(() => {
  let promptPresetDoc = null;
  let promptsDirty = false;
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
  qs('[data-action="save-prompts"]')?.addEventListener('click', savePrompts);
  qs('[data-action="save-prompts-as"]')?.addEventListener('click', async () => { const name = window.prompt('请输入新的提示词组合名称'); if (name?.trim()) await savePrompts(name.trim(), 'save_as', false); });
  qs('[data-action="default-prompts"]')?.addEventListener('click', () => activatePromptPreset('默认配置'));
  $('prompt-preset')?.addEventListener('change', async (event) => {
    const name = event.target.value;
    if (promptsDirty) {
      const choice = window.prompt('当前提示词有未保存修改。输入 save 保存后切换，输入 discard 放弃修改，其他内容取消切换。', 'save');
      if (choice === 'save' && !(await savePrompts())) { renderPromptPresetOptions(); return; }
      if (choice !== 'save' && choice !== 'discard') { renderPromptPresetOptions(); return; }
    }
    await activatePromptPreset(name);
  });
  loadPrompts();
  window.Prompts = { load: loadPrompts };
})();
