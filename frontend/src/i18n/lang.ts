// The UI language outside React: read by the API client (Accept-Language)
// and the date formatters. The I18nProvider owns changes; localStorage keeps
// the choice for signed-out pages such as /verify.

export const LANGUAGES = [
  { code: 'en', label: 'English' },
  { code: 'nl', label: 'Nederlands' },
] as const;

export type Lang = (typeof LANGUAGES)[number]['code'];

export const LANG_STORAGE_KEY = 'learn-lang';

export function isLang(v: unknown): v is Lang {
  return LANGUAGES.some((l) => l.code === v);
}

/** Saved choice, else the browser's first supported language, else English. */
export function detectLang(): Lang {
  if (typeof window === 'undefined') return 'en';
  try {
    const saved = localStorage.getItem(LANG_STORAGE_KEY);
    if (isLang(saved)) return saved;
  } catch {
    /* private mode */
  }
  for (const tag of navigator.languages ?? [navigator.language]) {
    const primary = tag?.toLowerCase().split('-')[0];
    if (isLang(primary)) return primary;
  }
  return 'en';
}

let current: Lang = detectLang();

export function getLang(): Lang {
  return current;
}

/** BCP 47 locale for Intl date formatting in the current language. */
export function getLocale(): string {
  return current === 'nl' ? 'nl-NL' : 'en-GB';
}

export function storeLang(lang: Lang): void {
  current = lang;
  try {
    localStorage.setItem(LANG_STORAGE_KEY, lang);
  } catch {
    /* private mode */
  }
  if (typeof document !== 'undefined') document.documentElement.lang = lang;
}
