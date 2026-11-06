import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { AlertTriangle, CheckCircle2, Download, FileUp, ImageIcon, X } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { Label } from '@/components/ui/Input';
import {
  commitImport,
  downloadCsvTemplate,
  previewImport,
  type ImportFormat,
  type ImportPreview,
  type ImportResult,
} from '@/api/importer';
import type { QuestionCategory } from '@/api/quizzes';

const FORMATS: { value: ImportFormat; label: string }[] = [
  { value: '', label: 'Detect from file' },
  { value: 'moodle_xml', label: 'Moodle XML' },
  { value: 'gift', label: 'GIFT' },
  { value: 'aiken', label: 'Aiken' },
  { value: 'csv', label: 'CSV' },
];

const selectClasses =
  'h-9 w-full rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:border-primary';

/**
 * Import questions from Moodle XML, GIFT, Aiken or CSV: pick a file, check
 * the preview and warnings, choose the category, import. The preview saves
 * nothing; importing sends the same file again.
 */
export function ImportDialog({
  courseId,
  categories,
  defaultCategoryId,
  onClose,
}: {
  courseId: string;
  categories: QuestionCategory[];
  defaultCategoryId: string | null;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const [file, setFile] = useState<File | null>(null);
  const [format, setFormat] = useState<ImportFormat>('');
  const [categoryId, setCategoryId] = useState(defaultCategoryId ?? categories[0]?.id ?? '');
  const [preview, setPreview] = useState<ImportPreview | null>(null);
  const [result, setResult] = useState<ImportResult | null>(null);

  const runPreview = useMutation({
    mutationFn: () => previewImport(courseId, file!, format),
    onSuccess: (p) => {
      setPreview(p);
      setResult(null);
    },
  });
  const runCommit = useMutation({
    mutationFn: () => commitImport(courseId, file!, format, categoryId),
    onSuccess: (r) => {
      setResult(r);
      void qc.invalidateQueries({ queryKey: ['question-categories'] });
      void qc.invalidateQueries({ queryKey: ['questions'] });
      void qc.invalidateQueries({ queryKey: ['media'] });
    },
  });
  const template = useMutation({ mutationFn: downloadCsvTemplate });
  const error = (runPreview.error ?? runCommit.error ?? template.error) as Error | null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/40 p-4"
      role="dialog"
      aria-modal="true"
      aria-labelledby="import-title"
    >
      <div className="w-full max-w-3xl rounded-card border border-border bg-card p-5 shadow-lg space-y-4 my-8">
        <div className="flex items-center gap-2">
          <h3 id="import-title" className="font-heading text-lg font-bold flex-1">
            Import questions
          </h3>
          <Button variant="ghost" size="icon" onClick={onClose} aria-label="Close">
            <X className="w-4 h-4" />
          </Button>
        </div>
        <p className="text-sm text-muted-foreground">
          Moodle XML, GIFT, Aiken or CSV. Choice and true/false questions are imported; other types are skipped
          with a reason.{' '}
          <button
            type="button"
            className="inline-flex items-center gap-1 text-primary font-semibold hover:underline"
            disabled={template.isPending}
            onClick={() => template.mutate()}
          >
            <Download className="w-3.5 h-3.5" />
            Download the CSV template
          </button>
        </p>

        <div className="grid gap-3 sm:grid-cols-[1fr_200px]">
          <div>
            <Label htmlFor="import-file">File</Label>
            <input
              id="import-file"
              type="file"
              accept=".xml,.gift,.txt,.csv"
              className="block w-full text-sm file:mr-3 file:rounded-pill file:border file:border-border file:bg-card file:px-3 file:py-1.5 file:text-xs file:font-semibold"
              onChange={(e) => {
                setFile(e.target.files?.[0] ?? null);
                setPreview(null);
                setResult(null);
              }}
            />
          </div>
          <div>
            <Label htmlFor="import-format">Format</Label>
            <select
              id="import-format"
              className={selectClasses}
              value={format}
              onChange={(e) => {
                setFormat(e.target.value as ImportFormat);
                setPreview(null);
                setResult(null);
              }}
            >
              {FORMATS.map((f) => (
                <option key={f.value} value={f.value}>
                  {f.label}
                </option>
              ))}
            </select>
          </div>
        </div>
        <Button size="sm" variant="secondary" disabled={!file || runPreview.isPending} onClick={() => runPreview.mutate()}>
          <FileUp className="w-4 h-4" />
          {runPreview.isPending ? 'Reading…' : 'Preview'}
        </Button>

        {error ? <p className="text-sm text-destructive">{error.message}</p> : null}

        {preview ? (
          <div className="space-y-3 border-t border-border pt-3">
            <p className="text-sm">
              <span className="font-semibold">{preview.questions.length}</span> question
              {preview.questions.length === 1 ? '' : 's'} ready to import
              {preview.warnings.length > 0 ? `, ${preview.warnings.length} warning${preview.warnings.length === 1 ? '' : 's'}` : ''}{' '}
              <span className="text-muted-foreground">({preview.format})</span>
            </p>
            <Warnings warnings={preview.warnings} label="Item" />
            {preview.questions.length > 0 ? (
              <div className="max-h-80 overflow-y-auto rounded-lg border border-border">
                <table className="w-full text-sm">
                  <thead className="bg-secondary text-xs text-muted-foreground sticky top-0">
                    <tr>
                      <th className="text-left px-2 py-1.5 w-8">#</th>
                      <th className="text-left px-2 py-1.5">Question</th>
                      <th className="text-left px-2 py-1.5">Answers</th>
                    </tr>
                  </thead>
                  <tbody>
                    {preview.questions.map((q) => (
                      <tr key={q.index} className="border-t border-border align-top">
                        <td className="px-2 py-1.5 text-muted-foreground">{q.index}</td>
                        <td className="px-2 py-1.5">
                          <p className="whitespace-pre-wrap">{q.prompt}</p>
                          <p className="text-xs text-muted-foreground mt-0.5">
                            {q.kind === 'single' ? 'One answer' : 'Several answers'} · {q.points} point
                            {q.points === 1 ? '' : 's'}
                            {q.images.length > 0 ? (
                              <span className="inline-flex items-center gap-1 ml-1">
                                · <ImageIcon className="w-3 h-3" /> {q.images.join(', ')}
                              </span>
                            ) : null}
                          </p>
                        </td>
                        <td className="px-2 py-1.5">
                          <ul className="space-y-0.5">
                            {q.options.map((o, i) => (
                              <li key={i} className={o.correct ? 'font-semibold text-success' : undefined}>
                                {o.correct ? '✓ ' : '· '}
                                {o.label}
                              </li>
                            ))}
                          </ul>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : null}

            {!result && preview.questions.length > 0 ? (
              <div className="flex flex-wrap items-end gap-2">
                <div className="min-w-[200px] flex-1">
                  <Label htmlFor="import-category">Import into category</Label>
                  {categories.length === 0 ? (
                    <p className="text-sm text-muted-foreground">Add a category to the question bank first.</p>
                  ) : (
                    <select
                      id="import-category"
                      className={selectClasses}
                      value={categoryId}
                      onChange={(e) => setCategoryId(e.target.value)}
                    >
                      {categories.map((c) => (
                        <option key={c.id} value={c.id}>
                          {c.name}
                        </option>
                      ))}
                    </select>
                  )}
                </div>
                <Button size="sm" disabled={!categoryId || runCommit.isPending} onClick={() => runCommit.mutate()}>
                  {runCommit.isPending ? 'Importing…' : `Import ${preview.questions.length} question${preview.questions.length === 1 ? '' : 's'}`}
                </Button>
              </div>
            ) : null}
          </div>
        ) : null}

        {result ? (
          <div className="space-y-2 rounded-lg border border-success/40 bg-success/5 p-3">
            <p className="text-sm flex items-center gap-2 font-semibold">
              <CheckCircle2 className="w-4 h-4 text-success" />
              Imported {result.imported} question{result.imported === 1 ? '' : 's'}
              {result.skipped > 0 ? `, skipped ${result.skipped}` : ''}
              {result.images > 0 ? `, ${result.images} picture${result.images === 1 ? '' : 's'} added to the media library` : ''}
            </p>
            <Warnings warnings={result.warnings} label="Item" />
            <Button size="sm" variant="secondary" onClick={onClose}>
              Done
            </Button>
          </div>
        ) : null}
      </div>
    </div>
  );
}

function Warnings({ warnings, label }: { warnings: { index: number; message: string }[]; label: string }) {
  if (warnings.length === 0) return null;
  return (
    <ul className="max-h-40 overflow-y-auto space-y-1 text-xs">
      {warnings.map((w, i) => (
        <li key={i} className="flex items-start gap-1.5">
          <AlertTriangle className="w-3.5 h-3.5 shrink-0 text-warning mt-0.5" />
          <span>
            {w.index > 0 ? `${label} ${w.index}: ` : ''}
            {w.message}
          </span>
        </li>
      ))}
    </ul>
  );
}
