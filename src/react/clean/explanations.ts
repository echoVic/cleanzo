import type { JunkItem } from "../../mygo";
import { ruleDesc } from "../../i18n";
import type { useT } from "../../ui/i18n-react";

/** 由官方工具清理的规则，以及各自调用的命令名。 */
const commandTools = new Map([
  ["review.simulators", "xcrun"], ["dev.go-mod", "go"], ["dev.homebrew-old", "brew"], ["dev.pnpm-store", "pnpm"],
]);

/** 规则清理时调用的命令名；不是工具规则时为 undefined。 */
export function commandTool(id: string): string | undefined {
  return commandTools.get(id);
}

/** Explain only the scope the cleanup rule actually owns. Custom paths have no inferred safety. */
export function cleanupExplanation(it: JunkItem, t: ReturnType<typeof useT>) {
  const custom = it.id.startsWith("custom:");
  const app = it.id.startsWith("app:");
  const browser = it.group === "browsers";
  const command = commandTool(it.id) !== undefined;
  const content = custom ? t("clean.explain.custom") : app ? t("clean.explain.app") : ruleDesc(it.id);
  const consequence = custom ? t("clean.explain.customEffect")
    : browser ? t("clean.explain.browserEffect")
    : app ? t("clean.explain.appEffect")
    : it.trash ? t("clean.explain.trashEffect")
    : command ? t("clean.explain.toolEffect") : t("clean.explain.deleteEffect");
  const method = t(command ? "clean.explain.tool" : it.trash ? "clean.explain.trash" : "clean.explain.delete");
  return { content, consequence, method };
}
