// 仅 CLEANZO_SMOKE_DIR 开启时由 Go 注入，先于应用脚本执行。
(() => {
  const errors = [];
  const text = value => {
    try { return value instanceof Error ? value.stack || value.message : typeof value === "string" ? value : JSON.stringify(value) ?? String(value); }
    catch { return String(value); }
  };
  const original = console.error;
  console.error = (...args) => {
    errors.push(`console.error: ${args.map(text).join(" ")}`);
    original.apply(console, args);
  };
  window.addEventListener("error", event => errors.push(`error: ${event.message || event.target?.src || "resource load failed"}`), true);
  window.addEventListener("unhandledrejection", event => errors.push(`unhandledrejection: ${text(event.reason)}`));
  const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
  const waitFor = async predicate => {
    const deadline = Date.now() + 60000;
    while (!predicate()) {
      if (Date.now() >= deadline) throw Error("smoke: DOM/scan did not settle");
      await delay(100);
    }
  };
  const smoke = window.__cleanzoSmoke = {
    errors, ready: false,
    async waitReady() {
      await waitFor(() => smoke.ready && document.readyState === "complete");
      return true;
    },
    async waitTheme(dark) {
      await waitFor(() => window.matchMedia("(prefers-color-scheme: dark)").matches === dark);
      return true;
    },
    async visit(view) {
      // 直接点击现有侧栏；未安装 AI 工具时入口可能隐藏，仍验证其空状态。
      const nav = document.querySelector(`.nav-item[data-view="${view}"]`);
      if (!nav) throw Error(`smoke: missing navigation ${view}`);
      nav.click();
      const root = document.querySelector(`#view-${view}`);
      await waitFor(() => root && !root.classList.contains("hidden") && root.querySelector(".cz-react"));
      await delay(1000);
      if (view === "clean") {
        const scan = root.querySelector(".ha-cta") || root.querySelector('[data-action="scan"]');
        if (!scan || scan.disabled) throw Error("smoke: scan button unavailable");
        scan.click(); // 只扫描，绝不调用清理操作。
        await waitFor(() => !root.querySelector(".ha-cta"));
        await delay(100);
        await waitFor(() => !nav.classList.contains("busy"));
        const failure = root.querySelector(".empty.err");
        if (failure) throw Error(`smoke: clean scan failed: ${failure.textContent}`);
      }
      await delay(1000);
      return true;
    },
  };
  window.addEventListener("cleanzo:ready", () => { smoke.ready = true; });
})();
