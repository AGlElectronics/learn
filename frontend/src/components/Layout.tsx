import { useEffect, useState } from 'react';
import { Link, Outlet, useRouterState } from '@tanstack/react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Award, BarChart3, GraduationCap, LogOut, Menu, Presentation, Settings, UsersRound, X } from 'lucide-react';
import { getReportScope } from '@/api/reports';
import { isOidcAuthMode, signOutOidc } from '@/auth/oidc';
import { DenConnection } from '@/components/DenConnection';
import { LanguageSwitcher, useLanguageSync } from '@/components/LanguageSwitcher';
import { ThemeToggle } from '@/components/ThemeToggle';
import { useBrand } from '@/brand/BrandProvider';
import { isDenPairingRequired, useDenState } from '@/hooks/useDenState';
import { useMe, PERMS } from '@/hooks/useMe';
import { useT, type MessageKey } from '@/i18n';
import { useThemeSync } from '@/lib/theme';
import { cn } from '@/lib/utils';

type NavItem = {
  to: string;
  label: MessageKey;
  icon: React.ComponentType<{ className?: string }>;
  perms: string[]; // empty = always visible
  /** Also visible to staff of at least one course. */
  courseStaff?: boolean;
};

const NAV: NavItem[] = [
  { to: '/courses', label: 'nav.myCourses', icon: GraduationCap, perms: [] },
  { to: '/teach', label: 'nav.teach', icon: Presentation, perms: [PERMS.courseTeach, PERMS.courseManage] },
  { to: '/course-roles', label: 'nav.courseRoles', icon: UsersRound, perms: [PERMS.staffManage] },
  { to: '/qualifications', label: 'nav.qualifications', icon: Award, perms: [PERMS.qualificationManage] },
  { to: '/reports', label: 'nav.reports', icon: BarChart3, perms: [PERMS.reportView], courseStaff: true },
  { to: '/settings', label: 'nav.settings', icon: Settings, perms: [PERMS.settingsManage] },
];

function Wordmark() {
  const { brand } = useBrand();
  return (
    <div className="flex items-center gap-2.5">
      {brand.logoUrl ? (
        <img src={brand.logoUrl} alt="" className="h-8 w-8 rounded-md object-contain" />
      ) : (
        <span className="rounded-pill bg-primary-soft p-1.5">
          <GraduationCap className="w-5 h-5 text-primary" />
        </span>
      )}
      <span className="leading-none">
        <span className="block font-heading text-xl font-extrabold tracking-tight">
          {brand.productName}
        </span>
        {brand.organizationName ? (
          <span className="block text-[11px] font-medium text-muted-foreground mt-0.5">
            {brand.organizationName}
          </span>
        ) : null}
      </span>
    </div>
  );
}

function NavLinks({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const { canAny } = useMe();
  const t = useT();
  const staffQ = useQuery({
    queryKey: ['reports', 'scope'],
    queryFn: ({ signal }) => getReportScope(signal),
    enabled: !canAny(PERMS.reportView) && canAny(PERMS.courseTeach, PERMS.courseManage),
    retry: false,
    staleTime: 60_000,
  });
  const isCourseStaff = (staffQ.data?.courses.length ?? 0) > 0;
  return (
    <div className="space-y-0.5">
      {NAV.map((item) => {
        const visible =
          item.perms.length === 0 || canAny(...item.perms) || (item.courseStaff === true && isCourseStaff);
        if (!visible) return null;
        const isActive =
          pathname === item.to || (item.to !== '/' && pathname.startsWith(item.to + '/'));
        return (
          <Link
            key={`${item.to}-${item.label}`}
            to={item.to}
            onClick={onNavigate}
            className={cn(
              'flex items-center gap-2.5 px-3 py-2 rounded-pill text-sm font-medium whitespace-nowrap transition-colors',
              isActive
                ? 'bg-accent text-accent-foreground'
                : 'text-muted-foreground hover:bg-secondary hover:text-foreground'
            )}
          >
            <item.icon className="w-4 h-4 shrink-0" />
            {t(item.label)}
          </Link>
        );
      })}
    </div>
  );
}

/** Shown instead of the app while the backend runs with DEN_MODE=required and is not paired. */
function DenPairingGate({ canManage }: { canManage: boolean }) {
  const qc = useQueryClient();
  const t = useT();
  return (
    <div className="min-h-screen grid place-items-center bg-background text-foreground p-6">
      <div className="max-w-xl w-full rounded-2xl border border-border bg-card p-6 space-y-4">
        <div className="flex justify-center">
          <Wordmark />
        </div>
        {canManage ? (
          <>
            <h1 className="font-heading text-lg font-bold text-center">{t('den.connectTitle')}</h1>
            <ol className="list-decimal space-y-1 pl-5 text-sm">
              <li>{t('den.stepPair')}</li>
              <li>{t('den.stepCopy')}</li>
              <li>{t('den.stepPaste')}</li>
            </ol>
            <DenConnection base="/api/v1/den" onPaired={() => void qc.invalidateQueries()} />
          </>
        ) : (
          <div className="space-y-1 text-center">
            <h1 className="font-heading text-lg font-bold">{t('den.waitingTitle')}</h1>
            <p className="text-sm text-muted-foreground">{t('den.waiting')}</p>
          </div>
        )}
        <div className="flex justify-center gap-2">
          <ThemeToggle className="rounded-pill" />
          <LanguageSwitcher persist />
          {isOidcAuthMode() ? (
            <button
              type="button"
              className="inline-flex items-center gap-2 text-sm px-3 py-2 rounded-pill text-muted-foreground hover:bg-secondary hover:text-foreground transition-colors"
              onClick={() => {
                signOutOidc().catch(() => undefined);
              }}
            >
              <LogOut className="w-4 h-4 shrink-0" />
              {t('nav.signOut')}
            </button>
          ) : null}
        </div>
      </div>
    </div>
  );
}

export function Layout() {
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const { me, can } = useMe();
  const denState = useDenState();
  const [drawerOpen, setDrawerOpen] = useState(false);
  useThemeSync('/api/v1/me/preferences');
  useLanguageSync();
  const t = useT();

  // Collapse the mobile drawer whenever the route changes.
  useEffect(() => {
    setDrawerOpen(false);
  }, [pathname]);

  if (me && isDenPairingRequired(denState.data)) {
    return <DenPairingGate canManage={can(PERMS.settingsManage)} />;
  }

  const footer = (
    <div className="mt-auto p-2 border-t border-border space-y-0.5">
      <ThemeToggle className="rounded-pill" />
      <LanguageSwitcher persist />
      {isOidcAuthMode() ? (
        <button
          type="button"
          className="w-full flex items-center gap-2 text-left text-sm px-3 py-2 rounded-pill text-muted-foreground hover:bg-secondary hover:text-foreground transition-colors"
          onClick={() => {
            signOutOidc().catch(() => undefined);
          }}
        >
          <LogOut className="w-4 h-4 shrink-0" />
          {t('nav.signOut')}
        </button>
      ) : null}
    </div>
  );

  return (
    <div className="min-h-screen w-full flex flex-col md:flex-row bg-background text-foreground">
      <header className="md:hidden sticky top-0 z-40 flex items-center gap-2 px-3 py-2.5 border-b border-border bg-card">
        <button
          type="button"
          className="p-2 rounded-pill hover:bg-secondary transition-colors"
          onClick={() => setDrawerOpen(true)}
          aria-label={t('nav.openMenu')}
        >
          <Menu className="w-5 h-5" />
        </button>
        <Wordmark />
      </header>

      {drawerOpen ? (
        <div
          className="md:hidden fixed inset-0 z-50 bg-black/50 backdrop-blur-[2px]"
          onClick={() => setDrawerOpen(false)}
        >
          <aside
            className="absolute inset-y-0 left-0 w-72 max-w-[85vw] bg-card border-r border-border flex flex-col"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="px-4 py-4 flex items-center justify-between border-b border-border">
              <Wordmark />
              <button
                type="button"
                className="p-2 rounded-pill hover:bg-secondary transition-colors"
                onClick={() => setDrawerOpen(false)}
                aria-label={t('nav.closeMenu')}
              >
                <X className="w-5 h-5" />
              </button>
            </div>
            <nav className="p-2 flex-1 overflow-y-auto">
              <NavLinks onNavigate={() => setDrawerOpen(false)} />
            </nav>
            {footer}
          </aside>
        </div>
      ) : null}

      <aside className="hidden md:flex shrink-0 md:w-60 md:min-h-screen border-r border-border bg-card flex-col">
        <div className="px-4 py-4 border-b border-border">
          <Wordmark />
        </div>
        <nav className="p-2 md:flex-1 overflow-y-auto">
          <NavLinks />
        </nav>
        {footer}
      </aside>

      <main className="flex-1 min-w-0 min-h-0 flex flex-col w-full md:min-h-screen">
        <Outlet />
      </main>
    </div>
  );
}
