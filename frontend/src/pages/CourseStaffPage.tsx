import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { UserPlus, X } from 'lucide-react';
import { enrolPerson, listCourseStaff, personRef, unenrol, type StaffedCourse } from '@/api/courses';
import { PeoplePicker } from '@/components/PeoplePicker';
import { Button } from '@/components/ui/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { useT } from '@/i18n';

/**
 * Course roles, for training managers (staff.manage): who teaches which
 * course. Adding a trainer here needs no edit rights on the course; the
 * last trainer of a course can't be removed.
 */
export function CourseStaffPage() {
  const t = useT();
  const [q, setQ] = useState('');
  const staffQ = useQuery({
    queryKey: ['course-staff', q.trim()],
    queryFn: ({ signal }) => listCourseStaff(q.trim(), signal),
    retry: false,
  });
  return (
    <PageContainer>
      <PageHeader title={t('nav.courseRoles')} description={t('staff.description')} />
      <div className="max-w-3xl space-y-4">
        <div>
          <Label htmlFor="staff-course-filter">{t('staff.filter')}</Label>
          <Input id="staff-course-filter" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        {staffQ.isLoading ? <LoadingRow /> : null}
        {staffQ.isError ? <ErrorRow message={(staffQ.error as Error).message} /> : null}
        {(staffQ.data?.courses ?? []).map((c) => (
          <StaffCard key={c.id} course={c} />
        ))}
        {staffQ.isSuccess && staffQ.data.courses.length === 0 ? (
          <p className="text-sm text-muted-foreground">{t('staff.none')}</p>
        ) : null}
      </div>
    </PageContainer>
  );
}

function StaffCard({ course }: { course: StaffedCourse }) {
  const t = useT();
  const qc = useQueryClient();
  const refresh = () => void qc.invalidateQueries({ queryKey: ['course-staff'] });
  const add = useMutation({
    mutationFn: (p: Parameters<typeof personRef>[0]) => enrolPerson(course.id, { ...personRef(p), role: 'trainer' }),
    onSuccess: refresh,
  });
  const remove = useMutation({ mutationFn: unenrol, onSuccess: refresh });
  const trainerIds = new Set(course.trainers.map((tr) => tr.user_id));
  const error = (add.error ?? remove.error) as Error | null;
  return (
    <Card>
      <CardHeader>
        <CardTitle>{course.title}</CardTitle>
        <CardDescription>
          {course.archived ? t('staff.archived') : course.published ? t('staff.published') : t('staff.draft')}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <ul className="space-y-1">
          {course.trainers.map((tr) => (
            <li key={tr.enrolment_id} className="flex items-center justify-between gap-2 text-sm">
              <span className="truncate">
                {tr.display_name}
                {tr.email ? <span className="text-muted-foreground"> · {tr.email}</span> : null}
              </span>
              {course.trainers.length > 1 && !course.archived ? (
                <Button
                  variant="ghost"
                  size="sm"
                  className="text-destructive"
                  title={t('staff.remove')}
                  disabled={remove.isPending}
                  onClick={() => {
                    if (confirm(t('staff.confirmRemove', { name: tr.display_name, course: course.title }))) {
                      remove.mutate(tr.enrolment_id);
                    }
                  }}
                >
                  <X className="w-4 h-4" />
                </Button>
              ) : null}
            </li>
          ))}
        </ul>
        {course.trainers.length === 1 ? <p className="text-xs text-muted-foreground">{t('staff.lastTrainer')}</p> : null}
        {!course.archived ? (
          <PeoplePicker
            id={`staff-add-${course.id}`}
            label={t('staff.add')}
            exclude={trainerIds}
            actions={(p) => (
              <Button variant="secondary" size="sm" disabled={add.isPending} onClick={() => add.mutate(p)}>
                <UserPlus className="w-4 h-4" />
                {t('role.trainer')}
              </Button>
            )}
          />
        ) : null}
        {error ? <ErrorRow message={error.message} /> : null}
      </CardContent>
    </Card>
  );
}
