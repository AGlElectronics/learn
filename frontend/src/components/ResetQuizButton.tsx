import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { RotateCcw } from 'lucide-react';
import { resetAttempts } from '@/api/quizzes';
import { Button } from '@/components/ui/Button';
import { Input, Label } from '@/components/ui/Input';
import { PERMS, useMe } from '@/hooks/useMe';
import { useT } from '@/i18n';

/**
 * Resets a quiz for the current cycle: course managers only, after typing
 * the quiz's title. Past cycles and certificates stay; the reset is audited.
 */
export function ResetQuizButton({
  activityId,
  title,
  onDone,
}: {
  activityId: string;
  title: string;
  onDone: () => void;
}) {
  const t = useT();
  const { can } = useMe();
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState('');
  const [result, setResult] = useState<string | null>(null);
  const reset = useMutation({
    mutationFn: () => resetAttempts(activityId, typed),
    onSuccess: (r) => {
      setOpen(false);
      setTyped('');
      setResult(t('quiz.resetDone', { attempts: r.attempts, completions: r.completions }));
      onDone();
    },
  });
  if (!can(PERMS.courseManage)) return null;

  if (!open) {
    return (
      <div className="flex flex-col items-end gap-1">
        <Button variant="ghost" size="sm" className="text-destructive" onClick={() => setOpen(true)}>
          <RotateCcw className="w-4 h-4" />
          {t('quiz.reset')}
        </Button>
        {result ? <p className="text-xs text-muted-foreground">{result}</p> : null}
      </div>
    );
  }
  const matches = typed.trim() === title.trim();
  return (
    <form
      className="space-y-2 rounded-md border border-destructive/40 p-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (matches) reset.mutate();
      }}
    >
      <p className="text-sm">{t('quiz.resetExplain')}</p>
      <Label htmlFor={`reset-${activityId}`}>{t('quiz.resetType', { title })}</Label>
      <Input id={`reset-${activityId}`} value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" />
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" size="sm" onClick={() => setOpen(false)}>
          {t('common.cancel')}
        </Button>
        <Button type="submit" variant="destructive" size="sm" disabled={!matches || reset.isPending}>
          {t('quiz.resetConfirm')}
        </Button>
      </div>
      {reset.isError ? <p className="text-sm text-destructive">{(reset.error as Error).message}</p> : null}
    </form>
  );
}
