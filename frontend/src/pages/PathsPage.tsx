import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { ArrowLeft, Pencil, Route } from 'lucide-react';
import { createPath, listPaths } from '@/api/paths';
import { Button, LinkButton } from '@/components/ui/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { EmptyState, ErrorRow, LoadingRow } from '@/components/ui/States';

/**
 * Learning paths: ordered sets of courses for a role. Enrolling someone in
 * a path (by hand, self-enrolment or a linked group) enrols them in every
 * course of it. Needs course.manage (server-checked).
 */
export function PathsPage() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const pathsQ = useQuery({
    queryKey: ['paths'],
    queryFn: ({ signal }) => listPaths(signal),
    retry: false,
  });
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const create = useMutation({
    mutationFn: () => createPath({ name: name.trim(), description: description.trim() }),
    onSuccess: (p) => {
      void qc.invalidateQueries({ queryKey: ['paths'] });
      void navigate({ to: '/teach/paths/$pathId', params: { pathId: p.id } });
    },
  });

  return (
    <PageContainer>
      <PageHeader
        title="Learning paths"
        description="Ordered sets of courses for a role. People on a path are enrolled in all of its courses."
        actions={
          <LinkButton to="/teach" variant="secondary" size="sm">
            <ArrowLeft className="w-4 h-4" />
            Courses
          </LinkButton>
        }
      />
      <div className="grid gap-6 lg:grid-cols-[1fr_320px]">
        <div>
          {pathsQ.isLoading ? (
            <LoadingRow />
          ) : pathsQ.isError ? (
            <ErrorRow message={(pathsQ.error as Error).message} />
          ) : pathsQ.data && pathsQ.data.paths.length > 0 ? (
            <div className="grid gap-4 sm:grid-cols-2">
              {pathsQ.data.paths.map((p) => (
                <Card key={p.id}>
                  <CardHeader>
                    <CardTitle>{p.name}</CardTitle>
                    {p.description ? <CardDescription>{p.description}</CardDescription> : null}
                  </CardHeader>
                  <CardContent className="space-y-3">
                    <p className="text-xs text-muted-foreground">
                      {p.courses.length} course{p.courses.length === 1 ? '' : 's'} · {p.active_enrolments} enrolled
                      {p.sequential ? ' · in order' : ''}
                      {p.self_enrol ? ' · open' : ''}
                      {p.awards_qualification ? ` · awards ${p.awards_qualification.name}` : ''}
                    </p>
                    <LinkButton to={`/teach/paths/${p.id}`} variant="secondary" size="sm">
                      <Pencil className="w-4 h-4" />
                      Manage
                    </LinkButton>
                  </CardContent>
                </Card>
              ))}
            </div>
          ) : (
            <EmptyState icon={Route} title="No paths yet" description="Create one with the form on the right." />
          )}
        </div>

        <Card className="h-fit">
          <CardHeader>
            <CardTitle>New path</CardTitle>
            <CardDescription>Add courses and people after creating it.</CardDescription>
          </CardHeader>
          <CardContent>
            <form
              className="space-y-3"
              onSubmit={(e) => {
                e.preventDefault();
                if (name.trim()) create.mutate();
              }}
            >
              <div>
                <Label htmlFor="path-name">Name</Label>
                <Input id="path-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="New steward" />
              </div>
              <div>
                <Label htmlFor="path-description">Description</Label>
                <Input
                  id="path-description"
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder="Who this path is for (optional)"
                />
              </div>
              <Button type="submit" className="w-full" disabled={!name.trim() || create.isPending}>
                <Route className="w-4 h-4" />
                Create path
              </Button>
              {create.isError ? <p className="text-sm text-destructive">{(create.error as Error).message}</p> : null}
            </form>
          </CardContent>
        </Card>
      </div>
    </PageContainer>
  );
}
