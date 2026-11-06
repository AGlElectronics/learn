import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { Award, RefreshCw } from 'lucide-react';
import { Button, LinkButton } from '@/components/ui/Button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/Card';
import { ErrorRow } from '@/components/ui/States';
import {
  formatDate,
  myQualifications,
  renewCourse,
  STATUS_LABEL,
  type MyQualification,
} from '@/api/qualifications';
import { useT } from '@/i18n';
import { cn } from '@/lib/utils';

function StatusLine({ q }: { q: MyQualification }) {
  const t = useT();
  if (q.status === 'revoked') {
    return (
      <p className="text-xs text-destructive">
        {t('cert.status.revoked')}
        {q.revoked_reason ? `: ${q.revoked_reason}` : ''}
      </p>
    );
  }
  if (q.status === 'expired') {
    return <p className="text-xs text-warning">{t('quals.expiredOn', { date: formatDate(q.expires_at) ?? '' })}</p>;
  }
  return (
    <p className={cn('text-xs', q.expiring_soon ? 'text-warning' : 'text-success')}>
      {q.expires_at ? t('quals.validUntil', { date: formatDate(q.expires_at) ?? '' }) : t('quals.validForever')}
      {q.expiring_soon ? ` · ${t('quals.expiringSoon')}` : ''}
    </p>
  );
}

/** "My qualifications" on the learner home: latest certificate per qualification, with renewal. */
export function MyQualifications() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const t = useT();
  const q = useQuery({
    queryKey: ['qualifications', 'mine'],
    queryFn: ({ signal }) => myQualifications(signal),
  });
  const renew = useMutation({
    mutationFn: (courseId: string) => renewCourse(courseId),
    onSuccess: (res) => {
      void qc.invalidateQueries({ queryKey: ['qualifications'] });
      void qc.invalidateQueries({ queryKey: ['courses'] });
      void navigate({ to: '/courses/$courseId', params: { courseId: res.course_id } });
    },
  });

  const list = q.data?.qualifications ?? [];
  if (q.isLoading || (q.isSuccess && list.length === 0)) return null;

  return (
    <div className="mt-10">
      <h2 className="font-heading text-lg font-bold mb-3">{t('quals.mine')}</h2>
      {q.isError ? (
        <ErrorRow message={t('quals.loadError')} />
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {list.map((item) => (
            <Card key={item.id}>
              <CardHeader>
                <div className="flex items-center justify-between gap-2">
                  <CardTitle>{item.qualification_name}</CardTitle>
                  <span className="text-[11px] font-semibold rounded-pill bg-secondary px-2 py-0.5">
                    {t(STATUS_LABEL[item.status])}
                  </span>
                </div>
              </CardHeader>
              <CardContent className="space-y-3">
                <StatusLine q={item} />
                {item.renewal_in_progress ? (
                  <p className="text-xs text-muted-foreground">{t('quals.renewalInProgress')}</p>
                ) : item.renewable_from && !item.renewable && item.status === 'valid' ? (
                  <p className="text-xs text-muted-foreground">
                    {t('quals.renewalOpens', { date: formatDate(item.renewable_from) ?? '' })}
                  </p>
                ) : null}
                <div className="flex flex-wrap gap-2">
                  <LinkButton to={`/certificates/${item.id}`} variant="secondary" size="sm">
                    <Award className="w-4 h-4" />
                    {t('quals.certificate')}
                  </LinkButton>
                  {item.renewal_in_progress && item.course_id ? (
                    <LinkButton to={`/courses/${item.course_id}`} variant="ghost" size="sm">
                      {t('quals.continueCourse')}
                    </LinkButton>
                  ) : item.renewable && item.course_id ? (
                    <Button size="sm" disabled={renew.isPending} onClick={() => renew.mutate(item.course_id!)}>
                      <RefreshCw className="w-4 h-4" />
                      {t('quals.renew')}
                    </Button>
                  ) : null}
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
      {renew.isError ? <ErrorRow message={(renew.error as Error).message} /> : null}
    </div>
  );
}
