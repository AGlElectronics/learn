import { useEffect, useState } from 'react';
import {
  ArrowDownWideNarrow,
  BarChart3,
  CheckCircle2,
  Circle,
  FileUp,
  Lock,
  Plus,
  Save,
  Square,
  SquareCheck,
  Trash2,
  X,
} from 'lucide-react';
import { ImportDialog } from '@/components/ImportDialog';
import { useQuestionStats, type QuestionStat } from '@/api/stats';
import { Button } from '@/components/ui/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { HotspotEditor } from '@/components/Hotspot';
import { MediaPicker, MediaThumb } from '@/components/media/MediaPicker';
import {
  createCategory,
  createOption,
  createQuestion,
  deleteCategory,
  deleteOption,
  deleteQuestion,
  replaceRegions,
  updateCategory,
  updateOption,
  updateQuestion,
  useBankMutation,
  useCategories,
  useQuestions,
  type Question,
  type QuestionKind,
  type RegionInput,
} from '@/api/quizzes';
import type { MediaRef } from '@/api/media';
import { cn } from '@/lib/utils';

const textareaClasses =
  'w-full rounded-md border border-input bg-background px-3 py-2 text-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:border-primary';

/**
 * The course question bank: categories, their questions, and each
 * question's options with the correct ones ticked. Quizzes pick fixed
 * questions or draw at random from a category. Options lock once a
 * question has been answered in a submitted attempt.
 */
export function QuestionBank({ courseId }: { courseId: string }) {
  const categoriesQ = useCategories(courseId);
  const [selected, setSelected] = useState<string | null>(null);
  const [newName, setNewName] = useState('');
  const [importing, setImporting] = useState(false);
  const categories = categoriesQ.data?.categories ?? [];

  useEffect(() => {
    if (categories.length > 0 && !categories.some((c) => c.id === selected)) {
      setSelected(categories[0].id);
    }
  }, [categories, selected]);

  const add = useBankMutation((name: string) => createCategory(courseId, name));
  const rename = useBankMutation(({ id, name }: { id: string; name: string }) => updateCategory(id, { name }));
  const remove = useBankMutation((id: string) => deleteCategory(id));
  const current = categories.find((c) => c.id === selected) ?? null;
  const error = (add.error ?? rename.error ?? remove.error) as Error | null;

  return (
    <Card>
      <CardHeader>
        <CardTitle>Question bank</CardTitle>
        <CardDescription>
          Questions for this course's quizzes, grouped in categories. A quiz can use a question directly or draw
          some at random from a category.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {categoriesQ.isLoading ? (
          <LoadingRow />
        ) : categoriesQ.isError ? (
          <ErrorRow message="Could not load the question bank." />
        ) : (
          <div className="flex flex-wrap gap-1.5">
            {categories.map((c) => (
              <button
                key={c.id}
                type="button"
                onClick={() => setSelected(c.id)}
                className={cn(
                  'text-sm px-3 py-1 rounded-pill border transition-colors',
                  c.id === selected
                    ? 'bg-accent text-accent-foreground border-primary/40 font-medium'
                    : 'border-border text-muted-foreground hover:bg-secondary'
                )}
              >
                {c.name} <span className="text-xs opacity-70">({c.question_count})</span>
              </button>
            ))}
          </div>
        )}
        <form
          className="flex gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (!newName.trim()) return;
            add.mutate(newName, { onSuccess: (c) => {
              setNewName('');
              setSelected(c.id);
            } });
          }}
        >
          <Input value={newName} onChange={(e) => setNewName(e.target.value)} placeholder="New category name" />
          <Button type="submit" variant="secondary" disabled={!newName.trim() || add.isPending}>
            <Plus className="w-4 h-4" />
            Add category
          </Button>
          <Button type="button" variant="ghost" onClick={() => setImporting(true)}>
            <FileUp className="w-4 h-4" />
            Import
          </Button>
        </form>
        {importing ? (
          <ImportDialog
            courseId={courseId}
            categories={categories}
            defaultCategoryId={selected}
            onClose={() => setImporting(false)}
          />
        ) : null}
        {error ? <p className="text-sm text-destructive">{error.message}</p> : null}

        {current ? (
          <div className="space-y-3 border-t border-border pt-4">
            <div className="flex items-center gap-2">
              <h4 className="font-heading font-bold flex-1 truncate">{current.name}</h4>
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  const name = prompt('Rename category', current.name);
                  if (name && name.trim()) rename.mutate({ id: current.id, name });
                }}
              >
                Rename
              </Button>
              <Button
                variant="ghost"
                size="sm"
                className="text-destructive"
                onClick={() => {
                  if (confirm(`Delete category "${current.name}" and all its questions?`)) remove.mutate(current.id);
                }}
              >
                <Trash2 className="w-4 h-4" />
              </Button>
            </div>
            <CategoryQuestions courseId={courseId} categoryId={current.id} />
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}

/** Hardest first: lowest % correct; questions nobody answered go last. */
function byDifficulty(stats: Map<string, QuestionStat> | undefined) {
  return (a: Question, b: Question) => {
    const pa = stats?.get(a.id)?.percent_correct ?? null;
    const pb = stats?.get(b.id)?.percent_correct ?? null;
    if (pa === null || pb === null) return pa === pb ? 0 : pa === null ? 1 : -1;
    return pa - pb;
  };
}

function CategoryQuestions({ courseId, categoryId }: { courseId: string; categoryId: string }) {
  const questionsQ = useQuestions(categoryId);
  const statsQ = useQuestionStats(courseId);
  const [adding, setAdding] = useState(false);
  const [hardestFirst, setHardestFirst] = useState(false);
  if (questionsQ.isLoading) return <LoadingRow />;
  if (questionsQ.isError) return <ErrorRow message="Could not load questions." />;
  const loaded = questionsQ.data?.questions ?? [];
  const numbered = loaded.map((q, i) => ({ q, n: i + 1 }));
  const questions = hardestFirst
    ? [...numbered].sort((a, b) => byDifficulty(statsQ.data)(a.q, b.q))
    : numbered;
  return (
    <div className="space-y-3">
      {loaded.length === 0 && !adding ? (
        <p className="text-sm text-muted-foreground">No questions in this category yet.</p>
      ) : null}
      {loaded.length > 1 ? (
        <Button
          variant={hardestFirst ? 'secondary' : 'ghost'}
          size="sm"
          aria-pressed={hardestFirst}
          onClick={() => setHardestFirst((v) => !v)}
        >
          <ArrowDownWideNarrow className="w-4 h-4" />
          {hardestFirst ? 'Sorted: hardest first' : 'Sort: hardest first'}
        </Button>
      ) : null}
      {questions.map(({ q, n }) => (
        <QuestionEditor key={q.id} courseId={courseId} question={q} index={n} stat={statsQ.data?.get(q.id)} />
      ))}
      {adding ? (
        <NewQuestionForm courseId={courseId} categoryId={categoryId} onDone={() => setAdding(false)} />
      ) : (
        <Button variant="secondary" size="sm" onClick={() => setAdding(true)}>
          <Plus className="w-4 h-4" />
          Add question
        </Button>
      )}
    </div>
  );
}

const KIND_LABEL: Record<QuestionKind, string> = {
  single: 'One answer',
  multiple: 'Several answers',
  hotspot: 'Tap on picture',
};

function KindToggle({
  kind,
  onChange,
  disabled,
  kinds = ['single', 'multiple'],
}: {
  kind: QuestionKind;
  onChange: (k: QuestionKind) => void;
  disabled?: boolean;
  kinds?: QuestionKind[];
}) {
  return (
    <div className="inline-flex rounded-pill border border-border p-0.5 text-xs">
      {kinds.map((k) => (
        <button
          key={k}
          type="button"
          disabled={disabled}
          onClick={() => onChange(k)}
          className={cn(
            'px-2.5 py-1 rounded-pill transition-colors disabled:opacity-50',
            k === kind ? 'bg-primary text-primary-foreground font-semibold' : 'text-muted-foreground hover:bg-secondary'
          )}
        >
          {KIND_LABEL[k]}
        </button>
      ))}
    </div>
  );
}

function CorrectTick({ kind, correct }: { kind: QuestionKind; correct: boolean }) {
  if (kind === 'single') {
    return correct ? <CheckCircle2 className="w-4 h-4 text-success" /> : <Circle className="w-4 h-4" />;
  }
  return correct ? <SquareCheck className="w-4 h-4 text-success" /> : <Square className="w-4 h-4" />;
}

type DraftOption = { label: string; correct: boolean; media: MediaRef | null };

function NewQuestionForm({ courseId, categoryId, onDone }: { courseId: string; categoryId: string; onDone: () => void }) {
  const [kind, setKind] = useState<QuestionKind>('single');
  const [prompt, setPrompt] = useState('');
  const [feedback, setFeedback] = useState('');
  const [points, setPoints] = useState(1);
  const [media, setMedia] = useState<MediaRef | null>(null);
  const [feedbackMedia, setFeedbackMedia] = useState<MediaRef | null>(null);
  const [regions, setRegions] = useState<RegionInput[]>([]);
  const [clicks, setClicks] = useState('');
  const [options, setOptions] = useState<DraftOption[]>([
    { label: '', correct: true, media: null },
    { label: '', correct: false, media: null },
  ]);
  const hotspot = kind === 'hotspot';
  const create = useBankMutation(() =>
    createQuestion(categoryId, {
      kind,
      prompt,
      feedback,
      points,
      media_id: media?.id ?? null,
      feedback_media_id: feedbackMedia?.id ?? null,
      options: hotspot
        ? []
        : options
            .filter((o) => o.label.trim() || o.media)
            .map((o) => ({ label: o.label, correct: o.correct, media_id: o.media?.id ?? null })),
      ...(hotspot ? { regions, hotspot_clicks: clicks ? Number(clicks) : null } : {}),
    })
  );
  const hotspotReady = !hotspot || (!!media && regions.some((r) => r.correct));

  const toggle = (i: number) =>
    setOptions((opts) =>
      opts.map((o, j) =>
        kind === 'single' ? { ...o, correct: i === j } : i === j ? { ...o, correct: !o.correct } : o
      )
    );

  return (
    <form
      className="space-y-3 rounded-lg border border-primary/40 p-3"
      onSubmit={(e) => {
        e.preventDefault();
        create.mutate(undefined, { onSuccess: onDone });
      }}
    >
      <div className="flex flex-wrap items-center gap-2 justify-between">
        <KindToggle
          kind={kind}
          kinds={['single', 'multiple', 'hotspot']}
          onChange={(k) => {
            setKind(k);
            if (k === 'single') {
              const first = Math.max(0, options.findIndex((o) => o.correct));
              setOptions((opts) => opts.map((o, j) => ({ ...o, correct: j === first })));
            }
          }}
        />
        <label className="flex items-center gap-2 text-sm">
          Points
          <Input
            type="number"
            min={1}
            value={points}
            onChange={(e) => setPoints(Number(e.target.value) || 1)}
            className="w-20"
          />
        </label>
      </div>
      <div>
        <Label htmlFor={`new-q-${categoryId}`}>Question</Label>
        <textarea
          id={`new-q-${categoryId}`}
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          rows={2}
          className={textareaClasses}
          placeholder="What colour is a fire exit sign?"
          autoFocus
        />
      </div>
      <div>
        <Label>{hotspot ? 'Picture to tap on (required)' : 'Picture, audio or video (optional)'}</Label>
        <MediaPicker
          courseId={courseId}
          kinds={hotspot ? ['image'] : ['image', 'audio', 'video']}
          value={media}
          onChange={(m) => {
            setMedia(m);
            setRegions([]);
          }}
        />
      </div>
      {hotspot ? (
        <div className="space-y-2">
          {media ? (
            <HotspotEditor media={media} regions={regions} onChange={setRegions} />
          ) : (
            <p className="text-sm text-muted-foreground">Add the picture, then draw the regions on it.</p>
          )}
          <label className="flex items-center gap-2 text-sm">
            Taps allowed
            <Input
              type="number"
              min={1}
              value={clicks}
              onChange={(e) => setClicks(e.target.value)}
              placeholder={String(Math.max(1, regions.filter((r) => r.correct).length))}
              className="w-24"
            />
            <span className="text-xs text-muted-foreground">blank = one per correct region</span>
          </label>
        </div>
      ) : null}
      <div className={cn('space-y-1.5', hotspot && 'hidden')}>
        <Label>Answers (tick the correct {kind === 'single' ? 'one' : 'ones'}; text, picture or both)</Label>
        {options.map((o, i) => (
          <div key={i} className="flex items-start gap-2">
            <button type="button" onClick={() => toggle(i)} aria-label="Toggle correct" className="shrink-0 mt-2">
              <CorrectTick kind={kind} correct={o.correct} />
            </button>
            <div className="flex-1 space-y-1">
              <Input
                value={o.label}
                onChange={(e) =>
                  setOptions((opts) => opts.map((x, j) => (j === i ? { ...x, label: e.target.value } : x)))
                }
                placeholder={`Answer ${i + 1}`}
              />
              <MediaPicker
                courseId={courseId}
                value={o.media}
                onChange={(m) => setOptions((opts) => opts.map((x, j) => (j === i ? { ...x, media: m } : x)))}
              />
            </div>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              disabled={options.length <= 2}
              onClick={() => setOptions((opts) => opts.filter((_, j) => j !== i))}
            >
              <X className="w-4 h-4" />
            </Button>
          </div>
        ))}
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => setOptions((opts) => [...opts, { label: '', correct: false, media: null }])}
        >
          <Plus className="w-4 h-4" />
          Add answer
        </Button>
      </div>
      <div>
        <Label htmlFor={`new-fb-${categoryId}`}>Feedback (shown after submitting)</Label>
        <textarea
          id={`new-fb-${categoryId}`}
          value={feedback}
          onChange={(e) => setFeedback(e.target.value)}
          rows={2}
          className={textareaClasses}
          placeholder="Why the answer is right (optional)"
        />
        <MediaPicker
          courseId={courseId}
          kinds={['image', 'audio', 'video']}
          label="feedback picture / audio / video"
          value={feedbackMedia}
          onChange={setFeedbackMedia}
        />
      </div>
      <div className="flex gap-2">
        <Button type="submit" size="sm" disabled={!prompt.trim() || !hotspotReady || create.isPending}>
          <Save className="w-4 h-4" />
          Save question
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onDone}>
          Cancel
        </Button>
      </div>
      {create.isError ? <p className="text-sm text-destructive">{(create.error as Error).message}</p> : null}
    </form>
  );
}

function QuestionEditor({
  courseId,
  question,
  index,
  stat,
}: {
  courseId: string;
  question: Question;
  index: number;
  stat?: QuestionStat;
}) {
  const [editing, setEditing] = useState(false);
  const [prompt, setPrompt] = useState(question.prompt);
  const [feedback, setFeedback] = useState(question.feedback);
  const [points, setPoints] = useState(question.points);
  const [newOption, setNewOption] = useState('');
  const [regions, setRegions] = useState<RegionInput[]>(question.regions ?? []);
  const [regionsDirty, setRegionsDirty] = useState(false);
  useEffect(() => {
    setRegions(question.regions ?? []);
    setRegionsDirty(false);
  }, [question.regions]);
  const hotspot = question.kind === 'hotspot';

  const save = useBankMutation(() => updateQuestion(question.id, { prompt, feedback, points }));
  const setKind = useBankMutation((kind: QuestionKind) => updateQuestion(question.id, { kind }));
  const setMedia = useBankMutation(
    (body: { media_id?: string | null; feedback_media_id?: string | null; hotspot_clicks?: number | null }) =>
      updateQuestion(question.id, body)
  );
  const saveRegions = useBankMutation(() => replaceRegions(question.id, regions));
  const remove = useBankMutation(() => deleteQuestion(question.id));
  const addOption = useBankMutation(() => createOption(question.id, { label: newOption }));
  const changeOption = useBankMutation(
    ({ id, ...body }: { id: string; label?: string; correct?: boolean; media_id?: string | null }) =>
      updateOption(id, body)
  );
  const removeOption = useBankMutation((id: string) => deleteOption(id));
  const error = (save.error ??
    setKind.error ??
    setMedia.error ??
    saveRegions.error ??
    remove.error ??
    addOption.error ??
    changeOption.error ??
    removeOption.error) as Error | null;
  const locked = question.locked;

  return (
    <div className="rounded-lg border border-border p-3 space-y-2">
      <div className="flex items-start gap-2">
        <span className="text-xs font-semibold text-muted-foreground mt-0.5">{index}.</span>
        {editing ? (
          <div className="flex-1 space-y-2">
            <textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} rows={2} className={textareaClasses} />
            <textarea
              value={feedback}
              onChange={(e) => setFeedback(e.target.value)}
              rows={2}
              className={textareaClasses}
              placeholder="Feedback shown after submitting"
            />
            <MediaPicker
              courseId={courseId}
              kinds={['image', 'audio', 'video']}
              label="feedback picture / audio / video"
              value={question.feedback_media}
              disabled={setMedia.isPending}
              onChange={(m) => setMedia.mutate({ feedback_media_id: m?.id ?? null })}
            />
            <div className="flex items-center gap-2">
              <label className="flex items-center gap-2 text-sm">
                Points
                <Input
                  type="number"
                  min={1}
                  value={points}
                  onChange={(e) => setPoints(Number(e.target.value) || 1)}
                  className="w-20"
                />
              </label>
              <div className="flex-1" />
              <Button
                size="sm"
                disabled={!prompt.trim() || save.isPending}
                onClick={() => save.mutate(undefined, { onSuccess: () => setEditing(false) })}
              >
                <Save className="w-4 h-4" />
                Save
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setEditing(false)}>
                Cancel
              </Button>
            </div>
          </div>
        ) : (
          <div className="flex-1 min-w-0">
            <p className="text-sm font-medium whitespace-pre-wrap">{question.prompt}</p>
            <p className="text-xs text-muted-foreground mt-0.5">
              {question.points} point{question.points === 1 ? '' : 's'}
              {question.feedback ? ` · feedback: ${question.feedback}` : ''}
              {question.feedback_media ? ' · feedback has media' : ''}
            </p>
            {stat ? <QuestionStats stat={stat} /> : null}
          </div>
        )}
        {!editing ? (
          <div className="flex gap-1 shrink-0">
            <Button
              variant="ghost"
              size="sm"
              onClick={() => {
                setPrompt(question.prompt);
                setFeedback(question.feedback);
                setPoints(question.points);
                setEditing(true);
              }}
            >
              Edit
            </Button>
            <Button
              variant="ghost"
              size="sm"
              className="text-destructive"
              onClick={() => {
                if (confirm('Delete this question?')) remove.mutate(undefined);
              }}
            >
              <Trash2 className="w-4 h-4" />
            </Button>
          </div>
        ) : null}
      </div>

      <div className="flex items-center gap-2">
        {hotspot ? (
          <span className="text-xs font-semibold text-muted-foreground">{KIND_LABEL.hotspot}</span>
        ) : (
          <KindToggle kind={question.kind} disabled={locked || setKind.isPending} onChange={(k) => setKind.mutate(k)} />
        )}
        {locked ? (
          <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
            <Lock className="w-3 h-3" />
            Answered in a quiz — reset the quiz to change answers
          </span>
        ) : null}
      </div>

      <MediaPicker
        courseId={courseId}
        kinds={hotspot ? ['image'] : ['image', 'audio', 'video']}
        value={question.media}
        disabled={setMedia.isPending || (hotspot && locked)}
        onChange={(m) => {
          if (hotspot && !m) return;
          setMedia.mutate({ media_id: m?.id ?? null });
        }}
      />

      {hotspot && question.media ? (
        <div className="space-y-2">
          <HotspotEditor
            media={question.media}
            regions={regions}
            disabled={locked}
            onChange={(r) => {
              setRegions(r);
              setRegionsDirty(true);
            }}
          />
          <div className="flex flex-wrap items-center gap-2">
            {regionsDirty ? (
              <Button size="sm" disabled={saveRegions.isPending} onClick={() => saveRegions.mutate(undefined)}>
                <Save className="w-4 h-4" />
                Save regions
              </Button>
            ) : null}
            <label className="flex items-center gap-2 text-sm">
              Taps allowed
              <Input
                type="number"
                min={1}
                disabled={locked}
                defaultValue={question.hotspot_clicks ?? ''}
                placeholder={String(Math.max(1, (question.regions ?? []).filter((r) => r.correct).length))}
                onBlur={(e) => {
                  const v = e.target.value ? Number(e.target.value) : null;
                  if (v !== question.hotspot_clicks) setMedia.mutate({ hotspot_clicks: v });
                }}
                className="w-24 h-8"
              />
            </label>
          </div>
        </div>
      ) : null}

      <ul className={cn('space-y-1', hotspot && 'hidden')}>
        {question.options.map((o) => (
          <li key={o.id} className="flex flex-wrap items-center gap-2">
            <button
              type="button"
              disabled={locked || changeOption.isPending}
              onClick={() => changeOption.mutate({ id: o.id, correct: !o.correct })}
              aria-label={o.correct ? 'Correct answer' : 'Mark as correct'}
              className="shrink-0 disabled:opacity-60"
            >
              <CorrectTick kind={question.kind} correct={o.correct} />
            </button>
            <div className="flex-1 min-w-0">
              <OptionLabel
                label={o.label}
                disabled={locked}
                allowEmpty={!!o.media}
                onSave={(label) => changeOption.mutate({ id: o.id, label })}
              />
            </div>
            <Button
              variant="ghost"
              size="sm"
              disabled={locked || removeOption.isPending}
              onClick={() => removeOption.mutate(o.id)}
            >
              <X className="w-4 h-4" />
            </Button>
            <div className="basis-full pl-6">
              {locked && o.media ? (
                <MediaThumb media={o.media} className="max-h-16" />
              ) : (
                <MediaPicker
                  courseId={courseId}
                  value={o.media}
                  disabled={locked || changeOption.isPending}
                  onChange={(m) => {
                    if (!m && !o.label.trim()) return;
                    changeOption.mutate({ id: o.id, media_id: m?.id ?? null });
                  }}
                />
              )}
            </div>
          </li>
        ))}
      </ul>
      {!locked && !hotspot ? (
        <form
          className="flex gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (newOption.trim()) addOption.mutate(undefined, { onSuccess: () => setNewOption('') });
          }}
        >
          <Input value={newOption} onChange={(e) => setNewOption(e.target.value)} placeholder="Another answer" />
          <Button type="submit" variant="ghost" size="sm" disabled={!newOption.trim() || addOption.isPending}>
            <Plus className="w-4 h-4" />
          </Button>
        </form>
      ) : null}
      {error ? <p className="text-sm text-destructive">{error.message}</p> : null}
    </div>
  );
}

const pct = (v: number | null) => (v === null ? '–' : `${Math.round(v)}%`);

/** Attempts, % correct, and how answers were spread (option picks or region hits). */
function QuestionStats({ stat }: { stat: QuestionStat }) {
  if (stat.attempts === 0) {
    return <p className="text-xs text-muted-foreground mt-1">Not answered in a quiz yet.</p>;
  }
  const hard = stat.percent_correct !== null && stat.percent_correct < 50;
  return (
    <div className="mt-1 space-y-0.5 text-xs text-muted-foreground">
      <p className="flex items-center gap-1">
        <BarChart3 className="w-3 h-3" />
        {stat.attempts} answer{stat.attempts === 1 ? '' : 's'} ·{' '}
        <span className={cn('font-semibold', hard ? 'text-destructive' : 'text-foreground')}>
          {pct(stat.percent_correct)} correct
        </span>
        {stat.kind === 'hotspot' ? ` · ${stat.miss_taps} of ${stat.taps} taps missed` : ''}
      </p>
      {stat.kind === 'hotspot' ? (
        <p>
          {stat.regions.map((r, i) => (
            <span key={r.region_id} className={cn('mr-2', r.correct && 'text-success')}>
              Region {i + 1}
              {r.correct ? '' : ' (wrong)'}: {pct(r.hit_rate)} tapped
            </span>
          ))}
        </p>
      ) : (
        <p>
          {stat.options.map((o) => (
            <span key={o.option_id} className={cn('mr-2', o.correct && 'text-success')}>
              {o.label || 'Picture'}: {o.picks}× ({pct((o.picks * 100) / stat.attempts)})
            </span>
          ))}
        </p>
      )}
    </div>
  );
}

function OptionLabel({
  label,
  disabled,
  allowEmpty,
  onSave,
}: {
  label: string;
  disabled: boolean;
  /** The option has a picture, so the text may be blank. */
  allowEmpty?: boolean;
  onSave: (label: string) => void;
}) {
  const [value, setValue] = useState(label);
  useEffect(() => setValue(label), [label]);
  return (
    <Input
      value={value}
      disabled={disabled}
      onChange={(e) => setValue(e.target.value)}
      onBlur={() => {
        if ((value.trim() || allowEmpty) && value !== label) onSave(value);
      }}
      placeholder={allowEmpty ? 'Text (optional with a picture)' : undefined}
      className="h-8"
    />
  );
}
