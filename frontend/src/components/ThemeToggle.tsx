import { Monitor, Moon, Sun } from 'lucide-react';
import { useT, type MessageKey } from '@/i18n';
import { useThemePreference, type ThemePreference } from '@/lib/theme';
import { cn } from '@/lib/utils';

const OPTIONS: { value: ThemePreference; label: MessageKey; Icon: typeof Sun }[] = [
  { value: 'light', label: 'theme.light', Icon: Sun },
  { value: 'system', label: 'theme.system', Icon: Monitor },
  { value: 'dark', label: 'theme.dark', Icon: Moon },
];

/** Light / System / Dark switch; the choice is shared by every Con Services app. */
export function ThemeToggle({ className }: { className?: string }) {
  const t = useT();
  const { preference, setPreference } = useThemePreference();
  return (
    <div
      role="radiogroup"
      aria-label={t('theme.label')}
      className={cn('flex w-full items-center gap-0.5 rounded-pill border border-border bg-background p-0.5', className)}
    >
      {OPTIONS.map(({ value, label, Icon }) => {
        const active = preference === value;
        return (
          <button
            key={value}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => setPreference(value)}
            className={cn(
              'flex flex-1 items-center justify-center gap-1.5 rounded-pill px-2 py-1.5 text-xs font-medium transition-colors',
              active ? 'bg-primary text-primary-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'
            )}
          >
            <Icon className="h-3.5 w-3.5 shrink-0" />
            {t(label)}
          </button>
        );
      })}
    </div>
  );
}
