/* Galgame play / visual-novel mode. */
(() => {
  const playState = { plays: [], play: null, view: null, beats: [], viewOrdinal: 0, polling: 0, logSource: null, logReconnect: 0, advancing: false, choosing: false };

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
      setLogOpen(false);
      stopPlayPolling();
      hidePlayNav();
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
      setField('galgame-play-image-profile', play.image_profile_id || '');
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
    const stage = playStageLabel(view);
    const parts = [stage || statusLabel(status) || '未开局', `已缓存 ${buffer.text_ahead || 0} 屏`];
    if (buffer.image_generating) {
      const progress = Number(buffer.image_progress || 0);
      parts.push(progress > 0 ? `采样中 ${progress}%` : '配图中');
    } else if (buffer.image_error) {
      parts.push(`生成失败: ${buffer.image_error}`);
    }
    if (view.play?.last_error) parts.push(view.play.last_error);
    el.textContent = parts.join(' · ');
    el.title = '打开运行日志';
  }

  function playStageLabel(view) {
    const stage = view?.play?.stage || '';
    if (stage === 'planning') return '规划中';
    if (stage === 'storyboard') return '拆镜中';
    if (stage === 'writing') return (view?.progress?.write_head || 0) < 1 ? '写第一屏' : '写作中';
    return '';
  }

  function statusLabel(status) {
    return ({ idle: '未开局', running: '写作中', awaiting_choice: '等待选项', paused: '已暂停', completed: '已完结' })[status] || status;
  }

  function isLogOpen() { return $('galgame')?.classList.contains('is-log-open'); }

  function setLogOpen(on) {
    const root = $('galgame');
    const panel = $('galgame-runlog');
    const status = $('galgame-play-status');
    const open = Boolean(on) && isPlayMode();
    root?.classList.toggle('is-log-open', open);
    if (panel) panel.hidden = !open;
    if (status) status.setAttribute('aria-pressed', open ? 'true' : 'false');
    if (open) openLogStream();
    else closeLogStream();
  }

  function stickScroll(el) {
    return el && el.scrollHeight - el.scrollTop - el.clientHeight < 48;
  }

  // 运行日志走 SSE 增量推送 + 追加式渲染（与小说工作台 event/stream 面板同构）：
  // 事件每行 appendChild 一个节点，模型回复按帧 appendData，双向带上限头部裁剪，
  // 替代旧的 400ms 全量轮询 + textContent 整块替换。
  const LOG_MAX_EVENT_LINES = 500;
  const LOG_MAX_STREAM_CHARS = 256 * 1024;
  const LOG_EMPTY_EVENTS = '暂无事件';
  const LOG_EMPTY_STREAM = '暂无模型回复';
  const LOG_EMPTY_PLAY = '选择剧场局后显示运行日志。';
  const logStream = { chunks: [], chars: 0, pending: '', frame: 0, rebuild: false };

  function openLogStream() {
    closeLogStream();
    if (!isLogOpen() || !isPlayMode()) return;
    if (!playState.play) {
      renderLogEmpty();
      return;
    }
    const id = playState.play.id;
    const source = new EventSource(`/api/v2/galgame/plays/${encodeURIComponent(id)}/log/stream`);
    playState.logSource = source;
    source.onmessage = (event) => {
      if (playState.logSource !== source || playState.play?.id !== id) return;
      let payload;
      try { payload = JSON.parse(event.data); } catch (_) { return; }
      if (payload.type === 'snapshot') renderLogSnapshot(payload);
      else if (payload.type === 'events') appendLogEvents(String(payload.text || ''));
      else if (payload.type === 'stream') queueLogStream(String(payload.text || ''));
    };
    source.onerror = () => {
      if (playState.logSource !== source) return;
      source.close();
      playState.logSource = null;
      if (isLogOpen() && isPlayMode()) playState.logReconnect = window.setTimeout(openLogStream, 2500);
    };
  }

  function closeLogStream() {
    if (playState.logReconnect) {
      window.clearTimeout(playState.logReconnect);
      playState.logReconnect = 0;
    }
    if (playState.logSource) {
      playState.logSource.close();
      playState.logSource = null;
    }
    if (logStream.frame) {
      window.cancelAnimationFrame(logStream.frame);
      logStream.frame = 0;
    }
    logStream.pending = '';
  }

  function renderLogEmpty() {
    const events = $('galgame-runlog-events');
    if (events) events.textContent = LOG_EMPTY_PLAY;
    const stream = $('galgame-runlog-stream');
    if (stream) stream.textContent = '';
    logStream.chunks = [];
    logStream.chars = 0;
    logStream.pending = '';
    logStream.rebuild = false;
  }

  function resetEventsPane() {
    const events = $('galgame-runlog-events');
    if (!events) return null;
    if (!events.childElementCount) events.textContent = '';
    return events;
  }

  function renderLogSnapshot(payload) {
    const events = resetEventsPane();
    if (events) {
      events.textContent = '';
      const lines = String(payload.events || '').split('\n').filter(Boolean).slice(-LOG_MAX_EVENT_LINES);
      for (const line of lines) {
        const row = document.createElement('div');
        row.textContent = line;
        events.appendChild(row);
      }
      if (!lines.length) events.textContent = LOG_EMPTY_EVENTS;
      events.scrollTop = events.scrollHeight;
    }
    const text = String(payload.stream || '');
    logStream.chunks = text ? [text.slice(-LOG_MAX_STREAM_CHARS)] : [];
    logStream.chars = logStream.chunks.length ? logStream.chunks[0].length : 0;
    logStream.pending = '';
    logStream.rebuild = true;
    renderLogStream();
    const stream = $('galgame-runlog-stream');
    if (stream) {
      if (!text) stream.textContent = LOG_EMPTY_STREAM;
      stream.scrollTop = stream.scrollHeight;
    }
  }

  function appendLogEvents(text) {
    if (!text) return;
    const events = resetEventsPane();
    if (!events) return;
    const follow = stickScroll(events);
    for (const line of String(text).split('\n').filter(Boolean)) {
      const row = document.createElement('div');
      row.textContent = line;
      events.appendChild(row);
    }
    while (events.childElementCount > LOG_MAX_EVENT_LINES) events.firstElementChild.remove();
    if (follow) events.scrollTop = events.scrollHeight;
  }

  function queueLogStream(text) {
    if (!text) return;
    const stream = $('galgame-runlog-stream');
    if (stream && stream.textContent === LOG_EMPTY_STREAM) stream.textContent = '';
    logStream.chunks.push(text);
    logStream.chars += text.length;
    logStream.pending += text;
    while (logStream.chars > LOG_MAX_STREAM_CHARS && logStream.chunks.length > 1) {
      logStream.chars -= logStream.chunks.shift().length;
      logStream.rebuild = true;
    }
    if (logStream.chars > LOG_MAX_STREAM_CHARS) {
      logStream.chunks[0] = logStream.chunks[0].slice(-LOG_MAX_STREAM_CHARS);
      logStream.chars = logStream.chunks[0].length;
      logStream.rebuild = true;
    }
    if (!logStream.frame) logStream.frame = window.requestAnimationFrame(renderLogStream);
  }

  function renderLogStream() {
    logStream.frame = 0;
    const stream = $('galgame-runlog-stream');
    if (!stream || (!logStream.pending && !logStream.rebuild)) return;
    const follow = stickScroll(stream);
    if (logStream.rebuild) {
      stream.textContent = logStream.chunks.join('');
    } else if (logStream.pending) {
      let node = stream.firstChild;
      if (!node) {
        node = document.createTextNode('');
        stream.appendChild(node);
      }
      if (node.nodeType === Node.TEXT_NODE && !node.nextSibling) node.appendData(logStream.pending);
      else stream.textContent = logStream.chunks.join('');
    }
    logStream.pending = '';
    logStream.rebuild = false;
    if (follow) stream.scrollTop = stream.scrollHeight;
  }

  function liveHead() {
    return playState.view?.progress?.play_head || 0;
  }

  function displayHead() {
    const live = liveHead();
    if (!live) return 0;
    const head = playState.viewOrdinal || live;
    if (head >= live) return live;
    return Math.max(1, head);
  }

  function isReviewing() {
    const live = liveHead();
    return live > 0 && displayHead() < live;
  }

  function followLive() {
    playState.viewOrdinal = 0;
  }

  function clampViewOrdinal() {
    const live = liveHead();
    if (!live || playState.viewOrdinal <= 0 || playState.viewOrdinal >= live) playState.viewOrdinal = 0;
  }

  function currentBeat() {
    const head = liveHead();
    return (playState.beats || []).find((beat) => beat.ordinal === head) || playState.view?.beat || null;
  }

  function displayedBeat() {
    const head = displayHead();
    if (!head) return null;
    return (playState.beats || []).find((beat) => beat.ordinal === head) || (head === liveHead() ? playState.view?.beat : null);
  }

  function selectedChoice(beat) {
    return (playState.view?.progress?.choice_history || []).find((item) => item.ordinal === beat?.ordinal) || null;
  }

  function waitingAfterChoice() {
    if (playState.choosing) return true;
    const beat = currentBeat();
    if (!beat) return liveHead() > 0;
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

  function beatTextHTML(text) {
    const parts = String(text || '').split(/\n{2,}/).map((part) => part.trim()).filter(Boolean);
    if (!parts.length) return '<p></p>';
    return parts.map((part) => `<p>${esc(part).replace(/\n/g, '<br>')}</p>`).join('');
  }

  function setPlayHint(hint) {
    const el = $('galgame-play-hint');
    if (el) el.textContent = hint || '';
  }

  function renderPlayBeat() {
    const host = $('galgame-dialogue');
    if (!host || !isPlayMode()) return;
    clampViewOrdinal();
    const reviewing = isReviewing();
    if (!reviewing && waitingAfterChoice()) {
      host.innerHTML = loadingHTML();
      delete host.dataset.playSig;
      setPlayHint('正在续写…');
      renderPlayImage(playState.view?.image);
      renderPlayNav();
      return;
    }
    const beat = displayedBeat();
    if (!beat) {
      host.innerHTML = emptyPlayHTML();
      delete host.dataset.playSig;
      setPlayHint('');
      renderPlayImage(null);
      renderPlayNav();
      return;
    }
    const chosen = selectedChoice(beat);
    const liveChoice = !reviewing;
    const speaker = beat.speaker || (beat.kind === 'narration' ? '' : (character()?.name || ''));
    const choices = beat.kind === 'choice'
      ? `<div class="galgame-choices">${(beat.choices || []).map((choice) => {
        const on = chosen?.choice_id === choice.id;
        const disabled = !liveChoice || Boolean(chosen);
        return `<button type="button" data-choice-id="${esc(choice.id)}"${disabled ? ' disabled' : ''}${on ? ' class="is-selected"' : ''}>${esc(choice.label)}</button>`;
      }).join('')}</div>`
      : '';
    let hint = '单击继续';
    if (reviewing) hint = `回看 ${displayHead()} / ${liveHead()}`;
    else if (beat.kind === 'choice' && !chosen) hint = '请选择';
    else if (liveHead() >= (playState.view?.progress?.write_head || 0)) hint = '正在续写…';
    setPlayHint(hint);
    const html = `<article class="galgame-beat">${speaker ? `<strong>${esc(speaker)}</strong>` : ''}${beatTextHTML(beat.text)}${choices}</article>`;
    if (host.dataset.playSig !== html) {
      host.innerHTML = html;
      host.dataset.playSig = html;
    }
    renderPlayImage(displayImage());
    renderPlayNav();
  }

  function boundNewBeat(ordinal) {
    const beats = playState.beats || [];
    for (let i = beats.length - 1; i >= 0; i--) {
      const beat = beats[i];
      if (beat.ordinal > ordinal || beat.cg !== 'new') continue;
      return beat;
    }
    return null;
  }

  function displayImage() {
    if (!isReviewing()) return playState.view?.image;
    const bound = boundNewBeat(displayHead());
    if (!bound) return null;
    const liveImage = playState.view?.image;
    if (liveImage && ((bound.image_job_id && liveImage.job_id === bound.image_job_id) || liveImage.ordinal === bound.ordinal)) return liveImage;
    if (bound.image_error && !bound.image_job_id) return { status: 'failed', ordinal: bound.ordinal };
    if (!bound.image_job_id) return { status: 'pending', ordinal: bound.ordinal };
    return { status: 'completed', url: `/api/v2/image-jobs/${encodeURIComponent(bound.image_job_id)}/outputs/0`, ordinal: bound.ordinal, job_id: bound.image_job_id };
  }

  function hidePlayNav() {
    const nav = $('galgame-play-nav');
    if (nav) nav.hidden = true;
    setPlayHint('');
  }

  function renderPlayNav() {
    const nav = $('galgame-play-nav');
    const prev = $('galgame-play-prev');
    const next = $('galgame-play-next');
    if (!nav) return;
    const live = liveHead();
    const shown = displayHead();
    const show = isPlayMode() && live > 0 && (Boolean(displayedBeat()) || (!isReviewing() && waitingAfterChoice()));
    nav.hidden = !show;
    if (prev) prev.disabled = shown <= 1;
    if (next) next.disabled = shown >= live;
  }

  function reviewPrev() {
    if (!isPlayMode() || liveHead() < 2) return;
    const shown = displayHead();
    if (shown <= 1) return;
    playState.viewOrdinal = shown - 1;
    renderPlayBeat();
  }

  function reviewNext() {
    if (!isPlayMode()) return;
    const live = liveHead();
    const shown = displayHead();
    if (!live || shown >= live) return;
    playState.viewOrdinal = shown + 1;
    if (playState.viewOrdinal >= live) followLive();
    renderPlayBeat();
  }

  function continueOrReview() {
    if (isReviewing()) {
      reviewNext();
      return;
    }
    if (waitingAfterChoice()) return;
    advancePlay();
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
    followLive();
    setField('galgame-play-name', '');
    setField('galgame-play-premise', '');
    setField('galgame-play-persona', '');
    setField('galgame-play-image-profile', '');
    const select = $('galgame-play-select');
    if (select) select.value = '';
    renderPlayForm();
    if (isLogOpen()) openLogStream();
  }

  async function selectPlay(id) {
    if (!id) {
      clearPlayDraft();
      return;
    }
    try {
      followLive();
      playState.view = await api(`/api/v2/galgame/plays/${encodeURIComponent(id)}`);
      playState.play = playState.view.play;
      playState.beats = await api(`/api/v2/galgame/plays/${encodeURIComponent(id)}/beats`) || [];
      renderPlayForm();
      const select = $('galgame-play-select');
      if (select) select.value = id;
      if (isLogOpen()) openLogStream();
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
        image_profile_id: fieldValue('galgame-play-image-profile'),
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

  async function savePlay() {
    if (!playState.play) return notify('请先新建剧场局', 'galgame-settings-msg', 'error');
    try {
      const updated = await api(`/api/v2/galgame/plays/${encodeURIComponent(playState.play.id)}`, { method: 'PUT', body: JSON.stringify({
        name: fieldValue('galgame-play-name'),
        premise: fieldValue('galgame-play-premise'),
        user_persona: fieldValue('galgame-play-persona'),
        image_profile_id: fieldValue('galgame-play-image-profile'),
      }) });
      playState.play = updated;
      if (playState.view) playState.view.play = updated;
      playState.plays = (await api('/api/v2/galgame/plays') || []).filter((item) => item.character_id === character()?.id);
      renderPlayForm();
      notify('剧场设置已保存', 'galgame-settings-msg', 'success');
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
    if (isReviewing()) return;
    const beat = currentBeat();
    const progress = playState.view?.progress || {};
    if (beat?.kind === 'choice' && selectedChoice(beat) && progress.write_head > progress.play_head) {
      advancePlay();
    }
  }

  async function advancePlay() {
    if (!isPlayMode() || playState.advancing || !playState.play || isReviewing()) return;
    const beat = currentBeat();
    if (!beat || (beat.kind === 'choice' && !selectedChoice(beat))) return;
    const progress = playState.view?.progress || {};
    if (progress.play_head >= progress.write_head) return;
    playState.advancing = true;
    try {
      window.Galgame?.closeSettings?.();
      playState.view = await api(`/api/v2/galgame/plays/${encodeURIComponent(playState.play.id)}/advance`, { method: 'POST', body: '{}' });
      playState.play = playState.view.play;
      followLive();
      renderPlayBeat();
      renderPlayBuffer();
    } catch (error) {
      notify(error.message, 'galgame-settings-msg', 'error');
    } finally {
      playState.advancing = false;
    }
  }

  async function choosePlay(choiceId) {
    if (!playState.play || !choiceId || playState.choosing || isReviewing()) return;
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
    $('galgame-play-status')?.addEventListener('click', () => {
      if (isPlayMode()) setLogOpen(!isLogOpen());
    });
    $('galgame-runlog-close')?.addEventListener('click', () => setLogOpen(false));
    $('galgame-play-select')?.addEventListener('change', (event) => selectPlay(event.target.value));
    $('galgame-new-play')?.addEventListener('click', createPlay);
    $('galgame-save-play')?.addEventListener('click', savePlay);
    $('galgame-start-play')?.addEventListener('click', startPlay);
    $('galgame-pause-play')?.addEventListener('click', pausePlay);
    $('galgame-play-prev')?.addEventListener('click', (event) => {
      event.stopPropagation();
      reviewPrev();
    });
    $('galgame-play-next')?.addEventListener('click', (event) => {
      event.stopPropagation();
      reviewNext();
    });
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
        if (isReviewing() || choice.disabled) return;
        choosePlay(choice.dataset.choiceId);
        return;
      }
      if (event.target.closest('.galgame-empty, .galgame-loading')) return;
      continueOrReview();
    });
    $('galgame-image')?.closest('.galgame-image')?.addEventListener('click', () => {
      if (isPlayMode()) continueOrReview();
    });
  }

  initPlayUI();
  window.GalgamePlay = {
    isPlayMode,
    setMode: setPlayMode,
    load: loadPlays,
    onCharacterChange() {
      playState.play = null;
      playState.view = null;
      playState.beats = [];
      followLive();
      hidePlayNav();
      if (isPlayMode()) loadPlays();
    },
  };
})();
