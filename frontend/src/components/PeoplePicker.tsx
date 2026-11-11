import { useState, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { searchPeople, type Person } from '@/api/courses';
import { Input, Label } from '@/components/ui/Input';
import { ErrorRow } from '@/components/ui/States';
import { useT } from '@/i18n';

/** Stable key of a picker row (directory hits have no Learn id yet). */
export function personKey(p: Person): string {
  return p.id ?? `dir:${p.directory_id ?? ''}`;
}

/**
 * Searches the people of the caller's organization (and the identity
 * provider's directory when Learn has one). Emails only show for callers
 * the server returns them to (never trainers). actions renders the buttons
 * for one person; people in exclude (user ids) are hidden.
 */
export function PeoplePicker({
  id,
  label,
  exclude,
  actions,
}: {
  id: string;
  label: string;
  exclude?: Set<string>;
  actions: (p: Person) => ReactNode;
}) {
  const t = useT();
  const [q, setQ] = useState('');
  const query = q.trim();
  const peopleQ = useQuery({
    queryKey: ['people', query],
    queryFn: ({ signal }) => searchPeople(query, signal),
    enabled: query.length > 0,
    retry: false,
  });
  const people = (peopleQ.data?.people ?? []).filter((p) => !(p.id && exclude?.has(p.id)));

  return (
    <div>
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} value={q} onChange={(e) => setQ(e.target.value)} placeholder={t('people.searchPlaceholder')} />
      {peopleQ.isError ? <ErrorRow message={(peopleQ.error as Error).message} /> : null}
      {query && peopleQ.data ? (
        <ul className="mt-2 space-y-1" aria-label={t('people.results')}>
          {people.map((p) => (
            <li key={personKey(p)} className="flex items-center justify-between gap-2 text-sm">
              <span className="truncate">
                {p.display_name}
                {p.email ? <span className="text-muted-foreground"> · {p.email}</span> : null}
                {!p.id ? (
                  <span className="ml-1 rounded-full bg-muted text-muted-foreground px-2 py-0.5 text-[11px] font-semibold">
                    {t('people.fromDirectory')}
                  </span>
                ) : null}
              </span>
              <span className="flex gap-1 shrink-0">{actions(p)}</span>
            </li>
          ))}
          {people.length === 0 ? <li className="text-sm text-muted-foreground">{t('people.noMatch')}</li> : null}
        </ul>
      ) : null}
    </div>
  );
}
