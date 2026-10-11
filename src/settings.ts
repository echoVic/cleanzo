import { Cleaner, type Settings } from "./mygo";
import { $, baseName, displayPath, errText, esc } from "./shared";
import { ruleName, t } from "./i18n";
import { markJunkStale } from "./junk";

const groups = ["system", "apps", "browsers", "updates", "dev", "review"] as const;
const view = $("#view-settings");
const groupList = $("#settings-groups");
const ignoredList = $("#settings-ignored");
const customList = $("#settings-custom");
const addBtn = $<HTMLButtonElement>("#settings-add");
const status = $("#settings-status");
let settings: Settings | null = null;
let loading = false;
let saving = false;
let editingLocked = false;
let queued = 0;
let saveQueue = Promise.resolve();
let picking = false;
let loadSeq = 0;
let permissionSeq = 0;
let reloadAfterSave = false;
let access: boolean | null = null;
let readError: unknown = null;
let permissionError: unknown = null;
const errors = new Map<string, unknown>();
const appNames = new Map<string, string>();

/** 空数组在 Go 的绑定中可能为 null。 */
function normalized(s: Settings): Settings {
  return { disabledGroups: s.disabledGroups ?? [], ignored: s.ignored ?? [], custom: s.custom ?? [] };
}

function inlineError(key: string): string {
  return errors.has(key) ? `<p class="err-inline" role="alert">${esc(errText(errors.get(key)))}</p>` : "";
}

function ignoredName(id: string): string {
  if (id.startsWith("app:")) return appNames.get(id.slice(4).toLowerCase()) ?? id;
  return ruleName(id);
}

/** 保存期间锁住编辑，避免两份完整配置互相覆盖。 */
function lockControls() {
  const busy = loading || editingLocked || picking || !settings;
  view.querySelectorAll<HTMLInputElement | HTMLButtonElement>("#settings-groups input, #settings-ignored button, #settings-custom input, #settings-custom button, #settings-add")
    .forEach((el) => { el.disabled = busy; });
}

function renderPermission() {
  const el = $("#settings-fda");
  el.classList.toggle("granted", access === true);
  el.innerHTML = `<span class="permission-dot" aria-hidden="true"></span>${esc(t(access === null ? permissionError ? "settings.permissionUnknown" : "settings.checking" : access ? "settings.granted" : "settings.notGranted"))}`;
  const error = $("#settings-permission-error");
  error.textContent = permissionError ? errText(permissionError) : "";
  error.classList.toggle("hidden", !permissionError);
}

function renderSettings() {
  status.textContent = readError ? t("settings.readFail", { msg: errText(readError) }) : loading ? t("settings.loading") : "";
  status.classList.toggle("hidden", !readError && !loading);
  status.classList.toggle("err", !!readError);
  groupList.innerHTML = groups.map((g) => `<div>
    <label class="check"><input type="checkbox" data-group="${g}" ${!settings?.disabledGroups.includes(g) ? "checked" : ""}>${esc(g === "review" ? t("settings.reviewItems") : t(`junk.group.${g}`))}</label>
    ${inlineError(`group:${g}`)}</div>`).join("");
  renderIgnored();
  customList.innerHTML = settings?.custom.map((c, i) => `<div class="settings-item">
    <div class="settings-custom-line">
      <div class="settings-folder-info">
        <input data-index="${i}" data-field="name" value="${esc(c.name)}" aria-label="${esc(t("settings.name"))}">
        <span class="settings-folder-path muted small">${esc(displayPath(c.path))}</span>
      </div>
      <label class="check"><input type="checkbox" data-index="${i}" data-field="enabled" ${c.enabled ? "checked" : ""}>${esc(t("settings.enabled"))}</label>
      <button class="btn ghost small" type="button" data-remove="${i}">${esc(t("settings.remove"))}</button>
    </div><div class="settings-row-error">${inlineError(`custom:${c.path}`)}</div></div>`).join("") ?? "";
  const addError = $("#settings-add-error");
  addError.textContent = errors.has("add") ? errText(errors.get("add")) : "";
  addError.classList.toggle("hidden", !errors.has("add"));
  lockControls();
  renderPermission();
}

/** 串行保存最新配置；名称失焦时保留下一次点击的目标。 */
function save(update: (s: Settings) => Settings, key: string, nameInput?: HTMLInputElement): Promise<void> {
  if (!settings || loading) return Promise.resolve();
  const typed = nameInput?.value;
  queued++;
  saving = true;
  if (!nameInput) editingLocked = true;
  errors.delete(key);
  lockControls();
  const job = saveQueue.then(async () => {
    const before = settings!;
    const next = update(before);
    let saved = false;
    errors.delete(key);
    try {
      await Cleaner.saveSettings(next);
      settings = next;
      saved = true;
      markJunkStale();
    } catch (e) { errors.set(key, e); }
    finally {
      queued--;
      saving = queued > 0;
      if (!saving) editingLocked = false;
      if (nameInput) {
        // 不替换 DOM：失焦之后的 click/change 仍需送到原控件。
        if (nameInput.value === typed) {
          const folders = saved ? next.custom : before.custom;
          nameInput.value = folders[Number(nameInput.dataset.index)]?.name ?? nameInput.value;
        }
        const rowError = nameInput.closest(".settings-item")?.querySelector<HTMLElement>(".settings-row-error");
        if (rowError) rowError.innerHTML = inlineError(key);
        lockControls();
      } else renderSettings();
      if (!saving && reloadAfterSave && !picking) { reloadAfterSave = false; showSettings(); }
    }
  });
  saveQueue = job;
  return job;
}

async function checkPermission() {
  const seq = ++permissionSeq;
  try {
    const granted = await Cleaner.fullDiskAccess();
    if (seq !== permissionSeq) return;
    access = granted;
    permissionError = null;
  } catch (e) {
    if (seq !== permissionSeq) return;
    access = null;
    permissionError = e;
  }
  renderPermission();
}

/** 每次进入重新读取，包括在垃圾页新增的忽略项。 */
export function showSettings(): void {
  if (saving || picking) { reloadAfterSave = true; return; }
  const seq = ++loadSeq;
  loading = true;
  readError = null;
  errors.clear();
  renderSettings();
  void checkPermission();
  void Cleaner.info().then((info) => {
    if (seq === loadSeq) $("#settings-version").textContent = info.version;
  }).catch(() => {});
  void Cleaner.getSettings().then(async (s) => {
    if (seq !== loadSeq) return;
    settings = normalized(s);
    loading = false;
    renderSettings();
    // 名称查不到时保留 ID，不阻塞设置编辑。
    if (settings.ignored.some((id) => id.startsWith("app:"))) {
      try {
        const apps = await Cleaner.listApps();
        if (seq !== loadSeq) return;
        appNames.clear();
        apps.forEach((app) => appNames.set(app.bundleId.toLowerCase(), app.name));
        // 只更新忽略列表，保留正在编辑的目录名称。
        renderIgnored();
      } catch { /* 保留稳定 ID */ }
    }
  }).catch((e) => {
    if (seq !== loadSeq) return;
    settings = null;
    loading = false;
    readError = e;
    renderSettings();
  });
}

function renderIgnored() {
  ignoredList.innerHTML = settings?.ignored.length ? settings.ignored.map((id) => `<div class="settings-item">
    <div class="settings-item-line"><span>${esc(ignoredName(id))}</span><button class="btn ghost small" type="button" data-restore="${esc(id)}">${esc(t("settings.restore"))}</button></div>
    ${inlineError(`ignored:${id}`)}</div>`).join("") : `<p class="muted small">${esc(t("settings.ignoredEmpty"))}</p>`;
  lockControls();
}

export function refreshSettingsI18n(): void { renderSettings(); }

groupList.addEventListener("change", (e) => {
  const cb = e.target as HTMLInputElement;
  const group = cb.dataset.group;
  if (!group || !settings || cb.disabled) return;
  const checked = cb.checked;
  void save((s) => {
    const disabledGroups = s.disabledGroups.filter((g) => g !== group);
    if (!checked) disabledGroups.push(group);
    return { ...s, disabledGroups };
  }, `group:${group}`);
});
ignoredList.addEventListener("click", (e) => {
  const btn = (e.target as HTMLElement).closest<HTMLButtonElement>("button[data-restore]");
  const id = btn?.dataset.restore;
  if (!id || !settings || btn.disabled) return;
  void save((s) => ({ ...s, ignored: s.ignored.filter((v) => v !== id) }), `ignored:${id}`);
});
customList.addEventListener("change", (e) => {
  const cb = e.target as HTMLInputElement;
  const i = Number(cb.dataset.index);
  const folder = settings?.custom[i];
  if (cb.dataset.field !== "enabled" || !folder || !settings || cb.disabled) return;
  const enabled = cb.checked;
  void save((s) => ({ ...s, custom: s.custom.map((c) => c.path === folder.path ? { ...c, enabled } : c) }), `custom:${folder.path}`);
});
customList.addEventListener("blur", (e) => {
  const input = e.target as HTMLInputElement;
  const i = Number(input.dataset.index);
  const folder = settings?.custom[i];
  if (input.dataset.field !== "name" || !folder || !settings || input.disabled) return;
  const name = input.value.trim() || baseName(folder.path);
  if (name === folder.name && !saving) { input.value = name; return; }
  void save((s) => ({ ...s, custom: s.custom.map((c) => c.path === folder.path ? { ...c, name } : c) }), `custom:${folder.path}`, input);
}, true);
customList.addEventListener("keydown", (e) => {
  const input = e.target as HTMLInputElement;
  if (e.key === "Enter" && input.dataset.field === "name") { e.preventDefault(); input.blur(); }
});
customList.addEventListener("click", (e) => {
  const btn = (e.target as HTMLElement).closest<HTMLButtonElement>("button[data-remove]");
  if (!btn || btn.disabled || !settings) return;
  const i = Number(btn.dataset.remove);
  const folder = settings.custom[i];
  if (!folder) return;
  void save((s) => ({ ...s, custom: s.custom.filter((c) => c.path !== folder.path) }), `custom:${folder.path}`);
});
addBtn.addEventListener("click", async () => {
  if (!settings || loading || editingLocked || picking) return;
  picking = true;
  errors.delete("add");
  lockControls();
  try {
    const path = await Cleaner.pickFolder();
    if (path) await save((s) => ({ ...s, custom: [...s.custom, { name: baseName(path), path, enabled: true }] }), "add");
  } catch (e) { errors.set("add", e); }
  finally {
    picking = false;
    renderSettings();
    if (reloadAfterSave) { reloadAfterSave = false; showSettings(); }
  }
});
$("#settings-open-permission").addEventListener("click", () => {
  void Cleaner.openFullDiskAccessSettings().catch((e) => { permissionError = e; renderPermission(); });
});
window.addEventListener("focus", () => {
  if (!view.classList.contains("hidden")) void checkPermission();
});
