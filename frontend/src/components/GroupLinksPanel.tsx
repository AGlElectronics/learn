import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { RefreshCw, Trash2, UsersRound } from 'lucide-react';
import { ApiError } from '@/api/client';
import {
  addGroupLink,
  listGroupLinks,
  listKeycloakGroups,
  removeGroupLink,
  syncGroupLinks,
  type GroupSyncResult,
} from '@/api/groups';
import { Button } from '@/components/ui/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { ErrorRow, LoadingRow } from '@/components/ui/States';

function summary(r: GroupSyncResult) {
  const parts = [`${r.members} member(s)`];
  if (r.enrolled) parts.push(`${r.enrolled} enrolled`);
  if (r.reactivated) parts.push(`${r.reactivated} reactivated`);
  if (r.deactivated) parts.push(`${r.deactivated} deactivated`);
  if (r.users_created) parts.push(`${r.users_created} new user(s)`);
  return parts.join(' · ');
}

/**
 * Links the course to Keycloak groups. Members are enrolled as learners;
 * people who leave every linked group become inactive (progress kept) and
 * come back when they rejoin. Manual enrolments are never touched.
 */
export function GroupLinksPanel({ courseId }: { courseId: string }) {
  const qc = useQueryClient();
  const linksQ = useQuery({
    queryKey: ['group-links', courseId],
    queryFn: ({ signal }) => listGroupLinks(courseId, signal),
  });
  const groupsQ = useQuery({
    queryKey: ['keycloak-groups'],
    queryFn: ({ signal }) => listKeycloakGroups(signal),
    retry: false,
    staleTime: 60_000,
  });
  const [group, setGroup] = useState('');
  const [message, setMessage] = useState<string | null>(null);

  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['group-links', courseId] });
    void qc.invalidateQueries({ queryKey: ['enrolments', courseId] });
  };
  const add = useMutation({
    mutationFn: (name: string) => addGroupLink(courseId, name),
    onSuccess: (res) => {
      setGroup('');
      setMessage(
        res.sync_error
          ? `Linked, but the sync failed: ${res.sync_error}`
          : res.sync
            ? `Linked. ${summary(res.sync)}`
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
      setMessage(`Synced. ${summary(res)}${res.errors.length ? ` · ${res.errors.join('; ')}` : ''}`);
      refresh();
    },
  });

  const keycloakDown = groupsQ.error instanceof ApiError && groupsQ.error.status === 503;
  const links = linksQ.data?.links ?? [];
  const linked = new Set(links.map((l) => l.group_name));
  const choices = (groupsQ.data?.groups ?? []).filter((g) => !linked.has(g.name));
  const error = (add.error ?? remove.error ?? sync.error) as Error | null;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <UsersRound className="w-4 h-4" />
          Enrolment groups
        </CardTitle>
        <CardDescription>
          Members of these groups are enrolled as learners automatically. People who leave become inactive and
          keep their progress.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {linksQ.isLoading ? <LoadingRow message="Loading groups…" /> : null}
        {linksQ.isError ? <ErrorRow message={(linksQ.error as Error).message} /> : null}
        {links.length > 0 ? (
          <ul className="space-y-2">
            {links.map((l) => (
              <li key={l.group_name} className="flex items-start justify-between gap-2 text-sm">
                <div className="min-w-0">
                  <p className="font-medium truncate">{l.group_name}</p>
                  <p className="text-xs text-muted-foreground">
                    {l.members} member(s) · {l.active} enrolled
                    {l.last_synced_at ? ` · synced ${new Date(l.last_synced_at).toLocaleString()}` : ''}
                  </p>
                  {l.last_sync_error ? <p className="text-xs text-destructive">{l.last_sync_error}</p> : null}
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  title="Unlink group"
                  disabled={remove.isPending}
                  onClick={() => {
                    if (
                      confirm(
                        `Unlink "${l.group_name}"? Its members lose access (unless another linked group or a manual enrolment covers them); progress is kept.`
                      )
                    )
                      remove.mutate(l.group_name);
                  }}
                >
                  <Trash2 className="w-4 h-4" />
                </Button>
              </li>
            ))}
          </ul>
        ) : linksQ.isSuccess ? (
          <p className="text-sm text-muted-foreground">No groups linked.</p>
        ) : null}

        <form
          className="space-y-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (group.trim()) add.mutate(group.trim());
          }}
        >
          <Label htmlFor="group-link-name">Link a group</Label>
          {groupsQ.isSuccess ? (
            <select
              id="group-link-name"
              className="w-full h-9 rounded-md border border-input bg-background px-2 text-sm"
              value={group}
              onChange={(e) => setGroup(e.target.value)}
            >
              <option value="">Choose a group…</option>
              {choices.map((g) => (
                <option key={g.id} value={g.name}>
                  {g.path || g.name}
                </option>
              ))}
            </select>
          ) : (
            <Input
              id="group-link-name"
              value={group}
              onChange={(e) => setGroup(e.target.value)}
              placeholder="Group name"
            />
          )}
          {keycloakDown ? (
            <p className="text-xs text-muted-foreground">
              Keycloak sync is not configured; members are enrolled when they sign in.
            </p>
          ) : null}
          <div className="flex gap-2">
            <Button type="submit" size="sm" disabled={!group.trim() || add.isPending}>
              Link group
            </Button>
            {linksQ.data?.sync_available && links.length > 0 ? (
              <Button
                type="button"
                variant="secondary"
                size="sm"
                disabled={sync.isPending}
                onClick={() => sync.mutate()}
              >
                <RefreshCw className={sync.isPending ? 'w-4 h-4 animate-spin' : 'w-4 h-4'} />
                Sync now
              </Button>
            ) : null}
          </div>
        </form>
        {message ? <p className="text-xs text-muted-foreground">{message}</p> : null}
        {error ? <ErrorRow message={error.message} /> : null}
      </CardContent>
    </Card>
  );
}
