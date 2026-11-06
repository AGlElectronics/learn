import { MessageSquareHeart, Star } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/Card';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import {
  setFeedbackSettings,
  useFeedbackMutation,
  useFeedbackSummary,
  type FeedbackGroup,
} from '@/api/feedback';

/**
 * Course feedback for staff: the on/off and anonymous switches, then the
 * average rating, response count and comments for the course and each
 * session.
 */
export function FeedbackPanel({ courseId }: { courseId: string }) {
  const summaryQ = useFeedbackSummary(courseId);
  const save = useFeedbackMutation((body: { enabled?: boolean; anonymous?: boolean }) =>
    setFeedbackSettings(courseId, body)
  );
  const s = summaryQ.data;
  return (
    <Card className="h-fit">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <MessageSquareHeart className="w-4 h-4 text-primary" />
          Feedback
        </CardTitle>
        <CardDescription>
          When on, learners rate the course (1-5) after finishing it and each session after attending.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {summaryQ.isLoading ? (
          <LoadingRow />
        ) : summaryQ.isError || !s ? (
          <ErrorRow message="Could not load feedback." />
        ) : (
          <>
            <div className="flex flex-wrap gap-2">
              <Button
                variant={s.enabled ? 'primary' : 'secondary'}
                size="sm"
                disabled={save.isPending}
                onClick={() => save.mutate({ enabled: !s.enabled })}
              >
                {s.enabled ? 'Feedback: on' : 'Feedback: off'}
              </Button>
              <Button
                variant="secondary"
                size="sm"
                disabled={save.isPending}
                onClick={() => save.mutate({ anonymous: !s.anonymous })}
                title="Applies to new responses; anonymous responses never store who gave them"
              >
                {s.anonymous ? 'Anonymous: yes' : 'Anonymous: no'}
              </Button>
            </div>
            {save.isError ? <p className="text-sm text-destructive">{(save.error as Error).message}</p> : null}
            <FeedbackGroupView group={s.course} label="Course" />
            {s.sessions.map((g) => (
              <FeedbackGroupView key={g.activity_id} group={g} label="Session" />
            ))}
          </>
        )}
      </CardContent>
    </Card>
  );
}

function FeedbackGroupView({ group, label }: { group: FeedbackGroup; label: string }) {
  return (
    <div className="border-t border-border pt-3 space-y-1.5">
      <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        {label} · <span className="normal-case">{group.title}</span>
      </p>
      {group.count === 0 ? (
        <p className="text-sm text-muted-foreground">No responses yet.</p>
      ) : (
        <p className="text-sm flex items-center gap-1.5">
          <Star className="w-4 h-4 fill-primary text-primary" />
          <span className="font-semibold">{group.average?.toFixed(1)}</span>
          <span className="text-muted-foreground">
            from {group.count} response{group.count === 1 ? '' : 's'}
          </span>
        </p>
      )}
      {group.comments.length > 0 ? (
        <ul className="space-y-1.5 max-h-60 overflow-y-auto">
          {group.comments.map((c, i) => (
            <li key={i} className="text-sm rounded-md bg-secondary px-2.5 py-1.5">
              <p className="whitespace-pre-wrap">{c.comment}</p>
              <p className="text-xs text-muted-foreground mt-0.5">
                {'★'.repeat(c.rating)} · {c.display_name || 'Anonymous'} ·{' '}
                {new Date(c.created_at).toLocaleDateString()}
              </p>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
