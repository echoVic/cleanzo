import type { JunkItem } from "../../mygo";
import { ruleDesc } from "../../i18n";
import type { useT } from "../../ui/i18n-react";

const commandRules = new Set(["review.simulators", "dev.go-mod", "dev.homebrew-old", "dev.pnpm-store"]);

/** Explain only the scope the cleanup rule actually owns. Custom paths have no inferred safety. */
export function cleanupExplanation(it: JunkItem, t: ReturnType<typeof useT>) {
  const custom = it.id.startsWith("custom:");
  const app = it.id.startsWith("app:");
  const browser = it.group === "browsers";
  const command = commandRules.has(it.id);
  const content = custom ? t("clean.explain.custom") : app ? t("clean.explain.app") : ruleDesc(it.id);
  const consequence = custom ? t("clean.explain.customEffect")
    : browser ? t("clean.explain.browserEffect")
    : app ? t("clean.explain.appEffect")
    : it.trash ? t("clean.explain.trashEffect")
    : command ? t("clean.explain.toolEffect") : t("clean.explain.deleteEffect");
  const method = t(command ? "clean.explain.tool" : it.trash ? "clean.explain.trash" : "clean.explain.delete");
  return { content, consequence, method };
}
