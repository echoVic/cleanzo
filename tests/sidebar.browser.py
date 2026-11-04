"""Rendered shell regression checks; run with Vite serving http://127.0.0.1:5194/?mock."""
import json
import subprocess

SESSION = "cleanzo-design-final"


def browser(*args):
    return subprocess.check_output(
        ["agent-browser", "--session", SESSION, *args], text=True
    ).strip()


def evaluate(script):
    return json.loads(json.loads(browser("eval", f"JSON.stringify({script})")))


browser("set", "viewport", "1200", "800")
browser("open", "http://127.0.0.1:5194/?mock")
browser("wait", "--text", "Claude Code")
footer = evaluate("""(() => {
  const settings = document.querySelector('[data-view="settings"]').getBoundingClientRect();
  const version = document.querySelector('#app-version').getBoundingClientRect();
  return { gap: version.top - settings.bottom, bottom: innerHeight - version.bottom };
})()""")
assert 0 <= footer["gap"] <= 16, f"Settings must sit next to version: {footer}"
assert 0 <= footer["bottom"] <= 24, f"Footer must sit at window bottom: {footer}"

browser("set", "viewport", "900", "400")
browser("click", '[data-view="settings"]')
browser("wait", "350")
small = evaluate("""(() => {
  const settings = document.querySelector('[data-view="settings"]').getBoundingClientRect();
  const version = document.querySelector('#app-version').getBoundingClientRect();
  const scroll = document.querySelector('.nav-scroll');
  const active = document.querySelector('[aria-current="page"]');
  const pills = [...document.querySelectorAll('.nav-pill.placed')];
  return { settings: settings.bottom, version: version.bottom, height: innerHeight,
    scrollable: scroll.scrollHeight > scroll.clientHeight,
    pillCount: pills.length, delta: pills[0]?.getBoundingClientRect().top - active.getBoundingClientRect().top };
})()""")
assert small["settings"] <= small["height"] and small["version"] <= small["height"], small
assert small["scrollable"], small
assert small["pillCount"] == 1 and abs(small["delta"]) < 1, small

# Keyboard focus must scroll a main item into view without displacing the footer.
browser("focus", '[data-view="status"]')
browser("press", "Shift+Tab")
browser("focus", '[data-view="skills"]')
browser("press", "Enter")
browser("wait", "350")
focused = evaluate("""(() => {
  const active = document.querySelector('[aria-current="page"]');
  const rect = active.getBoundingClientRect();
  const scroll = document.querySelector('.nav-scroll').getBoundingClientRect();
  const pill = document.querySelector('.nav-pill.placed').getBoundingClientRect();
  return { view: active.dataset.view, visible: rect.top >= scroll.top && rect.bottom <= scroll.bottom,
    delta: pill.top - rect.top };
})()""")
assert focused["view"] == "skills" and focused["visible"] and abs(focused["delta"]) < 1, focused

# Installed tools may disappear while a later item is selected.
browser("eval", """document.querySelectorAll('[data-tool]').forEach(el => el.classList.add('hidden'))""")
browser("wait", "100")
dynamic = evaluate("""(() => {
  const active = document.querySelector('[aria-current="page"]').getBoundingClientRect();
  const pill = document.querySelector('.nav-pill.placed').getBoundingClientRect();
  const heading = document.querySelector('#nav-ai-section');
  return { delta: pill.top - active.top, heightDelta: pill.height - active.height,
    headingVisible: heading.getBoundingClientRect().height > 0 };
})()""")
assert abs(dynamic["delta"]) < 1 and abs(dynamic["heightDelta"]) < 1 and dynamic["headingVisible"], dynamic
# No highlight should remain when the selected tool group is entirely hidden.
browser("eval", """document.querySelector('[data-view="skills"]').classList.add('hidden')""")
browser("wait", "100")
empty = evaluate("""(() => ({
  pills: document.querySelectorAll('.nav-pill.placed').length,
  groupHidden: getComputedStyle(document.querySelector('.nav-ai-group')).display === 'none'
}))()""")
assert empty["pills"] == 0 and empty["groupHidden"], empty
print("PASS: pinned footer, short-window scrolling, keyboard navigation, dynamic selection alignment")
