import { getPref, setPref, setNativeLocale, type LangPref } from "./i18n";

export interface LanguageBridge {
  locale: string;
  setLanguage(preference: LangPref): Promise<string>;
  onLocaleChange(listener: (locale: string) => void): () => void;
}

// The existing page preference remains authoritative, including "system".
// Subscribe before migrating it so system changes cannot get lost at startup.
export function connectLanguage(bridge: LanguageBridge | undefined, refresh: () => void) {
  let changing = false;
  if (bridge) setNativeLocale(bridge.locale);
  const disconnect = bridge?.onLocaleChange(locale => {
    setNativeLocale(locale);
    if (!changing && getPref() === "system") refresh();
  }) ?? (() => {});

  async function change(preference: LangPref) {
    changing = true;
    try {
      const locale = await bridge?.setLanguage(preference);
      // A failed native call must not persist a page-only language change.
      if (locale) setNativeLocale(locale);
      setPref(preference);
      refresh();
    } finally {
      changing = false;
    }
  }

  return { change, initialize: () => change(getPref()), disconnect };
}
