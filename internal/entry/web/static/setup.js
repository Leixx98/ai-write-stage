const form = document.querySelector("#setup-form");
const presetInput = document.querySelector("#preset");
const providerRow = document.querySelector("#provider-row");
const providerInput = document.querySelector("#provider");
const typeRow = document.querySelector("#type-row");
const typeInput = document.querySelector("#type");
const keyInput = document.querySelector("#api-key");
const keyHint = document.querySelector("#key-hint");
const baseURLInput = document.querySelector("#base-url");
const modelInput = document.querySelector("#model");
const apiInput = document.querySelector("#api");
const testButton = document.querySelector("#test");
const saveButton = document.querySelector("#save");
const toast = document.querySelector("#toast");
let presets = [];
function notify(message, kind = "") {
  if (!toast) return;
  const text = String(message || "");
  if (!text) {
    toast.textContent = "";
    toast.className = "toast";
    return;
  }
  toast.textContent = text;
  toast.className = `toast toast-pop ${kind}`.trim();
  clearTimeout(toast._timer);
  void toast.offsetWidth;
  toast.classList.add("toast-pop");
  toast._timer = setTimeout(() => {
    toast.textContent = "";
    toast.className = "toast";
  }, 4500);
}

function selectedPreset() {
  return presets.find((item) => item.name === presetInput.value);
}

function refreshPreset() {
  const preset = selectedPreset();
  if (!preset) return;
  providerRow.hidden = !preset.need_type;
  typeRow.hidden = !preset.need_type;
  providerInput.required = Boolean(preset.need_type);
  typeInput.required = Boolean(preset.need_type);
  keyInput.required = !preset.api_key_optional;
  keyHint.textContent = preset.api_key_optional ? "可留空" : "必填";
  baseURLInput.value = preset.base_url || "";
}

async function loadPresets() {
  const response = await fetch("/api/setup/providers");
  const body = await response.json();
  if (!response.ok || body.code !== 0) throw new Error(body.msg || "无法读取服务商预设");
  presets = body.data;
  presetInput.replaceChildren(...presets.map((preset) => {
    const option = document.createElement("option");
    option.value = preset.name;
    option.textContent = preset.label;
    return option;
  }));
  refreshPreset();
}

async function waitForWorkbench() {
  for (;;) {
    try {
      const response = await fetch("/api/v2/state", {cache:"no-store"});
      if (response.ok) {
        location.replace("/");
        return;
      }
    } catch (_) {}
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
}

function setupPayload() {
  const preset = selectedPreset();
  return {preset:presetInput.value, provider:preset?.need_type ? providerInput.value : "", type:preset?.need_type ? typeInput.value : "", api:apiInput.value, api_key:keyInput.value, base_url:baseURLInput.value, model:modelInput.value};
}

presetInput.addEventListener("change", refreshPreset);
testButton.addEventListener("click", async () => {
  testButton.disabled = true;
  notify("正在测试连接...");
  try {
    const response = await fetch("/api/setup/test", {method:"POST", headers:{"Content-Type":"application/json"}, body:JSON.stringify(setupPayload())});
    const body = await response.json();
    if (!response.ok || body.code !== 0) throw new Error(body.msg || "连接失败");
    notify("连接测试成功。", "success");
  } catch (error) {
    notify(error.message, "error");
  } finally {
    testButton.disabled = false;
  }
});
form.addEventListener("submit", async (event) => {
  event.preventDefault();
  saveButton.disabled = true;
  notify("正在保存...");
  try {
    const response = await fetch("/api/setup", {method:"POST", headers:{"Content-Type":"application/json"}, body:JSON.stringify(setupPayload())});
    const body = await response.json();
    if (!response.ok || body.code !== 0) throw new Error(body.msg || "保存失败");
    notify("配置已保存，正在启动工作台...", "success");
    await waitForWorkbench();
  } catch (error) {
    saveButton.disabled = false;
    notify(error.message, "error");
  }
});

loadPresets().catch((error) => {
  saveButton.disabled = true;
  notify(error.message, "error");
});
