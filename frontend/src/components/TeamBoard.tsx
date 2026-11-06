import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Users, X } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { listKeycloakGroups } from '@/api/groups';
import {
  useMyTeams,
  useSaveTeamBoardSettings,
  useTeamBoard,
  useTeamBoardSettings,
  type TeamBoardTarget,
  type TeamGroupStat,
} from '@/api/teamboard';
import { useT } from '@/i18n';

/** Per-team progress bars. Boards never contain names, only counts. */
export function TeamBars({ groups }: { groups: TeamGroupStat[] }) {
  const t = useT();
  if (groups.length === 0) return <p className="text-sm text-muted-foreground">{t('team.noneSelected')}</p>;
  return (
    <ul className="space-y-3">
      {groups.map((g) =>
        g.percent === null ? (
          <li key={g.group_name}>
            <div className="flex items-baseline justify-between gap-2 text-sm">
              <span className="font-medium truncate">{g.group_name}</span>
              <span className="text-xs text-muted-foreground">{t('team.tooSmall')}</span>
            </div>
            <p className="text-xs text-muted-foreground mt-0.5">{t('team.tooSmallHint')}</p>
          </li>
        ) : (
          <li key={g.group_name}>
            <div className="flex items-baseline justify-between gap-2 text-sm">
              <span className="font-medium truncate">{g.group_name}</span>
              <span className="font-semibold tabular-nums">{g.percent}%</span>
            </div>
            <div className="h-2 rounded-pill bg-secondary overflow-hidden mt-1">
              <div className="h-full rounded-pill bg-primary transition-all" style={{ width: `${g.percent}%` }} />
            </div>
            <p className="text-xs text-muted-foreground mt-0.5">
              {t('team.completeOf', { done: g.complete_count ?? 0, count: g.members_count ?? 0 })}
            </p>
          </li>
        )
      )}
    </ul>
  );
}

/** Learner view in the course player; renders nothing unless the board is on. */
export function TeamBoardCard({ courseId }: { courseId: string }) {
  const t = useT();
  const q = useTeamBoard('course', courseId);
  if (!q.data || !q.data.enabled) return null;
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Users className="w-4 h-4 text-primary" />
          {t('team.progressTitle')}
        </CardTitle>
        <CardDescription>{t('team.progressDescription')}</CardDescription>
      </CardHeader>
      <CardContent>
        <TeamBars groups={q.data.groups} />
      </CardContent>
    </Card>
  );
}

/** "Your team: 87% trained" lines on My courses. */
export function MyTeamsSummary() {
  const t = useT();
  const q = useMyTeams();
  const teams = q.data?.teams ?? [];
  if (teams.length === 0) return null;
  return (
    <div className="mt-10">
      <h2 className="font-heading text-lg font-bold mb-3 flex items-center gap-2">
        <Users className="w-5 h-5 text-primary" />
        {t('team.yours')}
      </h2>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {teams.map((team) => (
          <Card key={`${team.target_type}-${team.target_id}`}>
            <CardHeader>
              <CardTitle>{team.title}</CardTitle>
              <CardDescription>{team.target_type === 'course' ? t('team.course') : t('team.qualification')}</CardDescription>
            </CardHeader>
            <CardContent className="space-y-1">
              {team.groups.map((g) => (
                <p key={g.group_name} className="text-sm">
                  {t('team.yourTeam')} <span className="font-medium">{g.group_name}</span>:{' '}
                  {g.percent === null ? (
                    <span className="text-muted-foreground">{t('team.tooSmall')}</span>
                  ) : (
                    <>
                      <span className="font-semibold">{g.percent}%</span>{' '}
                      {team.target_type === 'course' ? t('team.trained') : t('team.qualified')}
                    </>
                  )}
                </p>
              ))}
            </CardContent>
          </Card>
        ))}
      </div>
    </div>
  );
}

/**
 * Staff settings: turn the board on and choose which Keycloak groups it
 * shows, with a live preview.
 */
export function TeamBoardSettingsPanel({
  target,
  id,
  bare = false,
}: {
  target: TeamBoardTarget;
  id: string;
  /** Render without the surrounding card (inside another card). */
  bare?: boolean;
}) {
  const t = useT();
  const settingsQ = useTeamBoardSettings(target, id);
  const boardQ = useTeamBoard(target, id);
  const save = useSaveTeamBoardSettings(target, id);
  const groupsQ = useQuery({
    queryKey: ['keycloak-groups'],
    queryFn: ({ signal }) => listKeycloakGroups(signal),
    retry: false,
    staleTime: 5 * 60_000,
  });
  const [draft, setDraft] = useState('');
  const s = settingsQ.data;
  const listId = `team-groups-${target}-${id}`;

  const addGroup = () => {
    const name = draft.trim();
    if (!s || !name || s.groups.includes(name)) return;
    save.mutate({ groups: [...s.groups, name] }, { onSuccess: () => setDraft('') });
  };

  const body = settingsQ.isLoading ? (
    <LoadingRow />
  ) : settingsQ.isError || !s ? (
    <ErrorRow message={t('team.settingsError')} />
  ) : (
    <div className="space-y-3">
      <Button
        variant={s.enabled ? 'primary' : 'secondary'}
        size="sm"
        disabled={save.isPending}
        onClick={() => save.mutate({ enabled: !s.enabled })}
      >
        {s.enabled ? t('team.boardOn') : t('team.boardOff')}
      </Button>
      <div>
        <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground mb-1.5">{t('team.shown')}</p>
        {s.groups.length > 0 ? (
          <ul className="flex flex-wrap gap-1.5 mb-2">
            {s.groups.map((g) => (
              <li
                key={g}
                className="inline-flex items-center gap-1 rounded-pill bg-secondary px-2.5 py-0.5 text-sm"
              >
                {g}
                <button
                  type="button"
                  className="text-muted-foreground hover:text-destructive"
                  title={t('team.remove', { name: g })}
                  disabled={save.isPending}
                  onClick={() => save.mutate({ groups: s.groups.filter((x) => x !== g) })}
                >
                  <X className="w-3.5 h-3.5" />
                </button>
              </li>
            ))}
          </ul>
        ) : null}
        <form
          className="flex gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            addGroup();
          }}
        >
          <Input
            list={listId}
            value={draft}
            placeholder={t('team.groupPlaceholder')}
            onChange={(e) => setDraft(e.target.value)}
          />
          <datalist id={listId}>
            {(groupsQ.data?.groups ?? []).map((g) => (
              <option key={g.id} value={g.name} />
            ))}
          </datalist>
          <Button type="submit" size="sm" variant="secondary" disabled={save.isPending || !draft.trim()}>
            {t('common.add')}
          </Button>
        </form>
      </div>
      {save.isError ? <p className="text-sm text-destructive">{(save.error as Error).message}</p> : null}
      {boardQ.data && boardQ.data.groups.length > 0 ? (
        <div className="border-t border-border pt-3">
          <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground mb-2">{t('team.preview')}</p>
          <TeamBars groups={boardQ.data.groups} />
        </div>
      ) : null}
    </div>
  );

  if (bare) return body;
  return (
    <Card className="h-fit">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Users className="w-4 h-4 text-primary" />
          {t('team.board')}
        </CardTitle>
        <CardDescription>
          {t('team.boardDescription')}
        </CardDescription>
      </CardHeader>
      <CardContent>{body}</CardContent>
    </Card>
  );
}
