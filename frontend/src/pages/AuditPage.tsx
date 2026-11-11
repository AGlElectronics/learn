import { useState } from 'react';
import { useInfiniteQuery } from '@tanstack/react-query';
import { listAudit } from '@/api/admin';
import { Button } from '@/components/ui/Button';
import { Card, CardContent } from '@/components/ui/Card';
import { Label } from '@/components/ui/Input';
import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { getLocale, useT } from '@/i18n';

/** Actions the filter offers; keep in sync with internal/audit/audit.go. */
const ACTIONS = [
  'enrolment.added',
  'enrolment.removed',
  'enrolment.self',
  'enrolment.updated',
  'path_enrolment.added',
  'path_enrolment.removed',
  'course.published',
  'course.unpublished',
  'course.archived',
  'course.unarchived',
  'quiz.graded',
  'quiz.reset',
  'certificate.revoked',
  'qualification.saved',
  'qualification.deleted',
  'group_link.added',
  'group_link.removed',
  'session.signup_status',
  'den.paired',
  'den.synced',
  'den.unpaired',
  'settings.updated',
  'user.erased',
  'users.anonymised',
];

/** The audit log (settings.manage): who did what, newest first. */
export function AuditPage() {
  const t = useT();
  const [action, setAction] = useState('');
  const auditQ = useInfiniteQuery({
    queryKey: ['audit', action],
    queryFn: ({ pageParam, signal }) =>
      listAudit({ action: action || undefined, before: pageParam || undefined, limit: 50 }, signal),
    initialPageParam: 0,
    getNextPageParam: (last) => last.next_before ?? undefined,
    retry: false,
  });
  const entries = auditQ.data?.pages.flatMap((p) => p.entries) ?? [];
  return (
    <PageContainer>
      <PageHeader title={t('nav.audit')} description={t('audit.description')} />
      <div className="space-y-4">
        <div className="max-w-xs">
          <Label htmlFor="audit-action">{t('audit.action')}</Label>
          <select
            id="audit-action"
            className="w-full h-9 rounded-md border border-input bg-background px-2 text-sm"
            value={action}
            onChange={(e) => setAction(e.target.value)}
          >
            <option value="">{t('audit.allActions')}</option>
            {ACTIONS.map((a) => (
              <option key={a} value={a}>
                {a}
              </option>
            ))}
          </select>
        </div>
        {auditQ.isLoading ? <LoadingRow /> : null}
        {auditQ.isError ? <ErrorRow message={(auditQ.error as Error).message} /> : null}
        <Card>
          <CardContent className="p-0 overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-muted-foreground border-b border-border">
                  <th className="px-3 py-2">{t('audit.when')}</th>
                  <th className="px-3 py-2">{t('audit.who')}</th>
                  <th className="px-3 py-2">{t('audit.action')}</th>
                  <th className="px-3 py-2">{t('audit.target')}</th>
                  <th className="px-3 py-2">{t('audit.details')}</th>
                </tr>
              </thead>
              <tbody>
                {entries.map((e) => (
                  <tr key={e.id} className="border-b border-border align-top">
                    <td className="px-3 py-2 whitespace-nowrap">{new Date(e.at).toLocaleString(getLocale())}</td>
                    <td className="px-3 py-2">{e.actor_name || t('audit.system')}</td>
                    <td className="px-3 py-2 font-mono text-xs">{e.action}</td>
                    <td className="px-3 py-2 font-mono text-xs break-all">
                      {e.target_type}
                      {e.target_id ? `: ${e.target_id}` : ''}
                    </td>
                    <td className="px-3 py-2 font-mono text-xs break-all">
                      {Object.keys(e.details).length ? JSON.stringify(e.details) : ''}
                    </td>
                  </tr>
                ))}
                {auditQ.isSuccess && entries.length === 0 ? (
                  <tr>
                    <td colSpan={5} className="px-3 py-4 text-muted-foreground">
                      {t('audit.empty')}
                    </td>
                  </tr>
                ) : null}
              </tbody>
            </table>
          </CardContent>
        </Card>
        {auditQ.hasNextPage ? (
          <Button variant="secondary" size="sm" disabled={auditQ.isFetchingNextPage} onClick={() => void auditQ.fetchNextPage()}>
            {t('audit.more')}
          </Button>
        ) : null}
      </div>
    </PageContainer>
  );
}
