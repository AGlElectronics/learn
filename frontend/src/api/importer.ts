import { apiFetch } from '@/api/client';

export type ImportFormat = '' | 'moodle_xml' | 'gift' | 'aiken' | 'csv';

export type ImportWarning = { index: number; message: string };

export type ImportQuestion = {
  index: number;
  kind: 'single' | 'multiple';
  prompt: string;
  feedback: string;
  points: number;
  options: { label: string; correct: boolean }[];
  images: string[];
};

export type ImportPreview = { format: string; questions: ImportQuestion[]; warnings: ImportWarning[] };

export type ImportResult = {
  format: string;
  imported: number;
  skipped: number;
  images: number;
  warnings: ImportWarning[];
};

function importForm(file: File, format: ImportFormat, categoryId?: string) {
  const form = new FormData();
  form.append('file', file);
  if (format) form.append('format', format);
  if (categoryId) form.append('category_id', categoryId);
  return form;
}

/** Parse a file and show what would be imported; nothing is saved. */
export async function previewImport(courseId: string, file: File, format: ImportFormat) {
  const res = await apiFetch(`/api/v1/courses/${courseId}/question-import`, {
    method: 'POST',
    query: { step: 'preview' },
    body: importForm(file, format),
  });
  return (await res.json()) as ImportPreview;
}

/** Import the same file into a category. */
export async function commitImport(courseId: string, file: File, format: ImportFormat, categoryId: string) {
  const res = await apiFetch(`/api/v1/courses/${courseId}/question-import`, {
    method: 'POST',
    query: { step: 'commit' },
    body: importForm(file, format, categoryId),
  });
  return (await res.json()) as ImportResult;
}

/** Download the CSV template through the authenticated API. */
export async function downloadCsvTemplate() {
  const res = await apiFetch('/api/v1/question-import/csv-template');
  const url = URL.createObjectURL(await res.blob());
  const a = document.createElement('a');
  a.href = url;
  a.download = 'question-import-template.csv';
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}
