import { useEffect, useRef } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Languages } from 'lucide-react';
import { apiRequest } from '@/api/client';
import { LANGUAGES, isLang, useI18n, type Lang } from '@/i18n';
import { cn } from '@/lib/utils';

const PREFS_ENDPOINT = '/api/v1/me/preferences';
// Same cache entry as useThemeSync, so both read one request.
const PREFS_KEY = ['preferences', PREFS_ENDPOINT];

type LanguagePreference = { language?: string | null };

/**
 * Adopts the signed-in person's saved language (set on another device or
 * browser). Only reacts when the saved value changes, so a refetch that
 * races a fresh pick cannot flip the UI back.
 */
export function useLanguageSync(): void {
  const { lang, setLang } = useI18n();
  const q = useQuery({
    queryKey: PREFS_KEY,
    queryFn: () => apiRequest<LanguagePreference>(PREFS_ENDPOINT),
    staleTime: 30_000,
    retry: false,
  });
  const seen = useRef<string | null | undefined>(undefined);
  useEffect(() => {
    if (!q.data) return;
    const saved = q.data.language ?? null;
    if (saved === seen.current) return;
    seen.current = saved;
    if (isLang(saved) && saved !== lang) setLang(saved);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q.data]);
}

/**
 * Language picker. With persist, the choice is also saved to the person's
 * Learn preferences, which picks the language of course content everywhere;
 * without it (signed-out pages) it only lives in this browser.
 */
export function LanguageSwitcher({ persist = false, className }: { persist?: boolean; className?: string }) {
  const { lang, setLang, t } = useI18n();
  const qc = useQueryClient();
  const save = useMutation({
    mutationFn: (language: Lang) =>
      apiRequest<LanguagePreference>(PREFS_ENDPOINT, { method: 'PUT', body: { language } }),
    onSuccess: (p) => qc.setQueryData(PREFS_KEY, p),
  });
  return (
    <label className={cn('flex items-center gap-2 px-3 py-1.5 text-sm text-muted-foreground', className)}>
      <Languages className="w-4 h-4 shrink-0" aria-hidden />
      <span className="sr-only">{t('lang.label')}</span>
      <select
        value={lang}
        onChange={(e) => {
          const next = e.target.value;
          if (!isLang(next)) return;
          setLang(next);
          if (persist) save.mutate(next);
        }}
        className="flex-1 min-w-0 h-8 rounded-md border border-input bg-background px-2 text-sm text-foreground"
      >
        {LANGUAGES.map((l) => (
          <option key={l.code} value={l.code}>
            {l.label}
          </option>
        ))}
      </select>
    </label>
  );
}
