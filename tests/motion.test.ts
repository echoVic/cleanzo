import { expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { DUR, dur, motionLevel, setMotionLevelForTest } from "../src/ui/motion/motion";
import { collapseOut, mergePresence, popOut } from "../src/ui/motion/presence";
import { Collapse } from "../src/ui/motion/Collapse";
import { NumberTicker } from "../src/ui/motion/NumberTicker";
import { containRect } from "../src/ui/motion/fly";

test("without Web Animations every duration is zero", () => {
  expect(motionLevel()).toBe("none");
  for (const key of Object.keys(DUR) as (keyof typeof DUR)[]) expect(dur(key)).toBe(0);
});

test("reduced motion keeps only fades", () => {
  try {
    setMotionLevelForTest("reduced");
    expect(dur("fade")).toBe(120);
    for (const key of ["press", "state", "layout", "move", "tick", "spark"] as const) expect(dur(key)).toBe(0);
    setMotionLevelForTest("full");
    expect(dur("layout")).toBe(240);
    expect(dur("move")).toBe(360);
  } finally {
    setMotionLevelForTest(undefined);
  }
});

test("leaving items go back to their old positions and a returning key is not duplicated", () => {
  const key = (s: string) => s;
  const leaving = new Map([["b", { item: "b", index: 1 }], ["e", { item: "e", index: 9 }]]);
  expect(mergePresence(["a", "c", "d"], key, leaving).map((p) => p.key + (p.leaving ? "-" : ""))).toEqual(["a", "b-", "c", "d", "e-"]);
  expect(mergePresence(["a", "b"], key, leaving).map((p) => p.key + (p.leaving ? "-" : ""))).toEqual(["a", "b", "e-"]);
});

test("Collapse renders open content statically and nothing when closed", () => {
  expect(renderToStaticMarkup(createElement(Collapse, { open: true, children: "child" })))
    .toBe('<div class="cz-collapse" data-state="open"><div class="cz-collapse-in">child</div></div>');
  expect(renderToStaticMarkup(createElement(Collapse, { open: false, children: "child" }))).toBe("");
});

test("NumberTicker renders the final text, or the starting text when it counts up", () => {
  const fmt = (n: number) => n.toFixed(1);
  expect(renderToStaticMarkup(createElement(NumberTicker, { value: 27.2, format: fmt, as: "b" }))).toBe("<b>27.2</b>");
  expect(renderToStaticMarkup(createElement(NumberTicker, { value: 5, from: 0, format: fmt }))).toBe("<span>0.0</span>");
});

test("containRect letterboxes a 4:3 photo in a wide stage", () => {
  const r = containRect({ left: 0, top: 0, width: 800, height: 400 }, 4032, 3024);
  expect(Math.round(r.width)).toBe(533);
  expect(r.height).toBe(400);
  expect(Math.round(r.left)).toBe(133);
  expect(r.top).toBe(0);
});

test("Collapse appear stays open in static rendering without Web Animations", () => {
  expect(motionLevel()).toBe("none");
  expect(renderToStaticMarkup(createElement(Collapse, { open: true, appear: true, children: "child" })))
    .toBe('<div class="cz-collapse" data-state="open"><div class="cz-collapse-in">child</div></div>');
});

test("a row that comes back while leaving gets its leave animation undone", () => {
  const cancelled: string[] = [];
  const el = {
    style: {} as Record<string, string>,
    parentElement: null,
    nextElementSibling: null,
    getBoundingClientRect: () => ({ height: 40 }),
    animate(frames: Keyframe[]) {
      const name = "height" in frames[0]! ? "size" : "fade";
      return { finished: new Promise<void>(() => {}), cancel: () => cancelled.push(name) };
    },
  };
  const saved = (globalThis as { getComputedStyle?: unknown }).getComputedStyle;
  (globalThis as { getComputedStyle?: unknown }).getComputedStyle = () => ({ paddingTop: "8px", paddingBottom: "8px", borderTopWidth: "1px", borderBottomWidth: "1px", marginTop: "0px", marginBottom: "0px", rowGap: "0px" });
  try {
    setMotionLevelForTest("full");
    for (const leave of [collapseOut, popOut]) {
      cancelled.length = 0;
      const undo = leave(el as unknown as HTMLElement);
      expect(el.style.pointerEvents).toBe("none");
      undo(); // the same row is listed again before the animation ended
      expect(cancelled.length).toBeGreaterThan(0);
      expect(el.style.pointerEvents).toBe("");
      expect(el.style.overflow ?? "").toBe("");
    }
  } finally {
    setMotionLevelForTest(undefined);
    (globalThis as { getComputedStyle?: unknown }).getComputedStyle = saved;
  }
});
