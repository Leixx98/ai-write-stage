/* Immersive reader. Kept independent from the workbench runtime client. */
(() => {
  const LAST_CHAPTER_KEY = 'ainovel.reader.chapter.v1';
  const SCROLL_KEY = 'ainovel.reader.scroll.v1';
  const REFRESH_INTERVAL_MS = 10000;
  let chapters = [];
  let currentChapter = 0;
  let previousFocus = null;
  let refreshTimer = 0;
  let readerHistoryOwned = false;
  let scrollPositions = loadScrollPositions();

  function request(path) {
    return fetch(path, { headers: { Accept: 'application/json' } })
      .then(async (response) => {
        const body = await response.json().catch(() => ({}));
        if (!response.ok || (body.code !== undefined && body.code !== 0)) {
          throw new Error(body.msg || body.error || response.statusText);
        }
        return body.data !== undefined ? body.data : body;
      });
  }

  function loadScrollPositions() {
    try { return JSON.parse(localStorage.getItem(SCROLL_KEY) || '{}'); } catch (_) { return {}; }
  }

  function saveReaderState() {
    if (!currentChapter) return;
    const articlePane = document.getElementById('reader-content-pane');
    scrollPositions[currentChapter] = articlePane?.scrollTop || 0;
    try {
      localStorage.setItem(LAST_CHAPTER_KEY, String(currentChapter));
      localStorage.setItem(SCROLL_KEY, JSON.stringify(scrollPositions));
    } catch (_) { /* Reader state is optional. */ }
  }

  function createReader() {
    const reader = document.createElement('section');
    reader.id = 'reader-view';
    reader.className = 'reader-view';
    reader.hidden = true;
    reader.setAttribute('aria-label', '沉浸式阅读');
    reader.innerHTML = `
      <header class="reader-header">
        <div class="reader-brand"><strong id="reader-novel-name">未命名作品</strong><span id="reader-count"></span></div>
        <button id="reader-close" class="reader-close" type="button" aria-label="关闭沉浸式阅读" title="关闭">×</button>
      </header>
      <div class="reader-layout">
        <aside class="reader-sidebar" aria-label="正式章节">
          <div class="reader-sidebar-title">章节</div>
          <nav id="reader-chapters" class="reader-chapters"></nav>
        </aside>
        <main id="reader-content-pane" class="reader-content-pane">
          <article class="reader-article">
            <h1 id="reader-title"></h1>
            <div id="reader-content" class="reader-content"><p class="reader-muted">选择章节开始阅读</p></div>
          </article>
        </main>
      </div>`;
    document.body.appendChild(reader);
    document.getElementById('reader-close').addEventListener('click', closeReader);
    document.getElementById('reader-content-pane').addEventListener('scroll', debounce(saveReaderState, 150));
  }

  function debounce(fn, delay) {
    let timer = 0;
    return (...args) => {
      clearTimeout(timer);
      timer = setTimeout(() => fn(...args), delay);
    };
  }

  function renderChapterList() {
    const host = document.getElementById('reader-chapters');
    host.replaceChildren(...chapters.map((item) => {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = `reader-chapter${item.chapter === currentChapter ? ' active' : ''}`;
      button.dataset.chapter = String(item.chapter);
      const number = document.createElement('span');
      number.textContent = `第 ${item.chapter} 章`;
      const title = document.createElement('strong');
      title.textContent = item.title || `第 ${item.chapter} 章`;
      button.append(number, title);
      button.addEventListener('click', () => selectChapter(item.chapter, true));
      return button;
    }));
  }

  async function loadChapterList(selectDefault) {
    const data = await request('/api/v2/chapters');
    chapters = Array.isArray(data.chapters) ? data.chapters : [];
    document.getElementById('reader-novel-name').textContent = data.novel_name || '未命名作品';
    document.getElementById('reader-count').textContent = chapters.length ? `${chapters.length} 章` : '';
    renderChapterList();
    if (!chapters.length) {
      currentChapter = 0;
      document.getElementById('reader-title').textContent = '';
      document.getElementById('reader-content').innerHTML = '<p class="reader-muted">暂无已提交的正式章节</p>';
      return;
    }
    if (!selectDefault) return;
    const hashChapter = chapterFromHash();
    let savedChapter = 0;
    try { savedChapter = Number(localStorage.getItem(LAST_CHAPTER_KEY)); } catch (_) { /* Ignore storage errors. */ }
    const preferred = [hashChapter, savedChapter, chapters[chapters.length - 1].chapter]
      .find((chapter) => chapters.some((item) => item.chapter === chapter));
    await selectChapter(preferred, false);
  }

  async function selectChapter(chapter, updateURL) {
    if (!chapters.some((item) => item.chapter === chapter)) return;
    saveReaderState();
    currentChapter = chapter;
    renderChapterList();
    const content = document.getElementById('reader-content');
    const title = document.getElementById('reader-title');
    title.textContent = '';
    content.className = 'reader-content';
    content.innerHTML = '<p class="reader-muted">正在加载章节…</p>';
    if (updateURL) history.replaceState({ reader: true }, '', `#reader/${chapter}`);
    try {
      const data = await request(`/api/v2/chapters/${encodeURIComponent(chapter)}`);
      if (chapter !== currentChapter) return;
      title.textContent = data.title || `第 ${chapter} 章`;
      content.innerHTML = data.html || '<p class="reader-muted">本章暂无内容</p>';
      content.querySelectorAll('img').forEach((image) => {
        image.loading = 'lazy';
        image.decoding = 'async';
      });
      const pane = document.getElementById('reader-content-pane');
      requestAnimationFrame(() => { pane.scrollTop = Number(scrollPositions[chapter]) || 0; });
    } catch (error) {
      if (chapter !== currentChapter) return;
      content.textContent = `章节读取失败：${error.message}`;
      content.className = 'reader-content reader-error';
    }
  }

  function chapterFromHash() {
    const match = location.hash.match(/^#reader\/(\d+)$/);
    return match ? Number(match[1]) : 0;
  }

  async function openReader(pushHistory = false) {
    previousFocus = document.activeElement;
    const reader = document.getElementById('reader-view');
    reader.hidden = false;
    document.body.classList.add('reader-active');
    if (pushHistory && !location.hash.startsWith('#reader')) {
      history.pushState({ reader: true }, '', '#reader');
      readerHistoryOwned = true;
    }
    try {
      await loadChapterList(true);
      if (currentChapter) history.replaceState({ reader: true }, '', `#reader/${currentChapter}`);
    } catch (error) {
      document.getElementById('reader-content').textContent = `章节列表读取失败：${error.message}`;
    }
    clearInterval(refreshTimer);
    refreshTimer = setInterval(() => loadChapterList(false).catch(() => {}), REFRESH_INTERVAL_MS);
    document.getElementById('reader-close').focus();
  }

  function hideReader() {
    saveReaderState();
    clearInterval(refreshTimer);
    refreshTimer = 0;
    document.getElementById('reader-view').hidden = true;
    document.body.classList.remove('reader-active');
    readerHistoryOwned = false;
    previousFocus?.focus?.();
  }

  function closeReader() {
    if (readerHistoryOwned) {
      history.back();
      return;
    }
    if (location.hash.startsWith('#reader')) {
      history.replaceState(null, '', `${location.pathname}${location.search}`);
    }
    hideReader();
  }

  createReader();
  document.getElementById('reader-open')?.addEventListener('click', () => openReader(true));
  window.addEventListener('hashchange', () => {
    if (location.hash.startsWith('#reader')) {
      if (document.getElementById('reader-view').hidden) openReader(false);
      else {
        const chapter = chapterFromHash();
        if (chapter && chapter !== currentChapter) selectChapter(chapter, false);
      }
    } else if (!document.getElementById('reader-view').hidden) {
      hideReader();
    }
  });
  document.addEventListener('keydown', (event) => {
    if (event.key === 'Escape' && !document.getElementById('reader-view').hidden) closeReader();
  });
  if (location.hash.startsWith('#reader')) openReader(false);
})();
