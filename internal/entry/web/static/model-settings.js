/* Shared model library and workspace routing settings. */
(() => {
  const roles = [
    ['default', '默认模型'],
    ['architect', '架构师'],
    ['chapter_planner', '章节规划师'],
    ['writer', '写作者'],
    ['galgame', '酒馆对话'],
    ['editor', '编辑'],
    ['prompter', '图片提示词'],
    ['import_segment', '导入分段'],
    ['import_analyze', '导入分析'],
    ['import_synthesize', '导入整合'],
  ];
  const reasoningOptions = [
    ['', '模型默认'], ['off', '关闭'], ['low', '低'], ['medium', '中'],
    ['high', '高'], ['xhigh', '超高'], ['max', '最大'],
  ];
  let snapshot = null;
  let selectedProvider = '';
  let loading = false;

  function providers() { return snapshot?.providers || []; }
  function providerByName(name) { return providers().find((provider) => provider.name === name); }
  function modelValue(provider, model) { return JSON.stringify([provider, model]); }
  function parseModelValue(value) {
    try { const parsed = JSON.parse(value); return { provider: parsed[0] || '', model: parsed[1] || '' }; }
    catch { return { provider: '', model: '' }; }
  }
  function reasoningSelect(value, role, disabled = false) {
    return `<select data-model-reasoning="${esc(role)}" ${disabled ? 'disabled' : ''}>${reasoningOptions.map(([key, label]) => `<option value="${key}" ${key === (value || '') ? 'selected' : ''}>${label}</option>`).join('')}</select>`;
  }
  function modelOptions(value, allowInherit) {
    const options = [];
    if (allowInherit) options.push(`<option value="" ${!value ? 'selected' : ''}>跟随默认模型</option>`);
    providers().forEach((provider) => (provider.models || []).forEach((model) => {
      const optionValue = modelValue(provider.name, model.name);
      options.push(`<option value="${esc(optionValue)}" ${optionValue === value ? 'selected' : ''}>${esc(provider.name)} / ${esc(model.name)}</option>`);
    }));
    return options.join('');
  }

  function renderProviderList() {
    const host = $('model-provider-list');
    host.innerHTML = providers().map((provider) => `<button type="button" class="model-provider-item ${provider.name === selectedProvider ? 'active' : ''}" data-model-provider="${esc(provider.name)}"><strong>${esc(provider.name)}</strong><small>${provider.models?.length || 0} 个模型</small></button>`).join('');
    host.querySelectorAll('[data-model-provider]').forEach((button) => {
      button.onclick = () => { selectedProvider = button.dataset.modelProvider; renderProviderList(); renderProviderEditor(); };
    });
  }

  function renderProviderEditor() {
    const provider = providerByName(selectedProvider);
    $('model-editor-title').textContent = provider ? `服务商 · ${provider.name}` : '新增服务商';
    $('model-provider-name').value = provider?.name || '';
    $('model-provider-name').disabled = Boolean(provider);
    $('model-provider-type').value = provider?.type || '';
    $('model-provider-api').value = provider?.api || '';
    $('model-provider-url').value = provider?.base_url || '';
    $('model-provider-key').value = '';
    $('model-provider-key').placeholder = provider?.has_api_key ? `已配置 ${provider.api_key_hint || ''}，留空保留` : '输入 API Key';
    $('model-key-state').textContent = provider?.has_api_key ? `API Key 已配置：${provider.api_key_hint || '******'}` : '尚未配置 API Key';
    const models = provider?.models || [{ name: '', context_window: 0, temperature: null, reasoning_effort: '' }];
    $('model-config-list').innerHTML = models.map((model, index) => modelRow(model, index)).join('');
    bindModelRows();
  }

  function modelRow(model, index) {
    const schema = model.json_schema === true ? 'true' : model.json_schema === false ? 'false' : '';
    return `<div class="model-config-row" data-model-row="${index}">
      <label>模型 ID<input data-model-field="name" value="${esc(model.name || '')}" placeholder="model-name"></label>
      <label>上下文窗口<input data-model-field="context_window" type="number" min="0" step="1000" value="${model.context_window || ''}" placeholder="自动"></label>
      <label>温度<input data-model-field="temperature" type="number" min="0" max="2" step="0.1" value="${model.temperature ?? ''}" placeholder="模型默认"></label>
      <label>推理强度<select data-model-field="reasoning_effort">${reasoningOptions.map(([key, label]) => `<option value="${key}" ${key === (model.reasoning_effort || '') ? 'selected' : ''}>${label}</option>`).join('')}</select></label>
      <label>结构化输出<select data-model-field="json_schema"><option value="" ${schema === '' ? 'selected' : ''}>自动识别</option><option value="true" ${schema === 'true' ? 'selected' : ''}>支持</option><option value="false" ${schema === 'false' ? 'selected' : ''}>不支持</option></select></label>
      <button type="button" class="small danger model-remove" title="删除模型" aria-label="删除模型">×</button>
    </div>`;
  }

  function bindModelRows() {
    document.querySelectorAll('.model-config-row .model-remove').forEach((button) => {
      button.onclick = () => {
        const row = button.closest('.model-config-row');
        if (document.querySelectorAll('.model-config-row').length === 1) {
          notify('每个服务商至少保留一个模型', 'models-msg', 'error');
          return;
        }
        row.remove();
      };
    });
  }

  function readModels() {
    return [...document.querySelectorAll('.model-config-row')].map((row) => {
      const read = (name) => row.querySelector(`[data-model-field="${name}"]`)?.value.trim() || '';
      const temperature = read('temperature');
      const schema = read('json_schema');
      const model = {
        name: read('name'),
        context_window: Number(read('context_window') || 0),
        reasoning_effort: read('reasoning_effort'),
      };
      if (temperature !== '') model.temperature = Number(temperature);
      if (schema !== '') model.json_schema = schema === 'true';
      return model;
    });
  }

  function providerRequest(action) {
    const key = $('model-provider-key').value.trim();
    return {
      action,
      provider: $('model-provider-name').value.trim(),
      type: $('model-provider-type').value,
      api: $('model-provider-api').value,
      base_url: $('model-provider-url').value.trim(),
      api_key_action: key ? 'replace' : 'keep',
      api_key: key,
      models: readModels(),
    };
  }

  function renderRouting() {
    const roleConfig = snapshot?.roles || {};
    $('model-role-grid').innerHTML = roles.map(([role, label]) => {
      const config = role === 'default' ? { provider: snapshot.default_provider, model: snapshot.default_model, reasoning_effort: snapshot.reasoning_effort } : roleConfig[role];
      const value = config?.provider && config?.model ? modelValue(config.provider, config.model) : '';
      return `<div class="model-role-row"><div><strong>${label}</strong><small>${role === 'default' ? '工作区默认' : (config ? '单独配置' : '继承默认')}</small></div><select data-model-role="${role}">${modelOptions(value, role !== 'default')}</select>${reasoningSelect(config?.reasoning_effort || '', role, role !== 'default' && !config)}</div>`;
    }).join('');
    document.querySelectorAll('[data-model-role]').forEach((select) => {
      select.onchange = async () => {
        const role = select.dataset.modelRole;
        const selection = parseModelValue(select.value);
        await update({ action: 'select_model', role, ...selection, inherit_default: !select.value }, '模型选择已更新');
      };
    });
    document.querySelectorAll('[data-model-reasoning]').forEach((select) => {
      select.onchange = () => update({ action: 'set_reasoning', role: select.dataset.modelReasoning, reasoning_effort: select.value }, '推理强度已更新');
    });
  }

  function render() {
    if (!selectedProvider || !providerByName(selectedProvider)) selectedProvider = providers()[0]?.name || '';
    renderProviderList();
    renderProviderEditor();
    renderRouting();
  }

  async function update(body, message) {
    try {
      snapshot = await api('/api/v2/settings/models', { method: 'PUT', body: JSON.stringify(body) });
      render();
      notify(message, 'models-msg', 'success');
      return true;
    } catch (error) {
      notify(`模型设置失败：${error.message}`, 'models-msg', 'error');
      await load();
      return false;
    }
  }

  async function load() {
    if (loading) return;
    loading = true;
    try {
      snapshot = await api('/api/v2/settings/models');
      render();
    } catch (error) {
      notify(`模型配置加载失败：${error.message}`, 'models-msg', 'error');
    } finally { loading = false; }
  }

  $('model-add-provider').onclick = () => { selectedProvider = ''; renderProviderList(); renderProviderEditor(); };
  $('model-add-model').onclick = () => {
    $('model-config-list').insertAdjacentHTML('beforeend', modelRow({}, document.querySelectorAll('.model-config-row').length));
    bindModelRows();
  };
  $('model-save-provider').onclick = async () => {
    const name = $('model-provider-name').value.trim();
    if (!name) return notify('请输入服务商标识', 'models-msg', 'error');
    if (!readModels().every((model) => model.name)) return notify('模型 ID 不能为空', 'models-msg', 'error');
    if (await update(providerRequest('save_provider'), '模型库已保存')) selectedProvider = name;
    render();
  };
  $('model-test-provider').onclick = () => {
    const body = providerRequest('test_provider');
    body.model = body.models[0]?.name || '';
    update(body, '连接测试成功');
  };

  window.ModelSettings = { load };
})();
