import { useEffect, useState } from 'react';
import { Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input } from '@/components/ui/Input';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { MediaThumb, UploadForm } from '@/components/media/MediaPicker';
import { deleteMedia, updateMedia, useMediaLibrary, useMediaMutation, type Media } from '@/api/media';
import { ApiError } from '@/api/client';
import { cn } from '@/lib/utils';

function size(bytes: number) {
  return bytes >= 1024 * 1024 ? `${(bytes / 1024 / 1024).toFixed(1)} MB` : `${Math.max(1, Math.round(bytes / 1024))} KB`;
}

/**
 * The course media library. Nothing is deleted automatically: files no
 * content uses (including uploads that never finished) show under
 * "Unused", and staff delete them by hand.
 */
export function MediaLibrary({ courseId }: { courseId: string }) {
  const [unusedOnly, setUnusedOnly] = useState(false);
  const [uploading, setUploading] = useState(false);
  const libraryQ = useMediaLibrary(courseId, unusedOnly);
  const remove = useMediaMutation((id: string) => deleteMedia(id));
  const storageOff = libraryQ.error instanceof ApiError && libraryQ.error.code === 'unavailable';
  const items = libraryQ.data?.media ?? [];

  return (
    <Card>
      <CardHeader>
        <CardTitle>Media library</CardTitle>
        <CardDescription>
          Pictures, audio and video for this course. Files are never removed automatically — check "Unused" now and
          then and delete what you no longer need.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="flex flex-wrap items-center gap-1.5">
          {[false, true].map((u) => (
            <button
              key={String(u)}
              type="button"
              onClick={() => setUnusedOnly(u)}
              className={cn(
                'text-sm px-3 py-1 rounded-pill border transition-colors',
                u === unusedOnly
                  ? 'bg-accent text-accent-foreground border-primary/40 font-medium'
                  : 'border-border text-muted-foreground hover:bg-secondary'
              )}
            >
              {u ? 'Unused' : 'All'}
            </button>
          ))}
          <div className="flex-1" />
          <Button variant="secondary" size="sm" onClick={() => setUploading((v) => !v)}>
            {uploading ? 'Cancel' : 'Upload'}
          </Button>
        </div>
        {uploading ? (
          <div className="rounded-lg border border-border p-3">
            <UploadForm courseId={courseId} kinds={['image', 'audio', 'video']} onUploaded={() => setUploading(false)} />
          </div>
        ) : null}
        {libraryQ.isLoading ? (
          <LoadingRow />
        ) : storageOff ? (
          <p className="text-sm text-muted-foreground">File storage isn't configured on this server.</p>
        ) : libraryQ.isError ? (
          <ErrorRow message="Could not load the media library." />
        ) : items.length === 0 ? (
          <p className="text-sm text-muted-foreground">{unusedOnly ? 'Nothing unused.' : 'No files yet.'}</p>
        ) : (
          <ul className="space-y-2">
            {items.map((m) => (
              <MediaRow
                key={m.id}
                media={m}
                deleting={remove.isPending && remove.variables === m.id}
                onDelete={() => {
                  if (confirm(`Delete "${m.name || 'this file'}" permanently?`)) remove.mutate(m.id);
                }}
              />
            ))}
          </ul>
        )}
        {remove.isError ? <p className="text-sm text-destructive">{(remove.error as Error).message}</p> : null}
      </CardContent>
    </Card>
  );
}

function MediaRow({ media, deleting, onDelete }: { media: Media; deleting: boolean; onDelete: () => void }) {
  const [alt, setAlt] = useState(media.alt_text);
  useEffect(() => setAlt(media.alt_text), [media.alt_text]);
  const save = useMediaMutation((alt_text: string) => updateMedia(media.id, { alt_text }));
  const status =
    media.status === 'ready' ? null : media.upload_unfinished ? 'upload not finished' : 'uploading…';

  return (
    <li className="flex items-start gap-3 rounded-lg border border-border p-2">
      <div className="w-20 shrink-0">
        {media.status === 'ready' ? (
          <MediaThumb media={media} name={media.kind} className="h-16 w-20" />
        ) : (
          <span className="block text-xs text-muted-foreground">{media.kind}</span>
        )}
      </div>
      <div className="flex-1 min-w-0 space-y-1">
        <p className="text-sm font-medium truncate">{media.name || media.content_type}</p>
        <p className="text-xs text-muted-foreground">
          {size(media.size_bytes)} ·{' '}
          {media.unused ? 'unused' : `used ${media.usage_count} time${media.usage_count === 1 ? '' : 's'}`}
          {status ? <span className="text-destructive"> · {status}</span> : null}
        </p>
        {media.kind === 'image' ? (
          <Input
            value={alt}
            onChange={(e) => setAlt(e.target.value)}
            onBlur={() => {
              if (alt.trim() !== media.alt_text) save.mutate(alt.trim());
            }}
            placeholder="Alt text (required to use the picture)"
            className="h-8"
          />
        ) : null}
        {save.isError ? <p className="text-xs text-destructive">{(save.error as Error).message}</p> : null}
      </div>
      <Button
        variant="ghost"
        size="sm"
        className="text-destructive shrink-0"
        disabled={!media.unused || deleting}
        title={media.unused ? 'Delete' : 'In use — remove it from content first'}
        onClick={onDelete}
      >
        <Trash2 className="w-4 h-4" />
      </Button>
    </li>
  );
}
