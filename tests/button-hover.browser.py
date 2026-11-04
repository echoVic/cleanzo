"""Rendered Button states; requires Vite at http://127.0.0.1:5194."""
import json
import atexit
import subprocess
import sys
import tempfile
from pathlib import Path

SESSION = "cleanzo-button-hover"


def browser(*args):
    return subprocess.check_output(
        ["agent-browser", "--session", SESSION, *args], text=True
    ).strip()


def evaluate(script):
    return json.loads(json.loads(browser("eval", f"JSON.stringify({script})")))


# Render the production components so Tailwind's actual cascade is tested.
markup = subprocess.check_output(["bun", "-e", """
import "./tests/setup-dom";
import { createElement as h } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { Button } from "./src/ui/components/button";
import { AppRow } from "./src/react/apps/AppRow";
import { AppRowExpanded } from "./src/react/apps/AppRowExpanded";
import { loadIcon, resetIconsForTest } from "./src/react/icons/cache";
const app = { id: "/Applications/WeChat.app", path: "/Applications/WeChat.app",
  name: "WeChat", version: "4.1.15", running: true, size: 1.4 * 1024 ** 3,
  lastOpened: 1793721600 };
const noop = () => {};
resetIconsForTest(async () => "http://127.0.0.1:5194/favicon.svg");
await loadIcon(app.path);
const update = { state: "update", source: "brew", action: "brew",
  current: "4.1.15", latest: "4.2.0.15" };
console.log(renderToStaticMarkup(h("main", { className: "cz-react", style: { padding: 24 } },
  h("div", { id: "app-card" }, h(AppRow, { app, update, open: true,
    growthTag: null, busy: null, brewBusy: false, onToggle: noop, onUpgrade: noop },
    h(AppRowExpanded, { app, update, checking: false,
      usage: { app: app.size, data: 24.2 * 1024 ** 3, cache: 0 }, usageError: null,
      week: null, month: null, busy: null, brewBusy: false, stuck: false,
      upgradeFail: null, onUpgrade: noop, onCopy: noop, onQuit: noop,
      onForceQuit: noop, onUninstall: noop, onCleanCache: noop }))),
  ...["default", "destructive", "ghost", "link"].flatMap(variant =>
    [false, true].map(disabled => h(Button, { variant, size: "sm", disabled,
      id: `${variant}-${disabled ? "disabled" : "enabled"}` }, `${variant} ${disabled}`)))
)));
"""], text=True)

atexit.register(lambda: browser("close"))
with tempfile.TemporaryDirectory(prefix="cleanzo-button-hover-") as directory:
    page = Path(directory) / "index.html"
    page.write_text("""<!doctype html><meta charset="utf-8">
<link rel="stylesheet" href="http://127.0.0.1:5194/src/style.css?direct">
<link rel="stylesheet" href="http://127.0.0.1:5194/src/ui/index.css?direct">
""" + markup)
    browser("open", page.as_uri())
    browser("set", "viewport", "1000", "800")
    browser("wait", "#default-disabled")
    for theme in ("light", "dark"):
        browser("set", "media", theme)
        browser("hover", "body")
        icon_style = """(() => {
          const el = document.querySelector('#app-card .cz-app-img');
          const c = getComputedStyle(el);
          return { background: c.backgroundColor, filter: c.filter,
            opacity: c.opacity, transform: c.transform, borderRadius: c.borderRadius,
            width: c.width, height: c.height };
        })()"""
        icon_before = evaluate(icon_style)
        browser("hover", "#app-card .cz-app-img")
        browser("wait", "200")
        assert evaluate(icon_style) == icon_before, f"{theme}: icon style changed on hover"
        for selector in (
            "#app-card section:first-child button", "#default-disabled", "#destructive-disabled",
            "#ghost-disabled", "#link-disabled",
        ):
            browser("hover", "body")
            browser("wait", "200")
            read_style = f"""(() => {{
              const c = getComputedStyle(document.querySelector({json.dumps(selector)}));
              return {{ background: c.backgroundColor, color: c.color,
                opacity: c.opacity, scale: c.scale, transform: c.transform,
                decoration: c.textDecorationLine }};
            }})()"""
            before = evaluate(read_style)
            browser("hover", selector)
            browser("wait", "200")
            after = evaluate(read_style)
            assert after == before, f"{theme} {selector}: disabled hover changed {before} -> {after}"
        for variant in ("default", "destructive", "ghost", "link"):
            selector = f"#{variant}-enabled"
            browser("hover", "body")
            browser("wait", "200")
            read_style = f"""(() => {{
              const c = getComputedStyle(document.querySelector({json.dumps(selector)}));
              return {{ background: c.backgroundColor, color: c.color, decoration: c.textDecorationLine }};
            }})()"""
            before = evaluate(read_style)
            browser("hover", selector)
            browser("wait", "200")
            after = evaluate(read_style)
            assert after != before, f"{theme} {variant}: enabled hover feedback missing"
        if len(sys.argv) > 1:
            browser("hover", "#app-card section:first-child button")
            browser("wait", "200")
            browser("screenshot", str(Path(sys.argv[1]) / f"button-hover-{theme}.png"))
    print("PASS: app icon and disabled expanded update retain their styles; four Button variants preserve disabled states and enabled hover in light/dark")
