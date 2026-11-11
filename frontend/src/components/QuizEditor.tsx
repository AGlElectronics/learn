import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { AlertTriangle, ChevronDown, ChevronRight, ClipboardCheck, Plus, Save, Shuffle, Trash2, X } from 'lucide-react';
import { ResetQuizButton } from '@/components/ResetQuizButton';
import { Button } from '@/components/ui/Button';
import { Input, Label } from '@/components/ui/Input';
import { LoadingRow } from '@/components/ui/States';
import { updateActivity, type Activity } from '@/api/courses';
import {
  createSlot,
  deleteSlot,
  updateSlot,
  useBankMutation,
  useCategories,
  useQuestions,
  useSlots,
} from '@/api/quizzes';

const textareaClasses =
  'w-full rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:border-primary';

/**
 * One quiz in the course editor: title and intro, pass mark, attempt limit,
 * option shuffling, and the slot list (fixed questions or "draw N from a
 * category"). Problems that would block publishing are listed inline.
 */
export function QuizEditor({
  courseId,
  activity,
  onChanged,
  onDelete,
}: {
  courseId: string;
  activity: Activity;
  onChanged: () => void;
  onDelete: () => void;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div className="rounded-lg border border-border">
      <div className="flex items-center justify-between gap-2 px-3 py-2">
        <button
          type="button"
          className="flex items-center gap-2 min-w-0 text-left"
          onClick={() => setOpen((v) => !v)}
        >
          {open ? <ChevronDown className="w-4 h-4 shrink-0" /> : <ChevronRight className="w-4 h-4 shrink-0" />}
          <ClipboardCheck className="w-4 h-4 shrink-0 text-primary" />
          <span className="text-sm font-medium truncate">{activity.title}</span>
          <span className="text-[11px] font-semibold rounded-pill bg-primary-soft text-primary px-2 py-0.5 shrink-0">
            Quiz · pass {activity.pass_percent}%
          </span>
        </button>
        <Button
          variant="ghost"
          size="sm"
          className="text-destructive shrink-0"
          onClick={() => {
            if (confirm(`Delete quiz "${activity.title}" and all its attempts?`)) onDelete();
          }}
        >
          <Trash2 className="w-4 h-4" />
        </Button>
      </div>
      {open ? (
        <div className="border-t border-border p-3 space-y-4">
          <QuizSettingsForm activity={activity} onChanged={onChanged} />
          <QuizSlots courseId={courseId} activityId={activity.id} title={activity.title} onChanged={onChanged} />
        </div>
      ) : null}
    </div>
  );
}

function QuizSettingsForm({ activity, onChanged }: { activity: Activity; onChanged: () => void }) {
  const [title, setTitle] = useState(activity.title);
  const [body, setBody] = useState(activity.body);
  const [passPercent, setPassPercent] = useState(activity.pass_percent);
  const [maxAttempts, setMaxAttempts] = useState(activity.max_attempts ? String(activity.max_attempts) : '');
  const [shuffle, setShuffle] = useState(activity.shuffle_options);
  const save = useMutation({
    mutationFn: () =>
      updateActivity(activity.id, {
        title,
        body,
        pass_percent: passPercent,
        max_attempts: maxAttempts.trim() ? Number(maxAttempts) : null,
        shuffle_options: shuffle,
      }),
    onSuccess: onChanged,
  });

  return (
    <form
      className="space-y-2"
      onSubmit={(e) => {
        e.preventDefault();
        if (title.trim()) save.mutate();
      }}
    >
      <div>
        <Label htmlFor={`quiz-title-${activity.id}`}>Title</Label>
        <Input id={`quiz-title-${activity.id}`} value={title} onChange={(e) => setTitle(e.target.value)} />
      </div>
      <div>
        <Label htmlFor={`quiz-intro-${activity.id}`}>Introduction</Label>
        <textarea
          id={`quiz-intro-${activity.id}`}
          value={body}
          onChange={(e) => setBody(e.target.value)}
          rows={2}
          className={textareaClasses}
          placeholder="Shown before the learner starts (optional)"
        />
      </div>
      <div className="flex flex-wrap items-end gap-4">
        <label className="text-sm">
          <span className="font-semibold block mb-1">Pass mark (%)</span>
          <Input
            type="number"
            min={0}
            max={100}
            value={passPercent}
            onChange={(e) => setPassPercent(Math.min(100, Math.max(0, Number(e.target.value) || 0)))}
            className="w-24"
          />
        </label>
        <label className="text-sm">
          <span className="font-semibold block mb-1">Max attempts</span>
          <Input
            type="number"
            min={1}
            value={maxAttempts}
            onChange={(e) => setMaxAttempts(e.target.value)}
            placeholder="Unlimited"
            className="w-28"
          />
        </label>
        <label className="flex items-center gap-2 text-sm h-9">
          <input type="checkbox" checked={shuffle} onChange={(e) => setShuffle(e.target.checked)} />
          <Shuffle className="w-4 h-4" />
          Shuffle answers
        </label>
        <Button type="submit" size="sm" disabled={!title.trim() || save.isPending}>
          <Save className="w-4 h-4" />
          Save settings
        </Button>
      </div>
      {save.isError ? <p className="text-sm text-destructive">{(save.error as Error).message}</p> : null}
    </form>
  );
}

function QuizSlots({
  courseId,
  activityId,
  title,
  onChanged,
}: {
  courseId: string;
  activityId: string;
  title: string;
  onChanged: () => void;
}) {
  const slotsQ = useSlots(activityId);
  const categoriesQ = useCategories(courseId);
  const categories = categoriesQ.data?.categories ?? [];
  const [mode, setMode] = useState<'question' | 'draw'>('question');
  const [categoryId, setCategoryId] = useState('');
  const [questionId, setQuestionId] = useState('');
  const [drawCount, setDrawCount] = useState(1);
  const effectiveCategory = categoryId || categories[0]?.id || '';
  const questionsQ = useQuestions(mode === 'question' && effectiveCategory ? effectiveCategory : null);

  const add = useBankMutation(() =>
    mode === 'question'
      ? createSlot(activityId, { question_id: questionId })
      : createSlot(activityId, { category_id: effectiveCategory, draw_count: drawCount })
  );
  const changeDraw = useBankMutation(({ id, n }: { id: string; n: number }) => updateSlot(id, { draw_count: n }));
  const remove = useBankMutation((id: string) => deleteSlot(id));
  const error = (add.error ?? changeDraw.error ?? remove.error) as Error | null;

  if (slotsQ.isLoading) return <LoadingRow />;
  const slots = slotsQ.data?.slots ?? [];
  const problems = slotsQ.data?.problems ?? [];

  return (
    <div className="space-y-3">
      <h4 className="text-sm font-semibold">Questions in this quiz</h4>
      {slots.length > 0 ? (
        <ol className="space-y-1.5">
          {slots.map((s, i) => (
            <li key={s.id} className="flex items-center gap-2 text-sm rounded-md bg-secondary/60 px-2 py-1.5">
              <span className="text-xs text-muted-foreground w-5">{i + 1}.</span>
              {s.question_id ? (
                <span className="flex-1 truncate">{s.question_prompt}</span>
              ) : (
                <span className="flex-1 flex items-center gap-2">
                  Draw
                  <Input
                    type="number"
                    min={1}
                    defaultValue={s.draw_count ?? 1}
                    className="w-16 h-7"
                    onBlur={(e) => {
                      const n = Number(e.target.value);
                      if (n > 0 && n !== s.draw_count) changeDraw.mutate({ id: s.id, n });
                    }}
                  />
                  at random from <strong className="truncate">{s.category_name}</strong>
                </span>
              )}
              <Button variant="ghost" size="sm" onClick={() => remove.mutate(s.id)} disabled={remove.isPending}>
                <X className="w-4 h-4" />
              </Button>
            </li>
          ))}
        </ol>
      ) : null}
      {problems.length > 0 ? (
        <ul className="space-y-1 text-sm rounded-md border border-destructive/40 p-2">
          {problems.map((p) => (
            <li key={p} className="flex items-start gap-2 text-destructive">
              <AlertTriangle className="w-4 h-4 shrink-0 mt-0.5" />
              {p}
            </li>
          ))}
        </ul>
      ) : null}

      {categories.length === 0 ? (
        <p className="text-sm text-muted-foreground">Add questions to the question bank first.</p>
      ) : (
        <form
          className="flex flex-wrap items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            add.mutate(undefined, { onSuccess: () => setQuestionId('') });
          }}
        >
          <select
            value={mode}
            onChange={(e) => setMode(e.target.value as 'question' | 'draw')}
            className="h-9 rounded-md border border-input bg-background px-2 text-sm"
          >
            <option value="question">Add a question</option>
            <option value="draw">Draw at random</option>
          </select>
          {mode === 'draw' ? (
            <Input
              type="number"
              min={1}
              value={drawCount}
              onChange={(e) => setDrawCount(Math.max(1, Number(e.target.value) || 1))}
              className="w-20"
            />
          ) : null}
          <select
            value={effectiveCategory}
            onChange={(e) => {
              setCategoryId(e.target.value);
              setQuestionId('');
            }}
            className="h-9 rounded-md border border-input bg-background px-2 text-sm"
          >
            {categories.map((c) => (
              <option key={c.id} value={c.id}>
                {mode === 'draw' ? `from ${c.name} (${c.question_count})` : c.name}
              </option>
            ))}
          </select>
          {mode === 'question' ? (
            <select
              value={questionId}
              onChange={(e) => setQuestionId(e.target.value)}
              className="h-9 rounded-md border border-input bg-background px-2 text-sm max-w-xs"
            >
              <option value="">Pick a question…</option>
              {(questionsQ.data?.questions ?? []).map((q) => (
                <option key={q.id} value={q.id}>
                  {q.prompt.length > 60 ? `${q.prompt.slice(0, 60)}…` : q.prompt}
                </option>
              ))}
            </select>
          ) : null}
          <Button
            type="submit"
            variant="secondary"
            size="sm"
            disabled={add.isPending || (mode === 'question' ? !questionId : !effectiveCategory)}
          >
            <Plus className="w-4 h-4" />
            Add
          </Button>
        </form>
      )}

      <div className="flex justify-end">
        <ResetQuizButton activityId={activityId} title={title} onDone={onChanged} />
      </div>
      {error ? <p className="text-sm text-destructive">{error.message}</p> : null}
    </div>
  );
}
