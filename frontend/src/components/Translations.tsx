import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Languages, Save, X } from 'lucide-react';
import { updateCourse } from '@/api/courses';
import {
  getTranslations,
  languageName,
  putTranslations,
  type TranslationField,
  type TranslationScope,
} from '@/api/translations';
import { Button } from '@/components/ui/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { useI18n, type MessageKey } from '@/i18n';
import { cn } from '@/lib/utils';

const FIELD_LABEL: Record<string, MessageKey> = {
  'course.title': 'tr.field.courseTitle',
  'course.summary': 'tr.field.courseSummary',
  'section.title': 'tr.field.sectionTitle',
  'activity.title': 'tr.field.activityTitle',
  'activity.body': 'tr.field.activityBody',
  'activity_media.caption': 'tr.field.caption',
  'question.prompt': 'tr.field.questionPrompt',
  'question.feedback': 'tr.field.questionFeedback',
  'option.label': 'tr.field.optionLabel',
  'media.alt_text': 'tr.field.altText',
  'qualification.name': 'tr.field.name',
  'qualification.description': 'tr.field.description',
  'path.name': 'tr.field.name',
  'path.description': 'tr.field.description',
};

const LONG_FIELDS = new Set(['activity.body', 'question.prompt', 'question.feedback', 'course.summary']);

// Common picks for the "add language" box; any ISO 639 code works.
const SUGGESTED = ['en', 'nl', 'de', 'fr', 'es', 'it', 'pl', 'pt', 'sv', 'da'];

const fieldKey = (f: Pick<TranslationField, 'entity_type' | 'entity_id' | 'field'>) =>
  `${f.entity_type}:${f.entity_id}:${f.field}`;

/**
 * Course languages: which languages the course is offered in and which one
 * its own texts are written in. Learners get the best match of their
 * language; anything untranslated shows in the default language.
 */
export function CourseLanguagesPanel({
  courseId,
  languages,
  defaultLang,
  onChanged,
}: {
  courseId: string;
  languages: string[];
  defaultLang: string;
  onChanged: () => void;
}) {
  const { lang: uiLang, t } = useI18n();
  const [draft, setDraft] = useState('');
  const save = useMutation({
    mutationFn: (body: { languages?: string[]; default_lang?: string }) => updateCourse(courseId, body),
    onSuccess: () => {
      setDraft('');
      onChanged();
    },
  });
  const add = () => {
    const code = draft.trim().toLowerCase();
    if (!code || languages.includes(code)) return;
    save.mutate({ languages: [...languages, code] });
  };

  return (
    <div className="space-y-2 text-sm">
      <ul className="flex flex-wrap gap-1.5">
        {languages.map((l) => (
          <li key={l} className="inline-flex items-center gap-1 rounded-pill bg-secondary px-2.5 py-0.5">
            {languageName(l, uiLang)}
            {l === defaultLang ? (
              <span className="text-[11px] font-semibold text-primary">{t('tr.default')}</span>
            ) : (
              <button
                type="button"
                className="text-muted-foreground hover:text-destructive"
                title={t('tr.removeLanguage')}
                disabled={save.isPending}
                onClick={() => {
                  if (confirm(t('tr.confirmRemove', { lang: languageName(l, uiLang) }))) {
                    save.mutate({ languages: languages.filter((x) => x !== l) });
                  }
                }}
              >
                <X className="w-3.5 h-3.5" />
              </button>
            )}
          </li>
        ))}
      </ul>
      <form
        className="flex flex-wrap items-end gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          add();
        }}
      >
        <div className="w-32">
          <Label htmlFor={`lang-add-${courseId}`}>{t('tr.addLanguage')}</Label>
          <Input
            id={`lang-add-${courseId}`}
            list={`lang-suggest-${courseId}`}
            value={draft}
            maxLength={3}
            placeholder="de"
            onChange={(e) => setDraft(e.target.value)}
          />
          <datalist id={`lang-suggest-${courseId}`}>
            {SUGGESTED.filter((l) => !languages.includes(l)).map((l) => (
              <option key={l} value={l}>
                {languageName(l, uiLang)}
              </option>
            ))}
          </datalist>
        </div>
        <Button type="submit" size="sm" variant="secondary" disabled={!draft.trim() || save.isPending}>
          {t('common.add')}
        </Button>
        <div className="ml-auto">
          <Label htmlFor={`lang-default-${courseId}`}>{t('tr.writtenIn')}</Label>
          <select
            id={`lang-default-${courseId}`}
            className="h-9 rounded-md border border-input bg-background px-2 text-sm"
            value={defaultLang}
            disabled={save.isPending}
            onChange={(e) => save.mutate({ default_lang: e.target.value })}
          >
            {languages.map((l) => (
              <option key={l} value={l}>
                {languageName(l, uiLang)}
              </option>
            ))}
          </select>
        </div>
      </form>
      <p className="text-xs text-muted-foreground">{t('tr.languagesHint')}</p>
      {save.isError ? <p className="text-sm text-destructive">{(save.error as Error).message}</p> : null}
    </div>
  );
}

/**
 * Per-language table of every translatable text with the default text next
 * to an input for the translation. Missing translations are flagged; saving
 * sends only what changed, and clearing an input removes that translation.
 */
export function TranslationEditor({
  scope,
  id,
  languages,
}: {
  scope: TranslationScope;
  id: string;
  /** Languages that can be translated into (never the default language). */
  languages: string[];
}) {
  const { lang: uiLang, t } = useI18n();
  const qc = useQueryClient();
  const [lang, setLang] = useState(languages[0] ?? '');
  useEffect(() => {
    if (!languages.includes(lang)) setLang(languages[0] ?? '');
  }, [languages, lang]);
  const [onlyMissing, setOnlyMissing] = useState(false);
  const key = ['translations', scope, id, lang];
  const q = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => getTranslations(scope, id, lang, signal),
    enabled: lang !== '',
    retry: false,
  });
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  useEffect(() => {
    if (q.data) setDrafts(Object.fromEntries(q.data.fields.map((f) => [fieldKey(f), f.text])));
  }, [q.data]);

  const changed = useMemo(
    () => (q.data?.fields ?? []).filter((f) => (drafts[fieldKey(f)] ?? '') !== f.text),
    [q.data, drafts]
  );
  const save = useMutation({
    mutationFn: () =>
      putTranslations(
        scope,
        id,
        lang,
        changed.map((f) => ({
          entity_type: f.entity_type,
          entity_id: f.entity_id,
          field: f.field,
          text: (drafts[fieldKey(f)] ?? '').trim(),
        }))
      ),
    onSuccess: (sheet) => {
      qc.setQueryData(key, sheet);
      void qc.invalidateQueries({ queryKey: ['course', id] });
    },
  });

  if (languages.length === 0) {
    return <p className="text-sm text-muted-foreground">{t('tr.noLanguages')}</p>;
  }
  const sheet = q.data;
  const rows = (sheet?.fields ?? []).filter((f) => !onlyMissing || f.missing);

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3 text-sm">
        <div className="inline-flex rounded-pill border border-border bg-card p-0.5">
          {languages.map((l) => (
            <button
              key={l}
              type="button"
              onClick={() => {
                if (changed.length > 0 && !confirm(t('tr.discardChanges'))) return;
                setLang(l);
              }}
              className={cn(
                'px-3 py-1 rounded-pill text-sm font-medium transition-colors',
                l === lang ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:text-foreground'
              )}
            >
              {languageName(l, uiLang)}
            </button>
          ))}
        </div>
        {sheet ? (
          <span className={cn('text-xs font-semibold', sheet.missing_count > 0 ? 'text-warning' : 'text-success')}>
            {sheet.missing_count > 0 ? t('tr.missingCount', { count: sheet.missing_count }) : t('tr.complete')}
          </span>
        ) : null}
        <label className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
          <input type="checkbox" checked={onlyMissing} onChange={(e) => setOnlyMissing(e.target.checked)} />
          {t('tr.onlyMissing')}
        </label>
        <Button
          size="sm"
          className="ml-auto"
          disabled={changed.length === 0 || save.isPending}
          onClick={() => save.mutate()}
        >
          <Save className="w-4 h-4" />
          {changed.length > 0 ? t('tr.saveCount', { count: changed.length }) : t('common.save')}
        </Button>
      </div>
      {save.isError ? <ErrorRow message={(save.error as Error).message} /> : null}
      {q.isLoading ? (
        <LoadingRow />
      ) : q.isError ? (
        <ErrorRow message={(q.error as Error).message} />
      ) : rows.length === 0 ? (
        <p className="text-sm text-muted-foreground">{onlyMissing ? t('tr.nothingMissing') : t('tr.nothingToTranslate')}</p>
      ) : (
        <div className="overflow-x-auto rounded-card border border-border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-left">
                <th className="px-3 py-2 font-semibold w-48">{t('tr.col.field')}</th>
                <th className="px-3 py-2 font-semibold">{t('tr.col.default')}</th>
                <th className="px-3 py-2 font-semibold">{languageName(lang, uiLang)}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((f) => {
                const k = fieldKey(f);
                const type = `${f.entity_type}.${f.field}`;
                const label = FIELD_LABEL[type];
                const value = drafts[k] ?? '';
                const missing = value.trim() === '';
                return (
                  <tr key={k} className={cn('border-b border-border last:border-0 align-top', missing && 'bg-warning/5')}>
                    <td className="px-3 py-2">
                      <p className="font-medium">{label ? t(label) : type}</p>
                      {f.context ? <p className="text-xs text-muted-foreground line-clamp-2">{f.context}</p> : null}
                      {missing ? (
                        <span className="mt-1 inline-block rounded-pill bg-warning/15 text-warning px-2 py-0.5 text-[11px] font-semibold">
                          {t('tr.missing')}
                        </span>
                      ) : null}
                    </td>
                    <td className="px-3 py-2 text-muted-foreground whitespace-pre-wrap max-w-sm">
                      <p className="line-clamp-6">{f.default}</p>
                    </td>
                    <td className="px-3 py-2 min-w-[16rem]">
                      {LONG_FIELDS.has(type) ? (
                        <textarea
                          value={value}
                          rows={Math.min(8, Math.max(2, f.default.split('\n').length))}
                          onChange={(e) => setDrafts((d) => ({ ...d, [k]: e.target.value }))}
                          aria-label={label ? t(label) : type}
                          className="w-full rounded-md border border-input bg-background px-2 py-1.5 text-sm focus-visible:outline-none focus-visible:border-primary"
                        />
                      ) : (
                        <Input
                          value={value}
                          onChange={(e) => setDrafts((d) => ({ ...d, [k]: e.target.value }))}
                          aria-label={label ? t(label) : type}
                        />
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

/** Course settings card: languages plus the translation table. */
export function CourseTranslationsCard({
  courseId,
  languages,
  defaultLang,
  onChanged,
}: {
  courseId: string;
  languages: string[];
  defaultLang: string;
  onChanged: () => void;
}) {
  const { t } = useI18n();
  const targets = languages.filter((l) => l !== defaultLang);
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Languages className="w-4 h-4 text-primary" />
          {t('tr.title')}
        </CardTitle>
        <CardDescription>{t('tr.description')}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-5">
        <CourseLanguagesPanel courseId={courseId} languages={languages} defaultLang={defaultLang} onChanged={onChanged} />
        <div className="border-t border-border pt-4">
          <TranslationEditor scope="course" id={courseId} languages={targets} />
        </div>
      </CardContent>
    </Card>
  );
}
