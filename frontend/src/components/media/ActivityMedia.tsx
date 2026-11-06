import { useEffect, useState } from 'react';
import { ArrowDown, ArrowUp, Link2, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { Input, Label } from '@/components/ui/Input';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { MediaPicker, MediaThumb } from '@/components/media/MediaPicker';
import { MediaView } from '@/components/media/MediaView';
import {
  addActivityMedia,
  deleteActivityMedia,
  updateActivityMedia,
  useActivityMedia,
  useMediaMutation,
  type ActivityMedia,
} from '@/api/media';

function Embed({ item }: { item: ActivityMedia }) {
  return (
    <div className="aspect-video w-full overflow-hidden rounded-md bg-black">
      <iframe
        src={item.embed_url}
        title={item.caption || 'Embedded video'}
        className="h-full w-full"
        allow="fullscreen; picture-in-picture; encrypted-media"
        referrerPolicy="strict-origin-when-cross-origin"
        loading="lazy"
      />
    </div>
  );
}

/**
 * The attachments below a page's text: pictures as a gallery, then audio,
 * video and embedded YouTube / Vimeo videos, each with its caption.
 */
export function ActivityMediaView({ activityId }: { activityId: string }) {
  const q = useActivityMedia(activityId);
  const items = q.data?.attachments ?? [];
  if (q.isLoading || q.isError || items.length === 0) return null;
  const images = items.filter((a) => a.media?.kind === 'image');
  const others = items.filter((a) => a.media?.kind !== 'image');
  return (
    <div className="mt-5 space-y-4">
      {images.length > 0 ? (
        <div className="grid grid-cols-2 sm:grid-cols-3 gap-3">
          {images.map((a) => (
            <figure key={a.id} className="space-y-1">
              <MediaView media={a.media!} className="w-full max-h-64 object-contain border border-border" />
              {a.caption ? <figcaption className="text-xs text-muted-foreground">{a.caption}</figcaption> : null}
            </figure>
          ))}
        </div>
      ) : null}
      {others.map((a) => (
        <figure key={a.id} className="space-y-1">
          {a.media ? <MediaView media={a.media} /> : a.embed_url ? <Embed item={a} /> : null}
          {a.caption ? <figcaption className="text-xs text-muted-foreground">{a.caption}</figcaption> : null}
        </figure>
      ))}
    </div>
  );
}

/** Staff editor for a page's attachments: add, caption, reorder, remove. */
export function ActivityMediaEditor({ courseId, activityId }: { courseId: string; activityId: string }) {
  const q = useActivityMedia(activityId);
  const [url, setUrl] = useState('');
  const add = useMediaMutation((body: { media_id: string } | { external_url: string }) => addActivityMedia(activityId, body));
  const update = useMediaMutation(({ id, ...body }: { id: string; caption?: string; position?: number }) =>
    updateActivityMedia(id, body)
  );
  const remove = useMediaMutation((id: string) => deleteActivityMedia(id));
  const items = q.data?.attachments ?? [];
  const move = useMediaMutation(async ({ from, to }: { from: number; to: number }) => {
    const order = [...items];
    const [it] = order.splice(from, 1);
    order.splice(to, 0, it);
    await Promise.all(
      order.map((a, k) => (a.position === k ? null : updateActivityMedia(a.id, { position: k })))
    );
  });
  const error = (add.error ?? update.error ?? remove.error ?? move.error) as Error | null;

  return (
    <div className="space-y-2">
      <Label>Attachments (pictures, audio, video, YouTube / Vimeo links)</Label>
      {q.isLoading ? (
        <LoadingRow />
      ) : q.isError ? (
        <ErrorRow message="Could not load attachments." />
      ) : (
        <ul className="space-y-1.5">
          {items.map((a, i) => (
            <li key={a.id} className="flex items-center gap-2 rounded-md border border-border p-1.5">
              <div className="w-20 shrink-0">
                {a.media ? (
                  <MediaThumb media={a.media} className="h-12 w-20" />
                ) : (
                  <span className="inline-flex items-center gap-1 text-xs text-muted-foreground truncate">
                    <Link2 className="w-3 h-3" />
                    video link
                  </span>
                )}
              </div>
              <CaptionInput caption={a.caption} onSave={(caption) => update.mutate({ id: a.id, caption })} />
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={i === 0 || move.isPending}
                aria-label="Move up"
                onClick={() => move.mutate({ from: i, to: i - 1 })}
              >
                <ArrowUp className="w-4 h-4" />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                disabled={i === items.length - 1 || move.isPending}
                aria-label="Move down"
                onClick={() => move.mutate({ from: i, to: i + 1 })}
              >
                <ArrowDown className="w-4 h-4" />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="text-destructive"
                aria-label="Remove attachment"
                onClick={() => remove.mutate(a.id)}
              >
                <Trash2 className="w-4 h-4" />
              </Button>
            </li>
          ))}
        </ul>
      )}
      <div className="flex flex-wrap items-start gap-3">
        <MediaPicker
          courseId={courseId}
          kinds={['image', 'audio', 'video']}
          value={null}
          onChange={(m) => {
            if (m) add.mutate({ media_id: m.id });
          }}
        />
        <form
          className="flex flex-1 min-w-[16rem] gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (url.trim()) add.mutate({ external_url: url.trim() }, { onSuccess: () => setUrl('') });
          }}
        >
          <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://www.youtube.com/watch?v=…" className="h-8" />
          <Button type="submit" variant="ghost" size="sm" disabled={!url.trim() || add.isPending}>
            Add link
          </Button>
        </form>
      </div>
      {error ? <p className="text-sm text-destructive">{error.message}</p> : null}
    </div>
  );
}

function CaptionInput({ caption, onSave }: { caption: string; onSave: (caption: string) => void }) {
  const [value, setValue] = useState(caption);
  useEffect(() => setValue(caption), [caption]);
  return (
    <Input
      value={value}
      onChange={(e) => setValue(e.target.value)}
      onBlur={() => {
        if (value !== caption) onSave(value);
      }}
      placeholder="Caption (optional)"
      className="h-8 flex-1"
    />
  );
}
