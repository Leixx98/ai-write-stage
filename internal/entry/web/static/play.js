/* Galgame play / visual-novel mode. */
(() => {
  const playState = { plays: [], play: null, view: null, beats: [], polling: 0, advancing: false, choosing: false };

  function character() { return window.Galgame?.getState?.()?.character; }
  function stage() { return document.querySelector('.galgame-stage'); }
  function isPlayMode() { return stage()?.classList.contains('is-play'); }
  function fieldValue(id) { return $(id)?.value?.trim() || ''; }
  function setField(id, value) { const el = $(id); if (el) el.value = value ?? ''; }

  function setPlayMode(on) {
    const root = stage();
    if (root) root.classList.toggle('is-play', on);
    $('galgame-mode-chat')?.classList.toggle('active', !on);
    $('galgame-mode-play')?.classList.toggle('active', on);
    const status = $('galgame-play-status');
    if (status) status.hidden = !on;
    window.Galgame?.syncSettingsMode?.(on);
    if (on) {
      Promise.resolve(loadPlays()).then(() => {
        if (isPlayMode() && character() && !playState.play) window.Galgame?.openSettings?.();
      });
      startPlayPolling();
    } else {
      stopPlayPolling();
      window.Galgame?.renderDialogue?.();
    }
  }

  async function loadPlays() {
    const select = $('galgame-play-select');
    if (!character()) {
      playState.plays = [];
      if (select) select.innerHTML = '<option value="">请先选择角色</option>';
      renderPlayForm();
      return;
    }
    try {
      playState.plays = (await api('/api/v2/galgame/plays') || []).filter((item) => item.character_id === character().id);
    } catch (error) {
      notify(`剧场加载失败：${error.message}`, 'galgame-settings-msg', 'error');
      return;
    }
    if (select) {
      select.innerHTML = `<option value="">新建剧场局</option>` + playState.plays.map((item) => `<option value="${esc(item.id)}">${esc(item.name || item.id)}</option>`).join('');
      select.value = playState.play?.id || '';
    }
    if (!playState.play && playState.plays[0] && !fieldValue('galgame-play-name') && !fieldValue('galgame-play-premise')) await selectPlay(playState.plays[0].id);
    else renderPlayForm();
  }

  function renderPlayForm() {
    const play = playState.play;
    if (play) {
      setField('galgame-play-name', play.name || '');
      setField('galgame-play-premise', play.premise || '');
      setField('galgame-play-persona', play.user_persona || '');
    }
    if (isPlayMode()) $('galgame-session-name').textContent = play?.name || '剧场';
    renderPlayBuffer();
    if (isPlayMode()) renderPlayBeat();
  }

  function renderPlayBuffer() {
    const el = $('galgame-play-status');
    if (!el) return;
    const view = playState.view;
    if (!view) { el.textContent = '未开局'; return; }
    const buffer = view.buffer || {};
    const status = view.play?.status || '';
    el.textContent = `${status || '未开局'} · 已缓存 ${buffer.text_ahead || 0} 屏 · 配图中 ${buffer.images_pending || 0} 张${view.play?.last_error ? ` · ${view.play.last_error}` : ''}`;
  }

  function currentBeat() {
    const head = playState.view?.progress?.play_head || 0;
    return (playState.beats || []).find((beat) => beat.ordinal === head) || playState.view?.beat || null;
  }

  function selectedChoice(beat) {
    return (playState.view?.progress?.choice_history || []).find((item) => item.ordinal === beat?.ordinal) || null;
  }

  function waitingAfterChoice() {
    if (playState.choosing) return true;
    const beat = currentBeat();
    if (!beat) return (playState.view?.progress?.play_head || 0) > 0;
    return beat.kind === 'choice' && Boolean(selectedChoice(beat));
  }

  function loadingHTML() {
    const err = playError();
    if (err) {
      return `<div class="galgame-loading"><p class="notice error">${esc(err)}</p><div class="button-row"><button type="button" data-galgame-play-action="start">重新开始写作</button></div></div>`;
    }
    return `<div class="galgame-loading"><span class="galgame-loading-spinner" aria-hidden="true"></span><p>正在续写…</p></div>`;
  }

  function playError() {
    return playState.view?.play?.last_error || playState.play?.last_error || '';
  }

  function emptyPlayHTML() {
    const err = playError();
    const errHTML = err ? `<p class="notice error">${esc(err)}</p>` : '';
    if (!character()) {
      return `<div class="galgame-empty"><p class="muted">剧场需要一张角色卡。请先在对话页创建或选择角色。</p><div class="button-row"><button type="button" data-galgame-play-action="open-chat-settings">去创建角色</button></div></div>`;
    }
    if (playState.play) {
      const hint = err ? '上次写作失败，可以改预设后重新开始。' : `剧场「${esc(playState.play.name || '未命名')}」已创建。点击开始写作后，第一屏会显示在这里。`;
      return `<div class="galgame-empty"><p class="muted">${esc(hint)}</p>${errHTML}<div class="button-row"><button type="button" data-galgame-play-action="start">${err ? '重新开始写作' : '开始写作'}</button><button type="button" class="small" data-galgame-play-action="open-settings">打开剧场设置</button></div></div>`;
    }
    return `<div class="galgame-empty"><p class="muted">还没有剧场局。选择角色后填写局名和剧情预设，即可开局。</p><div class="button-row"><button type="button" data-galgame-play-action="open-settings">打开剧场设置</button></div></div>`;
  }

  function renderPlayBeat() {
    const host = $('galgame-dialogue');
    if (!host || !isPlayMode()) return;
    const beat = currentBeat();
    if (waitingAfterChoice()) {
      host.innerHTML = loadingHTML();
      delete host.dataset.playSig;
      return;
    }
    if (!beat) {
      host.innerHTML = emptyPlayHTML();
      delete host.dataset.playSig;
      renderPlayImage(null);
      return;
    }
    const speaker = beat.speaker || (beat.kind === 'narration' ? '' : (character()?.name || ''));
    const choices = beat.kind === 'choice'
      ? `<div class="galgame-choices">${(beat.choices || []).map((choice) => `<button type="button" data-choice-id="${esc(choice.id)}">${esc(choice.label)}</button>`).join('')}</div>`
      : '';
    const hint = beat.kind === 'choice' ? '请选择' : (playState.view?.progress?.play_head >= playState.view?.progress?.write_head ? '正在续写…' : '单击继续');
    const html = `<article class="galgame-beat">${speaker ? `<strong>${esc(speaker)}</strong>` : ''}<p>${esc(beat.text)}</p>${choices}<div class="galgame-play-hint">${esc(hint)}</div></article>`;
    if (host.dataset.playSig !== html) {
      host.innerHTML = html;
      host.dataset.playSig = html;
    }
    renderPlayImage(playState.view?.image);
  }

  function renderPlayImage(image) {
    const status = $('galgame-image-status');
    if (image?.status === 'completed' && image.url) {
      window.Galgame?.showImage?.(image.url, 'play');
      if (status) status.textContent = '';
      return;
    }
    window.Galgame?.hideImage?.('play');
    if (!status) return;
    if (image?.status === 'failed' || image?.status === 'timeout' || image?.status === 'cancelled') status.textContent = '配图失败';
    else if (image?.ordinal || image?.job_id || image?.status === 'pending') status.textContent = '配图中';
    else status.textContent = '';
  }

  function clearPlayDraft() {
    playState.play = null;
    playState.view = null;
    playState.beats = [];
    setField('galgame-play-name', '');
    setField('galgame-play-premise', '');
    setField('galgame-play-persona', '');
    const select = $('galgame-play-select');
    if (select) select.value = '';
    renderPlayForm();
  }

  async function selectPlay(id) {
    if (!id) {
      clearPlayDraft();
      return;
    }
    try {
      playState.view = await api(`/api/v2/galgame/plays/${encodeURIComponent(id)}`);
      playState.play = playState.view.play;
      playState.beats = await api(`/api/v2/galgame/plays/${encodeURIComponent(id)}/beats`) || [];
      renderPlayForm();
      const select = $('galgame-play-select');
      if (select) select.value = id;
    } catch (error) {
      notify(error.message, 'galgame-settings-msg', 'error');
    }
  }

  async function createPlay() {
    if (!character()) {
      window.Galgame?.openSettings?.();
      return notify('请先选择或保存角色卡', 'galgame-settings-msg', 'error');
    }
    const premise = fieldValue('galgame-play-premise');
    if (!premise) {
      window.Galgame?.openSettings?.();
      return notify('请填写剧情预设', 'galgame-settings-msg', 'error');
    }
    try {
      const created = await api('/api/v2/galgame/plays', { method: 'POST', body: JSON.stringify({
        name: fieldValue('galgame-play-name') || `${character().name || '角色'} 剧场`,
        character_id: character().id,
        premise,
        user_persona: fieldValue('galgame-play-persona'),
      }) });
      playState.plays = (await api('/api/v2/galgame/plays') || []).filter((item) => item.character_id === character().id);
      await selectPlay(created.id);
      const select = $('galgame-play-select');
      if (select) {
        select.innerHTML = `<option value="">新建剧场局</option>` + playState.plays.map((item) => `<option value="${esc(item.id)}">${esc(item.name || item.id)}</option>`).join('');
        select.value = created.id;
      }
      notify('剧场已创建，可以开始写作', 'galgame-settings-msg', 'success');
    } catch (error) {
      notify(error.message, 'galgame-settings-msg', 'error');
    }
  }

  async function startPlay() {
    if (!playState.play) {
      window.Galgame?.openSettings?.();
      return notify('请先填写剧情预设并新建剧场局', 'galgame-settings-msg', 'error');
    }
    try {
      await api(`/api/v2/galgame/plays/${encodeURIComponent(playState.play.id)}/start`, { method: 'POST', body: '{}' });
      window.Galgame?.closeSettings?.();
      startPlayPolling();
      notify('后台写作已开始', 'galgame-settings-msg', 'success');
    } catch (error) {
      notify(error.message, 'galgame-settings-msg', 'error');
    }
  }

  async function pausePlay() {
    if (!playState.play) return;
    try {
      await api(`/api/v2/galgame/plays/${encodeURIComponent(playState.play.id)}/pause`, { method: 'POST', body: '{}' });
      await refreshPlayView();
      notify('剧场写作已暂停', 'galgame-settings-msg', 'success');
    } catch (error) {
      notify(error.message, 'galgame-settings-msg', 'error');
    }
  }

  async function refreshPlayView() {
    if (!playState.play) return;
    playState.view = await api(`/api/v2/galgame/plays/${encodeURIComponent(playState.play.id)}`);
    playState.play = playState.view.play;
    const from = (playState.beats[playState.beats.length - 1]?.ordinal || 0) + 1;
    const extra = await api(`/api/v2/galgame/plays/${encodeURIComponent(playState.play.id)}/beats?from=${from}`) || [];
    if (extra.length) playState.beats = playState.beats.concat(extra);
    renderPlayForm();
    renderPlayBeat();
    maybeFollowResolvedChoice();
  }

  function maybeFollowResolvedChoice() {
    const beat = currentBeat();
    const progress = playState.view?.progress || {};
    if (beat?.kind === 'choice' && selectedChoice(beat) && progress.write_head > progress.play_head) {
      advancePlay();
    }
  }

  async function advancePlay() {
    if (!isPlayMode() || playState.advancing || !playState.play) return;
    const beat = currentBeat();
    if (!beat || (beat.kind === 'choice' && !selectedChoice(beat))) return;
    const progress = playState.view?.progress || {};
    if (progress.play_head >= progress.write_head) return;
    playState.advancing = true;
    try {
      window.Galgame?.closeSettings?.();
      playState.view = await api(`/api/v2/galgame/plays/${encodeURIComponent(playState.play.id)}/advance`, { method: 'POST', body: '{}' });
      playState.play = playState.view.play;
      renderPlayBeat();
      renderPlayBuffer();
    } catch (error) {
      notify(error.message, 'galgame-settings-msg', 'error');
    } finally {
      playState.advancing = false;
    }
  }

  async function choosePlay(choiceId) {
    if (!playState.play || !choiceId || playState.choosing) return;
    playState.choosing = true;
    window.Galgame?.closeSettings?.();
    renderPlayBeat();
    try {
      playState.view = await api(`/api/v2/galgame/plays/${encodeURIComponent(playState.play.id)}/choose`, { method: 'POST', body: JSON.stringify({ choice_id: choiceId }) });
      playState.play = playState.view.play;
      renderPlayBeat();
      renderPlayBuffer();
      startPlayPolling();
    } catch (error) {
      playState.choosing = false;
      notify(error.message, 'toast', 'error');
      notify(error.message, 'galgame-settings-msg', 'error');
      renderPlayBeat();
    } finally {
      playState.choosing = false;
    }
  }

  function startPlayPolling() {
    stopPlayPolling();
    if (!isPlayMode() || !playState.play) return;
    playState.polling = window.setInterval(() => {
      refreshPlayView().catch(() => {});
    }, 2000);
    refreshPlayView().catch(() => {});
  }

  function stopPlayPolling() {
    if (playState.polling) {
      window.clearInterval(playState.polling);
      playState.polling = 0;
    }
  }

  function handlePlayAction(action) {
    if (action === 'open-settings') {
      window.Galgame?.openSettings?.();
      return;
    }
    if (action === 'open-chat-settings') {
      setPlayMode(false);
      const characters = window.Galgame?.getState?.()?.characters || [];
      if (!characters.length) window.Galgame?.newCharacter?.();
      else window.Galgame?.openSettings?.();
      return;
    }
    if (action === 'start') return startPlay();
  }

  function initPlayUI() {
    $('galgame-mode-chat')?.addEventListener('click', () => setPlayMode(false));
    $('galgame-mode-play')?.addEventListener('click', () => setPlayMode(true));
    $('galgame-play-select')?.addEventListener('change', (event) => selectPlay(event.target.value));
    $('galgame-new-play')?.addEventListener('click', createPlay);
    $('galgame-start-play')?.addEventListener('click', startPlay);
    $('galgame-pause-play')?.addEventListener('click', pausePlay);
    $('galgame-dialogue')?.addEventListener('click', (event) => {
      if (!isPlayMode()) return;
      const action = event.target.closest('[data-galgame-play-action]');
      if (action) {
        event.stopPropagation();
        handlePlayAction(action.dataset.galgamePlayAction);
        return;
      }
      const choice = event.target.closest('[data-choice-id]');
      if (choice) {
        event.stopPropagation();
        choosePlay(choice.dataset.choiceId);
        return;
      }
      if (event.target.closest('.galgame-empty, .galgame-loading')) return;
      if (waitingAfterChoice()) return;
      advancePlay();
    });
    $('galgame-image')?.closest('.galgame-image')?.addEventListener('click', () => {
      if (isPlayMode()) advancePlay();
    });
  }

  initPlayUI();
  window.GalgamePlay = {
    setMode: setPlayMode,
    load: loadPlays,
    onCharacterChange() {
      playState.play = null;
      playState.view = null;
      playState.beats = [];
      if (isPlayMode()) loadPlays();
    },
  };
})();
