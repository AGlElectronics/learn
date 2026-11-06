import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { Copy, X } from 'lucide-react';
import { copyCourse, type Course } from '@/api/courses';
import { Button } from '@/components/ui/Button';
import { Input, Label } from '@/components/ui/Input';

/**
 * Copies a course for a new edition: sections, pages, quizzes, question
 * bank, media, rules and settings. Learners, attempts, completions and
 * session slots stay behind; the copy starts unpublished with the caller
 * as its trainer.
 */
export function CopyCourseDialog({ course, onClose }: { course: Pick<Course, 'id' | 'title'>; onClose: () => void }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [title, setTitle] = useState(`${course.title} (copy)`);
  const [includeGroups, setIncludeGroups] = useState(false);
  const copy = useMutation({
    mutationFn: () => copyCourse(course.id, { title: title.trim(), include_group_links: includeGroups }),
    onSuccess: (created) => {
      void qc.invalidateQueries({ queryKey: ['courses'] });
      onClose();
      void navigate({ to: '/teach/$courseId', params: { courseId: created.id } });
    },
  });

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/40 p-4"
      role="dialog"
      aria-modal="true"
      aria-labelledby="copy-course-title"
      onClick={onClose}
    >
      <div
        className="w-full max-w-md rounded-card border border-border bg-card p-5 shadow-lg space-y-4 my-16"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center gap-2">
          <h3 id="copy-course-title" className="font-heading text-lg font-bold flex-1">
            Copy course
          </h3>
          <Button variant="ghost" size="icon" aria-label="Close" onClick={onClose}>
            <X className="w-4 h-4" />
          </Button>
        </div>
        <p className="text-sm text-muted-foreground">
          Copies the content of <span className="font-semibold text-foreground">{course.title}</span> for a new
          edition. No learners, results or session times are copied; the copy starts as a draft and you become its
          trainer.
        </p>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (title.trim()) copy.mutate();
          }}
        >
          <div>
            <Label htmlFor="copy-title">Title</Label>
            <Input id="copy-title" value={title} onChange={(e) => setTitle(e.target.value)} autoFocus />
          </div>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={includeGroups} onChange={(e) => setIncludeGroups(e.target.checked)} />
            Also link the same enrolment groups
          </label>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="secondary" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={!title.trim() || copy.isPending}>
              <Copy className="w-4 h-4" />
              {copy.isPending ? 'Copying…' : 'Copy course'}
            </Button>
          </div>
          {copy.isError ? <p className="text-sm text-destructive">{(copy.error as Error).message}</p> : null}
        </form>
      </div>
    </div>
  );
}
