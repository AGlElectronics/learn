import { useEffect, useRef, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useParams, useSearch } from '@tanstack/react-router';
import { CheckCircle2, QrCode } from 'lucide-react';
import { Button, LinkButton } from '@/components/ui/Button';
import { Card, CardContent } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { PageContainer } from '@/components/ui/PageHeader';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { checkIn, formatSlotTime } from '@/api/sessions';
import { useT } from '@/i18n';

/**
 * Where the venue QR lands: /checkin/{slot}?code=… checks the signed-in
 * learner in straight away. Without a code (or after a stale one) the
 * learner can type the code shown under the QR.
 */
export function CheckinPage() {
  const { slotId } = useParams({ from: '/app/checkin/$slotId' });
  const { code } = useSearch({ from: '/app/checkin/$slotId' });
  const qc = useQueryClient();
  const t = useT();
  const [typed, setTyped] = useState('');
  const submit = useMutation({
    mutationFn: (c: string) => checkIn(slotId, c),
    onSuccess: (res) => {
      void qc.invalidateQueries({ queryKey: ['course', res.course_id] });
      void qc.invalidateQueries({ queryKey: ['session', res.signup.activity_id] });
      void qc.invalidateQueries({ queryKey: ['courses'] });
    },
  });
  const sent = useRef(false);
  useEffect(() => {
    if (code && !sent.current) {
      sent.current = true;
      submit.mutate(code);
    }
  }, [code, submit]);

  const res = submit.data;
  return (
    <PageContainer>
      <Card className="max-w-md mx-auto">
        <CardContent className="py-8 space-y-4 text-center">
          {submit.isPending ? <LoadingRow message={t('checkin.checking')} /> : null}
          {res ? (
            <>
              <CheckCircle2 className="w-14 h-14 text-success mx-auto" />
              <h1 className="font-heading text-2xl font-bold">
                {res.already_attended ? t('checkin.already') : t('checkin.done')}
              </h1>
              <p className="text-sm text-muted-foreground">
                {res.activity_title} · {formatSlotTime(res.slot)}
                {res.signup.walk_in ? ` · ${t('checkin.walkIn')}` : ''}
              </p>
              <LinkButton to={`/courses/${res.course_id}`} variant="secondary" size="sm">
                {t('checkin.openCourse')}
              </LinkButton>
            </>
          ) : null}
          {submit.isError ? <ErrorRow message={(submit.error as Error).message} /> : null}
          {!res && !submit.isPending ? (
            <form
              className="space-y-2 text-left"
              onSubmit={(e) => {
                e.preventDefault();
                if (typed.trim()) submit.mutate(typed.trim());
              }}
            >
              <div className="flex justify-center">
                <QrCode className="w-10 h-10 text-muted-foreground" />
              </div>
              <Label htmlFor="checkin-code">{t('checkin.codeLabel')}</Label>
              <Input
                id="checkin-code"
                value={typed}
                onChange={(e) => setTyped(e.target.value.toUpperCase())}
                placeholder="ABCD2345"
                autoComplete="off"
                className="font-mono tracking-widest"
              />
              <Button type="submit" className="w-full" disabled={!typed.trim()}>
                {t('checkin.submit')}
              </Button>
            </form>
          ) : null}
        </CardContent>
      </Card>
    </PageContainer>
  );
}
