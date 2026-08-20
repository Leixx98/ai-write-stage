/* Provider-neutral image-generation settings. */
(() => {
  const state = { loaded: false, settings: null, profiles: [], providers: [], workflows: [], instances: [], workflowCapabilities: {}, selectedProfileID: '', basePane: 'scenes' };
  const providerBase = '/api/v2/image-generation/providers/comfyui';

  function scene(name) { return state.settings?.[name] || {}; }
  function enabled(name) { return Boolean(scene(name).enabled); }
  function optionList(selected = '', inherited = true) {
    return `<option value="">${inherited ? '继承场景默认方案' : '未选择方案'}</option>${state.profiles.map((profile) => `<option value="${esc(profile.id)}"${profile.id === selected ? ' selected' : ''}>${esc(profile.name || profile.id)}</option>`).join('')}`;
  }
  function renderProfileOptions() {
    ['novel', 'chat', 'play'].forEach((name) => {
      const select = $(`image-${name}-profile`);
      if (select) select.innerHTML = optionList(scene(name).default_profile_id || '', false);
    });
    ['galgame-image-profile', 'galgame-play-image-profile'].forEach((id) => {
      const select = $(id);
      if (select) {
        const current = select.value;
        select.innerHTML = optionList(current);
      }
    });
  }
  function ensureOverrideControls() {
    if (!$('galgame-image-profile')) {
      const anchor = $('galgame-session-name-input')?.closest('label');
      if (anchor) anchor.insertAdjacentHTML('afterend', '<label>对话生图方案<select id="galgame-image-profile"></select></label>');
    }
    if (!$('galgame-play-image-profile')) {
      const anchor = $('galgame-play-name')?.closest('label');
      if (anchor) anchor.insertAdjacentHTML('afterend', '<label>剧场生图方案<select id="galgame-play-image-profile"></select></label>');
    }
  }
  function renderSettings() {
    if (!state.settings) return;
    $('image-novel-enabled').checked = enabled('novel');
    $('image-novel-auto').checked = Boolean(scene('novel').auto_generate);
    $('image-chat-enabled').checked = enabled('chat');
    $('image-chat-policy').value = scene('chat').chat_policy || 'every_reply';
    $('image-chat-every-n').value = Number(scene('chat').every_n || 3);
    $('image-play-enabled').checked = enabled('play');
    $('image-play-auto').checked = Boolean(scene('play').auto_generate);
    $('image-chat-every-n-wrap').hidden = $('image-chat-policy').value !== 'every_n';
    renderProfileOptions();
    applyVisibility();
  }
  function readSettings() {
    return {
      version: 1,
      novel: { enabled: $('image-novel-enabled').checked, auto_generate: $('image-novel-auto').checked, default_profile_id: $('image-novel-profile').value },
      chat: { enabled: $('image-chat-enabled').checked, auto_generate: $('image-chat-policy').value !== 'manual', chat_policy: $('image-chat-policy').value, every_n: Number($('image-chat-every-n').value || 3), default_profile_id: $('image-chat-profile').value },
      play: { enabled: $('image-play-enabled').checked, auto_generate: $('image-play-auto').checked, default_profile_id: $('image-play-profile').value },
    };
  }
  async function saveSettings() {
    try {
      state.settings = await api('/api/v2/image-generation/settings', { method: 'PUT', body: JSON.stringify(readSettings()) });
      renderSettings();
      notify('场景生图设置已保存', 'image-settings-msg', 'success');
      window.dispatchEvent(new CustomEvent('image-settings-changed'));
    } catch (error) { notify(error.message, 'image-settings-msg', 'error'); }
  }
  function renderProfileList() {
    const host = $('image-profile-list');
    if (!host) return;
    host.innerHTML = state.profiles.length ? state.profiles.map((profile) => `<button type="button" class="profile-list-item${profile.id === state.selectedProfileID ? ' active' : ''}" data-image-profile-id="${esc(profile.id)}"><strong>${esc(profile.name || profile.id)}</strong><small>${esc(profile.provider)}</small></button>`).join('') : '<span class="muted">暂无生图方案</span>';
  }
  function selectedProfile() { return state.profiles.find((profile) => profile.id === state.selectedProfileID) || null; }
  function renderProviderFields() {
    const providerID = $('image-profile-provider')?.value || 'comfyui';
    document.querySelectorAll('.provider-option-comfyui').forEach((element) => { element.hidden = providerID !== 'comfyui'; });
    const workflowID = $('image-profile-workflow')?.value || '';
    const capabilities = providerID === 'comfyui' ? (state.workflowCapabilities[workflowID] || {}) : (state.providers.find((provider) => provider.id === providerID)?.capabilities || {});
    $('image-profile-ratio-wrap').hidden = !capabilities.aspect_ratio;
    $('image-profile-count-wrap').hidden = !capabilities.image_count;
  }
  async function refreshWorkflowCapabilities() {
    if (($('image-profile-provider')?.value || '') !== 'comfyui') return renderProviderFields();
    const workflowID = $('image-profile-workflow')?.value || '';
    if (!workflowID) return renderProviderFields();
    if (!state.workflowCapabilities[workflowID]) {
      const schema = await api(`${providerBase}/workflows/${encodeURIComponent(workflowID)}/schema`).catch(() => ({ fields: [] }));
      const fields = new Set((schema?.fields || []).map((field) => String(field.id || '').toLowerCase()));
      state.workflowCapabilities[workflowID] = { aspect_ratio: fields.has('width') && fields.has('height'), image_count: fields.has('image_count') || fields.has('batch_size') };
    }
    renderProviderFields();
  }
  function editProfile(profile = null) {
    state.selectedProfileID = profile?.id || '';
    $('image-profile-id').value = profile?.id || '';
    $('image-profile-id').disabled = Boolean(profile?.id);
    $('image-profile-name').value = profile?.name || '';
    $('image-profile-provider').value = profile?.provider || state.providers[0]?.id || 'comfyui';
    $('image-profile-preset').value = profile?.prompter_preset_id || '';
    $('image-profile-ratio').value = profile?.aspect_ratio || '';
    $('image-profile-count').value = Number(profile?.image_count || 1);
    $('image-profile-timeout').value = Number(profile?.timeout_ms || 600000);
    $('image-profile-workflow').value = profile?.provider_options?.workflow_id || '';
    $('image-profile-instance').value = profile?.provider_options?.instance_id || '';
    renderProviderFields();
    refreshWorkflowCapabilities();
    renderProfileList();
  }
  function readProfile() {
    const provider = $('image-profile-provider').value;
    const providerOptions = {};
    if (provider === 'comfyui') {
      providerOptions.workflow_id = $('image-profile-workflow').value;
      if ($('image-profile-instance').value) providerOptions.instance_id = $('image-profile-instance').value;
    }
    return {
      id: $('image-profile-id').value.trim(), name: $('image-profile-name').value.trim(), provider,
      prompter_preset_id: $('image-profile-preset').value.trim(), aspect_ratio: $('image-profile-ratio-wrap').hidden ? '' : $('image-profile-ratio').value,
      image_count: $('image-profile-count-wrap').hidden ? 1 : Number($('image-profile-count').value || 1),
      timeout_ms: Number($('image-profile-timeout').value || 600000), provider_options: providerOptions,
    };
  }
  async function saveProfile() {
    const profile = readProfile();
    if (!profile.id || !profile.name) return notify('请填写方案标识和名称', 'image-settings-msg', 'error');
    try {
      const method = state.selectedProfileID ? 'PUT' : 'POST';
      const url = state.selectedProfileID ? `/api/v2/image-generation/profiles/${encodeURIComponent(profile.id)}` : '/api/v2/image-generation/profiles';
      await api(url, { method, body: JSON.stringify(profile) });
      await loadData();
      editProfile(state.profiles.find((item) => item.id === profile.id));
      notify('生图方案已保存', 'image-settings-msg', 'success');
    } catch (error) { notify(error.message, 'image-settings-msg', 'error'); }
  }
  async function deleteProfile() {
    if (!state.selectedProfileID) return;
    try {
      await api(`/api/v2/image-generation/profiles/${encodeURIComponent(state.selectedProfileID)}`, { method: 'DELETE' });
      state.selectedProfileID = '';
      await loadData();
      editProfile(null);
      notify('生图方案已删除', 'image-settings-msg', 'success');
    } catch (error) { notify(error.message, 'image-settings-msg', 'error'); }
  }
  function applyVisibility() {
    document.body.dataset.imageNovelEnabled = String(enabled('novel'));
    document.body.dataset.imageChatEnabled = String(enabled('chat'));
    document.body.dataset.imagePlayEnabled = String(enabled('play'));
    const novelPlaceholder = $('image-placeholder');
    if (novelPlaceholder) novelPlaceholder.hidden = !enabled('novel');
    const chatPlaceholder = $('galgame-image-placeholder');
    const chatStatus = $('galgame-image-status');
    if (chatPlaceholder && !$('galgame-image')?.src) chatPlaceholder.hidden = !enabled(isPlayModeSafe() ? 'play' : 'chat');
    if (chatStatus && !enabled(isPlayModeSafe() ? 'play' : 'chat')) chatStatus.textContent = '';
    document.querySelectorAll('[data-galgame-action="generate-image"]').forEach((button) => { button.hidden = !enabled('chat'); });
  }
  function isPlayModeSafe() { return Boolean(window.GalgamePlay?.isPlayMode?.()); }
  function showPane(name) {
    if (name !== 'comfyui') state.basePane = name;
    document.querySelectorAll('[data-image-settings-view]').forEach((button) => button.classList.toggle('active', button.dataset.imageSettingsView === name));
    $('image-settings-scenes').hidden = state.basePane !== 'scenes';
    $('image-settings-profiles').hidden = state.basePane !== 'profiles';
    $('image-settings-comfyui').hidden = name !== 'comfyui';
    document.body.classList.toggle('comfy-overlay-open', name === 'comfyui');
    if (name === 'comfyui') window.ComfyUI?.load();
  }
  function closeComfyUI() { showPane(state.basePane); }
  async function loadData() {
	ensureOverrideControls();
    const [settings, profiles, providers, workflows, instances] = await Promise.all([
      api('/api/v2/image-generation/settings'), api('/api/v2/image-generation/profiles'), api('/api/v2/image-generation/providers'),
      api(`${providerBase}/workflows`).catch(() => []), api(`${providerBase}/instances`).catch(() => ({ instances: [] })),
    ]);
    state.settings = settings;
    state.profiles = Array.isArray(profiles) ? profiles : profiles?.profiles || [];
    state.providers = Array.isArray(providers) ? providers : [];
    state.workflows = Array.isArray(workflows) ? workflows : workflows?.workflows || [];
    state.instances = Array.isArray(instances) ? instances : instances?.instances || [];
    $('image-profile-provider').innerHTML = state.providers.map((provider) => `<option value="${esc(provider.id)}">${esc(provider.name)}</option>`).join('');
    $('image-profile-workflow').innerHTML = `<option value="">请选择 Workflow</option>${state.workflows.map((workflow) => `<option value="${esc(workflow.id)}">${esc(workflow.name || workflow.id)}</option>`).join('')}`;
    $('image-profile-instance').innerHTML = `<option value="">工作流默认</option>${state.instances.map((instance) => `<option value="${esc(instance.id)}">${esc(instance.name || instance.id)}</option>`).join('')}`;
    renderSettings();
    renderProfileList();
  }
  async function load() {
    try {
      await loadData();
      state.loaded = true;
      if (!state.selectedProfileID) editProfile(null);
    } catch (error) { notify(error.message, 'image-settings-msg', 'error'); }
  }

  document.querySelectorAll('[data-image-settings-view]').forEach((button) => button.addEventListener('click', () => showPane(button.dataset.imageSettingsView)));
  document.querySelectorAll('[data-action="close-comfyui"]').forEach((button) => button.addEventListener('click', closeComfyUI));
  document.addEventListener('keydown', (event) => {
    if (event.key !== 'Escape' || $('image-settings-comfyui')?.hidden) return;
    if (!$('node-modal')?.hidden) return;
    closeComfyUI();
  });
  $('image-chat-policy')?.addEventListener('change', renderSettings);
  $('image-settings-save')?.addEventListener('click', saveSettings);
  $('image-profile-new')?.addEventListener('click', () => editProfile(null));
  $('image-profile-save')?.addEventListener('click', saveProfile);
  $('image-profile-delete')?.addEventListener('click', deleteProfile);
  $('image-profile-provider')?.addEventListener('change', renderProviderFields);
  $('image-profile-workflow')?.addEventListener('change', refreshWorkflowCapabilities);
  $('image-profile-list')?.addEventListener('click', (event) => {
    const button = event.target.closest('[data-image-profile-id]');
    if (button) editProfile(state.profiles.find((profile) => profile.id === button.dataset.imageProfileId));
  });
  window.ImageGeneration = { load, enabled, scene, profiles: () => state.profiles, optionList, applyVisibility, showPane };
  load();
})();
