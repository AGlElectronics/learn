import { useRef } from 'react';
import { ImageOff, Loader2 } from 'lucide-react';
import { useMediaUrl, type MediaRef } from '@/api/media';
import { cn } from '@/lib/utils';

/**
 * MediaView shows one stored file (picture, audio or video) through a
 * short-lived viewing link that refreshes itself; a link that expired
 * mid-view is fetched again when the element reports a load error.
 */
export function MediaView({
  media,
  className,
  alt,
}: {
  media: MediaRef;
  className?: string;
  /** Overrides the file's alt text, e.g. "" for a decorative thumbnail. */
  alt?: string;
}) {
  const { url, error, refresh: refetch } = useMediaUrl(media.id);
  // A file that is genuinely broken would otherwise refetch forever.
  const retries = useRef(0);
  const refresh = () => {
    if (retries.current++ < 2) refetch();
  };
  if (error) {
    return (
      <div className={cn('flex items-center gap-2 text-xs text-muted-foreground p-2', className)}>
        <ImageOff className="w-4 h-4" />
        {error.message || 'Could not load this file.'}
      </div>
    );
  }
  if (!url) {
    return (
      <div className={cn('flex items-center justify-center p-4 text-muted-foreground', className)}>
        <Loader2 className="w-4 h-4 animate-spin" />
      </div>
    );
  }
  if (media.kind === 'audio') {
    return <audio controls preload="metadata" src={url} onError={refresh} className={cn('w-full', className)} />;
  }
  if (media.kind === 'video') {
    return (
      <video controls preload="metadata" src={url} onError={refresh} className={cn('w-full rounded-md bg-black', className)} />
    );
  }
  return (
    <img
      src={url}
      alt={alt ?? media.alt_text}
      onError={refresh}
      draggable={false}
      className={cn('max-w-full rounded-md', className)}
    />
  );
}
