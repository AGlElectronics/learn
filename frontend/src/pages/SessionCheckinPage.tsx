import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useParams } from '@tanstack/react-router';
import { ArrowLeft, Check, RotateCcw, UserX } from 'lucide-react';
import { QRCodeSVG } from 'qrcode.react';
import { Button, LinkButton } from '@/components/ui/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/Card';
import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { formatSlotTime, getCheckinCode, getRoster, setSignupStatus, type RosterEntry } from '@/api/sessions';
import { useT, type MessageKey } from '@/i18n';
import { cn } from '@/lib/utils';

const STATUS_LABEL: Record<RosterEntry['status'], MessageKey> = {
  booked: 'roster.booked',
  waitlist: 'roster.waitlist',
  attended: 'roster.attended',
  no_show: 'roster.noShow',
};

function absoluteUrl(url: string) {
  return url.startsWith('/') ? `${window.location.origin}${url}` : url;
}

/**
 * Staff screen for the venue: a big QR that rotates every 30 seconds
 * (fetched every 10 s so it never shows a stale code), the code in text
 * for people typing it, and the live roster with manual attendance buttons.
 */
export function SessionCheckinPage() {
  const { slotId } = useParams({ from: '/app/teach/sessions/$slotId/checkin' });
  const qc = useQueryClient();
  const t = useT();
  const codeQ = useQuery({
    queryKey: ['checkin-code', slotId],
    queryFn: ({ signal }) => getCheckinCode(slotId, signal),
    refetchInterval: 10_000,
    refetchIntervalInBackground: true,
    retry: false,
  });
  const rosterQ = useQuery({
    queryKey: ['roster', slotId],
    queryFn: ({ signal }) => getRoster(slotId, signal),
    refetchInterval: 5_000,
    retry: false,
  });
  const mark = useMutation({
    mutationFn: ({ id, status }: { id: string; status: 'attended' | 'no_show' | 'booked' }) =>
      setSignupStatus(id, status),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['roster', slotId] }),
  });

  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);

  if (rosterQ.isLoading) {
    return (
      <PageContainer>
        <LoadingRow message={t('session.loading')} />
      </PageContainer>
    );
  }
  if (rosterQ.isError) {
    return (
      <PageContainer>
        <ErrorRow message={t('roster.loadError')} />
      </PageContainer>
    );
  }
  const roster = rosterQ.data!;
  const code = codeQ.data;
  const secondsLeft = code ? Math.max(0, Math.ceil((new Date(code.expires_at).getTime() - now) / 1000)) : 0;
  const attended = roster.signups.filter((s) => s.status === 'attended').length;
  const expected = roster.signups.filter((s) => s.status !== 'waitlist').length;

  return (
    <PageContainer>
      <PageHeader
        title={t('roster.title', { title: roster.activity_title })}
        description={`${formatSlotTime(roster.slot)}${roster.slot.location ? ` · ${roster.slot.location}` : ''}`}
        actions={
          <LinkButton to={`/teach/${roster.course_id}`} variant="ghost" size="sm">
            <ArrowLeft className="w-4 h-4" />
            {t('roster.back')}
          </LinkButton>
        }
      />
      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_380px]">
        <Card>
          <CardContent className="flex flex-col items-center gap-4 py-8">
            {codeQ.isError ? (
              <ErrorRow message={(codeQ.error as Error).message} />
            ) : code ? (
              <>
                <div className="rounded-xl bg-white p-4">
                  <QRCodeSVG value={absoluteUrl(code.url)} size={360} level="M" />
                </div>
                <p className="font-mono text-4xl font-bold tracking-[0.3em]">{code.code}</p>
                <p className="text-sm text-muted-foreground">
                  {t('roster.scanHint', { seconds: secondsLeft })}
                </p>
              </>
            ) : (
              <LoadingRow message={t('roster.gettingCode')} />
            )}
            <p className="text-xs text-muted-foreground text-center max-w-sm">
              {t('roster.windowHint')}
            </p>
          </CardContent>
        </Card>

        <Card className="h-fit">
          <CardHeader>
            <CardTitle>{t('roster.roster')}</CardTitle>
            <CardDescription>
              {t('roster.summary', { attended, expected, seats: roster.slot.capacity })}
            </CardDescription>
          </CardHeader>
          <CardContent>
            {roster.signups.length === 0 ? (
              <p className="text-sm text-muted-foreground">{t('roster.empty')}</p>
            ) : (
              <ul className="space-y-2">
                {roster.signups.map((s) => (
                  <li key={s.id} className="flex items-center justify-between gap-2 text-sm">
                    <div className="min-w-0">
                      <p className="font-medium truncate">{s.display_name}</p>
                      <p
                        className={cn(
                          'text-xs',
                          s.status === 'attended' ? 'text-success' : 'text-muted-foreground'
                        )}
                      >
                        {t(STATUS_LABEL[s.status])}
                        {s.status === 'waitlist' && s.waitlist_position ? ` #${s.waitlist_position}` : ''}
                        {s.walk_in ? ` · ${t('checkin.walkIn')}` : ''}
                      </p>
                    </div>
                    <div className="flex gap-1 shrink-0">
                      {s.status !== 'attended' ? (
                        <Button
                          variant="secondary"
                          size="sm"
                          disabled={mark.isPending}
                          onClick={() => mark.mutate({ id: s.id, status: 'attended' })}
                          title={t('roster.markAttended')}
                        >
                          <Check className="w-4 h-4" />
                        </Button>
                      ) : null}
                      {s.status !== 'no_show' ? (
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={mark.isPending}
                          onClick={() => mark.mutate({ id: s.id, status: 'no_show' })}
                          title={t('roster.markNoShow')}
                        >
                          <UserX className="w-4 h-4" />
                        </Button>
                      ) : null}
                      {s.status === 'attended' || s.status === 'no_show' ? (
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={mark.isPending}
                          onClick={() => mark.mutate({ id: s.id, status: 'booked' })}
                          title={t('roster.backToBooked')}
                        >
                          <RotateCcw className="w-4 h-4" />
                        </Button>
                      ) : null}
                    </div>
                  </li>
                ))}
              </ul>
            )}
            {mark.isError ? <ErrorRow message={(mark.error as Error).message} /> : null}
          </CardContent>
        </Card>
      </div>
    </PageContainer>
  );
}
