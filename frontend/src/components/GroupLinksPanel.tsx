import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, RefreshCw, Trash2, UsersRound } from 'lucide-react';
import {
  addGroupLink,
  listGroupLinks,
  listKeycloakGroups,
  removeGroupLink,
  syncGroupLinks,
  type GroupDirectoryMode,
  type GroupSyncResult,
} from '@/api/groups';
import { Button } from '@/components/ui/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { useT } from '@/i18n';

export function syncSummary(r: GroupSyncResult) {
  const parts = [`${r.members} member(s)`];
  if (r.enrolled) parts.push(`${r.enrolled} enrolled`);
  if (r.reactivated) parts.push(`${r.reactivated} reactivated`);
  if (r.deactivated) parts.push(`${r.deactivated} deactivated`);
  if (r.users_created) parts.push(`${r.users_created} new user(s)`);
  return parts.join(' · ');
}

/**
 * Shown when Learn has no group directory (GROUP_DIRECTORY=off or no
 * Keycloak admin client): links then only match the groups people sign in
 * with, so members appear one by one as they sign in.
 */
export function GroupDirectoryWarning({ mode, available }: { mode?: GroupDirectoryMode; available?: boolean }) {
  const t = useT();
  if (available !== false) return null;
  return (
    <p role="alert" className="flex gap-2 rounded-md border border-border bg-muted px-3 py-2 text-xs">
      <AlertTriangle className="w-4 h-4 shrink-0 text-warning" />
      <span>{mode === 'off' ? t('groups.directoryOff') : t('groups.directoryMissing')}</span>
    </p>
  );
}

/**
 * The group picker: a list of the groups the caller may link when the
 * directory answers, else a free-text full path.
 */
export function GroupPathInput({
  id,
  value,
  onChange,
  exclude,
}: {
  id: string;
  value: string;
  onChange: (path: string) => void;
  exclude: Set<string>;
}) {
  const t = useT();
  const groupsQ = useQuery({
    queryKey: ['keycloak-groups'],
    queryFn: ({ signal }) => listKeycloakGroups(signal),
    retry: false,
    staleTime: 60_000,
  });
  const key = (p: string) => p.trim().replace(/^\//, '').toLowerCase();
  const choices = (groupsQ.data?.groups ?? []).filter((g) => !exclude.has(key(g.path)));
  return (
    <>
      <Label htmlFor={id}>{t('groups.link')}</Label>
      {groupsQ.isSuccess ? (
        <select
          id={id}
          className="w-full h-9 rounded-md border border-input bg-background px-2 text-sm"
          value={value}
          onChange={(e) => onChange(e.target.value)}
        >
          <option value="">{t('groups.choose')}</option>
          {choices.map((g) => (
            <option key={g.id} value={g.path}>
              {g.path}
            </option>
          ))}
        </select>
      ) : (
        <Input id={id} value={value} onChange={(e) => onChange(e.target.value)} placeholder="/learn/cohort-a" />
      )}
      {groupsQ.data?.limited ? (
        <p className="text-xs text-muted-foreground">
          {t('groups.limited', { patterns: (groupsQ.data.linkable ?? []).join(', ') })}
        </p>
      ) : null}
    </>
  );
}

/**
 * Links the course to directory groups by full path. Members are enrolled
 * as learners; people who leave every linked group become inactive
 * (progress kept) and come back when they rejoin. Manual enrolments are
 * never touched.
 */
export function GroupLinksPanel({ courseId, archived = false }: { courseId: string; archived?: boolean }) {
  const qc = useQueryClient();
  const t = useT();
  const linksQ = useQuery({
    queryKey: ['group-links', courseId],
    queryFn: ({ signal }) => listGroupLinks(courseId, signal),
  });
  const [group, setGroup] = useState('');
  const [message, setMessage] = useState<string | null>(null);

  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['group-links', courseId] });
    void qc.invalidateQueries({ queryKey: ['enrolments', courseId] });
  };
  const add = useMutation({
    mutationFn: (path: string) => addGroupLink(courseId, path),
    onSuccess: (res) => {
      setGroup('');
      setMessage(
        res.sync_error
          ? `Linked, but the sync failed: ${res.sync_error}`
          : res.sync
            ? `Linked. ${syncSummary(res.sync)}`
            : 'Linked. Members are enrolled as they sign in.'
      );
      refresh();
    },
  });
  const remove = useMutation({
    mutationFn: (name: string) => removeGroupLink(courseId, name),
    onSuccess: (res) => {
      setMessage(`Unlinked. ${res.deactivated} enrolment(s) made inactive.`);
      refresh();
    },
  });
  const sync = useMutation({
    mutationFn: () => syncGroupLinks(courseId),
    onSuccess: (res) => {
      setMessage(`Synced. ${syncSummary(res)}${res.errors.length ? ` · ${res.errors.join('; ')}` : ''}`);
      refresh();
    },
  });

  const links = linksQ.data?.links ?? [];
  const linked = new Set(links.map((l) => l.group_name));
  const error = (add.error ?? remove.error ?? sync.error) as Error | null;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <UsersRound className="w-4 h-4" />
          {t('groups.title')}
        </CardTitle>
        <CardDescription>{t('groups.description')}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <GroupDirectoryWarning mode={linksQ.data?.directory} available={linksQ.data?.sync_available} />
        {linksQ.isLoading ? <LoadingRow message="Loading groups…" /> : null}
        {linksQ.isError ? <ErrorRow message={(linksQ.error as Error).message} /> : null}
        {links.length > 0 ? (
          <ul className="space-y-2">
            {links.map((l) => (
              <li key={l.group_name} className="flex items-start justify-between gap-2 text-sm">
                <div className="min-w-0">
                  <p className="font-medium truncate">{l.group_path}</p>
                  <p className="text-xs text-muted-foreground">
                    {l.members} member(s) · {l.active} enrolled
                    {l.last_synced_at ? ` · synced ${new Date(l.last_synced_at).toLocaleString()}` : ''}
                  </p>
                  {!l.path_resolved ? <p className="text-xs text-warning">{t('groups.unresolved')}</p> : null}
                  {l.last_sync_error ? <p className="text-xs text-destructive">{l.last_sync_error}</p> : null}
                </div>
                {!archived ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    title="Unlink group"
                    disabled={remove.isPending}
                    onClick={() => {
                      if (
                        confirm(
                          `Unlink "${l.group_path}"? Its members lose access (unless another linked group or a manual enrolment covers them); progress is kept.`
                        )
                      )
                        remove.mutate(l.group_name);
                    }}
                  >
                    <Trash2 className="w-4 h-4" />
                  </Button>
                ) : null}
              </li>
            ))}
          </ul>
        ) : linksQ.isSuccess ? (
          <p className="text-sm text-muted-foreground">No groups linked.</p>
        ) : null}

        {!archived ? (
          <form
            className="space-y-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (group.trim()) add.mutate(group.trim());
            }}
          >
            <GroupPathInput id="group-link-name" value={group} onChange={setGroup} exclude={linked} />
            <div className="flex gap-2">
              <Button type="submit" size="sm" disabled={!group.trim() || add.isPending}>
                {t('groups.linkButton')}
              </Button>
              {linksQ.data?.sync_available && links.length > 0 ? (
                <Button type="button" variant="secondary" size="sm" disabled={sync.isPending} onClick={() => sync.mutate()}>
                  <RefreshCw className={sync.isPending ? 'w-4 h-4 animate-spin' : 'w-4 h-4'} />
                  Sync now
                </Button>
              ) : null}
            </div>
          </form>
        ) : null}
        {message ? <p className="text-xs text-muted-foreground">{message}</p> : null}
        {error ? <ErrorRow message={error.message} /> : null}
      </CardContent>
    </Card>
  );
}
