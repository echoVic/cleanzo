"""Native select appearance follows the app's emulated theme (Vite :5194).

WebView2 overrides prefers-color-scheme while Windows can stay light. An
ambiguous light/dark color-scheme leaves native popups using the OS palette.
This checks the computed native-control contract; Windows popup pixels still
need a separate native retest.
"""
import atexit
import json
import subprocess

SESSION = "cleanzo-native-select"


def browser(*args):
    return subprocess.check_output(
        ["agent-browser", "--session", SESSION, *args], text=True
    ).strip()


def evaluate(script):
    return json.loads(json.loads(browser("eval", f"JSON.stringify({script})")))


atexit.register(lambda: browser("close"))
browser("open", "http://127.0.0.1:5194/?mock")
browser("wait", '.nav-item[data-view="settings"]')
browser("click", '.nav-item[data-view="settings"]')
browser("wait", "#lang-select")
for theme in ("dark", "light", "dark"):
    browser("set", "media", theme)
    controls = evaluate("""Array.from(document.querySelectorAll('#view-settings select'))
      .map(el => ({ id: el.id, scheme: getComputedStyle(el).colorScheme,
        value: el.value, options: el.options.length }))""")
    assert len(controls) >= 2, "appearance and language selects missing"
    for control in controls:
        assert control["scheme"] == theme, f"{theme}: ambiguous native palette: {control}"
    browser("select", "#lang-select", "en")
    assert evaluate("document.querySelector('#lang-select').value") == "en"
    browser("select", "#lang-select", "zh")
    assert evaluate("document.querySelector('#lang-select').value") == "zh"
browser("click", '.nav-item[data-view="apps"]')
browser("wait", "#view-apps .select")
for theme in ("light", "dark"):
    browser("set", "media", theme)
    scheme = evaluate("getComputedStyle(document.querySelector('#view-apps .select')).colorScheme")
    assert scheme == theme, f"apps sort in {theme}: {scheme}"
print("PASS: settings and apps native controls use a single light/dark palette; language selection still works")
