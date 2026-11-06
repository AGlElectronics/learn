import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { CalendarClock, ChevronDown, ChevronRight, Plus, QrCode, Save, Trash2, X } from 'lucide-react';
import { Button, LinkButton } from '@/components/ui/Button';
import { Input, Label } from '@/components/ui/Input';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { updateActivity, type Activity } from '@/api/courses';
import {
  createSessionSlot,
  deleteSessionSlot,
  formatSlotTime,
  getSessionInfo,
  updateSessionSlot,
  type SessionSlot,
  type SlotInput,
} from '@/api/sessions';

const textareaClasses =
  'w-full rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:border-primary';

/** ISO timestamp -> value for <input type="datetime-local"> in local time. */
function toLocalInput(iso: string) {
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function fromLocalInput(value: string) {
  return new Date(value).toISOString();
}

/**
 * One in-person session in the course editor: title and joining notes, and
 * its time slots (start, end, location, capacity). Each slot links to the
 * check-in screen staff show at the venue.
 */
export function SessionEditor({
  activity,
  onChanged,
  onDelete,
}: {
  activity: Activity;
  onChanged: () => void;
  onDelete: () => void;
}) {
  const [open, setOpen] = useState(false);
  const infoQ = useQuery({
    queryKey: ['session', activity.id],
    queryFn: ({ signal }) => getSessionInfo(activity.id, signal),
    enabled: open,
  });
  const slots = infoQ.data?.slots ?? [];

  return (
    <div className="rounded-lg border border-border">
      <div className="flex items-center justify-between gap-2 px-3 py-2">
        <button
          type="button"
          className="flex items-center gap-2 min-w-0 text-left"
          onClick={() => setOpen((v) => !v)}
        >
          {open ? <ChevronDown className="w-4 h-4 shrink-0" /> : <ChevronRight className="w-4 h-4 shrink-0" />}
          <CalendarClock className="w-4 h-4 shrink-0 text-primary" />
          <span className="text-sm font-medium truncate">{activity.title}</span>
          <span className="text-[11px] font-semibold rounded-pill bg-primary-soft text-primary px-2 py-0.5 shrink-0">
            In-person session
          </span>
        </button>
        <Button
          variant="ghost"
          size="sm"
          className="text-destructive shrink-0"
          onClick={() => {
            if (confirm(`Delete session "${activity.title}" with all its times and bookings?`)) onDelete();
          }}
        >
          <Trash2 className="w-4 h-4" />
        </Button>
      </div>
      {open ? (
        <div className="border-t border-border p-3 space-y-4">
          <SessionSettingsForm activity={activity} onChanged={onChanged} />
          <div className="space-y-2">
            <h4 className="text-sm font-semibold">Times</h4>
            {infoQ.isLoading ? <LoadingRow /> : null}
            {infoQ.isError ? <ErrorRow message="Could not load the session times." /> : null}
            {infoQ.data && slots.length === 0 ? (
              <p className="text-sm text-muted-foreground">No times yet. Learners can book once you add one.</p>
            ) : null}
            {slots.map((slot) => (
              <SlotRow key={slot.id} activityId={activity.id} slot={slot} />
            ))}
            <AddSlotForm activityId={activity.id} />
          </div>
        </div>
      ) : null}
    </div>
  );
}

function SessionSettingsForm({ activity, onChanged }: { activity: Activity; onChanged: () => void }) {
  const [title, setTitle] = useState(activity.title);
  const [body, setBody] = useState(activity.body);
  const save = useMutation({
    mutationFn: () => updateActivity(activity.id, { title, body }),
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
        <Label htmlFor={`session-title-${activity.id}`}>Title</Label>
        <Input id={`session-title-${activity.id}`} value={title} onChange={(e) => setTitle(e.target.value)} />
      </div>
      <div>
        <Label htmlFor={`session-body-${activity.id}`}>What to know</Label>
        <textarea
          id={`session-body-${activity.id}`}
          value={body}
          onChange={(e) => setBody(e.target.value)}
          rows={2}
          className={textareaClasses}
          placeholder="Where to meet, what to bring (optional)"
        />
      </div>
      <Button type="submit" size="sm" variant="secondary" disabled={!title.trim() || save.isPending}>
        <Save className="w-4 h-4" />
        Save
      </Button>
      {save.isError ? <p className="text-sm text-destructive">{(save.error as Error).message}</p> : null}
    </form>
  );
}

function SlotFields({
  idPrefix,
  value,
  onChange,
}: {
  idPrefix: string;
  value: { start: string; end: string; location: string; capacity: string };
  onChange: (v: { start: string; end: string; location: string; capacity: string }) => void;
}) {
  return (
    <div className="grid gap-2 sm:grid-cols-2">
      <div>
        <Label htmlFor={`${idPrefix}-start`}>Starts</Label>
        <Input
          id={`${idPrefix}-start`}
          type="datetime-local"
          value={value.start}
          onChange={(e) => onChange({ ...value, start: e.target.value })}
        />
      </div>
      <div>
        <Label htmlFor={`${idPrefix}-end`}>Ends</Label>
        <Input
          id={`${idPrefix}-end`}
          type="datetime-local"
          value={value.end}
          onChange={(e) => onChange({ ...value, end: e.target.value })}
        />
      </div>
      <div>
        <Label htmlFor={`${idPrefix}-location`}>Location</Label>
        <Input
          id={`${idPrefix}-location`}
          value={value.location}
          onChange={(e) => onChange({ ...value, location: e.target.value })}
          placeholder="Hall A, main entrance"
        />
      </div>
      <div>
        <Label htmlFor={`${idPrefix}-capacity`}>Seats</Label>
        <Input
          id={`${idPrefix}-capacity`}
          type="number"
          min={1}
          value={value.capacity}
          onChange={(e) => onChange({ ...value, capacity: e.target.value })}
        />
      </div>
    </div>
  );
}

function toInput(v: { start: string; end: string; location: string; capacity: string }): SlotInput | null {
  if (!v.start || !v.end || !(Number(v.capacity) > 0)) return null;
  return {
    starts_at: fromLocalInput(v.start),
    ends_at: fromLocalInput(v.end),
    location: v.location,
    capacity: Number(v.capacity),
  };
}

function AddSlotForm({ activityId }: { activityId: string }) {
  const qc = useQueryClient();
  const [adding, setAdding] = useState(false);
  const [value, setValue] = useState({ start: '', end: '', location: '', capacity: '20' });
  const add = useMutation({
    mutationFn: (input: SlotInput) => createSessionSlot(activityId, input),
    onSuccess: () => {
      setAdding(false);
      setValue({ start: '', end: '', location: value.location, capacity: value.capacity });
      void qc.invalidateQueries({ queryKey: ['session', activityId] });
    },
  });
  if (!adding) {
    return (
      <Button variant="ghost" size="sm" onClick={() => setAdding(true)}>
        <Plus className="w-4 h-4" />
        Add a time
      </Button>
    );
  }
  const input = toInput(value);
  return (
    <form
      className="space-y-2 rounded-lg border border-border p-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (input) add.mutate(input);
      }}
    >
      <SlotFields idPrefix={`new-slot-${activityId}`} value={value} onChange={setValue} />
      <div className="flex gap-2">
        <Button type="submit" size="sm" disabled={!input || add.isPending}>
          <Plus className="w-4 h-4" />
          Add time
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={() => setAdding(false)}>
          Cancel
        </Button>
      </div>
      {add.isError ? <p className="text-sm text-destructive">{(add.error as Error).message}</p> : null}
    </form>
  );
}

function SlotRow({ activityId, slot }: { activityId: string; slot: SessionSlot }) {
  const qc = useQueryClient();
  const refresh = () => void qc.invalidateQueries({ queryKey: ['session', activityId] });
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState({
    start: toLocalInput(slot.starts_at),
    end: toLocalInput(slot.ends_at),
    location: slot.location,
    capacity: String(slot.capacity),
  });
  const save = useMutation({
    mutationFn: (input: SlotInput) => updateSessionSlot(slot.id, input),
    onSuccess: () => {
      setEditing(false);
      refresh();
    },
  });
  const remove = useMutation({ mutationFn: () => deleteSessionSlot(slot.id), onSuccess: refresh });

  if (editing) {
    const input = toInput(value);
    return (
      <form
        className="space-y-2 rounded-lg border border-primary/40 p-3"
        onSubmit={(e) => {
          e.preventDefault();
          if (input) save.mutate(input);
        }}
      >
        <SlotFields idPrefix={`slot-${slot.id}`} value={value} onChange={setValue} />
        <div className="flex gap-2">
          <Button type="submit" size="sm" disabled={!input || save.isPending}>
            <Save className="w-4 h-4" />
            Save
          </Button>
          <Button type="button" variant="ghost" size="sm" onClick={() => setEditing(false)}>
            <X className="w-4 h-4" />
            Cancel
          </Button>
        </div>
        {save.isError ? <p className="text-sm text-destructive">{(save.error as Error).message}</p> : null}
      </form>
    );
  }
  const people = slot.booked + slot.waitlist;
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border px-3 py-2">
      <div className="min-w-0 text-sm">
        <p className="font-medium">{formatSlotTime(slot)}</p>
        <p className="text-xs text-muted-foreground">
          {slot.location ? `${slot.location} · ` : ''}
          {slot.booked}/{slot.capacity} booked
          {slot.waitlist > 0 ? ` · ${slot.waitlist} waiting` : ''}
        </p>
      </div>
      <div className="flex gap-1 shrink-0">
        <LinkButton to={`/teach/sessions/${slot.id}/checkin`} variant="secondary" size="sm">
          <QrCode className="w-4 h-4" />
          Check-in
        </LinkButton>
        <Button variant="ghost" size="sm" onClick={() => setEditing(true)}>
          Edit
        </Button>
        <Button
          variant="ghost"
          size="sm"
          className="text-destructive"
          disabled={remove.isPending}
          onClick={() => {
            const warn = people > 0 ? ` ${people} booking(s) will be removed; people already marked attended keep their completion.` : '';
            if (confirm(`Delete this time?${warn}`)) remove.mutate();
          }}
        >
          <Trash2 className="w-4 h-4" />
        </Button>
      </div>
      {remove.isError ? <p className="text-sm text-destructive w-full">{(remove.error as Error).message}</p> : null}
    </div>
  );
}
