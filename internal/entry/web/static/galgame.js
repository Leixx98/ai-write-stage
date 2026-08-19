/* Galgame / tavern view. */
(() => {
  let galgameState = { characters: [], sessions: [], character: null, session: null, selectedImageJobId: '', loaded: false };

  function fieldValue(id) { return $(id)?.value?.trim() || ''; }
  function setField(id, value) { const el = $(id); if (el) el.value = value ?? ''; }
  function isPlayMode() { return document.querySelector('.galgame-stage')?.classList.contains('is-play'); }

  function openGalgameSettings() {
    const drawer = $('galgame-drawer');
    const backdrop = $('galgame-drawer-backdrop');
    if (drawer) drawer.hidden = false;
    if (backdrop) backdrop.hidden = false;
    syncSettingsMode(isPlayMode());
    loadGalgame();
  }

  function closeGalgameSettings() {
    const drawer = $('galgame-drawer');
    const backdrop = $('galgame-drawer-backdrop');
    if (drawer) drawer.hidden = true;
    if (backdrop) backdrop.hidden = true;
  }

  function syncSettingsMode(playMode) {
    const title = $('galgame-settings-title');
    if (title) title.textContent = playMode ? '剧场设置' : '对话设置';
    const chat = $('galgame-chat-settings');
    const play = $('galgame-play-settings');
    if (chat) chat.hidden = playMode;
    if (play) play.hidden = !playMode;
  }

  async function loadGalgame() {
    if (galgameState.loaded) return;
    galgameState.loaded = true;
    try {
      galgameState.characters = await api('/api/v2/galgame/characters') || [];
      galgameState.sessions = await api('/api/v2/galgame/sessions') || [];
    } catch (error) {
      galgameState.loaded = false;
      notify(`Galgame 加载失败：${error.message}`, 'galgame-settings-msg', 'error');
      return;
    }
    renderGalgameSelectors();
    if (galgameState.characters[0]) await selectGalgameCharacter(galgameState.characters[0].id);
    else {
      renderCharacterForm();
      renderSessionForm();
      renderGalgameDialogue();
    }
  }

  function characterSessions() {
    return galgameState.sessions.filter((session) => session.character_id === galgameState.character?.id);
  }

  function fillCharacterSelect(el) {
    if (!el) return;
    const placeholder = galgameState.characters.length ? '选择角色' : '尚未创建角色';
    el.innerHTML = `<option value="">${placeholder}</option>` + galgameState.characters.map((character) => `<option value="${esc(character.id)}">${esc(character.name || character.id)}</option>`).join('');
    el.value = galgameState.character?.id || '';
  }

  function renderGalgameSelectors() {
    fillCharacterSelect($('galgame-character-select'));
    fillCharacterSelect($('galgame-play-character-select'));
    const sessions = $('galgame-session-select');
    if (!sessions) return;
    const items = characterSessions();
    sessions.innerHTML = `<option value="">${items.length ? '选择会话' : '尚未创建会话'}</option>` + items.map((session) => `<option value="${esc(session.id)}">${esc(session.name || session.id)}</option>`).join('');
    sessions.value = galgameState.session?.id || '';
  }

  function renderCharacterForm() {
    const character = galgameState.character || {};
    $('galgame-character-name').textContent = character.name || '选择角色';
    setField('galgame-character-name-input', character.name || '');
    setField('galgame-description', character.description || '');
    setField('galgame-personality', character.personality || '');
    setField('galgame-scenario', character.scenario || '');
    setField('galgame-first-message', character.first_mes || '');
    setField('galgame-example-dialogue', character.mes_example || '');
    setField('galgame-system-prompt', character.system_prompt || '');
    setField('galgame-post-history', character.post_history_instructions || '');
    setField('galgame-alternate-greetings', (character.alternate_greetings || []).join('\n'));
    setField('galgame-extensions', JSON.stringify(character.extensions || {}, null, 2));
    const greetings = [character.first_mes || '无开场白', ...(character.alternate_greetings || [])];
    const greetingSelect = $('galgame-greeting-select');
    if (greetingSelect) greetingSelect.innerHTML = greetings.map((greeting, index) => `<option value="${index}">${index === 0 ? '默认' : `备用 ${index}`}：${esc(greeting.slice(0, 36))}</option>`).join('');
  }

  function renderSessionForm() {
    const session = galgameState.session || {};
    if (!isPlayMode()) $('galgame-session-name').textContent = session.name || 'Galgame 会话';
    setField('galgame-session-name-input', session.name || '');
    setField('galgame-user-persona', session.user_persona || '');
  }

  async function selectGalgameCharacter(id) {
    if (!id) {
      if (isPlayMode()) {
        galgameState.character = null;
        galgameState.session = null;
        renderCharacterForm();
        renderSessionForm();
        renderGalgameSelectors();
        window.GalgamePlay?.onCharacterChange();
        return;
      }
      newGalgameCharacter();
      return;
    }
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
      window.GalgamePlay?.onCharacterChange();
    } catch (error) {
      notify(error.message, 'galgame-settings-msg', 'error');
    }
  }

  async function selectGalgameSession(id) {
    if (!id) {
      galgameState.session = null;
      renderSessionForm();
      renderGalgameSelectors();
      renderGalgameDialogue();
      return;
    }
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

  function imageOwnerAllowed(owner) {
    if (isPlayMode()) return owner === 'play';
    return owner !== 'play';
  }

  function selectGalgameImage(jobId) {
    galgameState.selectedImageJobId = jobId || '';
    const status = $('galgame-image-status');
    if (status) status.textContent = '';
    if (jobId) showGalgameImage(galgameImageUrl(jobId));
    else hideGalgameImage('', true);
    markSelectedGalgameMessage();
  }

  function markSelectedGalgameMessage() {
    document.querySelectorAll('#galgame-dialogue .galgame-message').forEach((element) => {
      element.classList.toggle('selected', Boolean(galgameState.selectedImageJobId) && element.dataset.imageJobId === galgameState.selectedImageJobId);
    });
  }

  function emptyChatHTML() {
    if (!galgameState.character) {
      return `<div class="galgame-empty"><p class="muted">还没有角色卡。可以手写保存，也可以导入 JSON。</p><div class="button-row"><button type="button" data-galgame-action="new-character">新建角色</button><button type="button" class="small" data-galgame-action="open-settings">打开对话设置</button></div></div>`;
    }
    if (!galgameState.session) {
      return `<div class="galgame-empty"><p class="muted">已选择 ${esc(galgameState.character.name || '角色')}，还没有会话。</p><div class="button-row"><button type="button" data-galgame-action="new-session">新建会话</button><button type="button" class="small" data-galgame-action="open-settings">打开对话设置</button></div></div>`;
    }
    return '<div class="muted">选择角色并创建会话。</div>';
  }

  function renderGalgameDialogue() {
    if (isPlayMode()) return;
    const host = $('galgame-dialogue');
    if (!host) return;
    const messages = galgameState.session?.messages || [];
    if (!messages.length) {
      host.innerHTML = emptyChatHTML();
      return;
    }
    host.innerHTML = messages.map((message) => {
      const jobId = message.image_job_id || '';
      const hasImage = message.role === 'assistant' && Boolean(jobId);
      const selected = hasImage && jobId === galgameState.selectedImageJobId ? ' selected' : '';
      const thinking = message.streaming && !message.content ? '<p class="galgame-thinking">生成中…</p>' : '';
      return `<article class="galgame-message ${message.role === 'assistant' ? 'character' : 'user'}${hasImage ? ' has-image' : ''}${selected}${message.streaming ? ' streaming' : ''}"${hasImage ? ` data-image-job-id="${esc(jobId)}"` : ''}><strong>${esc(message.name || (message.role === 'assistant' ? galgameState.character?.name || '角色' : '你'))}</strong>${thinking}<p class="galgame-content">${esc(message.content)}</p></article>`;
    }).join('');
    host.scrollTop = host.scrollHeight;
  }

  function readExtensions() {
    const value = fieldValue('galgame-extensions');
    if (!value) return {};
    const parsed = JSON.parse(value);
    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') throw new Error('Extensions 必须是 JSON 对象');
    return parsed;
  }

  function characterFormData() {
    return {
      ...(galgameState.character || {}),
      id: galgameState.character?.id || '',
      name: fieldValue('galgame-character-name-input'),
      description: fieldValue('galgame-description'),
      personality: fieldValue('galgame-personality'),
      scenario: fieldValue('galgame-scenario'),
      first_mes: fieldValue('galgame-first-message'),
      mes_example: fieldValue('galgame-example-dialogue'),
      system_prompt: fieldValue('galgame-system-prompt'),
      post_history_instructions: fieldValue('galgame-post-history'),
      alternate_greetings: ($('galgame-alternate-greetings')?.value || '').split(/\r?\n/).map((value) => value.trim()).filter(Boolean),
      extensions: readExtensions(),
    };
  }

  function newGalgameCharacter() {
    galgameState.character = null;
    galgameState.session = null;
    renderCharacterForm();
    renderSessionForm();
    renderGalgameSelectors();
    renderGalgameDialogue();
    window.GalgamePlay?.onCharacterChange();
    openGalgameSettings();
    notify('填写角色名，并至少填写描述、性格、场景或 System Prompt 中的一项，然后保存。', 'galgame-settings-msg', '');
  }

  async function saveGalgameCharacter() {
    try {
      const body = characterFormData();
      if (!body.name) return notify('请填写角色名', 'galgame-settings-msg', 'error');
      if (!body.description && !body.personality && !body.scenario && !body.system_prompt) {
        return notify('请至少填写角色描述、性格、场景或 System Prompt 中的一项', 'galgame-settings-msg', 'error');
      }
      const saved = await api(body.id ? `/api/v2/galgame/characters/${encodeURIComponent(body.id)}` : '/api/v2/galgame/characters', { method: body.id ? 'PUT' : 'POST', body: JSON.stringify(body) });
      galgameState.character = saved;
      galgameState.characters = await api('/api/v2/galgame/characters') || [];
      renderCharacterForm();
      renderGalgameSelectors();
      renderGalgameDialogue();
      window.GalgamePlay?.onCharacterChange();
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
      const input = $('galgame-card-file');
      if (input) input.value = '';
    }
  }

  async function newGalgameSession() {
    if (!galgameState.character) return notify('请先保存角色卡', 'galgame-settings-msg', 'error');
    try {
      const session = await api('/api/v2/galgame/sessions', { method: 'POST', body: JSON.stringify({
        name: fieldValue('galgame-session-name-input') || `${galgameState.character.name || '角色'} 会话`,
        character_id: galgameState.character.id,
        user_persona: fieldValue('galgame-user-persona'),
        greeting_index: Number($('galgame-greeting-select')?.value || 0),
      }) });
      galgameState.sessions = await api('/api/v2/galgame/sessions') || [];
      await selectGalgameSession(session.id);
      notify('会话已创建', 'galgame-settings-msg', 'success');
    } catch (error) {
      notify(error.message, 'galgame-settings-msg', 'error');
    }
  }

  async function saveGalgameSession() {
    if (!galgameState.session) return notify('请先创建会话', 'galgame-settings-msg', 'error');
    try {
      galgameState.session = await api(`/api/v2/galgame/sessions/${encodeURIComponent(galgameState.session.id)}`, { method: 'PUT', body: JSON.stringify({
        name: fieldValue('galgame-session-name-input'),
        user_persona: fieldValue('galgame-user-persona'),
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
    if (isPlayMode()) return;
    const input = fieldValue('galgame-user-input');
    if (!input || !galgameState.session) return;
    $('galgame-user-input').value = '';
    $('galgame-input').querySelector('button').disabled = true;
    const userMessage = { role: 'user', content: input };
    const assistant = { role: 'assistant', name: galgameState.character?.name || '角色', content: '', thinking: '', streaming: true };
    galgameState.session.messages = [...(galgameState.session.messages || []), userMessage, assistant];
    renderGalgameDialogue();
    try {
      const response = await fetch(`/api/v2/galgame/sessions/${encodeURIComponent(galgameState.session.id)}/generate`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
        body: JSON.stringify({ user_input: input }),
      });
      if (!response.ok) {
        const payload = await response.json().catch(() => ({}));
        throw new Error(payload.msg || payload.error || `HTTP ${response.status}`);
      }
      let done = null;
      await readGalgameSSE(response, (event) => {
        if (event.type === 'thinking') {
          assistant.thinking = (assistant.thinking || '') + (event.delta || '');
          updateStreamingReply(assistant, '思考中…');
        } else if (event.type === 'text') {
          assistant.content += event.delta || '';
          updateStreamingReply(assistant, '');
        } else if (event.type === 'done') {
          done = event;
        } else if (event.type === 'error') {
          throw new Error(event.error || '生成失败');
        }
      });
      if (!done) throw new Error('生成中断');
      galgameState.session = done.session;
      renderGalgameDialogue();
      await watchGalgameImage(done.image_job, done.image_error);
    } catch (error) {
      notify(error.message, 'galgame-settings-msg', 'error');
      if (galgameState.session?.messages?.length) {
        galgameState.session.messages = galgameState.session.messages.filter((message) => message !== assistant && message !== userMessage);
        renderGalgameDialogue();
      }
    } finally {
      $('galgame-input').querySelector('button').disabled = false;
    }
  }

  async function readGalgameSSE(response, onEvent) {
    if (!response.body) throw new Error('浏览器不支持流式读取');
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });
      const parts = buffer.split('\n\n');
      buffer = parts.pop();
      for (const part of parts) {
        const line = part.split('\n').find((item) => item.startsWith('data: '));
        if (!line) continue;
        onEvent(JSON.parse(line.slice(6)));
      }
    }
  }

  function updateStreamingReply(message, thinkingLabel) {
    const host = $('galgame-dialogue');
    if (!host) return;
    const articles = host.querySelectorAll('.galgame-message.character');
    const last = articles[articles.length - 1];
    if (!last) return;
    let thinking = last.querySelector('.galgame-thinking');
    const content = last.querySelector('.galgame-content');
    if (thinkingLabel) {
      if (!thinking) {
        thinking = document.createElement('p');
        thinking.className = 'galgame-thinking';
        last.insertBefore(thinking, content);
      }
      thinking.textContent = thinkingLabel;
    } else if (thinking) {
      thinking.remove();
    }
    if (content) content.textContent = message.content || '';
    host.scrollTop = host.scrollHeight;
  }

  function showGalgameImage(url, owner) {
    if (!url || !imageOwnerAllowed(owner)) return;
    const image = $('galgame-image');
    const frame = image?.closest('.galgame-image');
    const placeholder = $('galgame-image-placeholder');
    if (!image || !frame) return;
    frame.classList.remove('is-waiting');
    if (image.getAttribute('data-src') === url) {
      image.hidden = false;
      if (placeholder) placeholder.hidden = true;
      return;
    }
    image.onload = () => {
      const width = image.naturalWidth || 1;
      const height = image.naturalHeight || 1;
      image.style.aspectRatio = `${width} / ${height}`;
      frame.dataset.orientation = width >= height ? 'landscape' : 'portrait';
      frame.style.setProperty('--galgame-image-ratio', String(width / height));
      if (frame.classList.contains('is-waiting')) return;
      image.hidden = false;
      if (placeholder) placeholder.hidden = true;
    };
    image.onerror = () => {
      image.hidden = true;
      if (placeholder) placeholder.hidden = owner === 'play';
    };
    image.setAttribute('data-src', url);
    image.src = url;
  }

  function hideGalgameImage(owner, showPlaceholder = false) {
    if (!imageOwnerAllowed(owner)) return;
    const image = $('galgame-image');
    const frame = image?.closest('.galgame-image');
    const placeholder = $('galgame-image-placeholder');
    if (frame) frame.classList.toggle('is-waiting', !showPlaceholder);
    if (image) image.hidden = true;
    if (placeholder) placeholder.hidden = !showPlaceholder;
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

  function handleGalgameAction(action) {
    if (action === 'open-settings') return openGalgameSettings();
    if (action === 'new-character') return newGalgameCharacter();
    if (action === 'new-session') {
      openGalgameSettings();
      return newGalgameSession();
    }
  }

  function initGalgameUI() {
    $('galgame-dialogue')?.addEventListener('click', (event) => {
      const action = event.target.closest('[data-galgame-action]');
      if (action) {
        event.preventDefault();
        handleGalgameAction(action.dataset.galgameAction);
        return;
      }
      const message = event.target.closest('.galgame-message.has-image');
      if (message) selectGalgameImage(message.dataset.imageJobId);
    });
    $('galgame-settings')?.addEventListener('click', openGalgameSettings);
    $('galgame-close-settings')?.addEventListener('click', closeGalgameSettings);
    $('galgame-drawer-backdrop')?.addEventListener('click', closeGalgameSettings);
    $('galgame-character-select')?.addEventListener('change', (event) => selectGalgameCharacter(event.target.value));
    $('galgame-play-character-select')?.addEventListener('change', (event) => selectGalgameCharacter(event.target.value));
    $('galgame-session-select')?.addEventListener('change', (event) => selectGalgameSession(event.target.value));
    $('galgame-new-character')?.addEventListener('click', newGalgameCharacter);
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
  window.Galgame = {
    load: loadGalgame,
    getState: () => galgameState,
    showImage: showGalgameImage,
    hideImage: hideGalgameImage,
    renderDialogue: renderGalgameDialogue,
    openSettings: openGalgameSettings,
    closeSettings: closeGalgameSettings,
    syncSettingsMode,
    newCharacter: newGalgameCharacter,
    selectCharacter: selectGalgameCharacter,
  };
})();
