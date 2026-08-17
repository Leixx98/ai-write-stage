/* ComfyUI canvas, bridge, and unit-image controls. */
(() => {
  let workflows = [];
  let selectedWorkflow = null;
  let currentJob = null;
  let comfyConfig = {};
  let instances = [];
  let fieldValues = {};
  let uploadedMedia = [];
  let bridgeConfig = null;
  let bridgeSchema = null;
  let bridgeJob = null;
  let bridgePrompterPresets = [];
  async function testConnection() {
    const draft = { enabled: $('comfy-enabled').checked, base_url: $('comfy-url').value.trim(), timeout_ms: Number($('comfy-timeout').value), poll_interval_ms: Number($('comfy-poll').value), client_id: $('comfy-client-id').value.trim(), max_response_bytes: Number($('comfy-max-bytes').value), strict: $('comfy-strict').checked, workflow_id: $('comfy-workflow-id').value.trim() };
    try {
      await api('/api/v2/comfyui/test-connection', { method: 'POST', body: JSON.stringify(draft) });
      notify('连接成功，配置尚未保存', 'comfy-msg', 'success');
    } catch (error) {
      const details = error.body?.data || {};
      const suffix = [details.phase && `phase: ${details.phase}`, details.retryable !== undefined && `retryable: ${details.retryable}`].filter(Boolean).join('; ');
      notify(`连接失败：${error.message}${suffix ? `（${suffix}）` : ''}`, 'comfy-msg', 'error');
    }
  }
  async function loadInstances() {
    try {
      const data = await api('/api/v2/comfyui/instances');
      const settings = data.settings || data;
      instances = data.instances || settings.instances || [];
      if (!Array.isArray(instances)) instances = [];
      if (settings.strategy) $('instance-strategy').value = settings.strategy;
      const defaultID = settings.default_instance_id || data.default_instance_id;
      $('instances-list').innerHTML = instances.length ? instances.map((item) => `<div class="instance-card ${item.health === 'healthy' ? 'healthy' : item.health === 'unhealthy' ? 'unhealthy' : ''}" data-instance-id="${esc(item.id)}"><label><input type="radio" name="default-instance" value="${esc(item.id)}" ${item.id === defaultID ? 'checked' : ''}><strong>${esc(item.name || item.id)}</strong></label><span>${esc(item.base_url || '')}</span><small>${esc(item.health || 'unknown')} · priority ${esc(item.priority ?? 0)} · queue ${esc(item.queue_length ?? '-')}</small></div>`).join('') : '<span class="muted">No instances loaded; the legacy Base URL is still available below.</span>';
    } catch (error) { notify(`Instance list unavailable: ${error.message}`, 'instances-msg', 'error'); }
  }
  async function validateWorkflow() { notify('Validation is performed when saving a workflow', 'workflow-msg'); }
  async function jobAction(action) { if (!currentJob?.job_id && !currentJob?.id) return notify('No active job', 'comfy-msg', 'error'); const id = currentJob.job_id || currentJob.id; try { renderJob(await api(`/api/v2/comfyui/jobs/${encodeURIComponent(id)}/${action}`, { method: 'POST', body: '{}' })); } catch (error) { notify(`${action} failed: ${error.message}`, 'comfy-msg', 'error'); } }

  /* Canvas-first ComfyUI editor. This override is intentionally kept at the end of
     the legacy client so old API routes remain available while the visible UI uses
     a graph, node inspector and test inspector. */
  let canvasMode = 'workflow';
  let canvasView = { x: 0, y: 0, k: 1 };
  let canvasPositions = {};
  let canvasPointer = null;
  let nodeEditorID = '';
  let canvasLoaded = false;
  function workflowAPI(workflow) { return workflow?.api_json || workflow?.workflow || {}; }
  function inferCanvasField(nodeID, input, value) { const id = /^[A-Za-z_][A-Za-z0-9_.-]{0,63}$/.test(input) ? input : `field_${nodeID}_${String(input).replace(/[^A-Za-z0-9_.-]/g, '_')}`; const lower = id.toLowerCase(); const kind = typeof value === 'number' ? 'number' : typeof value === 'boolean' ? 'boolean' : 'string'; let control = lower.includes('prompt') || lower.includes('text') ? 'textarea' : lower.includes('seed') || lower.includes('steps') || lower.includes('width') || lower.includes('height') ? 'number' : kind === 'boolean' ? 'boolean' : 'text'; return { id, node_id: nodeID, input, label: input, control, value_type: kind, default: value, source: kind === 'string' && (control === 'text' || control === 'textarea') ? 'prompter' : 'default', editable: true }; }
  function canvasKey(id) { return `ainovel-comfy-canvas:${id || 'draft'}`; }
  function restoreCanvas(id) {
    try {
      const v = JSON.parse(localStorage.getItem(canvasKey(id)) || '{}');
      canvasView = { x: 0, y: 0, k: 1, ...(v.viewport || {}) };
      // Persisted canvas documents store nodes as an array; the renderer uses
      // source node IDs as keys for fast position lookup.
      if (Array.isArray(v.nodes)) {
        canvasPositions = {};
        v.nodes.forEach((node) => {
          const key = node.source_node_id || node.id?.replace(/^node-/, '');
          if (key != null) canvasPositions[String(key)] = { x: Number(node.x) || 40, y: Number(node.y) || 40 };
        });
      } else {
        canvasPositions = v.nodes && typeof v.nodes === 'object' ? v.nodes : {};
      }
    } catch (_) {
      canvasView = { x: 0, y: 0, k: 1 };
      canvasPositions = {};
    }
  }
  function formConfig(config = {}) { comfyConfig = { ...config }; const map = { 'comfy-url': 'base_url', 'comfy-timeout': 'timeout_ms', 'comfy-poll': 'poll_interval_ms', 'comfy-client-id': 'client_id', 'comfy-max-bytes': 'max_response_bytes', 'comfy-workflow-id': 'workflow_id' }; Object.entries(map).forEach(([id, key]) => { if ($(id)) $(id).value = config[key] ?? ''; }); if ($('comfy-enabled')) $('comfy-enabled').checked = !!config.enabled; if ($('comfy-strict')) $('comfy-strict').checked = config.strict !== false; }
  async function loadComfyUI() { if (canvasLoaded) return; canvasLoaded = true; try { formConfig(await api('/api/v2/comfyui/config')); await loadInstances(); await loadWorkflows(); await loadBridgeConfig(); } catch (error) { canvasLoaded = false; notify(`ComfyUI 配置加载失败：${error.message}`, 'comfy-msg', 'error'); } }
  async function saveComfyUI() { const c = { enabled: $('comfy-enabled')?.checked ?? true, base_url: $('comfy-url')?.value.trim() || '', timeout_ms: Number($('comfy-timeout')?.value || 600000), poll_interval_ms: Number($('comfy-poll')?.value || 1000), client_id: $('comfy-client-id')?.value.trim() || 'ainovel-web', max_response_bytes: Number($('comfy-max-bytes')?.value || 52428800), strict: $('comfy-strict')?.checked !== false, workflow_id: $('comfy-workflow-id')?.value.trim() || '' }; try { formConfig(await api('/api/v2/comfyui/config', { method: 'PUT', body: JSON.stringify(c) })); notify('Connection settings saved', 'comfy-msg', 'success'); } catch (error) { notify(`Save failed: ${error.message}`, 'comfy-msg', 'error'); } }
  function workflowFieldCount(w) {
    if (selectedWorkflow?.id && w?.id === selectedWorkflow.id) return canvasFields(selectedWorkflow).length;
    const configFields = w?.config?.fields || w?.schema?.fields || w?.fields;
    if (Array.isArray(configFields)) return configFields.length;
    return Number.isFinite(Number(w?.field_count)) && Number(w.field_count) > 0 ? Number(w.field_count) : 0;
  }
  function renderWorkflowList() { const host = $('workflow-list'); if (!host) return; const query = ($('workflow-search')?.value || '').toLowerCase(); host.innerHTML = workflows.filter((w) => !query || String(w.name || w.id).toLowerCase().includes(query)).map((w) => `<button class="workflow-item ${selectedWorkflow?.id === w.id ? 'active' : ''}" data-workflow-id="${esc(w.id)}"><strong>${esc(w.name || w.id)}</strong><small>${workflowFieldCount(w)} 个可配置字段</small></button>`).join('') || '<span class="muted">暂无工作流</span>'; }
  async function loadWorkflows(preferredID = '') { try { const data = await api('/api/v2/comfyui/workflows') || []; workflows = data.workflows || data || []; if (!Array.isArray(workflows)) workflows = []; renderWorkflowList(); refreshBridgeWorkflowOptions(); const target = preferredID && workflows.some((w) => w.id === preferredID) ? preferredID : (selectedWorkflow?.id && workflows.some((w) => w.id === selectedWorkflow.id) ? selectedWorkflow.id : workflows[0]?.id); if (target) await selectWorkflow(target); } catch (error) { notify(`工作流加载失败：${error.message}`, 'workflow-msg', 'error'); } }
  function nodeList() { return Object.entries(workflowAPI(selectedWorkflow)).map(([id, node]) => ({ id, ...(node || {}) })); }
  function normalizeWorkflowResponse(data) { if (data?.workflow && data.workflow.id) return { ...data.workflow, config: data.config || data.workflow.config, canvas: data.canvas }; return data || {}; }
  function graphLayout() { const list = nodeList(); const incoming = {}; list.forEach((n) => { incoming[n.id] = []; }); list.forEach((n) => Object.values(n.inputs || {}).forEach((v) => { if (Array.isArray(v) && v.length && incoming[v[0]]) incoming[n.id].push(String(v[0])); })); const depth = {}; const visit = (id, stack = new Set()) => { if (depth[id] != null) return depth[id]; if (stack.has(id)) return 0; stack.add(id); depth[id] = Math.max(0, ...(incoming[id] || []).map((x) => visit(x, stack) + 1)); return depth[id]; }; list.forEach((n) => visit(n.id)); const rows = {}; list.forEach((n) => { const d = depth[n.id] || 0; (rows[d] ||= []).push(n); }); list.forEach((n) => { if (!canvasPositions[n.id]) { const d = depth[n.id] || 0; const row = rows[d].indexOf(n); canvasPositions[n.id] = { x: 50 + d * 240, y: 40 + row * 110 }; } }); return { list, depth }; }
  function renderCanvas() { const svg = $('workflow-canvas'); if (!svg) return; const empty = $('canvas-empty'); const { list } = graphLayout(); if (empty) empty.hidden = list.length > 0; const edgeHost = $('graph-edges'); const nodeHost = $('graph-nodes'); if (!list.length) { edgeHost.innerHTML = ''; nodeHost.innerHTML = ''; return; } const edgeParts = []; list.forEach((n) => Object.values(n.inputs || {}).forEach((v) => { if (!Array.isArray(v) || !v.length || !canvasPositions[v[0]]) return; const a = canvasPositions[v[0]], b = canvasPositions[n.id]; edgeParts.push(`<path class="graph-edge" d="M ${a.x + 176} ${a.y + 35} C ${a.x + 210} ${a.y + 35}, ${b.x - 34} ${b.y + 35}, ${b.x} ${b.y + 35}"/>`); })); edgeHost.innerHTML = edgeParts.join(''); const fields = canvasFields(); nodeHost.innerHTML = list.map((n) => { const p = canvasPositions[n.id]; const exposed = fields.filter((f) => String(f.node_id) === String(n.id)).length; const title = String(n.class_type || 'Node').replace(/_/g, ' '); return `<g class="graph-node ${exposed ? 'has-exposed' : ''}" data-node-id="${esc(n.id)}" transform="translate(${p.x},${p.y})"><rect width="176" height="70"></rect><text x="10" y="22">${esc(title.slice(0, 24))}</text><text class="node-class" x="10" y="40">#${esc(n.id)}</text>${exposed ? `<text class="node-pill" x="10" y="58">已选 ${exposed} 个字段</text>` : '<text class="node-class" x="10" y="58">点击选择可配置输入</text>'}</g>`; }).join(''); const vp = $('graph-viewport'); vp.setAttribute('transform', `translate(${canvasView.x},${canvasView.y}) scale(${canvasView.k})`); if ($('canvas-zoom-label')) $('canvas-zoom-label').textContent = `${Math.round(canvasView.k * 100)}%`; }
  function fitCanvas() { const { list } = graphLayout(); if (!list.length) return; const xs = list.map((n) => canvasPositions[n.id].x), ys = list.map((n) => canvasPositions[n.id].y); const svg = $('workflow-canvas'); const w = svg.clientWidth || 800, h = svg.clientHeight || 600; const gw = Math.max(...xs) - Math.min(...xs) + 230, gh = Math.max(...ys) - Math.min(...ys) + 120; canvasView.k = Math.max(.35, Math.min(1.5, Math.min(w / gw, h / gh))); canvasView.x = (w - gw * canvasView.k) / 2 - Math.min(...xs) * canvasView.k; canvasView.y = (h - gh * canvasView.k) / 2 - Math.min(...ys) * canvasView.k; renderCanvas(); persistCanvas(); }
    function renderNodeEditor(nodeID) { const node = workflowAPI(selectedWorkflow)[nodeID] || {}; const fields = canvasFields(); const existing = new Map(fields.filter((f) => String(f.node_id) === String(nodeID)).map((f) => [f.input, f])); const controls = { text: '文本', textarea: '多行文本', number: '数字', slider: '滑块', dropdown: '下拉框', boolean: '开关', image: '图片' }; const sourceLabels = { prompter: '由提示词模型生成', default: '使用工作流默认值', runtime: '仅运行时输入' }; const rows = Object.entries(node.inputs || {}).filter(([, value]) => !Array.isArray(value)).map(([input, value]) => { const f = existing.get(input) || inferCanvasField(nodeID, input, value); const opts = Object.keys(controls).map((x) => `<option value="${x}" ${f.control === x ? 'selected' : ''}>${controls[x]}</option>`).join(''); const source = normalizeFieldSource(f); const sourceOptions = Object.entries(sourceLabels).map(([key, label]) => `<option value="${key}" ${source === key ? 'selected' : ''}>${label}</option>`).join(''); return `<div class="node-field-row ${existing.has(input) ? 'exposed' : ''}" data-field-input="${esc(input)}" data-field-value-type="${esc(f.value_type || typeof value)}"><div class="node-field-head"><input type="checkbox" data-field-expose ${existing.has(input) ? 'checked' : ''}><code>${esc(input)}</code></div><label class="field-config-label">JSON 字段键<input data-field-id placeholder="例如 text" value="${esc(f.id)}"></label><label class="field-config-label">字段来源<select data-field-source>${sourceOptions}</select></label><input data-field-label placeholder="字段说明" value="${esc(f.label || input)}"><select data-field-control>${opts}</select><input data-field-default placeholder="默认值" value="${esc(f.default ?? value)}"><div class="node-field-extra"><label>最小值<input data-field-min value="${esc(f.min ?? '')}"></label><label>最大值<input data-field-max value="${esc(f.max ?? '')}"></label><label>步长<input data-field-step value="${esc(f.step ?? '')}"></label></div></div>`; }).join(''); const host = $('node-field-editor'); const modal = $('modal-field-editor'); if (host) host.innerHTML = '<span class="muted">字段配置已移至弹窗</span>'; if (modal) modal.innerHTML = rows || '<span class="muted">此节点没有可编辑输入</span>'; }
  function openNodePopup(nodeID) { nodeEditorID = String(nodeID); const node = workflowAPI(selectedWorkflow)[nodeID] || {}; $('inspector-node-title').textContent = `${node.class_type || 'Node'} #${nodeID}`; $('node-inspector-empty').hidden = true; $('node-inspector-body').hidden = false; $('modal-node-title').textContent = `${node.class_type || 'Node'} #${nodeID}`; $('modal-node-subtitle').textContent = '选择要在运行面板中编辑的字段'; renderNodeEditor(nodeID); $('node-modal').hidden = false; }
  function closeNodePopup() { $('node-modal').hidden = true; nodeEditorID = ''; }
    function readFieldEditor(host, nodeID = nodeEditorID) { return [...(host || document).querySelectorAll('.node-field-row')].filter((row) => row.querySelector('[data-field-expose]')?.checked).map((row) => { const val = row.querySelector('[data-field-default]')?.value || ''; const valueType = row.dataset.fieldValueType === 'boolean' ? 'boolean' : (val !== '' && !Number.isNaN(Number(val)) ? 'number' : 'string'); const cast = valueType === 'number' ? Number(val) : valueType === 'boolean' ? val === 'true' : val; return { id: row.querySelector('[data-field-id]')?.value.trim() || '', node_id: String(nodeID), input: row.dataset.fieldInput, label: row.querySelector('[data-field-label]')?.value || row.dataset.fieldInput, control: row.querySelector('[data-field-control]')?.value || 'text', value_type: valueType, default: cast, min: row.querySelector('[data-field-min]')?.value || undefined, max: row.querySelector('[data-field-max]')?.value || undefined, step: row.querySelector('[data-field-step]')?.value || undefined, editable: true, source: row.querySelector('[data-field-source]')?.value || 'default' }; }); }
    async function saveNodeFields() { if (!selectedWorkflow || !nodeEditorID) return; const source = $('modal-field-editor'); const notes = Object.fromEntries(canvasFields().map((f) => [f.id, f.note || ''])); const keep = canvasFields().filter((f) => String(f.node_id) !== String(nodeEditorID)); const added = readFieldEditor(source, nodeEditorID).map((f) => ({ ...f, note: notes[f.id] || '' })); const fields = keep.concat(added); const fieldError = validateCanvasFieldIDs(fields); if (fieldError) return notify(fieldError, 'comfy-msg', 'error'); selectedWorkflow.config = { ...(selectedWorkflow.config || {}), format: 'ainovel_workflow_config_v1', version: 1, fields, bindings: fields.map((f) => ({ key: f.id, node_id: f.node_id, path: `inputs.${f.input}`, type: f.value_type, required: false })), defaults: Object.fromEntries(fields.map((f) => [f.id, f.default])) }; selectedWorkflow.bindings = selectedWorkflow.config.bindings; selectedWorkflow.defaults = selectedWorkflow.config.defaults; renderCanvas(); renderDynamicFields(selectedWorkflow.config, selectedWorkflow.defaults); const savedID = selectedWorkflow.id; closeNodePopup(); const saved = await saveWorkflowDefinition(true); if (!saved) return; if (savedID) { renderWorkflowList(); await loadPromptSchema(savedID, { silent: true }); } notify('节点字段已更新', 'comfy-msg', 'success'); }
    function renderDynamicFields(schema, defaults = {}) { const fields = canvasFields({ config: schema }); fieldValues = { ...defaults }; const host = $('dynamic-fields'); if (!host) return; const sourceLabels = { prompter: '提示词模型', default: '默认值', runtime: '运行时' }; host.innerHTML = fields.length ? fields.map((f) => { const value = fieldValues[f.id] ?? f.default ?? ''; const id = `field-${f.id.replace(/[^a-zA-Z0-9_-]/g, '-')}`; let control = `<input id="${id}" data-field-key="${esc(f.id)}" value="${esc(value)}">`; if (f.control === 'textarea') control = `<textarea id="${id}" data-field-key="${esc(f.id)}" rows="3">${esc(value)}</textarea>`; if (f.control === 'number' || f.control === 'slider') control = `<input id="${id}" data-field-key="${esc(f.id)}" type="${f.control === 'slider' ? 'range' : 'number'}" value="${esc(value)}" min="${esc(f.min ?? '')}" max="${esc(f.max ?? '')}" step="${esc(f.step ?? '')}">`; if (f.control === 'boolean') control = `<input id="${id}" data-field-key="${esc(f.id)}" type="checkbox" ${value ? 'checked' : ''}>`; if (f.control === 'dropdown') control = `<select id="${id}" data-field-key="${esc(f.id)}">${(f.options || []).map((o) => `<option ${String(o) === String(value) ? 'selected' : ''}>${esc(o)}</option>`).join('')}</select>`; return `<label class="dynamic-field"><span>${esc(f.label || f.input)}<small>${esc(sourceLabels[normalizeFieldSource(f)])} · #${esc(f.node_id)}.${esc(f.input)}</small></span>${control}</label>`; }).join('') : '<span class="muted">请先在节点中勾选字段，字段会显示在这里。</span>'; }
  function readDynamicFields() { const values = { ...fieldValues }; document.querySelectorAll('#dynamic-fields [data-field-key]').forEach((f) => { values[f.dataset.fieldKey] = f.type === 'checkbox' ? f.checked : (f.type === 'number' || f.type === 'range' ? Number(f.value) : f.value); }); return values; }
  async function selectWorkflow(id) {
    try {
      const template = $('bridge-prompter-template');
      if (template) { template.dataset.loaded = 'false'; template.dataset.dirty = 'false'; }
      selectedWorkflow = normalizeWorkflowResponse(await api(`/api/v2/comfyui/workflows/${encodeURIComponent(id)}`));
      restoreCanvas(id);
      let canvasDocumentLoaded = false;
      try {
        const canvas = await api(`/api/v2/comfyui/workflows/${encodeURIComponent(id)}/canvas`);
        canvasDocumentLoaded = !!canvas;
        if (canvas?.viewport) canvasView = { x: canvas.viewport.x || 0, y: canvas.viewport.y || 0, k: canvas.viewport.scale || 1 };
        if (Array.isArray(canvas?.nodes)) canvas.nodes.forEach((n) => {
          if (n.source_node_id) canvasPositions[n.source_node_id] = { x: n.x || 40, y: n.y || 40 };
        });
        // An empty fields array is meaningful: it means the user explicitly
        // un-exposed every field and must replace the workflow config.
        if (canvas?.prompter_template != null) selectedWorkflow.prompter_template = canvas.prompter_template || '';
        if (canvas?.prompter_preset != null) selectedWorkflow.prompter_preset = canvas.prompter_preset || '';
        if (Array.isArray(canvas?.fields)) {
          const fields = canvas.fields.map((f) => ({ ...f, label: f.name || f.label || f.input, id: f.id || `${f.node_id}::${f.input}` }));
          selectedWorkflow.config = {
            ...(selectedWorkflow.config || {}),
            fields,
            bindings: fields.map((f) => ({ key: f.id, node_id: f.node_id, path: `inputs.${f.input}`, type: f.value_type || 'string', required: !!f.required })),
            defaults: Object.fromEntries(fields.map((f) => [f.id, f.default])),
          };
          selectedWorkflow.defaults = selectedWorkflow.config.defaults;
          selectedWorkflow.bindings = selectedWorkflow.config.bindings;
        }
      } catch (_) {}
      try {
        const schema = await api(`/api/v2/comfyui/workflows/${encodeURIComponent(id)}/schema`);
        if (schema?.fields && !canvasDocumentLoaded && !canvasFields().length) selectedWorkflow.config = { ...(selectedWorkflow.config || {}), fields: schema.fields };
      } catch (_) {}
      renderWorkflowList();
      refreshBridgeWorkflowOptions();
      if ($('bridge-workflow-id') && workflows.some((workflow) => workflow.id === id)) $('bridge-workflow-id').value = id;
      renderCanvas();
      renderDynamicFields(selectedWorkflow.config || {}, selectedWorkflow.defaults || {});
      if (!Object.keys(canvasPositions).length) fitCanvas(); else renderCanvas();
      await loadPromptSchema(id, { silent: true });
    } catch (error) {
      notify(error.message, 'workflow-msg', 'error');
    }
  }
  function newWorkflow() { selectedWorkflow = { id: '', name: 'unit image', workflow: {}, config: { fields: [], bindings: [], defaults: {} }, defaults: {}, bindings: [] }; canvasPositions = {}; renderWorkflowList(); renderCanvas(); }
  async function importWorkflow(file) { try { const raw = JSON.parse(await file.text()); selectedWorkflow = { id: '', name: file.name.replace(/\.json$/i, ''), workflow: raw, config: { fields: [], bindings: [], defaults: {} }, defaults: {}, bindings: [] }; canvasPositions = {}; renderCanvas(); fitCanvas(); notify('工作流已导入。请点击节点暴露字段，然后保存。', 'workflow-msg', 'success'); } catch (error) { notify(`JSON 导入失败：${error.message}`, 'workflow-msg', 'error'); } }
  async function testJob() { if (!selectedWorkflow?.id) return notify('请先保存工作流再测试', 'comfy-msg', 'error'); const values = readDynamicFields(); const body = { workflow_id: selectedWorkflow.id, instance_id: document.querySelector('input[name="default-instance"]:checked')?.value || null, prompt: values.text || values.positive_prompt || '', negative_prompt: values.negative_prompt || '', parameters: values, field_values: values, mini_test_values: {}, inputs: uploadedMedia, mode: 'test' }; try { currentJob = await api(`/api/v2/comfyui/workflows/${encodeURIComponent(selectedWorkflow.id)}/run`, { method: 'POST', body: JSON.stringify(body) }); renderJob(currentJob); showInspector('run'); } catch (error) { notify(`提交任务失败：${error.message}`, 'comfy-msg', 'error'); } }
  function outputList(job = {}) { const outputs = Array.isArray(job.outputs) && job.outputs.length ? job.outputs : (Array.isArray(job.output) ? job.output : job.output ? [job.output] : []); return outputs; }
  function renderJob(job = {}) { if (!job.job_id && !job.id) return; currentJob = { ...currentJob, ...job }; const id = currentJob.job_id || currentJob.id; const status = currentJob.status || 'pending'; const statusLabels = { pending: '等待中', submitting: '提交中', queued: '排队中', running: '运行中', succeeded: '已完成', completed: '已完成', failed: '失败', timeout: '超时', cancelled: '已取消' }; if ($('job-status')) { $('job-status').textContent = statusLabels[status] || status; $('job-status').className = `job-status ${status}`; } if ($('job-phase')) $('job-phase').textContent = [currentJob.phase || '', currentJob.progress != null ? `${Math.round(currentJob.progress * 100)}%` : ''].filter(Boolean).join(' · '); renderOutputs(outputList(currentJob), id); if (!['succeeded', 'completed', 'failed', 'timeout', 'cancelled'].includes(status)) { clearTimeout(renderJob.pollTimer); renderJob.pollTimer = setTimeout(() => refreshJob(id), 1500); } }
  function renderOutputs(outputs, jobID = '') { const list = Array.isArray(outputs) ? outputs : outputs ? [outputs] : []; const host = $('job-outputs'); if (!host) return; host.innerHTML = list.length ? list.map((o, i) => { const url = o.url || o.preview_url || o.image_url || (jobID ? `/api/v2/comfyui/jobs/${encodeURIComponent(jobID)}/outputs/${i}` : ''); const mime = o.mime || o.content_type || ''; const isImage = o.kind === 'image' || o.previewable || mime.startsWith('image/') || /\.(png|jpe?g|webp|gif)$/i.test(String(o.filename || '')); return isImage ? `<figure class="output-item"><img src="${esc(url)}" alt="${ui('latestOutput')} ${i + 1}" loading="lazy" data-lightbox-src="${esc(url)}"><figcaption>${ui('image')} · ${esc(mime)}</figcaption></figure>` : `<div class="output-item output-text"><strong>${esc(o.kind || ui('file'))} · #${i + 1}</strong><pre>${esc(o.preview || o.filename || JSON.stringify(o))}</pre></div>`; }).join('') : `<span class="muted">${ui('noOutputs')}</span>`; }
  async function refreshJob(id) { try { renderJob(await api(`/api/v2/comfyui/jobs/${encodeURIComponent(id)}`)); } catch (_) {} }
  function showInspector(tab) { document.querySelectorAll('.inspector-tab').forEach((b) => b.classList.toggle('active', b.dataset.inspectorTab === tab)); $('node-inspector').hidden = tab !== 'node'; $('run-inspector').hidden = tab !== 'run'; if ($('bridge-inspector')) $('bridge-inspector').hidden = tab !== 'bridge'; if (tab === 'bridge') { setBridgeUnitDefaults(); loadPromptSchema($('bridge-workflow-id')?.value || selectedWorkflow?.id, { silent: true }); loadUnitImageJob(); } }
  function saveCanvasPosition(id, x, y) { canvasPositions[id] = { x, y }; renderCanvas(); persistCanvas(); }
  const canvasSVG = $('workflow-canvas');
  if (canvasSVG) { canvasSVG.addEventListener('wheel', (e) => { e.preventDefault(); const factor = e.deltaY < 0 ? 1.1 : .9; canvasView.k = Math.max(.3, Math.min(2.5, canvasView.k * factor)); renderCanvas(); }, { passive: false }); canvasSVG.addEventListener('pointerdown', (e) => { const node = e.target.closest('.graph-node'); const rect = canvasSVG.getBoundingClientRect(); canvasPointer = { startX: e.clientX, startY: e.clientY, x: canvasView.x, y: canvasView.y, node: node?.dataset.nodeId || '', nodeStart: node ? { ...canvasPositions[node.dataset.nodeId] } : null, rect }; canvasSVG.setPointerCapture(e.pointerId); }); canvasSVG.addEventListener('pointermove', (e) => { if (!canvasPointer) return; const dx = e.clientX - canvasPointer.startX, dy = e.clientY - canvasPointer.startY; if (canvasPointer.node) { canvasPositions[canvasPointer.node] = { x: canvasPointer.nodeStart.x + dx / canvasView.k, y: canvasPointer.nodeStart.y + dy / canvasView.k }; } else { canvasView.x = canvasPointer.x + dx; canvasView.y = canvasPointer.y + dy; } renderCanvas(); }); canvasSVG.addEventListener('pointerup', (e) => { if (!canvasPointer) return; const p = canvasPointer; canvasPointer = null; const moved = Math.abs(e.clientX - p.startX) + Math.abs(e.clientY - p.startY) > 4; if (p.node && !moved) openNodePopup(p.node); else persistCanvas(); }); }
  document.querySelector('[data-action="close-node"]')?.addEventListener('click', closeNodePopup);
  document.querySelectorAll('[data-action="close-node"]').forEach((el) => el.addEventListener('click', closeNodePopup));
  document.querySelectorAll('[data-action="save-node-fields"]').forEach((el) => el.addEventListener('click', saveNodeFields));
  document.querySelector('[data-action="save-workflow-definition"]')?.addEventListener('click', () => saveWorkflowDefinition());
  document.querySelector('[data-action="test-job"]')?.addEventListener('click', () => testJob());
  document.querySelector('[data-action="cancel-job"]')?.addEventListener('click', () => jobAction('cancel'));
  document.querySelector('[data-action="retry-job"]')?.addEventListener('click', () => jobAction('retry'));
  document.querySelector('[data-action="new-workflow"]')?.addEventListener('click', newWorkflow);
  document.querySelector('[data-action="import-workflow"]')?.addEventListener('click', () => $('workflow-file')?.click());
  $('workflow-file')?.addEventListener('change', (e) => e.target.files[0] && importWorkflow(e.target.files[0]));
  $('workflow-list')?.addEventListener('click', (e) => { const id = e.target.closest('[data-workflow-id]')?.dataset.workflowId; if (id) selectWorkflow(id); });
  $('workflow-search')?.addEventListener('input', renderWorkflowList);
  document.querySelectorAll('[data-inspector-tab]').forEach((b) => b.addEventListener('click', () => showInspector(b.dataset.inspectorTab)));
  document.querySelectorAll('[data-canvas-mode]').forEach((b) => b.addEventListener('click', () => { canvasMode = b.dataset.canvasMode; document.querySelectorAll('[data-canvas-mode]').forEach((x) => x.classList.toggle('active', x === b)); $('workflow-canvas').hidden = canvasMode !== 'workflow'; $('test-canvas').hidden = canvasMode !== 'test'; if (canvasMode === 'test') showInspector('run'); }));
  document.querySelector('[data-canvas-tool="zoom-in"]')?.addEventListener('click', () => { canvasView.k = Math.min(2.5, canvasView.k * 1.15); renderCanvas(); });
  document.querySelector('[data-canvas-tool="zoom-out"]')?.addEventListener('click', () => { canvasView.k = Math.max(.3, canvasView.k / 1.15); renderCanvas(); });
  document.querySelector('[data-canvas-tool="fit"]')?.addEventListener('click', fitCanvas);
  document.querySelector('[data-canvas-tool="fullscreen"]')?.addEventListener('click', () => $('canvas-stage')?.requestFullscreen?.());
  if ($('job-outputs')) $('job-outputs').addEventListener('click', (e) => { const src = e.target.dataset.lightboxSrc; if (src) window.open(src, '_blank', 'noopener'); });

  /* Mini test canvas mirrors Infinite Canvas' quick-run cards without duplicating
     workflow JSON. Values are transient input to the same run endpoint. */
  let testCanvasPrompt = '';
  let testCanvasView = { x: 0, y: 0, k: 1 };
  let testCanvasPointer = null;
  function translateStaticUI() {
    const text = new Map([
      ['API', '模型 API'], ['Settings', '设置'], ['Writing prompts', '写作提示词'], ['ComfyUI Canvas', 'ComfyUI 画布'], ['ComfyUI', 'ComfyUI'], ['Workflow', '工作流'], ['Test canvas', '测试画布'],
      ['Workflows', '工作流'], ['New workflow', '新建工作流'], ['Import API JSON', '导入 API JSON'], ['Import', '导入'], ['Search workflows', '搜索工作流'], ['No workflows loaded', '暂无工作流'],
      ['Connection', '连接状态'], ['Unknown', '未知'], ['Least queue', '最短队列'], ['Fit', '适配'], ['Fullscreen', '全屏'],
      ['Validate workflow', '校验工作流'], ['Export JSON', '导出 JSON'], ['Node', '节点'], ['Run', '运行'],
      ['Click a node on the canvas to configure exposed fields.', '点击画布节点配置可编辑字段。'], ['Close', '关闭'], ['Apply fields', '应用字段'],
      ['Run test', '运行测试'], ['Idle', '空闲'], ['Cancel', '取消'], ['Retry', '重试'],
      ['Expose fields from nodes to edit them here.', '请先在节点中勾选字段，字段会显示在这里。'], ['Results appear here', '结果将在这里显示'],
      ['Import or select a workflow to begin', '请导入或选择工作流'], ['Workflow graph', '工作流图'], ['Fit graph', '适配画布'], ['Advanced connection settings', '高级连接设置'], ['Base URL', '服务地址'], ['Timeout', '超时时间'], ['Poll interval', '轮询间隔'],
      ['Client ID', '客户端 ID'], ['Max response bytes', '最大响应字节数'], ['Default workflow ID', '默认工作流 ID'], ['Enabled', '启用'], ['Strict mode', '严格模式'],
    ]);
    document.querySelectorAll('body *').forEach((el) => {
      if (el.children.length === 0 && text.has(el.textContent.trim())) el.textContent = text.get(el.textContent.trim());
    });
    document.querySelectorAll('[placeholder]').forEach((el) => { if (text.has(el.getAttribute('placeholder'))) el.setAttribute('placeholder', text.get(el.getAttribute('placeholder'))); });
    document.querySelectorAll('[title]').forEach((el) => { if (text.has(el.getAttribute('title'))) el.setAttribute('title', text.get(el.getAttribute('title'))); });
  }
  function renderTestCards() {
    const host = $('test-cards'); if (!host) return;
    const output = outputList(currentJob || {}).find((o) => o.kind === 'image' || o.previewable || o.mime?.startsWith('image/') || /\.(png|jpe?g|webp|gif)$/i.test(String(o.filename || '')));
    const outputURL = output ? (output.url || output.preview_url || output.image_url || ((currentJob?.job_id || currentJob?.id) ? `/api/v2/comfyui/jobs/${encodeURIComponent(currentJob.job_id || currentJob.id)}/outputs/0` : '')) : '';
    host.style.transform = `translate(${testCanvasView.x}px,${testCanvasView.y}px) scale(${testCanvasView.k})`;
    host.innerHTML = `<article class="test-card" data-test-card="prompt" style="left:40px;top:60px"><h3>${ui('prompt')}</h3><textarea id="test-canvas-prompt" rows="5" placeholder="描述要生成的图片..."></textarea><small class="muted">写入 text</small></article><article class="test-card comfy" data-test-card="comfy" style="left:330px;top:110px"><h3>${ui('comfyui')}</h3><div class="muted">${esc(selectedWorkflow?.name || ui('noWorkflow'))}</div><button data-action="test-canvas-run">${ui('runTest')}</button><span class="job-status">${esc(currentJob?.status || ui('idle'))}</span></article><article class="test-card output" data-test-card="output" style="left:650px;top:75px"><h3>${ui('output')}</h3>${outputURL ? `<img src="${esc(outputURL)}" alt="${ui('latestOutput')}" data-lightbox-src="${esc(outputURL)}">` : `<div class="muted">${esc(currentJob ? `任务 ${currentJob.status || 'pending'}` : '运行后预览图片')}</div>`}</article>`;
    const prompt = $('test-canvas-prompt'); if (prompt) { prompt.value = testCanvasPrompt; prompt.addEventListener('input', (e) => { testCanvasPrompt = e.target.value; }); }
    host.querySelector('[data-action="test-canvas-run"]')?.addEventListener('click', () => testJob());
    host.querySelectorAll('[data-lightbox-src]').forEach((img) => img.addEventListener('click', () => window.open(img.dataset.lightboxSrc, '_blank', 'noopener')));
  }
  function syncTestCanvasView() { const host = $('test-cards'); if (host) host.style.transform = `translate(${testCanvasView.x}px,${testCanvasView.y}px) scale(${testCanvasView.k})`; }
  document.querySelectorAll('[data-canvas-mode]').forEach((b) => b.addEventListener('click', () => { if (b.dataset.canvasMode === 'test') renderTestCards(); }));
  if ($('test-canvas')) {
    $('test-canvas').addEventListener('wheel', (e) => { e.preventDefault(); testCanvasView.k = Math.max(.45, Math.min(2, testCanvasView.k * (e.deltaY < 0 ? 1.1 : .9))); syncTestCanvasView(); }, { passive: false });
    $('test-canvas').addEventListener('pointerdown', (e) => { if (e.target.closest('button,textarea,input,select,img')) return; testCanvasPointer = { x: e.clientX, y: e.clientY, ox: testCanvasView.x, oy: testCanvasView.y }; $('test-canvas').setPointerCapture(e.pointerId); });
    $('test-canvas').addEventListener('pointermove', (e) => { if (!testCanvasPointer) return; testCanvasView.x = testCanvasPointer.ox + e.clientX - testCanvasPointer.x; testCanvasView.y = testCanvasPointer.oy + e.clientY - testCanvasPointer.y; syncTestCanvasView(); });
    $('test-canvas').addEventListener('pointerup', () => { testCanvasPointer = null; });
  }
  const originalTestJob = testJob;
  testJob = async function testJobWithCanvas() { const prompt = testCanvasPrompt.trim(); if (prompt) { const originalRead = readDynamicFields; const values = originalRead(); values.text = prompt; fieldValues = values; } await originalTestJob(); renderTestCards(); };
  const originalRenderJob = renderJob;
  renderJob = function renderJobWithCanvas(job) { if (job?.unit_id || job?.trigger === 'unit') renderBridgeJob(job); else { originalRenderJob(job); renderTestCards(); } };
  translateStaticUI();

  function normalizeFieldSource(field = {}) {
    const source = String(field.source || '').toLowerCase();
    if (['prompter', 'default', 'runtime'].includes(source)) return source;
    const type = field.value_type || field.type || 'string';
    const control = field.control || 'text';
    return type === 'string' && (control === 'text' || control === 'textarea') ? 'prompter' : 'default';
  }

  function validateCanvasFieldIDs(fields) {
    const seen = new Set();
    for (const field of fields) {
      if (!/^[A-Za-z_][A-Za-z0-9_.-]{0,63}$/.test(field.id || '')) return `JSON 字段键“${field.id || '(空)'}”格式无效，请使用字母或下划线开头，最多 64 个字符。`;
      if (seen.has(field.id)) return `JSON 字段键“${field.id}”重复，请为每个字段设置唯一键。`;
      seen.add(field.id);
    }
    return '';
  }

  function refreshBridgeWorkflowOptions() {
    const select = $('bridge-workflow-id');
    if (!select) return;
    const current = select.value || bridgeConfig?.workflow_id || selectedWorkflow?.id || '';
    select.innerHTML = `<option value="">请选择工作流</option>${workflows.map((workflow) => `<option value="${esc(workflow.id)}">${esc(workflow.name || workflow.id)}</option>`).join('')}`;
    if (workflows.some((workflow) => workflow.id === current)) select.value = current;
  }

  function renderBridgeConfig(config = {}) {
    bridgeConfig = { ...config };
    refreshBridgeWorkflowOptions();
    $('bridge-enabled').checked = !!config.enabled;
    $('bridge-auto-generate').checked = !!config.auto_generate;
    $('bridge-workflow-id').value = config.workflow_id || selectedWorkflow?.id || '';
    $('bridge-strict').checked = config.strict !== false;
    $('bridge-parser-strict').checked = config.strict !== false;
    $('bridge-prompter-timeout').value = config.prompter_timeout_ms ?? 120000;
    $('bridge-previous-tail').value = config.previous_tail_chars ?? 1200;
    $('bridge-state').textContent = config.enabled ? '已启用' : '未启用';
    $('bridge-state').className = `job-status ${config.enabled ? 'succeeded' : ''}`;
  }

  async function loadBridgeConfig() {
    try {
      renderBridgeConfig(await api('/api/v2/comfyui/bridge'));
      const workflowID = bridgeConfig?.workflow_id || selectedWorkflow?.id;
      if (workflowID && selectedWorkflow?.id !== workflowID && workflows.some((workflow) => workflow.id === workflowID)) await selectWorkflow(workflowID);
      if (workflowID) await loadPromptSchema(workflowID, { silent: true });
      setBridgeUnitDefaults();
    } catch (error) {
      $('bridge-state').textContent = '加载失败';
      $('bridge-state').className = 'job-status failed';
      notify(`桥接设置加载失败：${error.message}`, 'bridge-msg', 'error');
    }
  }

  function readBridgeConfig() {
    return {
      enabled: $('bridge-enabled').checked,
      auto_generate: $('bridge-auto-generate').checked,
      workflow_id: $('bridge-workflow-id').value,
      strict: $('bridge-strict').checked,
      prompter_timeout_ms: Number($('bridge-prompter-timeout').value),
      previous_tail_chars: Number($('bridge-previous-tail').value),
    };
  }

  async function saveBridgeConfig() {
    const draft = readBridgeConfig();
    if (!draft.workflow_id) return notify('请选择用于小说单元图片的工作流。', 'bridge-msg', 'error');
    if (draft.prompter_timeout_ms < 1000 || draft.prompter_timeout_ms > 600000) return notify('提示词生成超时必须在 1000 到 600000 毫秒之间。', 'bridge-msg', 'error');
    if (draft.previous_tail_chars < 0 || draft.previous_tail_chars > 8000) return notify('上一单元结尾字符数必须在 0 到 8000 之间。', 'bridge-msg', 'error');
    try {
      renderBridgeConfig(await api('/api/v2/comfyui/bridge', { method: 'PUT', body: JSON.stringify(draft) }));
      applyPrompterEditorToWorkflow();
      await persistCanvas();
      await loadPromptSchema(draft.workflow_id, { silent: false });
      notify('小说图片桥接设置已保存。', 'bridge-msg', 'success');
    } catch (error) {
      notify(`桥接设置保存失败：${error.message}`, 'bridge-msg', 'error');
      renderBridgeValidationErrors(error, 'bridge-parser-result');
    }
  }

  function schemaSample(fields = []) {
    return Object.fromEntries(fields.map((field) => {
      const type = field.value_type || bridgeSchema?.schema?.properties?.[field.id]?.type;
      if (type === 'integer' || type === 'number') return [field.id, 0];
      if (type === 'boolean') return [field.id, false];
      return [field.id, field.id.includes('negative') ? '低质量，模糊' : '电影感场景描述'];
    }));
  }

  function applyPrompterEditorToWorkflow() {
    if (!selectedWorkflow) return;
    const template = $('bridge-prompter-template');
    if (template?.dataset.loaded === 'true') selectedWorkflow.prompter_template = template.value;
    const preset = $('bridge-prompter-preset');
    if (preset?.value) selectedWorkflow.prompter_preset = preset.value;
    const notesHost = $('bridge-field-notes');
    if (!notesHost) return;
    const inputs = notesHost.querySelectorAll('[data-field-note]');
    if (!inputs.length) return;
    const notes = {};
    inputs.forEach((el) => { notes[el.dataset.fieldNote] = el.value; });
    if (selectedWorkflow.config?.fields) {
      selectedWorkflow.config.fields = selectedWorkflow.config.fields.map((f) => ({ ...f, note: notes[f.id] ?? f.note ?? '' }));
    }
  }

  function renderPrompterPresets(data = null) {
    const select = $('bridge-prompter-preset');
    if (!select) return;
    if (!qs('[data-action="save-prompter-preset"]')) {
      const label = select.closest('label');
      const toolbar = document.createElement('div');
      toolbar.className = 'preset-toolbar bridge-prompter-toolbar';
      label?.parentNode?.insertBefore(toolbar, label);
      if (label) {
        label.classList.add('preset-label');
        label.htmlFor = select.id;
        toolbar.appendChild(label);
        toolbar.appendChild(select);
      }
      toolbar.insertAdjacentHTML('beforeend', '<button type="button" class="small" data-action="save-prompter-preset">覆盖保存</button><button type="button" class="small" data-action="save-prompter-preset-as">另存为</button>');
      qs('[data-action="save-prompter-preset"]')?.addEventListener('click', () => savePrompterPreset($('bridge-prompter-preset')?.value, 'save', true));
      qs('[data-action="save-prompter-preset-as"]')?.addEventListener('click', async () => { const name = window.prompt('请输入新的生图提示词预设名称'); if (name?.trim()) await savePrompterPreset(name.trim(), 'save_as', false); });
    }
    if (Array.isArray(data?.presets) && data.presets.length) bridgePrompterPresets = data.presets;
    const current = selectedWorkflow?.prompter_preset || data?.preset || 'natural';
    select.innerHTML = bridgePrompterPresets.map((preset) => `<option value="${esc(preset.id)}" ${preset.id === current ? 'selected' : ''}>${esc(preset.label)}</option>`).join('');
    if (![...select.options].some((option) => option.value === current) && current) {
      select.insertAdjacentHTML('beforeend', `<option value="${esc(current)}" selected>${esc(current)}</option>`);
    }
    const selected = bridgePrompterPresets.find((preset) => preset.id === select.value);
    const help = $('bridge-prompter-preset-help');
    if (help) help.textContent = selected?.description || '';
  }

  function applyPrompterPreset(id) {
    const preset = bridgePrompterPresets.find((item) => item.id === id);
    if (!preset) return;
    if (selectedWorkflow) {
      selectedWorkflow.prompter_preset = id;
      selectedWorkflow.prompter_template = preset.template || '';
    }
    const template = $('bridge-prompter-template');
    if (template) {
      template.value = preset.template || '';
      template.dataset.dirty = 'true';
      template.dataset.loaded = 'true';
    }
    const help = $('bridge-prompter-preset-help');
    if (help) help.textContent = preset.description || '';
  }

  async function savePrompterPreset(name, action = 'save', overwrite = true) {
    const workflowID = $('bridge-workflow-id')?.value || selectedWorkflow?.id;
    const template = $('bridge-prompter-template')?.value || '';
    if (!workflowID) return notify('请先选择已保存的工作流。', 'bridge-msg', 'error');
    if (!name?.trim()) return notify('生图提示词预设名称不能为空。', 'bridge-msg', 'error');
    if (!template.trim()) return notify('提示词模板不能为空。', 'bridge-msg', 'error');
    try {
      const data = await api('/api/v2/comfyui/prompter-presets', { method: 'PUT', body: JSON.stringify({ action, name: name.trim(), workflow_id: workflowID, template, overwrite }) });
      if (selectedWorkflow) {
        selectedWorkflow.prompter_preset = data.preset || name.trim();
        selectedWorkflow.prompter_template = data.template || template;
      }
      const editor = $('bridge-prompter-template');
      if (editor) editor.dataset.dirty = 'false';
      renderPromptSchema(data);
      notify('生图提示词已保存并应用到当前工作流。', 'bridge-msg', 'success');
    } catch (error) {
      notify(`生图提示词保存失败：${error.message}`, 'bridge-msg', 'error');
    }
  }

  function renderPromptSchema(data = null, error = null) {
    bridgeSchema = data;
    const fields = data?.fields || [];
    const jsonHost = $('bridge-field-json');
    const notesHost = $('bridge-field-notes');
    const template = $('bridge-prompter-template');
    if (error || !data) {
      if (jsonHost) jsonHost.textContent = '{}';
      if (notesHost) notesHost.innerHTML = `<span class="muted">${esc(error || '请先选择已保存的工作流。')}</span>`;
      return;
    }
    renderPrompterPresets(data);
    if (template && template.dataset.dirty !== 'true') {
      template.value = data.template || data.default_template || '';
      template.dataset.loaded = 'true';
      if (data.preset && selectedWorkflow && !selectedWorkflow.prompter_preset) selectedWorkflow.prompter_preset = data.preset;
    } else if (template) {
      template.dataset.loaded = 'true';
    }
    const existingNotes = {};
    notesHost?.querySelectorAll('[data-field-note]').forEach((el) => { existingNotes[el.dataset.fieldNote] = el.value; });
    if (jsonHost) jsonHost.textContent = JSON.stringify(data.field_json || {}, null, 2);
    if (notesHost) {
      notesHost.innerHTML = fields.length ? fields.map((field) => {
        const note = existingNotes[field.id] ?? field.note ?? '';
        const type = field.value_type || 'string';
        return `<label class="field-note"><code>${esc(field.id)}</code><small>#${esc(field.node_id)} · ${esc(field.input)} · ${esc(type)}</small><textarea data-field-note="${esc(field.id)}" rows="2" placeholder="填写该字段的含义、约束或示例">${esc(note)}</textarea></label>`;
      }).join('') : '<span class="muted">请先在节点中勾选字段。</span>';
    }
    const raw = $('bridge-parser-raw');
    if (raw && raw.dataset.schemaHash !== (data.schema_hash || '')) {
      raw.dataset.schemaHash = data.schema_hash || '';
      raw.dataset.edited = 'false';
    }
    if (raw && raw.dataset.edited !== 'true') raw.value = JSON.stringify(data.field_json || schemaSample(fields), null, 2);
  }

  async function loadPromptSchema(workflowID, options = {}) {
    if (!workflowID) {
      renderPromptSchema(null, '请先选择工作流。');
      return null;
    }
    try {
      const data = await api(`/api/v2/comfyui/workflows/${encodeURIComponent(workflowID)}/prompt-schema`);
      renderPromptSchema(data);
      return data;
    } catch (error) {
      renderPromptSchema(null, error.message);
      if (!options.silent) notify(`提示词字段加载失败：${error.message}`, 'bridge-msg', 'error');
      return null;
    }
  }

  function renderBridgeValidationErrors(error, targetID = 'bridge-parser-result') {
    const target = $(targetID);
    if (!target) return;
    const data = error?.body?.data || {};
    const errors = Array.isArray(data.errors) ? data.errors : [];
    target.className = 'parser-result error';
    target.innerHTML = `<strong>${esc(error?.message || '校验失败')}</strong>${error?.body?.code ? `<small>错误码：${esc(error.body.code)}</small>` : ''}${data.stage ? `<small>阶段：${esc(data.stage)}</small>` : ''}${errors.length ? `<ul>${errors.map((item) => `<li>${item.field ? `<code>${esc(item.field)}</code> ` : ''}${esc(item.message || item.rule || String(item))}</li>`).join('')}</ul>` : ''}`;
  }

  async function parsePrompterJSON() {
    const workflowID = $('bridge-workflow-id').value || selectedWorkflow?.id;
    if (!workflowID) return notify('请先选择桥接工作流。', 'bridge-parser-result', 'error');
    const body = { workflow_id: workflowID, raw: $('bridge-parser-raw').value, strict: $('bridge-parser-strict').checked };
    try {
      const result = await api('/api/v2/comfyui/prompter/parse', { method: 'POST', body: JSON.stringify(body) });
      $('bridge-parser-result').className = 'parser-result success';
      $('bridge-parser-result').innerHTML = `<strong>JSON 合法，字段可以绑定</strong><pre>${esc(JSON.stringify(result.values || {}, null, 2))}</pre>${(result.warnings || []).length ? `<small>${esc(result.warnings.join('；'))}</small>` : ''}`;
    } catch (error) {
      renderBridgeValidationErrors(error);
    }
  }

  function setBridgeUnitDefaults() {
    if (!$('bridge-unit-chapter').value && currentState?.CurrentChapter) $('bridge-unit-chapter').value = currentState.CurrentChapter;
    if (!$('bridge-unit-ordinal').value && currentState?.CurrentUnit) $('bridge-unit-ordinal').value = currentState.CurrentUnit;
  }

  async function loadUnitImageJob() {
    const chapter = Number($('bridge-unit-chapter').value);
    const ordinal = Number($('bridge-unit-ordinal').value);
    if (!Number.isInteger(chapter) || chapter < 1 || !Number.isInteger(ordinal) || ordinal < 1) return;
    try {
      renderBridgeJob(await api(`/api/v2/units/${chapter}/${ordinal}/image-job`));
    } catch (error) {
      if (error.status !== 404) notify(`已有任务读取失败：${error.message}`, 'bridge-job-error', 'error');
    }
  }

  const BRIDGE_TERMINAL_STATES = new Set(['succeeded', 'completed', 'failed', 'timeout', 'cancelled']);
  function renderBridgeJob(job = {}) {
    if (!job.job_id && !job.id) return;
    bridgeJob = { ...bridgeJob, ...job };
    const id = bridgeJob.job_id || bridgeJob.id;
    const status = bridgeJob.status || 'pending';
    const labels = { pending: '等待中', prompting: '生成提示词', validating: '校验 JSON', binding: '绑定字段', submitting: '提交中', queued: '排队中', running: '生成中', succeeded: '已完成', completed: '已完成', failed: '失败', timeout: '超时', cancelled: '已取消' };
    $('bridge-job-status').textContent = labels[status] || status;
    $('bridge-job-status').className = `job-status ${status}`;
    $('bridge-job-stage').textContent = [bridgeJob.stage || bridgeJob.phase, bridgeJob.attempt ? `第 ${bridgeJob.attempt} 次` : '', bridgeJob.schema_hash || ''].filter(Boolean).join(' · ');
    const error = bridgeJob.error;
    $('bridge-job-error').textContent = typeof error === 'string' ? error : error?.message || '';
    $('bridge-job-error').className = `notice ${error ? 'error' : ''}`;
    $('bridge-prompt-values').textContent = JSON.stringify(bridgeJob.prompt_values || {}, null, 2);
    const chapter = bridgeJob.chapter || Number($('bridge-unit-chapter').value);
    const ordinal = bridgeJob.ordinal || Number($('bridge-unit-ordinal').value);
    const output = Array.isArray(bridgeJob.outputs) ? bridgeJob.outputs[0] : bridgeJob.output;
    const outputURL = output?.url || output?.preview_url || (status === 'completed' || status === 'succeeded' ? `/api/v2/units/${encodeURIComponent(chapter)}/${encodeURIComponent(ordinal)}/image?v=${encodeURIComponent(id)}` : '');
    $('bridge-job-output').innerHTML = outputURL ? `<img src="${esc(outputURL)}" alt="单元生成图片" data-lightbox-src="${esc(outputURL)}"><small>${esc(bridgeJob.unit_id || `第 ${chapter} 章 · 单元 ${ordinal}`)}</small>` : '<span class="muted">生成完成后在此显示图片。</span>';
    const image = $('bridge-job-output').querySelector('img');
    if (image) image.onerror = () => { $('bridge-job-output').innerHTML = '<span class="notice error">图片文件无法显示，请检查任务输出和媒体接口。</span>'; };
    const terminal = BRIDGE_TERMINAL_STATES.has(status);
    qs('[data-action="retry-unit-image"]').disabled = !terminal || status === 'completed' || status === 'succeeded';
    qs('[data-action="regenerate-unit-prompt"]').disabled = !terminal || status === 'completed' || status === 'succeeded';
    qs('[data-action="cancel-unit-image"]').disabled = terminal;
    clearTimeout(renderBridgeJob.pollTimer);
    if (!terminal) renderBridgeJob.pollTimer = setTimeout(() => refreshBridgeJob(id), 1500);
  }

  async function generateUnitImage() {
    const chapter = Number($('bridge-unit-chapter').value);
    const ordinal = Number($('bridge-unit-ordinal').value);
    if (!Number.isInteger(chapter) || chapter < 1 || !Number.isInteger(ordinal) || ordinal < 1) return notify('请输入有效的章节和单元序号。', 'bridge-job-error', 'error');
    const body = { workflow_id: $('bridge-workflow-id').value || selectedWorkflow?.id || '', force: $('bridge-unit-force').checked };
    try {
      $('bridge-job-error').textContent = '';
      renderBridgeJob(await api(`/api/v2/units/${chapter}/${ordinal}/image/generate`, { method: 'POST', body: JSON.stringify(body) }));
    } catch (error) {
      renderBridgeValidationErrors(error, 'bridge-job-error');
    }
  }

  async function refreshBridgeJob(id) {
    try { renderBridgeJob(await api(`/api/v2/comfyui/jobs/${encodeURIComponent(id)}`)); }
    catch (error) { notify(`图片任务状态读取失败：${error.message}`, 'bridge-job-error', 'error'); }
  }

  async function bridgeJobAction(action, regeneratePrompt = false) {
    const id = bridgeJob?.job_id || bridgeJob?.id;
    if (!id) return notify('当前没有可操作的单元图片任务。', 'bridge-job-error', 'error');
    try {
      renderBridgeJob(await api(`/api/v2/comfyui/jobs/${encodeURIComponent(id)}/${action}`, { method: 'POST', body: JSON.stringify(action === 'retry' ? { regenerate_prompt: regeneratePrompt } : {}) }));
    } catch (error) {
      renderBridgeValidationErrors(error, 'bridge-job-error');
    }
  }

  qs('[data-action="open-bridge"]')?.addEventListener('click', () => showInspector('bridge'));
  qs('[data-action="save-bridge"]')?.addEventListener('click', saveBridgeConfig);
  qs('[data-action="refresh-prompter-prompt"]')?.addEventListener('click', () => loadPromptSchema($('bridge-workflow-id').value || selectedWorkflow?.id, { silent: false }));
  qs('[data-action="parse-prompter-json"]')?.addEventListener('click', parsePrompterJSON);
  qs('[data-action="generate-unit-image"]')?.addEventListener('click', generateUnitImage);
  qs('[data-action="load-unit-image-job"]')?.addEventListener('click', loadUnitImageJob);
  qs('[data-action="retry-unit-image"]')?.addEventListener('click', () => bridgeJobAction('retry', false));
  qs('[data-action="regenerate-unit-prompt"]')?.addEventListener('click', () => bridgeJobAction('retry', true));
  qs('[data-action="cancel-unit-image"]')?.addEventListener('click', () => bridgeJobAction('cancel'));
  $('bridge-prompter-template')?.addEventListener('input', (event) => { event.target.dataset.dirty = 'true'; event.target.dataset.loaded = 'true'; });
  $('bridge-prompter-preset')?.addEventListener('change', (event) => applyPrompterPreset(event.target.value));
  $('bridge-workflow-id')?.addEventListener('change', async (event) => {
    const workflowID = event.target.value;
    if (workflowID && workflowID !== selectedWorkflow?.id) await selectWorkflow(workflowID);
    await loadPromptSchema(workflowID, { silent: true });
  });
  $('bridge-parser-raw')?.addEventListener('input', (event) => { event.target.dataset.edited = 'true'; });
  $('bridge-job-output')?.addEventListener('click', (event) => { const src = event.target.dataset.lightboxSrc; if (src) window.open(src, '_blank', 'noopener'); });

  // Save the API workflow and canvas projection in order. Reloading the workflow
  // before the canvas PUT can resurrect stale exposed fields from disk.
  async function saveWorkflowDefinition(silent = false) {
    if (!selectedWorkflow) return false;
    const apiJSON = workflowAPI(selectedWorkflow);
    if (!Object.keys(apiJSON).length) {
      notify('请先导入 ComfyUI API JSON', 'workflow-msg', 'error');
      return false;
    }
    const fields = canvasFields();
    const fieldError = validateCanvasFieldIDs(fields);
    if (fieldError) {
      notify(fieldError, 'workflow-msg', 'error');
      return false;
    }
    const config = {
      format: 'ainovel_workflow_config_v1',
      version: 1,
      fields,
      bindings: fields.map((f) => ({ key: f.id, node_id: f.node_id, path: `inputs.${f.input}`, type: f.value_type || 'string', required: false })),
      defaults: Object.fromEntries(fields.map((f) => [f.id, f.default])),
      outputs: selectedWorkflow.config?.outputs || [],
    };
    const body = {
      format: 'comfyui_api_v1',
      id: selectedWorkflow.id,
      name: selectedWorkflow.name || 'workflow',
      api_json: apiJSON,
      workflow: apiJSON,
      config,
      bindings: config.bindings,
      defaults: config.defaults,
      output: selectedWorkflow.output || {},
      enabled: true,
      instance_id: document.querySelector('input[name="default-instance"]:checked')?.value || '',
    };
    try {
      const saved = normalizeWorkflowResponse(await api(selectedWorkflow.id ? `/api/v2/comfyui/workflows/${encodeURIComponent(selectedWorkflow.id)}` : '/api/v2/comfyui/workflows/import', {
        method: selectedWorkflow.id ? 'PUT' : 'POST',
        body: JSON.stringify(body),
      }));
      const savedID = saved.id || selectedWorkflow.id;
      selectedWorkflow = { ...saved, config, bindings: config.bindings, defaults: config.defaults, workflow: apiJSON, api_json: apiJSON, id: savedID, prompter_template: selectedWorkflow.prompter_template || '', prompter_preset: selectedWorkflow.prompter_preset || '' };
      // This must happen before loadWorkflows/selectWorkflow reads the canvas.
      if (!await persistCanvas()) return false;
      await loadWorkflows(savedID);
      if (!silent) notify('工作流已保存', 'workflow-msg', 'success');
      return true;
    } catch (error) {
      notify(`工作流保存失败：${error.message}`, 'workflow-msg', 'error');
      return false;
    }
  }

  // Canvas fields with exposed:false are legacy tombstones and must not appear
  // in the run inspector or be written back to the canonical document.
  function canvasFields(workflow = selectedWorkflow) {
    const cfg = workflow?.config || workflow?.schema || {};
    const fields = Array.isArray(cfg) ? cfg : (cfg.fields || workflow?.fields || []);
    return fields.filter((f) => f && f.exposed !== false).map((f) => ({
      ...f,
      id: f.id || `${f.node_id || f.node || ''}::${f.input || f.key || ''}`,
      node_id: f.node_id || f.node || '',
      input: f.input || f.key || '',
      label: f.label || f.name || f.input || f.key || '',
      control: f.control || 'text',
      value_type: f.value_type || f.type || 'string',
      source: normalizeFieldSource(f),
      note: f.note || '',
    }));
  }

  async function persistCanvas() {
    if (!selectedWorkflow?.id) return true;
    applyPrompterEditorToWorkflow();
    const fields = canvasFields().map((f) => ({ id: f.id, node_id: f.node_id, input: f.input, name: f.label || f.name || f.input, control: f.control, value_type: f.value_type || 'string', default: f.default, min: f.min, max: f.max, step: f.step, options: f.options || [], required: !!f.required, random_enabled: !!f.random_enabled, exposed: true, source: normalizeFieldSource(f), note: f.note || '' }));
    const list = nodeList();
    const nodes = list.map((n) => ({ id: `node-${n.id}`, kind: 'workflow', source_node_id: String(n.id), class_type: n.class_type || '', label: n.class_type || '', x: canvasPositions[n.id]?.x || 40, y: canvasPositions[n.id]?.y || 40, width: 176, height: 70, exposed_field_ids: fields.filter((f) => String(f.node_id) === String(n.id)).map((f) => f.id) }));
    const edges = [];
    list.forEach((n) => Object.entries(n.inputs || {}).forEach(([key, value]) => { if (Array.isArray(value) && value.length) edges.push({ id: `edge-${value[0]}-${n.id}-${key}`, source: `node-${value[0]}`, source_handle: `output-${value[1]}`, target: `node-${n.id}`, target_handle: key, kind: 'workflow', label: key }); }));
    const payload = { format: 'ainovel_comfy_canvas_v1', version: 1, id: selectedWorkflow.id, workflow_id: selectedWorkflow.id, title: selectedWorkflow.name || '', viewport: { x: canvasView.x, y: canvasView.y, scale: canvasView.k, min_scale: .2, max_scale: 3 }, nodes, edges, fields, prompter_preset: selectedWorkflow.prompter_preset || '', prompter_template: selectedWorkflow.prompter_template || '', mini_test_cards: [] };
    localStorage.setItem(canvasKey(selectedWorkflow.id), JSON.stringify(payload));
    try {
      await api(`/api/v2/comfyui/workflows/${encodeURIComponent(selectedWorkflow.id)}/canvas`, { method: 'PUT', body: JSON.stringify(payload) });
      return true;
    } catch (error) {
      notify(`画布保存失败：${error.message}`, 'workflow-msg', 'error');
      return false;
    }
  }

  qs('[data-action="save-comfyui"]')?.addEventListener('click', saveComfyUI);
  qs('[data-action="test-connection"]')?.addEventListener('click', testConnection);
  qs('[data-action="validate-workflow"]')?.addEventListener('click', validateWorkflow);
  if ($('dynamic-fields')) $('dynamic-fields').oninput = (event) => { if (event.target.dataset.fieldKey) fieldValues[event.target.dataset.fieldKey] = event.target.type === 'checkbox' ? event.target.checked : event.target.value; };
  window.ComfyUI = { load: loadComfyUI, onJobEvent(job) { renderJob(job); } };
})();
