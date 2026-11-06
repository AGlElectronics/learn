import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { CalendarCheck, CalendarClock, CheckCircle2, MapPin, QrCode, Users, X } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import {
  bookSlot,
  cancelBooking,
  formatSlotTime,
  getSessionInfo,
  type SessionSignup,
  type SessionSlot,
} from '@/api/sessions';
import { useT, type TFunction } from '@/i18n';
import { cn } from '@/lib/utils';

function statusText(t: TFunction, s: SessionSignup) {
  switch (s.status) {
    case 'booked':
      return t('session.status.booked');
    case 'waitlist':
      return t('session.status.waitlist', { n: s.waitlist_position ?? '?' });
    case 'attended':
      return s.walk_in ? t('session.status.walkIn') : t('session.status.attended');
    case 'no_show':
      return t('session.status.noShow');
  }
}

/**
 * The learner's view of an in-person session: its times with seats left,
 * booking (or joining the waitlist), cancelling until the start, and the
 * reminder to scan the QR at the venue. Attending completes the activity.
 */
export function SessionPlayer({ courseId, activityId }: { courseId: string; activityId: string }) {
  const qc = useQueryClient();
  const t = useT();
  const infoQ = useQuery({
    queryKey: ['session', activityId],
    queryFn: ({ signal }) => getSessionInfo(activityId, signal),
    retry: false,
  });
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['session', activityId] });
    void qc.invalidateQueries({ queryKey: ['course', courseId] });
  };
  const book = useMutation({ mutationFn: (slotId: string) => bookSlot(slotId), onSuccess: refresh });
  const cancel = useMutation({ mutationFn: (slotId: string) => cancelBooking(slotId), onSuccess: refresh });

  if (infoQ.isLoading) return <LoadingRow message={t('session.loading')} />;
  if (infoQ.isError) return <ErrorRow message={(infoQ.error as Error).message} />;
  const info = infoQ.data!;
  const mine = info.my_signup;
  const now = Date.now();
  const error = (book.error ?? cancel.error) as Error | null;

  return (
    <div className="rounded-card border border-border bg-card p-6 space-y-4">
      <div>
        <h2 className="font-heading text-xl font-bold mb-1 flex items-center gap-2">
          <CalendarClock className="w-5 h-5 text-primary" />
          {info.title}
        </h2>
        {info.body ? <p className="text-sm leading-relaxed whitespace-pre-wrap">{info.body}</p> : null}
      </div>

      {mine ? (
        <div
          className={cn(
            'rounded-lg px-3 py-2 text-sm flex items-start gap-2',
            mine.status === 'attended' ? 'bg-success/10 text-success' : 'bg-primary-soft text-primary'
          )}
        >
          {mine.status === 'attended' ? (
            <CheckCircle2 className="w-4 h-4 mt-0.5 shrink-0" />
          ) : (
            <CalendarCheck className="w-4 h-4 mt-0.5 shrink-0" />
          )}
          <span>{statusText(t, mine)}</span>
        </div>
      ) : null}

      {mine && (mine.status === 'booked' || mine.status === 'waitlist') ? (
        <p className="text-xs text-muted-foreground flex items-center gap-1.5">
          <QrCode className="w-4 h-4" />
          {t('session.scanHint')}
        </p>
      ) : null}

      {info.slots.length === 0 ? (
        <p className="text-sm text-muted-foreground">{t('session.noTimes')}</p>
      ) : (
        <ul className="space-y-2">
          {info.slots.map((slot) => (
            <SlotChoice
              key={slot.id}
              slot={slot}
              mine={mine && mine.slot_id === slot.id ? mine : null}
              canBook={!mine || mine.status === 'no_show'}
              started={new Date(slot.starts_at).getTime() <= now}
              ended={new Date(slot.ends_at).getTime() < now}
              busy={book.isPending || cancel.isPending}
              onBook={() => book.mutate(slot.id)}
              onCancel={() => {
                if (confirm(t('session.confirmCancel'))) cancel.mutate(slot.id);
              }}
            />
          ))}
        </ul>
      )}
      {error ? <ErrorRow message={error.message} /> : null}
    </div>
  );
}

function SlotChoice({
  slot,
  mine,
  canBook,
  started,
  ended,
  busy,
  onBook,
  onCancel,
}: {
  slot: SessionSlot;
  mine: SessionSignup | null;
  canBook: boolean;
  started: boolean;
  ended: boolean;
  busy: boolean;
  onBook: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const full = slot.seats_left === 0;
  return (
    <li
      className={cn(
        'flex flex-wrap items-center justify-between gap-2 rounded-lg border px-3 py-2',
        mine ? 'border-primary/60 bg-primary-soft/40' : 'border-border',
        ended && !mine ? 'opacity-60' : ''
      )}
    >
      <div className="min-w-0 text-sm">
        <p className="font-medium">{formatSlotTime(slot)}</p>
        <p className="text-xs text-muted-foreground flex flex-wrap items-center gap-x-3">
          {slot.location ? (
            <span className="inline-flex items-center gap-1">
              <MapPin className="w-3 h-3" />
              {slot.location}
            </span>
          ) : null}
          <span className="inline-flex items-center gap-1">
            <Users className="w-3 h-3" />
            {full ? t('session.full') : t('session.seatsLeft', { left: slot.seats_left, capacity: slot.capacity })}
            {slot.waitlist > 0 ? ` · ${t('session.waiting', { count: slot.waitlist })}` : ''}
          </span>
        </p>
      </div>
      <div className="shrink-0">
        {mine && (mine.status === 'booked' || mine.status === 'waitlist') ? (
          started ? (
            <span className="text-xs text-muted-foreground">{t('session.startedNoCancel')}</span>
          ) : (
            <Button variant="ghost" size="sm" disabled={busy} onClick={onCancel}>
              <X className="w-4 h-4" />
              {mine.status === 'waitlist' ? t('session.leaveWaitlist') : t('session.cancelBooking')}
            </Button>
          )
        ) : canBook && !started ? (
          <Button variant={full ? 'secondary' : 'primary'} size="sm" disabled={busy} onClick={onBook}>
            {full ? t('session.joinWaitlist') : t('session.book')}
          </Button>
        ) : null}
      </div>
    </li>
  );
}
