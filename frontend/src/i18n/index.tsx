import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import en from './en.json';
import nl from './nl.json';
import { getLang, storeLang, type Lang } from './lang';

export { LANGUAGES, getLang, getLocale, isLang, type Lang } from './lang';

type RawKey = keyof typeof en & string;
type BaseKey<K extends string> = K extends `${infer B}_one` ? B : K extends `${infer B}_other` ? B : K;
/** A message id; plural messages are stored as `<id>_one` / `<id>_other`. */
export type MessageKey = BaseKey<RawKey>;
export type Vars = Record<string, string | number>;

const catalogs: Record<Lang, Record<string, string>> = { en, nl };

/**
 * Looks a message up in lang (falling back to English, then the id) and
 * fills `{name}` placeholders. A numeric `count` picks `<id>_one` for 1
 * and `<id>_other` otherwise.
 */
export function translate(lang: Lang, key: MessageKey, vars?: Vars): string {
  const pick = (cat: Record<string, string>) => {
    if (typeof vars?.count === 'number') {
      const plural = cat[`${key}_${vars.count === 1 ? 'one' : 'other'}`];
      if (plural !== undefined) return plural;
    }
    return cat[key];
  };
  const text = pick(catalogs[lang]) ?? pick(catalogs.en) ?? key;
  return vars ? text.replace(/\{(\w+)\}/g, (m, name: string) => (name in vars ? String(vars[name]) : m)) : text;
}

export type TFunction = (key: MessageKey, vars?: Vars) => string;

type I18nValue = {
  lang: Lang;
  setLang: (lang: Lang) => void;
  t: TFunction;
};

const I18nContext = createContext<I18nValue | null>(null);

/**
 * Holds the UI language. Changing it stores the choice locally and refetches
 * every query, so course content comes back in the new language too (the
 * API client sends it as Accept-Language).
 */
export function I18nProvider({ children }: { children: React.ReactNode }) {
  const qc = useQueryClient();
  const [lang, setLangState] = useState<Lang>(getLang);
  useEffect(() => {
    document.documentElement.lang = lang;
  }, [lang]);
  const setLang = useCallback(
    (next: Lang) => {
      if (next === getLang()) return;
      storeLang(next);
      setLangState(next);
      void qc.invalidateQueries();
    },
    [qc]
  );
  const value = useMemo<I18nValue>(
    () => ({ lang, setLang, t: (key, vars) => translate(lang, key, vars) }),
    [lang, setLang]
  );
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nValue {
  const v = useContext(I18nContext);
  if (!v) throw new Error('useI18n must be used inside I18nProvider');
  return v;
}

/** The translate function for the current language. */
export function useT(): TFunction {
  return useI18n().t;
}
