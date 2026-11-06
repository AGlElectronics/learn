import { apiRequest } from '@/api/client';

/** What owns a set of translatable texts. */
export type TranslationScope = 'course' | 'qualification' | 'path';

/** One translatable text: the default-language text and its translation ("" = none yet). */
export type TranslationField = {
  entity_type: string;
  entity_id: string;
  field: string;
  /** Where the text sits, e.g. "Section › Activity". */
  context: string;
  default: string;
  text: string;
  missing: boolean;
};

export type TranslationSheet = {
  /** "" when a course offers no language besides its default. */
  lang: string;
  default_lang?: string;
  languages?: string[];
  fields: TranslationField[];
  missing_count: number;
};

/** One translation to save; empty text removes it. */
export type TranslationInput = Pick<TranslationField, 'entity_type' | 'entity_id' | 'field' | 'text'>;

function base(scope: TranslationScope, id: string) {
  const plural = scope === 'course' ? 'courses' : scope === 'qualification' ? 'qualifications' : 'paths';
  return `/api/v1/${plural}/${id}/translations`;
}

export function getTranslations(scope: TranslationScope, id: string, lang: string, signal?: AbortSignal) {
  return apiRequest<TranslationSheet>(base(scope, id), { signal, query: { lang: lang || undefined } });
}

export function putTranslations(scope: TranslationScope, id: string, lang: string, items: TranslationInput[]) {
  return apiRequest<TranslationSheet>(base(scope, id), {
    method: 'PUT',
    query: { lang },
    body: { translations: items },
  });
}

/** Display name of a language code in the UI language, e.g. "nl" → "Dutch". */
export function languageName(code: string, uiLang: string) {
  try {
    const name = new Intl.DisplayNames([uiLang], { type: 'language' }).of(code);
    return name && name !== code ? `${name} (${code})` : code;
  } catch {
    return code;
  }
}
