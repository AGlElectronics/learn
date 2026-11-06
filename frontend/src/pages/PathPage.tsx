import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate, useParams } from '@tanstack/react-router';
import { ArrowDown, ArrowLeft, ArrowUp, RefreshCw, Trash2, UserPlus, UsersRound } from 'lucide-react';
import { ApiError } from '@/api/client';
import { listTeachCourses, searchPeople } from '@/api/courses';
import { listKeycloakGroups, type GroupSyncResult } from '@/api/groups';
import {
  addPathGroupLink,
  deletePath,
  enrolInPath,
  getPath,
  listPathEnrolments,
  listPathGroupLinks,
  removePathEnrolment,
  removePathGroupLink,
  setPathCourses,
  syncPathGroupLinks,
  updatePath,
  type LearningPath,
} from '@/api/paths';
import { listQualifications } from '@/api/qualifications';
import { Button, LinkButton } from '@/components/ui/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { ErrorRow, LoadingRow } from '@/components/ui/States';

const selectClasses = 'w-full h-9 rounded-md border border-input bg-background px-2 text-sm';

function syncSummary(r: GroupSyncResult) {
  const parts = [`${r.members} member(s)`];
  if (r.enrolled) parts.push(`${r.enrolled} enrolled`);
  if (r.reactivated) parts.push(`${r.reactivated} reactivated`);
  if (r.deactivated) parts.push(`${r.deactivated} deactivated`);
  return parts.join(' · ');
}

function SettingsCard({ path, onSaved }: { path: LearningPath; onSaved: () => void }) {
  const [name, setName] = useState(path.name);
  const [description, setDescription] = useState(path.description);
  useEffect(() => {
    setName(path.name);
    setDescription(path.description);
  }, [path.name, path.description]);
  const qualsQ = useQuery({
    queryKey: ['qualifications', 'all'],
    queryFn: ({ signal }) => listQualifications(signal),
  });
  const save = useMutation({
    mutationFn: (body: Parameters<typeof updatePath>[1]) => updatePath(path.id, body),
    onSuccess: onSaved,
  });
  const quals = (qualsQ.data?.qualifications ?? []).filter((q) => q.organization_id === path.organization_id);

  return (
    <Card>
      <CardHeader>
        <CardTitle>Settings</CardTitle>
      </CardHeader>
      <CardContent className="space-y-3">
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (name.trim()) save.mutate({ name: name.trim(), description: description.trim() });
          }}
        >
          <div>
            <Label htmlFor="path-edit-name">Name</Label>
            <Input id="path-edit-name" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div>
            <Label htmlFor="path-edit-description">Description</Label>
            <Input id="path-edit-description" value={description} onChange={(e) => setDescription(e.target.value)} />
          </div>
          <Button
            type="submit"
            size="sm"
            disabled={save.isPending || !name.trim() || (name === path.name && description === path.description)}
          >
            Save
          </Button>
        </form>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={path.sequential}
            disabled={save.isPending}
            onChange={(e) => save.mutate({ sequential: e.target.checked })}
          />
          In order: each course unlocks when the previous one is finished
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={path.self_enrol}
            disabled={save.isPending}
            onChange={(e) => save.mutate({ self_enrol: e.target.checked })}
          />
          Open: anyone can join from My courses
        </label>
        <div>
          <Label htmlFor="path-qualification">Finishing the path awards</Label>
          <select
            id="path-qualification"
            className={selectClasses}
            value={path.awards_qualification_id ?? ''}
            disabled={save.isPending || qualsQ.isLoading}
            onChange={(e) => save.mutate({ awards_qualification_id: e.target.value || null })}
          >
            <option value="">No qualification</option>
            {path.awards_qualification && !quals.some((q) => q.id === path.awards_qualification?.id) ? (
              <option value={path.awards_qualification.id}>{path.awards_qualification.name}</option>
            ) : null}
            {quals.map((q) => (
              <option key={q.id} value={q.id}>
                {q.name}
                {q.valid_months ? ` (${q.valid_months} months)` : ''}
              </option>
            ))}
          </select>
        </div>
        {save.isError ? <p className="text-sm text-destructive">{(save.error as Error).message}</p> : null}
      </CardContent>
    </Card>
  );
}

function CoursesCard({ path, onSaved }: { path: LearningPath; onSaved: () => void }) {
  const coursesQ = useQuery({
    queryKey: ['courses', 'teach'],
    queryFn: ({ signal }) => listTeachCourses(signal),
  });
  const [pick, setPick] = useState('');
  const save = useMutation({
    mutationFn: (ids: string[]) => setPathCourses(path.id, ids),
    onSuccess: () => {
      setPick('');
      onSaved();
    },
  });
  const ids = path.courses.map((c) => c.course_id);
  const move = (from: number, to: number) => {
    const next = [...ids];
    const [id] = next.splice(from, 1);
    next.splice(to, 0, id);
    save.mutate(next);
  };
  const choices = (coursesQ.data?.courses ?? []).filter(
    (c) => c.organization_id === path.organization_id && !ids.includes(c.id)
  );

  return (
    <Card>
      <CardHeader>
        <CardTitle>Courses</CardTitle>
        <CardDescription>
          Everyone on the path is enrolled in these.{path.sequential ? ' Learners take them in this order.' : ''}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {path.courses.length === 0 ? <p className="text-sm text-muted-foreground">No courses yet.</p> : null}
        <ol className="space-y-1.5">
          {path.courses.map((c, i) => (
            <li key={c.course_id} className="flex items-center gap-2 text-sm">
              <span className="w-5 text-right text-muted-foreground">{i + 1}.</span>
              <span className="flex-1 min-w-0 truncate font-medium">
                {c.title}
                {!c.published ? <span className="ml-2 text-xs font-normal text-muted-foreground">draft</span> : null}
              </span>
              <Button
                variant="ghost"
                size="icon"
                aria-label="Move up"
                disabled={i === 0 || save.isPending}
                onClick={() => move(i, i - 1)}
              >
                <ArrowUp className="w-4 h-4" />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                aria-label="Move down"
                disabled={i === path.courses.length - 1 || save.isPending}
                onClick={() => move(i, i + 1)}
              >
                <ArrowDown className="w-4 h-4" />
              </Button>
              <Button
                variant="ghost"
                size="icon"
                aria-label="Remove from path"
                disabled={save.isPending}
                onClick={() => {
                  if (confirm(`Remove "${c.title}" from the path? Course enrolments and progress are kept.`))
                    save.mutate(ids.filter((id) => id !== c.course_id));
                }}
              >
                <Trash2 className="w-4 h-4" />
              </Button>
            </li>
          ))}
        </ol>
        <form
          className="flex gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (pick) save.mutate([...ids, pick]);
          }}
        >
          <select
            aria-label="Add a course"
            className={selectClasses}
            value={pick}
            onChange={(e) => setPick(e.target.value)}
          >
            <option value="">Add a course…</option>
            {choices.map((c) => (
              <option key={c.id} value={c.id}>
                {c.title}
                {c.published ? '' : ' (draft)'}
              </option>
            ))}
          </select>
          <Button type="submit" size="md" disabled={!pick || save.isPending}>
            Add
          </Button>
        </form>
        {save.isError ? <p className="text-sm text-destructive">{(save.error as Error).message}</p> : null}
      </CardContent>
    </Card>
  );
}

function PeopleCard({ path, onChanged }: { path: LearningPath; onChanged: () => void }) {
  const qc = useQueryClient();
  const enrolmentsQ = useQuery({
    queryKey: ['path-enrolments', path.id],
    queryFn: ({ signal }) => listPathEnrolments(path.id, signal),
  });
  const [q, setQ] = useState('');
  const peopleQ = useQuery({
    queryKey: ['people', q],
    queryFn: ({ signal }) => searchPeople(q, signal),
    enabled: q.trim().length > 0,
  });
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['path-enrolments', path.id] });
    onChanged();
  };
  const add = useMutation({
    mutationFn: (userId: string) => enrolInPath(path.id, userId),
    onSuccess: () => {
      setQ('');
      refresh();
    },
  });
  const remove = useMutation({ mutationFn: removePathEnrolment, onSuccess: refresh });
  const enrolments = enrolmentsQ.data?.enrolments ?? [];
  const activeIds = new Set(enrolments.filter((e) => e.status === 'active').map((e) => e.user_id));
  const error = (add.error ?? remove.error) as Error | null;

  return (
    <Card>
      <CardHeader>
        <CardTitle>People</CardTitle>
        <CardDescription>Adding someone enrols them in every course on the path.</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div>
          <Label htmlFor="path-people-search">Add a person</Label>
          <Input
            id="path-people-search"
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Search by name or email"
          />
          {peopleQ.data && q.trim() ? (
            <ul className="mt-2 space-y-1">
              {peopleQ.data.people.length === 0 ? (
                <li className="text-sm text-muted-foreground">Nobody found.</li>
              ) : (
                peopleQ.data.people.map((p) => (
                  <li key={p.id} className="flex items-center justify-between gap-2 text-sm">
                    <span className="min-w-0 truncate">
                      {p.display_name}
                      {p.email ? <span className="text-xs text-muted-foreground"> · {p.email}</span> : null}
                    </span>
                    <Button
                      size="sm"
                      variant="secondary"
                      disabled={activeIds.has(p.id) || add.isPending}
                      onClick={() => add.mutate(p.id)}
                    >
                      <UserPlus className="w-4 h-4" />
                      {activeIds.has(p.id) ? 'On path' : 'Add'}
                    </Button>
                  </li>
                ))
              )}
            </ul>
          ) : null}
        </div>
        {enrolmentsQ.isLoading ? <LoadingRow /> : null}
        {enrolmentsQ.isError ? <ErrorRow message={(enrolmentsQ.error as Error).message} /> : null}
        {enrolments.length > 0 ? (
          <ul className="divide-y divide-border">
            {enrolments.map((e) => (
              <li key={e.id} className="flex items-center gap-2 py-2 text-sm">
                <div className="flex-1 min-w-0">
                  <p className="font-medium truncate">
                    {e.display_name}
                    {e.status === 'inactive' ? (
                      <span className="ml-2 text-xs font-normal text-muted-foreground">inactive (left group)</span>
                    ) : null}
                  </p>
                  <p className="text-xs text-muted-foreground">
                    {e.courses_done}/{e.courses_total} courses{e.complete ? ' · complete' : ''}
                    {e.source === 'group' ? ' · via group' : ''}
                    {e.cycle > 1 ? ` · cycle ${e.cycle}` : ''}
                  </p>
                </div>
                <Button
                  variant="ghost"
                  size="icon"
                  aria-label="Remove from path"
                  disabled={remove.isPending}
                  onClick={() => {
                    if (confirm(`Remove ${e.display_name} from the path? Their course enrolments and progress are kept.`))
                      remove.mutate(e.id);
                  }}
                >
                  <Trash2 className="w-4 h-4" />
                </Button>
              </li>
            ))}
          </ul>
        ) : enrolmentsQ.isSuccess ? (
          <p className="text-sm text-muted-foreground">Nobody on this path yet.</p>
        ) : null}
        {error ? <p className="text-sm text-destructive">{error.message}</p> : null}
      </CardContent>
    </Card>
  );
}

function GroupsCard({ path, onChanged }: { path: LearningPath; onChanged: () => void }) {
  const qc = useQueryClient();
  const linksQ = useQuery({
    queryKey: ['path-group-links', path.id],
    queryFn: ({ signal }) => listPathGroupLinks(path.id, signal),
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
    void qc.invalidateQueries({ queryKey: ['path-group-links', path.id] });
    void qc.invalidateQueries({ queryKey: ['path-enrolments', path.id] });
    onChanged();
  };
  const add = useMutation({
    mutationFn: (name: string) => addPathGroupLink(path.id, name),
    onSuccess: (res) => {
      setGroup('');
      setMessage(
        res.sync_error
          ? `Linked, but the sync failed: ${res.sync_error}`
          : res.sync
            ? `Linked. ${syncSummary(res.sync)}`
            : 'Linked. Members join the path as they sign in.'
      );
      refresh();
    },
  });
  const remove = useMutation({
    mutationFn: (name: string) => removePathGroupLink(path.id, name),
    onSuccess: (res) => {
      setMessage(`Unlinked. ${res.deactivated} path enrolment(s) made inactive.`);
      refresh();
    },
  });
  const sync = useMutation({
    mutationFn: () => syncPathGroupLinks(path.id),
    onSuccess: (res) => {
      setMessage(`Synced. ${syncSummary(res)}${res.errors.length ? ` · ${res.errors.join('; ')}` : ''}`);
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
          Path groups
        </CardTitle>
        <CardDescription>
          Members of these groups join the path (and all its courses). People who leave become inactive and keep their
          progress.
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
                    {l.members} member(s) · {l.active} on path
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
                    if (confirm(`Unlink "${l.group_name}"? Its members leave the path unless something else covers them; progress is kept.`))
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
          <Label htmlFor="path-group-name">Link a group</Label>
          {groupsQ.isSuccess ? (
            <select id="path-group-name" className={selectClasses} value={group} onChange={(e) => setGroup(e.target.value)}>
              <option value="">Choose a group…</option>
              {choices.map((g) => (
                <option key={g.id} value={g.name}>
                  {g.path || g.name}
                </option>
              ))}
            </select>
          ) : (
            <Input id="path-group-name" value={group} onChange={(e) => setGroup(e.target.value)} placeholder="Group name" />
          )}
          {keycloakDown ? (
            <p className="text-xs text-muted-foreground">
              Keycloak sync is not configured; members join when they sign in.
            </p>
          ) : null}
          <div className="flex gap-2">
            <Button type="submit" size="sm" disabled={!group.trim() || add.isPending}>
              Link group
            </Button>
            {linksQ.data?.sync_available && links.length > 0 ? (
              <Button type="button" variant="secondary" size="sm" disabled={sync.isPending} onClick={() => sync.mutate()}>
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

/** One learning path: settings, ordered courses, people and linked groups. */
export function PathPage() {
  const { pathId } = useParams({ from: '/app/teach/paths/$pathId' });
  const qc = useQueryClient();
  const navigate = useNavigate();
  const pathQ = useQuery({
    queryKey: ['path', pathId],
    queryFn: ({ signal }) => getPath(pathId, signal),
    retry: false,
  });
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['path', pathId] });
    void qc.invalidateQueries({ queryKey: ['paths'] });
  };
  const remove = useMutation({
    mutationFn: () => deletePath(pathId),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['paths'] });
      void navigate({ to: '/teach/paths' });
    },
  });
  const path = pathQ.data;

  return (
    <PageContainer>
      <PageHeader
        title={path ? path.name : 'Learning path'}
        description={path?.description || 'Courses, people and groups on this path.'}
        actions={
          <>
            <LinkButton to="/teach/paths" variant="secondary" size="sm">
              <ArrowLeft className="w-4 h-4" />
              All paths
            </LinkButton>
            {path ? (
              <Button
                variant="ghost"
                size="sm"
                disabled={remove.isPending}
                onClick={() => {
                  if (
                    confirm(
                      `Delete "${path.name}"? Course enrolments and progress are kept; group enrolments nothing else covers become inactive.`
                    )
                  )
                    remove.mutate();
                }}
              >
                <Trash2 className="w-4 h-4" />
                Delete path
              </Button>
            ) : null}
          </>
        }
      />
      {pathQ.isLoading ? (
        <LoadingRow />
      ) : pathQ.isError || !path ? (
        <ErrorRow message="Could not load this path (it may not exist, or you may not manage it)." />
      ) : (
        <div className="grid gap-6 lg:grid-cols-2">
          <div className="space-y-6">
            <SettingsCard path={path} onSaved={refresh} />
            <CoursesCard path={path} onSaved={refresh} />
          </div>
          <div className="space-y-6">
            <PeopleCard path={path} onChanged={refresh} />
            <GroupsCard path={path} onChanged={refresh} />
          </div>
        </div>
      )}
      {remove.isError ? <ErrorRow message={(remove.error as Error).message} /> : null}
    </PageContainer>
  );
}
