import { useRef, useState } from 'react';
import { Circle, Square, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { MediaView } from '@/components/media/MediaView';
import type { MediaRef } from '@/api/media';
import type { Point, RegionInput, RegionShape } from '@/api/quizzes';
import { cn } from '@/lib/utils';

// Coordinates are normalized 0..1 of the picture, so regions and taps line
// up at any display size. The overlay SVG uses viewBox 0 0 1 1 stretched
// over the picture; a "circle" is therefore an ellipse in that space, which
// is what the server marks against.

const clamp = (v: number) => Math.min(1, Math.max(0, v));

function pointIn(el: Element, e: { clientX: number; clientY: number }): Point {
  const r = el.getBoundingClientRect();
  return { x: clamp((e.clientX - r.left) / r.width), y: clamp((e.clientY - r.top) / r.height) };
}

export function regionContains(region: RegionInput, p: Point) {
  const c = region.coords;
  if (region.shape === 'rect') {
    return p.x >= c.x && p.x <= c.x + c.w && p.y >= c.y && p.y <= c.y + c.h;
  }
  const dx = (p.x - c.cx) / c.rx;
  const dy = (p.y - c.cy) / c.ry;
  return dx * dx + dy * dy <= 1;
}

function regionFromBox(shape: RegionShape, a: Point, b: Point): RegionInput {
  const x = Math.min(a.x, b.x);
  const y = Math.min(a.y, b.y);
  const w = Math.abs(a.x - b.x);
  const h = Math.abs(a.y - b.y);
  if (shape === 'rect') return { shape, coords: { x, y, w, h }, correct: true };
  return { shape, coords: { cx: x + w / 2, cy: y + h / 2, rx: w / 2, ry: h / 2 }, correct: true };
}

function RegionShapeSvg({
  region,
  className,
  onPointerDown,
}: {
  region: RegionInput;
  className?: string;
  onPointerDown?: (e: React.PointerEvent) => void;
}) {
  const c = region.coords;
  const common = { className, vectorEffect: 'non-scaling-stroke' as const, onPointerDown };
  return region.shape === 'rect' ? (
    <rect x={c.x} y={c.y} width={c.w} height={c.h} {...common} />
  ) : (
    <ellipse cx={c.cx} cy={c.cy} rx={c.rx} ry={c.ry} {...common} />
  );
}

/**
 * HotspotEditor draws the regions on a hotspot picture: pick rectangle or
 * circle, drag on the picture, then toggle correct or delete the selected
 * region. Controlled: the parent saves `regions`.
 */
export function HotspotEditor({
  media,
  regions,
  onChange,
  disabled,
}: {
  media: MediaRef;
  regions: RegionInput[];
  onChange: (regions: RegionInput[]) => void;
  disabled?: boolean;
}) {
  const svgRef = useRef<SVGSVGElement>(null);
  const [tool, setTool] = useState<RegionShape>('rect');
  const [selected, setSelected] = useState<number | null>(null);
  const [drag, setDrag] = useState<{ from: Point; to: Point } | null>(null);
  const sel = selected !== null ? regions[selected] : undefined;

  return (
    <div className="space-y-2">
      {!disabled ? (
        <div className="flex flex-wrap items-center gap-1.5 text-xs">
          <span className="text-muted-foreground">Draw:</span>
          {(['rect', 'circle'] as const).map((s) => (
            <button
              key={s}
              type="button"
              onClick={() => setTool(s)}
              className={cn(
                'inline-flex items-center gap-1 px-2.5 py-1 rounded-pill border',
                s === tool ? 'bg-primary text-primary-foreground border-primary' : 'border-border text-muted-foreground'
              )}
            >
              {s === 'rect' ? <Square className="w-3 h-3" /> : <Circle className="w-3 h-3" />}
              {s === 'rect' ? 'Rectangle' : 'Circle'}
            </button>
          ))}
          {sel ? (
            <>
              <span className="mx-1 text-muted-foreground">Selected:</span>
              <Button
                type="button"
                variant="secondary"
                size="sm"
                onClick={() => onChange(regions.map((r, i) => (i === selected ? { ...r, correct: !r.correct } : r)))}
              >
                {sel.correct ? 'Correct — mark as distractor' : 'Distractor — mark as correct'}
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="sm"
                className="text-destructive"
                onClick={() => {
                  onChange(regions.filter((_, i) => i !== selected));
                  setSelected(null);
                }}
              >
                <Trash2 className="w-4 h-4" />
              </Button>
            </>
          ) : null}
        </div>
      ) : null}
      <div className="relative inline-block max-w-full select-none touch-none">
        <MediaView media={media} className="block w-full max-h-[480px] object-contain rounded-none" />
        <svg
          ref={svgRef}
          viewBox="0 0 1 1"
          preserveAspectRatio="none"
          className={cn('absolute inset-0 w-full h-full', !disabled && 'cursor-crosshair')}
          onPointerDown={(e) => {
            if (disabled || e.button !== 0) return;
            const p = pointIn(e.currentTarget, e);
            e.currentTarget.setPointerCapture(e.pointerId);
            setSelected(null);
            setDrag({ from: p, to: p });
          }}
          onPointerMove={(e) => {
            if (drag) setDrag({ ...drag, to: pointIn(e.currentTarget, e) });
          }}
          onPointerUp={() => {
            if (!drag) return;
            const r = regionFromBox(tool, drag.from, drag.to);
            setDrag(null);
            const big = r.shape === 'rect' ? r.coords.w > 0.01 && r.coords.h > 0.01 : r.coords.rx > 0.005 && r.coords.ry > 0.005;
            if (big) {
              onChange([...regions, r]);
              setSelected(regions.length);
            }
          }}
        >
          {regions.map((r, i) => (
            <RegionShapeSvg
              key={i}
              region={r}
              onPointerDown={(e) => {
                if (disabled) return;
                e.stopPropagation();
                setSelected(i);
              }}
              className={cn(
                'stroke-2 cursor-pointer',
                r.correct ? 'fill-success/25 stroke-success' : 'fill-destructive/20 stroke-destructive',
                i === selected && 'stroke-[3] [stroke-dasharray:4_3]'
              )}
            />
          ))}
          {drag ? (
            <RegionShapeSvg
              region={regionFromBox(tool, drag.from, drag.to)}
              className="fill-primary/20 stroke-primary stroke-2 pointer-events-none"
            />
          ) : null}
        </svg>
      </div>
      <p className="text-xs text-muted-foreground">
        {regions.filter((r) => r.correct).length} correct region(s), {regions.filter((r) => !r.correct).length}{' '}
        distractor(s). Green regions must each get one tap; taps anywhere else are wrong.
      </p>
    </div>
  );
}

/**
 * HotspotAnswer is the learner's side: tap the picture to place markers (up
 * to maxTaps), tap a marker to remove it. With `regions` (after
 * submitting) it shows the correct areas and which taps hit them.
 */
export function HotspotAnswer({
  media,
  maxTaps,
  taps,
  onChange,
  regions,
}: {
  media: MediaRef;
  maxTaps: number;
  taps: Point[];
  onChange?: (taps: Point[]) => void;
  regions?: RegionInput[];
}) {
  const readOnly = !onChange;
  const correctRegions = (regions ?? []).filter((r) => r.correct);
  return (
    <div className="space-y-1.5">
      <div className="relative inline-block max-w-full select-none">
        <MediaView media={media} className="block w-full max-h-[480px] object-contain rounded-none" />
        <svg
          viewBox="0 0 1 1"
          preserveAspectRatio="none"
          className={cn('absolute inset-0 w-full h-full', !readOnly && taps.length < maxTaps && 'cursor-crosshair')}
          onClick={(e) => {
            if (readOnly || taps.length >= maxTaps) return;
            onChange([...taps, pointIn(e.currentTarget, e)]);
          }}
        >
          {correctRegions.map((r, i) => (
            <RegionShapeSvg key={i} region={r} className="fill-success/20 stroke-success stroke-2 [stroke-dasharray:4_3]" />
          ))}
        </svg>
        {taps.map((t, i) => {
          const hit = regions ? correctRegions.some((r) => regionContains(r, t)) : undefined;
          return (
            <button
              key={i}
              type="button"
              disabled={readOnly}
              aria-label={readOnly ? `Tap ${i + 1}` : `Remove tap ${i + 1}`}
              onClick={() => onChange?.(taps.filter((_, j) => j !== i))}
              className={cn(
                'absolute -translate-x-1/2 -translate-y-1/2 w-6 h-6 rounded-full border-2 border-white text-[10px] font-bold text-white shadow',
                hit === undefined ? 'bg-primary' : hit ? 'bg-success' : 'bg-destructive'
              )}
              style={{ left: `${t.x * 100}%`, top: `${t.y * 100}%` }}
            >
              {i + 1}
            </button>
          );
        })}
      </div>
      {!readOnly ? (
        <p className="text-xs text-muted-foreground">
          Tap the picture to place {maxTaps === 1 ? 'a marker' : `up to ${maxTaps} markers`} ({taps.length}/{maxTaps}). Tap
          a marker to remove it.
        </p>
      ) : null}
    </div>
  );
}
