import { useQuery } from '@tanstack/react-query';
import { useParams } from '@tanstack/react-router';
import { ArrowLeft, CheckCircle2, Clock, GraduationCap } from 'lucide-react';
import { LinkButton } from '@/components/ui/Button';
import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { EmptyState, ErrorRow, LoadingRow } from '@/components/ui/States';
import { DueBadge } from '@/components/Rules';
import { getCourse } from '@/api/courses';
import { useGradebook } from '@/api/quizzes';
import { cn } from '@/lib/utils';

/**
 * Learners × quizzes for one course: best score, pass, and how many
 * attempts each learner has submitted. Course staff only (server-checked).
 */
export function GradebookPage() {
  const { courseId } = useParams({ from: '/app/teach/$courseId/grades' });
  const courseQ = useQuery({
    queryKey: ['course', courseId],
    queryFn: ({ signal }) => getCourse(courseId, signal),
    retry: false,
  });
  const gradesQ = useGradebook(courseId);

  return (
    <PageContainer>
      <PageHeader
        title={courseQ.data ? `Grades · ${courseQ.data.title}` : 'Grades'}
        description="Best submitted attempt per learner and quiz."
        actions={
          <LinkButton to={`/teach/${courseId}`} variant="secondary" size="sm">
            <ArrowLeft className="w-4 h-4" />
            Back to editor
          </LinkButton>
        }
      />
      {gradesQ.isLoading ? (
        <LoadingRow message="Loading grades…" />
      ) : gradesQ.isError || !gradesQ.data ? (
        <ErrorRow message="Could not load the gradebook (you may not teach this course)." />
      ) : gradesQ.data.rows.length === 0 ? (
        <EmptyState
          icon={GraduationCap}
          title="No learners yet"
          description="Enrol learners to see their progress and results here."
        />
      ) : (
        <div className="overflow-x-auto rounded-card border border-border bg-card">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-left">
                <th className="px-4 py-2 font-semibold">Learner</th>
                <th className="px-4 py-2 font-semibold whitespace-nowrap">Progress</th>
                <th className="px-4 py-2 font-semibold whitespace-nowrap">Due</th>
                {gradesQ.data.quizzes.map((q) => (
                  <th key={q.activity_id} className="px-4 py-2 font-semibold whitespace-nowrap">
                    {q.title}
                    <span className="block text-xs font-normal text-muted-foreground">pass {q.pass_percent}%</span>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {gradesQ.data.rows.map((row) => (
                <tr key={row.user_id} className="border-b border-border last:border-0">
                  <td className="px-4 py-2">
                    <p className="font-medium">{row.display_name}</p>
                    {row.email ? <p className="text-xs text-muted-foreground">{row.email}</p> : null}
                  </td>
                  <td className="px-4 py-2 whitespace-nowrap">
                    {row.progress.completed}/{row.progress.total}
                  </td>
                  <td className="px-4 py-2 whitespace-nowrap">
                    {row.due_at ? <DueBadge dueAt={row.due_at} overdue={row.overdue} /> : <span className="text-muted-foreground">—</span>}
                  </td>
                  {row.cells.map((cell) => (
                    <td key={cell.activity_id} className="px-4 py-2 whitespace-nowrap">
                      {cell.attempts === 0 ? (
                        <span className="text-muted-foreground">
                          {cell.in_progress ? (
                            <span className="inline-flex items-center gap-1">
                              <Clock className="w-3.5 h-3.5" />
                              in progress
                            </span>
                          ) : (
                            '—'
                          )}
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1.5">
                          {cell.passed ? <CheckCircle2 className="w-4 h-4 text-success" /> : null}
                          <span className={cn('font-semibold', !cell.passed && 'text-destructive')}>
                            {cell.best_percent ?? 0}%
                          </span>
                          <span className="text-xs text-muted-foreground">
                            {cell.attempts} attempt{cell.attempts === 1 ? '' : 's'}
                          </span>
                        </span>
                      )}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </PageContainer>
  );
}
