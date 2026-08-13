const $ = (id) => document.getElementById(id);
const events = $('events');
let currentState = null;
function esc(value) { return String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }
function renderState(s) {
  currentState = s;
  $('model').textContent = [s.Provider, s.ModelName, s.Style].filter(Boolean).join(' / ') || '未配置模型';
  $('status').textContent = s.StatusLabel || 'READY';
  $('status').style.color = s.IsRunning ? 'var(--accent)' : (s.StatusLabel === 'COMPLETE' ? 'var(--good)' : 'var(--good)');
  const rows = [['运行状态', s.RuntimeState], ['阶段', s.Phase], ['流程', s.Flow], ['当前章节', s.CurrentChapter], ['已完成章节', `${s.CompletedCount}/${s.TotalChapters}`], ['总字数', s.TotalWordCount], ['上下文', `${s.ContextTokens || 0}/${s.ContextWindow || 0}`], ['费用', `$${Number(s.TotalCostUSD || 0).toFixed(4)}`]];
  $('state').innerHTML = rows.map(([k,v]) => `<dt>${esc(k)}</dt><dd>${esc(v || '-')}</dd>`).join('');
  const chapters = (s.Outline || []).map(ch => `<div class="chapter-row ${ch.Chapter === s.CurrentChapter ? 'chapter-current' : ''}"><span>第${esc(ch.Chapter)}章</span><strong>${esc(ch.Title || '未命名')}</strong><small>${esc(ch.CoreEvent || '')}</small></div>`).join('');
  const agents = (s.Agents || []).filter(a => a.State && a.State !== 'idle').map(a => `<p><strong>${esc(a.Name)}</strong>：${esc(a.Summary || a.Tool || a.State)}</p>`).join('');
  $('detail').innerHTML = `<h3>作品</h3><p>${esc(s.NovelName || '未命名')}</p><h3>当前任务</h3>${agents || '<p>暂无</p>'}<h3>章节大纲</h3><div class="chapters">${chapters || '<p>暂无</p>'}</div><h3>前提</h3><p>${esc(s.Premise || '暂无')}</p><h3>最近提交</h3><p>${esc(s.LastCommitSummary || '暂无')}</p><h3>检查点</h3><p>${esc(s.LastCheckpointName || '暂无')}</p><h3>角色</h3><p>${esc((s.Characters || []).join('、') || '暂无')}</p>`;
}
async function refresh() { const r = await fetch('/api/state'); if (r.ok) { const data = await r.json(); renderState(data.snapshot); } }
function appendEvent(ev) { const el = document.createElement('div'); const cls = ev.Level === 'error' ? 'event-error' : (ev.Level === 'warn' ? 'event-warn' : ''); el.className = cls; el.innerHTML = `<span class="event-time">${esc(new Date(ev.Time).toLocaleTimeString())}</span><strong>${esc(ev.Category)}</strong> ${esc(ev.Summary || ev.Detail)}`; events.appendChild(el); events.scrollTop = events.scrollHeight; }
async function replay() { const r = await fetch('/api/replay'); if (!r.ok) return; const items = await r.json(); for (const item of items) { if (item.kind === 'ui_event') appendEvent({Time:item.time, Category:item.category, Summary:item.summary, Level:'info'}); if (item.kind === 'stream_clear') $('stream').textContent += '\n\n'; if (item.kind === 'stream_delta' && item.payload?.delta) $('stream').textContent += item.payload.delta; } $('stream').scrollTop = $('stream').scrollHeight; }
function connect() { const es = new EventSource('/api/events'); es.onmessage = e => { appendEvent(JSON.parse(e.data)); refresh(); }; es.onerror = () => { es.close(); setTimeout(connect, 2000); }; const ss = new EventSource('/api/stream'); ss.onmessage = e => { const p = JSON.parse(e.data); if (p.clear) $('stream').textContent += '\n\n'; else $('stream').textContent += p.delta || ''; $('stream').scrollTop = $('stream').scrollHeight; }; ss.onerror = () => { ss.close(); setTimeout(connect, 2000); }; }
async function command(path, body={}) { const r = await fetch(path, {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(body)}); if (!r.ok) { const e = await r.json().catch(() => ({error:r.statusText})); appendEvent({Time:new Date(), Category:'ERROR', Level:'error', Summary:e.error}); } refresh(); }
$('send').onclick = () => { const text = $('prompt').value.trim(); if (!text) return; const isNew = !currentState || (!currentState.NovelName && !currentState.Phase); const path = isNew ? '/api/start' : (currentState.IsRunning ? '/api/steer' : '/api/continue'); command(path, isNew ? {prompt:text} : {text}); $('prompt').value=''; };
$('pause').onclick = () => command('/api/abort'); $('abort').onclick = () => command('/api/abort');
$('prompt').addEventListener('keydown', e => { if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); $('send').click(); } });
refresh(); replay(); connect();
