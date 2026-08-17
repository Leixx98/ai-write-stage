/* Galgame / tavern view. */
(() => {
  let galgameState = { characters: [], sessions: [], character: null, session: null, selectedImageJobId: '', loaded: false };

  async function loadGalgame() {
    if (galgameState.loaded) return;
    galgameState.loaded = true;
    try {
      galgameState.characters = await api('/api/v2/galgame/characters') || [];
      galgameState.sessions = await api('/api/v2/galgame/sessions') || [];
    } catch (error) {
      notify(`Galgame 加载失败：${error.message}`, 'galgame-settings-msg', 'error');
      return;
    }
    renderGalgameSelectors();
    if (galgameState.characters[0]) await selectGalgameCharacter(galgameState.characters[0].id);
    else renderCharacterForm();
  }

  function characterSessions() {
    return galgameState.sessions.filter((session) => session.character_id === galgameState.character?.id);
  }

  function renderGalgameSelectors() {
    const characters = $('galgame-character-select');
    const sessions = $('galgame-session-select');
    if (!characters || !sessions) return;
    characters.innerHTML = galgameState.characters.map((character) => `<option value="${esc(character.id)}">${esc(character.name || character.id)}</option>`).join('');
    sessions.innerHTML = characterSessions().map((session) => `<option value="${esc(session.id)}">${esc(session.name || session.id)}</option>`).join('');
    if (galgameState.character) characters.value = galgameState.character.id;
    if (galgameState.session) sessions.value = galgameState.session.id;
  }

  function renderCharacterForm() {
    const character = galgameState.character || {};
    $('galgame-character-name').textContent = character.name || '选择角色';
    $('galgame-character-name-input').value = character.name || '';
    $('galgame-description').value = character.description || '';
    $('galgame-personality').value = character.personality || '';
    $('galgame-scenario').value = character.scenario || '';
    $('galgame-first-message').value = character.first_mes || '';
    $('galgame-example-dialogue').value = character.mes_example || '';
    $('galgame-system-prompt').value = character.system_prompt || '';
    $('galgame-post-history').value = character.post_history_instructions || '';
    $('galgame-alternate-greetings').value = (character.alternate_greetings || []).join('\n');
    $('galgame-extensions').value = JSON.stringify(character.extensions || {}, null, 2);
    const greetings = [character.first_mes || '无开场白', ...(character.alternate_greetings || [])];
    $('galgame-greeting-select').innerHTML = greetings.map((greeting, index) => `<option value="${index}">${index === 0 ? '默认' : `备用 ${index}`}：${esc(greeting.slice(0, 36))}</option>`).join('');
  }

  function renderSessionForm() {
    const session = galgameState.session || {};
    $('galgame-session-name').textContent = session.name || 'Galgame 会话';
    $('galgame-session-name-input').value = session.name || '';
    $('galgame-user-persona').value = session.user_persona || '';
  }

  async function selectGalgameCharacter(id) {
    try {
      galgameState.character = await api(`/api/v2/galgame/characters/${encodeURIComponent(id)}`);
      const matching = characterSessions();
      galgameState.session = null;
      renderCharacterForm();
      renderGalgameSelectors();
      if (matching[0]) await selectGalgameSession(matching[0].id);
      else {
        selectGalgameImage('');
        renderSessionForm();
        renderGalgameDialogue();
      }
    } catch (error) {
      notify(error.message, 'galgame-settings-msg', 'error');
    }
  }

  async function selectGalgameSession(id) {
    try {
      galgameState.session = await api(`/api/v2/galgame/sessions/${encodeURIComponent(id)}`);
      renderSessionForm();
      selectGalgameImage(lastGalgameImageJobId());
      renderGalgameDialogue();
      renderGalgameSelectors();
    } catch (error) {
      notify(error.message, 'galgame-settings-msg', 'error');
    }
  }

  function lastGalgameImageJobId() {
    const messages = galgameState.session?.messages || [];
    for (let index = messages.length - 1; index >= 0; index--) if (messages[index].image_job_id) return messages[index].image_job_id;
    return '';
  }

  function galgameImageUrl(jobId) { return jobId ? `/api/v2/comfyui/jobs/${encodeURIComponent(jobId)}/outputs/0` : ''; }

  function selectGalgameImage(jobId) {
    galgameState.selectedImageJobId = jobId || '';
    const status = $('galgame-image-status');
    if (status) status.textContent = '';
    if (jobId) showGalgameImage(galgameImageUrl(jobId));
    else {
      const image = $('galgame-image');
      if (image) { image.removeAttribute('src'); image.hidden = true; }
      const placeholder = $('galgame-image-placeholder');
      if (placeholder) placeholder.hidden = false;
    }
    markSelectedGalgameMessage();
  }

  function markSelectedGalgameMessage() {
    document.querySelectorAll('#galgame-dialogue .galgame-message').forEach((element) => {
      element.classList.toggle('selected', Boolean(galgameState.selectedImageJobId) && element.dataset.imageJobId === galgameState.selectedImageJobId);
    });
  }

  function renderGalgameDialogue() {
    const host = $('galgame-dialogue');
    if (!host) return;
    host.innerHTML = (galgameState.session?.messages || []).map((message) => {
      const jobId = message.image_job_id || '';
      const hasImage = message.role === 'assistant' && Boolean(jobId);
      const selected = hasImage && jobId === galgameState.selectedImageJobId ? ' selected' : '';
      return `<article class="galgame-message ${message.role === 'assistant' ? 'character' : 'user'}${hasImage ? ' has-image' : ''}${selected}"${hasImage ? ` data-image-job-id="${esc(jobId)}"` : ''}><strong>${esc(message.name || (message.role === 'assistant' ? galgameState.character?.name || '角色' : '你'))}</strong><p>${esc(message.content)}</p></article>`;
    }).join('') || '<div class="muted">选择角色并创建会话。</div>';
    host.scrollTop = host.scrollHeight;
  }

  function readExtensions() {
    const value = $('galgame-extensions').value.trim();
    if (!value) return {};
    const parsed = JSON.parse(value);
    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') throw new Error('Extensions 必须是 JSON 对象');
    return parsed;
  }

  function characterFormData() {
    return {
      ...(galgameState.character || {}),
      id: galgameState.character?.id || '',
      name: $('galgame-character-name-input').value.trim(),
      description: $('galgame-description').value.trim(),
      personality: $('galgame-personality').value.trim(),
      scenario: $('galgame-scenario').value.trim(),
      first_mes: $('galgame-first-message').value.trim(),
      mes_example: $('galgame-example-dialogue').value.trim(),
      system_prompt: $('galgame-system-prompt').value.trim(),
      post_history_instructions: $('galgame-post-history').value.trim(),
      alternate_greetings: $('galgame-alternate-greetings').value.split(/\r?\n/).map((value) => value.trim()).filter(Boolean),
      extensions: readExtensions(),
    };
  }

  async function saveGalgameCharacter() {
    try {
      const body = characterFormData();
      const saved = await api(body.id ? `/api/v2/galgame/characters/${encodeURIComponent(body.id)}` : '/api/v2/galgame/characters', { method: body.id ? 'PUT' : 'POST', body: JSON.stringify(body) });
      galgameState.character = saved;
      galgameState.characters = await api('/api/v2/galgame/characters') || [];
      renderCharacterForm();
      renderGalgameSelectors();
      notify('角色卡已保存', 'galgame-settings-msg', 'success');
    } catch (error) {
      notify(error.message, 'galgame-settings-msg', 'error');
    }
  }

  async function importGalgameCharacter(file) {
    try {
      const saved = await api('/api/v2/galgame/characters/import', { method: 'POST', body: await file.text() });
      galgameState.characters = await api('/api/v2/galgame/characters') || [];
      await selectGalgameCharacter(saved.id);
      notify('角色卡已导入并保存', 'galgame-settings-msg', 'success');
    } catch (error) {
      notify(`角色卡导入失败：${error.message}`, 'galgame-settings-msg', 'error');
    } finally {
      $('galgame-card-file').value = '';
    }
  }

  async function newGalgameSession() {
    if (!galgameState.character) return notify('请先选择角色', 'galgame-settings-msg', 'error');
    try {
      const session = await api('/api/v2/galgame/sessions', { method: 'POST', body: JSON.stringify({
        name: $('galgame-session-name-input').value.trim() || `${galgameState.character.name || '角色'} 会话`,
        character_id: galgameState.character.id,
        user_persona: $('galgame-user-persona').value.trim(),
		greeting_index: Number($('galgame-greeting-select').value || 0),
      }) });
      galgameState.sessions = await api('/api/v2/galgame/sessions') || [];
      await selectGalgameSession(session.id);
    } catch (error) {
      notify(error.message, 'galgame-settings-msg', 'error');
    }
  }

  async function saveGalgameSession() {
    if (!galgameState.session) return notify('请先创建会话', 'galgame-settings-msg', 'error');
    try {
      galgameState.session = await api(`/api/v2/galgame/sessions/${encodeURIComponent(galgameState.session.id)}`, { method: 'PUT', body: JSON.stringify({
        name: $('galgame-session-name-input').value.trim(),
        user_persona: $('galgame-user-persona').value.trim(),
      }) });
      galgameState.sessions = await api('/api/v2/galgame/sessions') || [];
      renderSessionForm();
      renderGalgameSelectors();
      notify('会话设置已保存', 'galgame-settings-msg', 'success');
    } catch (error) {
      notify(error.message, 'galgame-settings-msg', 'error');
    }
  }

  async function sendGalgameMessage(event) {
    event.preventDefault();
    const input = $('galgame-user-input').value.trim();
    if (!input || !galgameState.session) return;
    $('galgame-user-input').value = '';
    $('galgame-input').querySelector('button').disabled = true;
    try {
      const result = await api(`/api/v2/galgame/sessions/${encodeURIComponent(galgameState.session.id)}/generate`, { method: 'POST', body: JSON.stringify({ user_input: input }) });
      galgameState.session = result.session;
      renderGalgameDialogue();
      await watchGalgameImage(result.image_job, result.image_error);
    } catch (error) {
      notify(error.message, 'galgame-settings-msg', 'error');
    } finally {
      $('galgame-input').querySelector('button').disabled = false;
    }
  }

  function showGalgameImage(url) {
    const image = $('galgame-image');
    const frame = image?.closest('.galgame-image');
    if (!image || !frame) return;
    image.onload = () => {
      const width = image.naturalWidth || 1;
      const height = image.naturalHeight || 1;
      image.style.aspectRatio = `${width} / ${height}`;
      frame.dataset.orientation = width >= height ? 'landscape' : 'portrait';
      frame.style.setProperty('--galgame-image-ratio', String(width / height));
      image.hidden = false;
      $('galgame-image-placeholder').hidden = true;
    };
    image.onerror = () => { image.hidden = true; $('galgame-image-placeholder').hidden = false; };
    image.src = url;
  }

  async function watchGalgameImage(job, initialError = '') {
    const status = $('galgame-image-status');
    if (!job?.job_id) { status.textContent = initialError || '图片任务未创建'; return; }
    try {
      galgameState.selectedImageJobId = job.job_id;
      markSelectedGalgameMessage();
      status.textContent = '场景生成中...';
      let current = job;
      for (let index = 0; index < 600 && !['completed', 'failed', 'timeout', 'cancelled'].includes(current.status); index++) {
        await new Promise((resolve) => setTimeout(resolve, 1000));
        current = await api(`/api/v2/comfyui/jobs/${encodeURIComponent(current.job_id)}`);
      }
      const output = current.outputs?.[0] || current.output;
      const url = output?.url || (current.job_id ? `/api/v2/comfyui/jobs/${encodeURIComponent(current.job_id)}/outputs/0` : '');
      if (current.status === 'completed' && url) {
        galgameState.selectedImageJobId = current.job_id;
        showGalgameImage(url);
        markSelectedGalgameMessage();
        status.textContent = '';
        return;
      }
      status.textContent = current.error || `图片任务${current.status || '未完成'}`;
    } catch (error) {
      status.textContent = `生图失败：${error.message}`;
    }
  }

  function initGalgameUI() {
    $('galgame-dialogue')?.addEventListener('click', (event) => {
      const message = event.target.closest('.galgame-message.has-image');
      if (message) selectGalgameImage(message.dataset.imageJobId);
    });
    $('galgame-settings')?.addEventListener('click', () => { $('galgame-drawer').hidden = false; loadGalgame(); });
    $('galgame-close-settings')?.addEventListener('click', () => { $('galgame-drawer').hidden = true; });
    $('galgame-character-select')?.addEventListener('change', (event) => selectGalgameCharacter(event.target.value));
    $('galgame-session-select')?.addEventListener('change', (event) => selectGalgameSession(event.target.value));
    $('galgame-save-character')?.addEventListener('click', saveGalgameCharacter);
    $('galgame-save-session')?.addEventListener('click', saveGalgameSession);
    $('galgame-new-session')?.addEventListener('click', newGalgameSession);
    $('galgame-input')?.addEventListener('submit', sendGalgameMessage);
    $('galgame-card-file')?.addEventListener('change', (event) => {
      const file = event.target.files?.[0];
      if (file) importGalgameCharacter(file);
    });
  }

  initGalgameUI();
  window.Galgame = { load: loadGalgame };
})();
