import { expect, test } from "bun:test";
import { readFileSync, readdirSync } from "node:fs";
import { cn } from "../src/ui/cn";
import { buttonVariants } from "../src/ui/components/button";

/**
 * CSS 防回归：
 * a) src/ui/index.css 不允许 `--x: var(--x)` 自引用（含 fallback 形式）——循环引用会让
 *    变量计算为空，导致描边退化成 currentColor 黑线、次要文字/卡片底色丢失。
 * b) src/style.css 与 src/ui/index.css 不允许「无 @layer 的裸元素选择器」给 button/input/
 *    select/textarea/a/p/h1-h6 设置 color 或 font/font-weight：无 layer 规则会压过 Tailwind
 *    工具层，使 React 组件上的字号字重字色工具类失效（字色 currentColor、字重 400）。
 *    原生组件类（.btn、.seg button 等）是类作用域规则，不受影响。
 * c) 读到的变量都有定义：未定义的变量只剩 fallback，换主题时不跟着变。
 * d) 同一规则里 gap 简写不写在 row-gap / column-gap 之后，否则会把它们改回去。
 * e) 写死时长的 transform 过渡（不走 --t-* 令牌）在减弱动态效果时要逐个关掉。
 * f) React 的 ghost 按钮和 .btn.ghost 用同一个描边令牌。
 */

const indexCss = readFileSync(new URL("../src/ui/index.css", import.meta.url), "utf8");
const styleCss = readFileSync(new URL("../src/style.css", import.meta.url), "utf8");

/** 删除全部 @layer { ... } 块（brace 感知，支持嵌套）。 */
function stripLayerBlocks(css: string): string {
  let out = "";
  let i = 0;
  while (i < css.length) {
    const at = css.indexOf("@layer", i);
    if (at === -1) {
      out += css.slice(i);
      break;
    }
    out += css.slice(i, at);
    // 找到该 @layer 规则的起始 `{`
    let j = css.indexOf("{", at);
    if (j === -1) {
      out += css.slice(at);
      break;
    }
    let depth = 0;
    for (; j < css.length; j++) {
      if (css[j] === "{") depth++;
      else if (css[j] === "}") {
        depth--;
        if (depth === 0) {
          j++;
          break;
        }
      }
    }
    i = j;
  }
  return out;
}

/** 把 CSS 切成 [selector, body] 规则对（忽略 @media/@supports 外壳，只看内层声明）。 */
function splitRules(css: string): Array<[string, string]> {
  const rules: Array<[string, string]> = [];
  const re = /([^{}]+)\{([^{}]*)\}/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(css))) {
    rules.push([m[1]!.trim(), m[2]!]);
  }
  return rules;
}

const BARE_TAGS = ["button", "input", "select", "textarea", "a", "p", "h1", "h2", "h3", "h4", "h5", "h6"];

/** 选择器项是否以裸元素标签命中（`.seg button`、`.btn` 不算裸）。 */
function hitsBareTag(selectorItem: string, tag: string): boolean {
  // 去掉伪类/属性后取项首：裸标签出现在项首，或在逗号元素列表中出现。
  const item = selectorItem.trim();
  const atStart = new RegExp(`^${tag}(?=\\s*[,{]|$|\\s*::|\\s*\\[|\\s*:)`, "i").test(item);
  const afterComma = new RegExp(`,\\s*${tag}(?=\\s*[,{]|$|\\s*::|\\s*\\[|\\s*:)`, "i").test(item);
  return atStart || afterComma;
}

test("index.css 不存在 --x: var(--x) 自引用（含 fallback）", () => {
  const violations: string[] = [];
  for (const line of indexCss.split("\n")) {
    const m = line.match(/^\s*(--[\w-]+)\s*:\s*var\(\s*\1\b/);
    if (m) violations.push(line.trim());
  }
  expect(violations).toEqual([]);
});

test("无 layer 的裸元素规则不设置 color/font/font-weight（style.css + index.css）", () => {
  const violations: string[] = [];
  for (const [file, css] of [["src/style.css", styleCss], ["src/ui/index.css", indexCss]] as const) {
    const unlayered = stripLayerBlocks(css);
    for (const [selector, body] of splitRules(unlayered)) {
      if (!/(^|;)\s*(color|font|font-weight)\s*:/.test(body)) continue;
      for (const item of selector.split(",")) {
        for (const tag of BARE_TAGS) {
          if (hitsBareTag(item, tag)) {
            violations.push(`${file}: ${selector} { … }`);
          }
        }
      }
    }
  }
  expect(violations).toEqual([]);
});

const stripComments = (css: string) => css.replace(/\/\*[\s\S]*?\*\//g, "");
const sheets = [["src/style.css", stripComments(styleCss)], ["src/ui/index.css", stripComments(indexCss)]] as const;

/** 规则体里的声明名，按出现顺序。 */
function declNames(body: string): string[] {
  return body.split(";").map((d) => d.split(":")[0]!.trim()).filter(Boolean);
}

/** 拆出 `@media (prefers-reduced-motion: reduce) { … }` 块：返回块内文本与去掉这些块后的其余部分。 */
function splitReducedMotion(css: string): { reduced: string; rest: string } {
  let reduced = "";
  let rest = "";
  let i = 0;
  const re = /@media\s*\(prefers-reduced-motion:\s*reduce\)\s*\{/g;
  for (let m = re.exec(css); m; m = re.exec(css)) {
    rest += css.slice(i, m.index);
    let j = m.index + m[0].length;
    for (let depth = 1; depth > 0; j++) {
      if (css[j] === "{") depth++;
      else if (css[j] === "}") depth--;
    }
    reduced += css.slice(m.index + m[0].length, j - 1) + "\n";
    i = re.lastIndex = j;
  }
  return { reduced, rest: rest + css.slice(i) };
}

/** 页面代码里用字符串写出的变量名（如 style={{ "--cz-i": i }}）。 */
function varsSetInCode(dir: URL): Set<string> {
  const out = new Set<string>();
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      for (const v of varsSetInCode(new URL(`${entry.name}/`, dir))) out.add(v);
    } else if (/\.tsx?$/.test(entry.name)) {
      for (const m of readFileSync(new URL(entry.name, dir), "utf8").matchAll(/["'`](--[\w-]+)["'`]/g)) out.add(m[1]!);
    }
  }
  return out;
}

test("读到的 CSS 变量都有定义：样式表里声明，或页面代码里设置", () => {
  const css = sheets.map(([, s]) => s).join("\n");
  const declared = new Set([...css.matchAll(/(--[\w-]+)\s*:/g)].map((m) => m[1]!));
  const fromCode = varsSetInCode(new URL("../src/", import.meta.url));
  const read = new Set([...css.matchAll(/var\(\s*(--[\w-]+)/g)].map((m) => m[1]!));
  expect([...read].filter((v) => !declared.has(v) && !fromCode.has(v))).toEqual([]);
});

test("同一规则里 gap 不写在 row-gap / column-gap 之后", () => {
  const violations: string[] = [];
  for (const [file, css] of sheets) {
    for (const [selector, body] of splitRules(css)) {
      const names = declNames(body);
      const gap = names.lastIndexOf("gap");
      for (const longhand of ["row-gap", "column-gap"]) {
        const at = names.indexOf(longhand);
        if (at >= 0 && gap > at) violations.push(`${file}: ${selector} { ${longhand} … gap }`);
      }
    }
  }
  expect(violations).toEqual([]);
});

test("写死时长的 transform 过渡在减弱动态效果时关掉", () => {
  const missing: string[] = [];
  for (const [file, css] of sheets) {
    const { reduced, rest } = splitReducedMotion(css);
    const off = new Set<string>();
    for (const [selector, body] of splitRules(reduced)) {
      if (/(^|;)\s*transition\s*:\s*none\b/.test(body)) for (const s of selector.split(",")) off.add(s.trim().replace(/\s+/g, " "));
    }
    for (const [selector, body] of splitRules(rest)) {
      const transition = /(?:^|;)\s*transition\s*:([^;]+)/.exec(body)?.[1] ?? "";
      const fixed = transition.split(",").some((part) => /\btransform\b/.test(part) && /\d+m?s\b/.test(part) && !part.includes("var(--t-"));
      if (!fixed) continue;
      for (const s of selector.split(",")) {
        const name = s.trim().replace(/\s+/g, " ");
        if (!off.has(name)) missing.push(`${file}: ${name}`);
      }
    }
  }
  expect(missing).toEqual([]);
});

test("React 的 ghost 按钮和 .btn.ghost 用同一个描边令牌", () => {
  const token = /\.btn\.ghost\s*\{[^}]*border-color:\s*var\((--[\w-]+)\)/.exec(stripComments(styleCss))?.[1];
  expect(token).toBe("--border-strong");
  // Tailwind 颜色名来自 index.css 的 --color-<名>: var(<令牌>)。
  const color = new RegExp(`--color-([\\w-]+):\\s*var\\(${token}\\)`).exec(stripComments(indexCss))?.[1];
  expect(color).toBeDefined();
  for (const size of ["default", "sm", "lg"] as const) {
    expect(cn(buttonVariants({ variant: "ghost", size })).split(" ")).toContain(`border-${color}`);
  }
});
