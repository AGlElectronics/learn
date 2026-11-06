import {
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  redirect,
} from '@tanstack/react-router';

import { Layout } from '@/components/Layout';
import { CoursePlayerPage } from '@/pages/CoursePlayerPage';
import { GradebookPage } from '@/pages/GradebookPage';
import { LoginPage } from '@/pages/LoginPage';
import { MyCoursesPage } from '@/pages/MyCoursesPage';
import { OidcCallbackPage } from '@/pages/OidcCallbackPage';
import { SettingsPage } from '@/pages/SettingsPage';
import { CheckinPage } from '@/pages/CheckinPage';
import { CertificatePage } from '@/pages/CertificatePage';
import { VerifyPage } from '@/pages/VerifyPage';
import { SessionCheckinPage } from '@/pages/SessionCheckinPage';
import { TeachCoursePage } from '@/pages/TeachCoursePage';
import { TeachPage } from '@/pages/TeachPage';
import { ReportsPage } from '@/pages/ReportsPage';
import { PathsPage } from '@/pages/PathsPage';
import { PathPage } from '@/pages/PathPage';
import { getUserManager, isOidcAuthMode } from '@/auth/oidc';

// In OIDC mode a route behind the Layout checks there is a cached user
// before letting the component mount. If not, we redirect to /login and
// carry the attempted path so the post-login redirect lands there.
async function requireAuth(fullPath: string) {
  if (!isOidcAuthMode()) {
    return; // dev mode: nothing to check
  }
  const mgr = getUserManager();
  if (!mgr) {
    throw redirect({ to: '/login', search: { redirect: fullPath } });
  }
  const user = await mgr.getUser();
  if (!user || user.expired) {
    throw redirect({ to: '/login', search: { redirect: fullPath } });
  }
}

const rootRoute = createRootRoute({
  component: () => <Outlet />,
});

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  component: LoginPage,
  validateSearch: (search: Record<string, unknown>) => ({
    redirect: typeof search.redirect === 'string' ? search.redirect : undefined,
  }),
});

const authCallbackRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/auth/callback',
  component: OidcCallbackPage,
});

// Public: whoever scans a certificate QR code is usually not signed in.
const verifyRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/verify/$code',
  component: VerifyPage,
});

const appLayoutRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'app',
  component: Layout,
});

const indexRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/',
  beforeLoad: async () => {
    await requireAuth('/courses');
    throw redirect({ to: '/courses' });
  },
});

const myCoursesRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/courses',
  beforeLoad: ({ location }) => requireAuth(location.pathname),
  component: MyCoursesPage,
});

const coursePlayerRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/courses/$courseId',
  beforeLoad: ({ location }) => requireAuth(location.pathname),
  component: CoursePlayerPage,
});

const teachRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/teach',
  beforeLoad: ({ location }) => requireAuth(location.pathname),
  component: TeachPage,
});

const teachCourseRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/teach/$courseId',
  beforeLoad: ({ location }) => requireAuth(location.pathname),
  component: TeachCoursePage,
});

const gradebookRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/teach/$courseId/grades',
  beforeLoad: ({ location }) => requireAuth(location.pathname),
  component: GradebookPage,
});

const sessionCheckinRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/teach/sessions/$slotId/checkin',
  beforeLoad: ({ location }) => requireAuth(location.pathname),
  component: SessionCheckinPage,
});

// href (not pathname) so ?code survives the login round trip.
const checkinRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/checkin/$slotId',
  validateSearch: (search: Record<string, unknown>) => ({
    code: typeof search.code === 'string' ? search.code : undefined,
  }),
  beforeLoad: ({ location }) => requireAuth(location.href),
  component: CheckinPage,
});

const certificateRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/certificates/$certificateId',
  beforeLoad: ({ location }) => requireAuth(location.pathname),
  component: CertificatePage,
});

const settingsRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/settings',
  beforeLoad: ({ location }) => requireAuth(location.pathname),
  component: SettingsPage,
});

const reportsRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/reports',
  beforeLoad: ({ location }) => requireAuth(location.pathname),
  component: ReportsPage,
});

const pathsRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/teach/paths',
  beforeLoad: ({ location }) => requireAuth(location.pathname),
  component: PathsPage,
});

const pathRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/teach/paths/$pathId',
  beforeLoad: ({ location }) => requireAuth(location.pathname),
  component: PathPage,
});

const routeTree = rootRoute.addChildren([
  loginRoute,
  authCallbackRoute,
  verifyRoute,
  appLayoutRoute.addChildren([
    indexRoute,
    myCoursesRoute,
    coursePlayerRoute,
    teachRoute,
    teachCourseRoute,
    gradebookRoute,
    sessionCheckinRoute,
    checkinRoute,
    certificateRoute,
    settingsRoute,
    reportsRoute,
    pathsRoute,
    pathRoute,
  ]),
]);

export const router = createRouter({ routeTree });

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
