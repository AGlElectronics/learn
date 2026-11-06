import { useEffect, useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { CalendarClock, Lock, X } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { updateCourse, type Activity, type Enrolment } from '@/api/courses';
import { dueLabel, setEnrolmentDue, setPrerequisites } from '@/api/rules';
import { useT } from '@/i18n';
import { cn } from '@/lib/utils';

/** "Due 12 Nov 2026" or a red "Overdue since …" pill; nothing without a date. */
export function DueBadge({ dueAt, overdue, className }: { dueAt?: string | null; overdue?: boolean; className?: string }) {
  const t = useT();
  const label = dueLabel(t, dueAt, !!overdue);
  if (!label) return null;
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-pill px-2 py-0.5 text-xs font-medium',
        overdue ? 'bg-destructive/10 text-destructive' : 'bg-secondary text-muted-foreground',
        className
      )}
    >
      <CalendarClock className="w-3 h-3" />
      {label}
    </span>
  );
}

/** What a locked activity still needs, for the learner. */
export function LockedNotice({ needs }: { needs?: string[] }) {
  const t = useT();
  return (
    <div className="rounded-card border border-border bg-card p-6 flex items-start gap-3">
      <Lock className="w-5 h-5 text-muted-foreground shrink-0 mt-0.5" />
      <div>
        <p className="font-heading font-bold">{t('locked.title')}</p>
        <p className="text-sm text-muted-foreground">
          {needs && needs.length > 0
            ? t('locked.finishThese', { items: needs.map((n) => `"${n}"`).join(', ') })
            : t('locked.finishEarlier')}
        </p>
      </div>
    </div>
  );
}

/**
 * PrerequisitesEditor picks which activities of the course must be
 * complete before this one opens. The server rejects cycles.
 */
export function PrerequisitesEditor({
  activity,
  courseActivities,
  onChanged,
}: {
  activity: Activity;
  courseActivities: Activity[];
  onChanged: () => void;
}) {
  const t = useT();
  const current = activity.prerequisites ?? [];
  const save = useMutation({
    mutationFn: (requires: string[]) => setPrerequisites(activity.id, requires),
    onSuccess: onChanged,
  });
  const byId = new Map(courseActivities.map((a) => [a.id, a]));
  const choices = courseActivities.filter((a) => a.id !== activity.id && !current.includes(a.id));

  return (
    <div className="flex flex-wrap items-center gap-1.5 text-xs">
      <span className="inline-flex items-center gap-1 text-muted-foreground">
        <Lock className="w-3 h-3" />
        {t('prereq.opensAfter')}
      </span>
      {current.length === 0 ? <span className="text-muted-foreground">{t('prereq.none')}</span> : null}
      {current.map((id) => (
        <span key={id} className="inline-flex items-center gap-1 rounded-pill border border-border px-2 py-0.5">
          {byId.get(id)?.title ?? t('prereq.unknown')}
          <button
            type="button"
            aria-label={t('prereq.remove')}
            disabled={save.isPending}
            onClick={() => save.mutate(current.filter((x) => x !== id))}
          >
            <X className="w-3 h-3" />
          </button>
        </span>
      ))}
      {choices.length > 0 ? (
        <select
          value=""
          disabled={save.isPending}
          onChange={(e) => {
            if (e.target.value) save.mutate([...current, e.target.value]);
          }}
          className="h-7 rounded-md border border-input bg-background px-2 text-xs"
          aria-label={t('prereq.add')}
        >
          <option value="">{t('prereq.addOption')}</option>
          {choices.map((a) => (
            <option key={a.id} value={a.id}>
              {a.title}
            </option>
          ))}
        </select>
      ) : null}
      {save.isError ? <span className="basis-full text-destructive">{(save.error as Error).message}</span> : null}
    </div>
  );
}

/** Course setting: learners are due this many days after enrolling. */
export function DueDaysControl({
  courseId,
  dueDays,
  onChanged,
}: {
  courseId: string;
  dueDays: number | null;
  onChanged: () => void;
}) {
  const t = useT();
  const [value, setValue] = useState(dueDays === null ? '' : String(dueDays));
  useEffect(() => setValue(dueDays === null ? '' : String(dueDays)), [dueDays]);
  const save = useMutation({
    mutationFn: (due_days: number | null) => updateCourse(courseId, { due_days }),
    onSuccess: onChanged,
  });
  const next = value.trim() ? Number(value) : null;
  return (
    <form
      className="flex flex-wrap items-center gap-2 text-sm"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate(next);
      }}
    >
      <CalendarClock className="w-4 h-4 text-muted-foreground" />
      {t('dueDays.due')}
      <Input
        type="number"
        min={1}
        value={value}
        onChange={(e) => setValue(e.target.value)}
        placeholder="—"
        className="w-20 h-8"
        aria-label={t('dueDays.aria')}
      />
      {t('dueDays.after')}
      {next !== dueDays ? (
        <Button type="submit" size="sm" variant="secondary" disabled={save.isPending}>
          {t('common.save')}
        </Button>
      ) : null}
      <span className="text-xs text-muted-foreground">{t('dueDays.hint')}</span>
      {save.isError ? <span className="basis-full text-sm text-destructive">{(save.error as Error).message}</span> : null}
    </form>
  );
}

function toDateInput(iso: string | null) {
  if (!iso) return '';
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** Staff override of one learner's due date (end of the chosen local day). */
export function EnrolmentDueControl({ enrolment, onChanged }: { enrolment: Enrolment; onChanged: () => void }) {
  const t = useT();
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(toDateInput(enrolment.due_at));
  const save = useMutation({
    mutationFn: (due: string | null) => setEnrolmentDue(enrolment.id, due),
    onSuccess: () => {
      setEditing(false);
      onChanged();
    },
  });
  if (!editing) {
    return (
      <span className="inline-flex flex-wrap items-center gap-1">
        <DueBadge dueAt={enrolment.due_at} overdue={enrolment.overdue} />
        <button
          type="button"
          className="text-xs text-primary hover:underline"
          onClick={() => {
            setValue(toDateInput(enrolment.due_at));
            setEditing(true);
          }}
        >
          {enrolment.due_at ? t('due.change') : t('due.set')}
        </button>
      </span>
    );
  }
  return (
    <span className="inline-flex flex-wrap items-center gap-1">
      <Input type="date" value={value} onChange={(e) => setValue(e.target.value)} className="h-7 w-36 text-xs" />
      <Button
        size="sm"
        variant="secondary"
        disabled={!value || save.isPending}
        onClick={() => save.mutate(new Date(`${value}T23:59:59`).toISOString())}
      >
        {t('common.save')}
      </Button>
      {enrolment.due_at ? (
        <Button size="sm" variant="ghost" disabled={save.isPending} onClick={() => save.mutate(null)}>
          {t('common.clear')}
        </Button>
      ) : null}
      <Button size="sm" variant="ghost" onClick={() => setEditing(false)}>
        {t('common.cancel')}
      </Button>
      {save.isError ? <span className="basis-full text-xs text-destructive">{(save.error as Error).message}</span> : null}
    </span>
  );
}
