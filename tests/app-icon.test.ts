import { expect, test } from "bun:test";
import { createElement, type ReactElement, type Ref } from "react";

const { hasAppIcon, loadIcon, peekIcon, resetIconsForTest } = await import("../src/react/icons/cache");

test("hasAppIcon: only absolute .app bundle paths", () => {
  expect(hasAppIcon("/Applications/Safari.app")).toBe(true);
  expect(hasAppIcon("/Applications/Safari.APP/")).toBe(true);
  expect(hasAppIcon("")).toBe(false);
  expect(hasAppIcon("Safari.app")).toBe(false);
  expect(hasAppIcon("/usr/local/bin/node")).toBe(false);
  expect(hasAppIcon("/Applications/Safari.app/Contents/MacOS/Safari")).toBe(false);
  expect(hasAppIcon("C:\\Program Files\\Acme\\acme.exe")).toBe(false);
});

test("loadIcon asks once per path, shares the answer and remembers it", async () => {
  const asked: string[] = [];
  resetIconsForTest(async (p) => { asked.push(p); return "data:image/png;base64,QQ=="; });
  expect(peekIcon("/A.app")).toBeUndefined();
  const [a, b] = await Promise.all([loadIcon("/A.app"), loadIcon("/A.app")]);
  expect(a).toBe("data:image/png;base64,QQ==");
  expect(b).toBe(a);
  expect(await loadIcon("/A.app")).toBe(a);
  expect(peekIcon("/A.app")).toBe(a);
  expect(asked).toEqual(["/A.app"]);
});

test("a failed or empty answer is remembered as no icon", async () => {
  let calls = 0;
  resetIconsForTest(async (p) => { calls++; if (p === "/Bad.app") throw new Error("bridge"); return ""; });
  expect(await loadIcon("/Bad.app")).toBe("");
  expect(await loadIcon("/Bad.app")).toBe("");
  expect(await loadIcon("/None.app")).toBe("");
  expect(peekIcon("/None.app")).toBe("");
  expect(calls).toBe(2);
});

test("AppIcon shows the letter until the icon is known, then the image in the same box", async () => {
  const { renderToStaticMarkup } = await import("react-dom/server");
  const { AppIcon } = await import("../src/react/icons/AppIcon");
  resetIconsForTest(async () => "data:image/png;base64,AAAA");
  const letter = createElement("div", { className: "st-ico", style: { background: "#f00" } }, "S") as ReactElement<{ ref?: Ref<HTMLElement> }>;
  const render = (path: string) => renderToStaticMarkup(createElement(AppIcon, { path, box: "st-ico", children: letter }));
  const before = render("/Applications/Safari.app");
  expect(before).toBe('<div class="st-ico" style="background:#f00">S</div>');
  await loadIcon("/Applications/Safari.app");
  const after = render("/Applications/Safari.app");
  expect(after).toContain('<img class="st-ico cz-app-img" src="data:image/png;base64,AAAA" alt=""');
  expect(after).toContain('aria-hidden="true"');
  expect(render("")).toBe(before);
});
