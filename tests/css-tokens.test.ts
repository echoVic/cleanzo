import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";

/**
 * CSS 防回归：
 * a) src/ui/index.css 不允许 `--x: var(--x)` 自引用（含 fallback 形式）——循环引用会让
 *    变量计算为空，导致描边退化成 currentColor 黑线、次要文字/卡片底色丢失。
 * b) src/style.css 与 src/ui/index.css 不允许「无 @layer 的裸元素选择器」给 button/input/
 *    select/textarea/a/p/h1-h6 设置 color 或 font/font-weight：无 layer 规则会压过 Tailwind
 *    工具层，使 React 组件上的字号字重字色工具类失效（字色 currentColor、字重 400）。
 *    原生组件类（.btn、.seg button 等）是类作用域规则，不受影响。
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
