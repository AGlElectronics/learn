import { useId, useState } from 'react';
import { FileAudio, FileVideo, ImagePlus, Upload, X } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { Input, Label } from '@/components/ui/Input';
import { LoadingRow } from '@/components/ui/States';
import { MediaView } from '@/components/media/MediaView';
import {
  acceptFor,
  MEDIA_MAX_MB,
  MEDIA_TYPES,
  uploadMedia,
  useMediaLibrary,
  useMediaMutation,
  type Media,
  type MediaKind,
  type MediaRef,
} from '@/api/media';
import { cn } from '@/lib/utils';

const KIND_WORD: Record<MediaKind, string> = { image: 'picture', audio: 'audio', video: 'video' };

export function kindsLabel(kinds: MediaKind[]) {
  return kinds.map((k) => KIND_WORD[k]).join(' / ');
}

/**
 * MediaPicker attaches one file: upload a new one (pictures need alt text)
 * or choose a ready file from the course library. The parent saves the
 * chosen id; this component only uploads and picks.
 */
export function MediaPicker({
  courseId,
  kinds = ['image'],
  value,
  onChange,
  disabled,
  label,
}: {
  courseId: string;
  kinds?: MediaKind[];
  value: MediaRef | null | undefined;
  onChange: (media: MediaRef | null) => void;
  disabled?: boolean;
  label?: string;
}) {
  const [open, setOpen] = useState(false);
  const word = label ?? kindsLabel(kinds);

  return (
    <div className="space-y-2">
      {value ? (
        <div className="flex items-start gap-2">
          <MediaThumb media={value} className="max-h-32" />
          {!disabled ? (
            <div className="flex flex-col gap-1">
              <Button type="button" variant="ghost" size="sm" onClick={() => setOpen((v) => !v)}>
                Change
              </Button>
              <Button type="button" variant="ghost" size="sm" className="text-destructive" onClick={() => onChange(null)}>
                <X className="w-4 h-4" />
                Remove
              </Button>
            </div>
          ) : null}
        </div>
      ) : !disabled ? (
        <Button type="button" variant="ghost" size="sm" onClick={() => setOpen((v) => !v)}>
          <ImagePlus className="w-4 h-4" />
          Add {word}
        </Button>
      ) : null}
      {open && !disabled ? (
        <PickerPanel
          courseId={courseId}
          kinds={kinds}
          onPick={(m) => {
            onChange(m);
            setOpen(false);
          }}
          onClose={() => setOpen(false)}
        />
      ) : null}
    </div>
  );
}

/** A small preview: the picture itself, or an icon for audio/video. */
export function MediaThumb({ media, className, name }: { media: MediaRef; className?: string; name?: string }) {
  if (media.kind === 'image') {
    return <MediaView media={media} className={cn('object-contain border border-border', className)} />;
  }
  const Icon = media.kind === 'audio' ? FileAudio : FileVideo;
  return (
    <span className="inline-flex items-center gap-1.5 rounded-md border border-border px-2 py-1 text-xs">
      <Icon className="w-4 h-4" />
      {name || media.content_type}
    </span>
  );
}

function PickerPanel({
  courseId,
  kinds,
  onPick,
  onClose,
}: {
  courseId: string;
  kinds: MediaKind[];
  onPick: (m: MediaRef) => void;
  onClose: () => void;
}) {
  const [tab, setTab] = useState<'upload' | 'library'>('upload');
  return (
    <div className="rounded-lg border border-border p-3 space-y-3 bg-card">
      <div className="flex items-center gap-1">
        {(['upload', 'library'] as const).map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => setTab(t)}
            className={cn(
              'text-xs px-3 py-1 rounded-pill border transition-colors',
              t === tab ? 'bg-accent text-accent-foreground border-primary/40 font-medium' : 'border-border text-muted-foreground'
            )}
          >
            {t === 'upload' ? 'Upload new' : 'From library'}
          </button>
        ))}
        <div className="flex-1" />
        <Button type="button" variant="ghost" size="sm" onClick={onClose} aria-label="Close">
          <X className="w-4 h-4" />
        </Button>
      </div>
      {tab === 'upload' ? (
        <UploadForm courseId={courseId} kinds={kinds} onUploaded={onPick} />
      ) : (
        <LibraryChooser courseId={courseId} kinds={kinds} onPick={onPick} />
      )}
    </div>
  );
}

export function UploadForm({
  courseId,
  kinds,
  onUploaded,
}: {
  courseId: string;
  kinds: MediaKind[];
  onUploaded: (m: Media) => void;
}) {
  const altId = useId();
  const [file, setFile] = useState<File | null>(null);
  const [alt, setAlt] = useState('');
  const [progress, setProgress] = useState<number | null>(null);
  const kind = file ? MEDIA_TYPES[file.type] : undefined;
  const tooBig = file && kind ? file.size > MEDIA_MAX_MB[kind] * 1024 * 1024 : false;
  const upload = useMediaMutation(() => uploadMedia(courseId, file!, alt.trim(), setProgress));
  const problem = !file
    ? null
    : !kind || !kinds.includes(kind)
      ? `This file type isn't allowed here (${kindsLabel(kinds)} only).`
      : tooBig
        ? `${KIND_WORD[kind]} files can be at most ${MEDIA_MAX_MB[kind]} MB.`
        : null;
  const needsAlt = kind === 'image' && !alt.trim();
  const start = () => {
    if (!file || problem || needsAlt) return;
    setProgress(0);
    upload.mutate(undefined, {
      onSuccess: (m) => onUploaded(m),
      onSettled: () => setProgress(null),
    });
  };

  // Not a <form>: the picker often sits inside another form.
  return (
    <div className="space-y-2">
      <input
        type="file"
        accept={acceptFor(kinds)}
        onChange={(e) => setFile(e.target.files?.[0] ?? null)}
        className="block w-full text-sm file:mr-3 file:rounded-pill file:border file:border-border file:bg-card file:px-3 file:py-1 file:text-xs"
      />
      {kind === 'image' ? (
        <div>
          <Label htmlFor={altId}>Alt text (describes the picture for screen readers)</Label>
          <Input
            id={altId}
            value={alt}
            onChange={(e) => setAlt(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                start();
              }
            }}
            placeholder="A green fire exit sign above a door"
          />
        </div>
      ) : null}
      {problem ? <p className="text-sm text-destructive">{problem}</p> : null}
      {progress !== null ? (
        <div className="h-2 rounded-pill bg-secondary overflow-hidden">
          <div className="h-full bg-primary transition-all" style={{ width: `${Math.round(progress * 100)}%` }} />
        </div>
      ) : null}
      <Button type="button" size="sm" onClick={start} disabled={!file || !!problem || needsAlt || upload.isPending}>
        <Upload className="w-4 h-4" />
        {upload.isPending ? 'Uploading…' : 'Upload'}
      </Button>
      {upload.isError ? <p className="text-sm text-destructive">{(upload.error as Error).message}</p> : null}
    </div>
  );
}

function LibraryChooser({
  courseId,
  kinds,
  onPick,
}: {
  courseId: string;
  kinds: MediaKind[];
  onPick: (m: MediaRef) => void;
}) {
  const libraryQ = useMediaLibrary(courseId);
  if (libraryQ.isLoading) return <LoadingRow />;
  if (libraryQ.isError) return <p className="text-sm text-destructive">{(libraryQ.error as Error).message}</p>;
  const items = (libraryQ.data?.media ?? []).filter((m) => m.status === 'ready' && kinds.includes(m.kind));
  if (items.length === 0) {
    return <p className="text-sm text-muted-foreground">No {kindsLabel(kinds)} files in this course yet.</p>;
  }
  return (
    <ul className="grid grid-cols-2 sm:grid-cols-3 gap-2 max-h-72 overflow-y-auto">
      {items.map((m) => {
        const noAlt = m.kind === 'image' && !m.alt_text.trim();
        return (
          <li key={m.id}>
            <button
              type="button"
              disabled={noAlt}
              onClick={() => onPick(m)}
              title={noAlt ? 'Add alt text in the media library first' : m.name}
              className="w-full rounded-md border border-border p-1.5 text-left hover:border-primary disabled:opacity-50 disabled:cursor-not-allowed"
            >
              <MediaThumb media={m} name={m.name} className="h-20 w-full" />
              <span className="block text-xs truncate mt-1">{m.name || m.content_type}</span>
              {noAlt ? <span className="block text-xs text-destructive">needs alt text</span> : null}
            </button>
          </li>
        );
      })}
    </ul>
  );
}
