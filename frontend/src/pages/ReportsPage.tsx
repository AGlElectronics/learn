import { useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { BarChart3, Download } from 'lucide-react';
import { ApiError } from '@/api/client';
import {
  downloadReportCsv,
  getCompletionReport,
  getQualificationReport,
  getReportScope,
  type CompletionFilters,
  type CompletionStatus,
  type QualificationFilters,
  type QualificationReportStatus,
} from '@/api/reports';
import { Button } from '@/components/ui/Button';
import { Input, Label } from '@/components/ui/Input';
import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { EmptyState, ErrorRow, LoadingRow } from '@/components/ui/States';
import { getLocale, useT, type MessageKey } from '@/i18n';
import { cn } from '@/lib/utils';

const selectClasses = 'w-full h-9 rounded-md border border-input bg-background px-2 text-sm';

const COMPLETION_STATUS: Record<CompletionStatus, { label: MessageKey; tone: string }> = {
  done: { label: 'reports.status.done', tone: 'bg-primary-soft text-primary' },
  in_progress: { label: 'reports.status.inProgress', tone: 'bg-secondary text-foreground' },
  not_started: { label: 'reports.status.notStarted', tone: 'bg-secondary text-muted-foreground' },
  overdue: { label: 'reports.status.overdue', tone: 'bg-destructive/10 text-destructive' },
};

const QUALIFICATION_STATUS: Record<QualificationReportStatus, { label: MessageKey; tone: string }> = {
  valid: { label: 'cert.status.valid', tone: 'bg-primary-soft text-primary' },
  expiring: { label: 'reports.status.expiring', tone: 'bg-warning/15 text-warning' },
  expired: { label: 'cert.status.expired', tone: 'bg-destructive/10 text-destructive' },
  missing: { label: 'reports.status.missing', tone: 'bg-secondary text-muted-foreground' },
};

function StatusPill({ label, tone }: { label: MessageKey; tone: string }) {
  const t = useT();
  return <span className={cn('text-[11px] font-semibold rounded-pill px-2 py-0.5 whitespace-nowrap', tone)}>{t(label)}</span>;
}

function date(iso: string | null) {
  return iso ? new Date(iso).toLocaleDateString(getLocale()) : '—';
}

function DownloadButton({ onDownload }: { onDownload: () => Promise<void> }) {
  const t = useT();
  const dl = useMutation({ mutationFn: onDownload });
  return (
    <div className="flex items-center gap-2">
      <Button variant="secondary" size="sm" disabled={dl.isPending} onClick={() => dl.mutate()}>
        <Download className="w-4 h-4" />
        {dl.isPending ? t('reports.preparing') : t('reports.download')}
      </Button>
      {dl.isError ? <span className="text-xs text-destructive">{(dl.error as Error).message}</span> : null}
    </div>
  );
}

function Table({ head, children }: { head: string[]; children: React.ReactNode }) {
  return (
    <div className="overflow-x-auto rounded-card border border-border bg-card">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-border text-left">
            {head.map((h) => (
              <th key={h} className="px-4 py-2 font-semibold whitespace-nowrap">
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  );
}

function CompletionReport({ courses }: { courses: { id: string; title: string }[] }) {
  const t = useT();
  const [filters, setFilters] = useState<CompletionFilters>({ course: '', group: '', status: '' });
  const q = useQuery({
    queryKey: ['reports', 'completion', filters],
    queryFn: ({ signal }) => getCompletionReport(filters, signal),
    retry: false,
  });
  const rows = q.data?.rows ?? [];

  return (
    <div className="space-y-4">
      <div className="grid gap-3 sm:grid-cols-[1fr_1fr_180px_auto] items-end">
        <div>
          <Label htmlFor="report-course">{t('reports.course')}</Label>
          <select
            id="report-course"
            className={selectClasses}
            value={filters.course}
            onChange={(e) => setFilters({ ...filters, course: e.target.value })}
          >
            <option value="">{t('reports.allCourses')}</option>
            {courses.map((c) => (
              <option key={c.id} value={c.id}>
                {c.title}
              </option>
            ))}
          </select>
        </div>
        <div>
          <Label htmlFor="report-group">{t('reports.group')}</Label>
          <Input
            id="report-group"
            placeholder={t('reports.anyGroup')}
            defaultValue={filters.group}
            onBlur={(e) => setFilters({ ...filters, group: e.target.value.trim() })}
            onKeyDown={(e) => {
              if (e.key === 'Enter') setFilters({ ...filters, group: e.currentTarget.value.trim() });
            }}
          />
        </div>
        <div>
          <Label htmlFor="report-status">{t('reports.status')}</Label>
          <select
            id="report-status"
            className={selectClasses}
            value={filters.status}
            onChange={(e) => setFilters({ ...filters, status: e.target.value as CompletionStatus | '' })}
          >
            <option value="">{t('reports.anyStatus')}</option>
            {Object.entries(COMPLETION_STATUS).map(([k, v]) => (
              <option key={k} value={k}>
                {t(v.label)}
              </option>
            ))}
          </select>
        </div>
        <DownloadButton onDownload={() => downloadReportCsv('completion', filters)} />
      </div>

      {q.isLoading ? (
        <LoadingRow message={t('reports.loading')} />
      ) : q.isError ? (
        <ErrorRow message={(q.error as Error).message} />
      ) : rows.length === 0 ? (
        <EmptyState icon={BarChart3} title={t('reports.noneTitle')} description={t('reports.noneCompletion')} />
      ) : (
        <Table
          head={[
            t('reports.person'),
            t('reports.course'),
            t('reports.progress'),
            t('reports.due'),
            t('reports.lastActivity'),
            t('reports.status'),
          ]}
        >
          {rows.map((r) => (
            <tr key={`${r.course_id}-${r.user_id}`} className="border-b border-border last:border-0">
              <td className="px-4 py-2">
                <p className="font-medium">{r.display_name}</p>
                {r.email ? <p className="text-xs text-muted-foreground">{r.email}</p> : null}
              </td>
              <td className="px-4 py-2">
                {r.course_title}
                {r.source === 'group' ? <span className="block text-xs text-muted-foreground">{t('reports.viaGroup')}</span> : null}
              </td>
              <td className="px-4 py-2 whitespace-nowrap">
                {r.completed}/{r.total}
              </td>
              <td className="px-4 py-2 whitespace-nowrap">{date(r.due_at)}</td>
              <td className="px-4 py-2 whitespace-nowrap">{date(r.last_activity_at)}</td>
              <td className="px-4 py-2">
                <StatusPill {...COMPLETION_STATUS[r.status]} />
              </td>
            </tr>
          ))}
        </Table>
      )}
    </div>
  );
}

function QualificationReport({ qualifications }: { qualifications: { id: string; name: string }[] }) {
  const t = useT();
  const [filters, setFilters] = useState<QualificationFilters>({ qualification: '', group: '', status: '' });
  const q = useQuery({
    queryKey: ['reports', 'qualifications', filters],
    queryFn: ({ signal }) => getQualificationReport(filters, signal),
    retry: false,
  });
  const rows = q.data?.rows ?? [];

  return (
    <div className="space-y-4">
      <div className="grid gap-3 sm:grid-cols-[1fr_1fr_180px_auto] items-end">
        <div>
          <Label htmlFor="report-qualification">{t('reports.qualification')}</Label>
          <select
            id="report-qualification"
            className={selectClasses}
            value={filters.qualification}
            onChange={(e) => setFilters({ ...filters, qualification: e.target.value })}
          >
            <option value="">{t('reports.allQualifications')}</option>
            {qualifications.map((x) => (
              <option key={x.id} value={x.id}>
                {x.name}
              </option>
            ))}
          </select>
        </div>
        <div>
          <Label htmlFor="report-qgroup">{t('reports.group')}</Label>
          <Input
            id="report-qgroup"
            placeholder={t('reports.anyGroup')}
            defaultValue={filters.group}
            onBlur={(e) => setFilters({ ...filters, group: e.target.value.trim() })}
            onKeyDown={(e) => {
              if (e.key === 'Enter') setFilters({ ...filters, group: e.currentTarget.value.trim() });
            }}
          />
        </div>
        <div>
          <Label htmlFor="report-qstatus">{t('reports.status')}</Label>
          <select
            id="report-qstatus"
            className={selectClasses}
            value={filters.status}
            onChange={(e) => setFilters({ ...filters, status: e.target.value as QualificationReportStatus | '' })}
          >
            <option value="">{t('reports.anyStatus')}</option>
            {Object.entries(QUALIFICATION_STATUS).map(([k, v]) => (
              <option key={k} value={k}>
                {t(v.label)}
              </option>
            ))}
          </select>
        </div>
        <DownloadButton onDownload={() => downloadReportCsv('qualifications', filters)} />
      </div>
      <p className="text-xs text-muted-foreground">
        {t('reports.qualificationsHint')}
      </p>

      {q.isLoading ? (
        <LoadingRow message={t('reports.loading')} />
      ) : q.isError ? (
        <ErrorRow message={(q.error as Error).message} />
      ) : rows.length === 0 ? (
        <EmptyState icon={BarChart3} title={t('reports.noneTitle')} description={t('reports.noneQualifications')} />
      ) : (
        <Table
          head={[
            t('reports.person'),
            t('reports.qualification'),
            t('cert.issued'),
            t('reports.expires'),
            t('reports.status'),
          ]}
        >
          {rows.map((r) => (
            <tr key={`${r.qualification_id}-${r.user_id}`} className="border-b border-border last:border-0">
              <td className="px-4 py-2">
                <p className="font-medium">{r.display_name}</p>
                {r.email ? <p className="text-xs text-muted-foreground">{r.email}</p> : null}
              </td>
              <td className="px-4 py-2">
                {r.qualification_name}
                {r.required ? <span className="block text-xs text-muted-foreground">{t('reports.requiredByGroup')}</span> : null}
              </td>
              <td className="px-4 py-2 whitespace-nowrap">{date(r.issued_at)}</td>
              <td className="px-4 py-2 whitespace-nowrap">{r.certificate_id && !r.expires_at ? t('reports.never') : date(r.expires_at)}</td>
              <td className="px-4 py-2">
                <StatusPill {...QUALIFICATION_STATUS[r.status]} />
              </td>
            </tr>
          ))}
        </Table>
      )}
    </div>
  );
}

/**
 * Completion per learner and course, and qualification status per person.
 * report.view sees everything; trainers see completion for their own
 * courses (the server scopes both the data and the CSV).
 */
export function ReportsPage() {
  const t = useT();
  const scopeQ = useQuery({
    queryKey: ['reports', 'scope'],
    queryFn: ({ signal }) => getReportScope(signal),
    retry: false,
  });
  const [tab, setTab] = useState<'completion' | 'qualifications'>('completion');
  const scope = scopeQ.data;
  const forbidden = scopeQ.error instanceof ApiError && scopeQ.error.status === 403;

  return (
    <PageContainer>
      <PageHeader title={t('nav.reports')} description={t('reports.description')} />
      {scopeQ.isLoading ? (
        <LoadingRow />
      ) : forbidden || (scope && !scope.all && scope.courses.length === 0) ? (
        <EmptyState icon={BarChart3} title={t('reports.noAccessTitle')} description={t('reports.noAccess')} />
      ) : scopeQ.isError || !scope ? (
        <ErrorRow message={t('reports.loadError')} />
      ) : (
        <div className="space-y-4">
          {scope.all ? (
            <div className="inline-flex rounded-pill border border-border bg-card p-0.5">
              {(['completion', 'qualifications'] as const).map((k) => (
                <button
                  key={k}
                  type="button"
                  onClick={() => setTab(k)}
                  className={cn(
                    'px-3.5 py-1.5 rounded-pill text-sm font-medium transition-colors',
                    tab === k ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:text-foreground'
                  )}
                >
                  {k === 'completion' ? t('reports.tabCompletion') : t('reports.tabQualifications')}
                </button>
              ))}
            </div>
          ) : null}
          {tab === 'qualifications' && scope.all ? (
            <QualificationReport qualifications={scope.qualifications} />
          ) : (
            <CompletionReport courses={scope.courses} />
          )}
        </div>
      )}
    </PageContainer>
  );
}
