import { useMutation, useQuery } from '@tanstack/react-query';
import { Award } from 'lucide-react';
import { listQualifications, setCourseQualification, type QualificationRef } from '@/api/qualifications';

/** Course settings: which qualification finishing the course awards (or none). */
export function QualificationPicker({
  courseId,
  organizationId,
  current,
  onChanged,
}: {
  courseId: string;
  organizationId: string;
  current: QualificationRef | null | undefined;
  onChanged: () => void;
}) {
  const q = useQuery({
    queryKey: ['qualifications', 'all'],
    queryFn: ({ signal }) => listQualifications(signal),
  });
  const save = useMutation({
    mutationFn: (id: string | null) => setCourseQualification(courseId, id),
    onSuccess: onChanged,
  });
  const options = (q.data?.qualifications ?? []).filter((x) => x.organization_id === organizationId);

  return (
    <div className="flex flex-wrap items-center gap-2 text-sm">
      <Award className="w-4 h-4 text-muted-foreground" />
      Awards
      <select
        className="h-8 rounded-md border border-input bg-background px-2 text-sm"
        aria-label="Awarded qualification"
        value={current?.id ?? ''}
        disabled={save.isPending || q.isLoading}
        onChange={(e) => save.mutate(e.target.value || null)}
      >
        <option value="">No qualification</option>
        {current && !options.some((o) => o.id === current.id) ? (
          <option value={current.id}>{current.name}</option>
        ) : null}
        {options.map((o) => (
          <option key={o.id} value={o.id}>
            {o.name}
            {o.valid_months ? ` (${o.valid_months} months)` : ''}
          </option>
        ))}
      </select>
      <span className="text-xs text-muted-foreground">
        (learners get a certificate when they finish; manage qualifications in Settings)
      </span>
      {save.isError ? <span className="basis-full text-sm text-destructive">{(save.error as Error).message}</span> : null}
    </div>
  );
}
