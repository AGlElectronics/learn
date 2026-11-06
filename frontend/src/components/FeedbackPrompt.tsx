import { useEffect, useState } from 'react';
import { MessageSquareHeart, Star } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { submitFeedback, useFeedbackMutation, useMyFeedback, type FeedbackTarget } from '@/api/feedback';
import { cn } from '@/lib/utils';

/**
 * Asks the learner for a 1-5 rating when the course collects feedback:
 * on the course once they have finished it, and on the open session once
 * they attended it. Each target can be rated once.
 */
export function FeedbackPrompt({
  courseId,
  activityId,
  completed,
}: {
  courseId: string;
  /** The open activity; prompts only when it is an attended session. */
  activityId: string | null;
  /** Completed activity count, so finishing the course refreshes eligibility. */
  completed: number;
}) {
  const mineQ = useMyFeedback(courseId);
  const { refetch } = mineQ;
  useEffect(() => {
    void refetch();
  }, [completed, refetch]);
  const mine = mineQ.data;
  if (!mine || !mine.enabled) return null;
  const session = mine.sessions.find((s) => s.activity_id === activityId);
  return (
    <div className="space-y-3 mt-4">
      {session && session.eligible ? (
        <RatingCard courseId={courseId} target={session} anonymous={mine.anonymous} what="this session" />
      ) : null}
      {mine.course.eligible ? (
        <RatingCard courseId={courseId} target={mine.course} anonymous={mine.anonymous} what="this course" />
      ) : null}
    </div>
  );
}

function RatingCard({
  courseId,
  target,
  anonymous,
  what,
}: {
  courseId: string;
  target: FeedbackTarget;
  anonymous: boolean;
  what: string;
}) {
  const [rating, setRating] = useState(0);
  const [hover, setHover] = useState(0);
  const [comment, setComment] = useState('');
  const send = useFeedbackMutation(() =>
    submitFeedback(courseId, { activity_id: target.activity_id, rating, comment })
  );
  if (target.submitted) {
    return (
      <p className="text-sm text-muted-foreground flex items-center gap-2">
        <MessageSquareHeart className="w-4 h-4 text-primary" />
        Thanks for your feedback on {what}.
      </p>
    );
  }
  const shown = hover || rating;
  return (
    <form
      className="rounded-card border border-primary/30 bg-card p-4 space-y-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (rating > 0) send.mutate(undefined);
      }}
    >
      <div>
        <p className="font-semibold text-sm flex items-center gap-2">
          <MessageSquareHeart className="w-4 h-4 text-primary" />
          How was {what}?
        </p>
        <p className="text-xs text-muted-foreground">
          {target.title} · {anonymous ? 'Your feedback is anonymous.' : 'Trainers see your name with your feedback.'}
        </p>
      </div>
      <div className="flex gap-1" role="radiogroup" aria-label="Rating" onMouseLeave={() => setHover(0)}>
        {[1, 2, 3, 4, 5].map((n) => (
          <button
            key={n}
            type="button"
            role="radio"
            aria-checked={rating === n}
            aria-label={`${n} star${n === 1 ? '' : 's'}`}
            onMouseEnter={() => setHover(n)}
            onClick={() => setRating(n)}
            className="p-0.5"
          >
            <Star className={cn('w-6 h-6', n <= shown ? 'fill-primary text-primary' : 'text-muted-foreground')} />
          </button>
        ))}
      </div>
      <textarea
        value={comment}
        onChange={(e) => setComment(e.target.value)}
        rows={2}
        maxLength={4000}
        placeholder="Anything to add? (optional)"
        className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:border-primary"
      />
      <Button type="submit" size="sm" disabled={rating === 0 || send.isPending}>
        Send feedback
      </Button>
      {send.isError ? <p className="text-sm text-destructive">{(send.error as Error).message}</p> : null}
    </form>
  );
}
