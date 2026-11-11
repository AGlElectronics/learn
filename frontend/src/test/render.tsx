import type { ReactElement } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render } from '@testing-library/react';
import { vi } from 'vitest';
import { I18nProvider } from '@/i18n';

type Route = (url: URL, init: RequestInit) => unknown;

/**
 * Stubs fetch with a map of "METHOD /path" to a JSON body (or a function of
 * the request). Unknown requests answer 404. Returns the mock so a test can
 * look at the calls.
 */
export function mockApi(routes: Record<string, unknown | Route>) {
  const fetchMock = vi.fn(async (input: string | URL, init: RequestInit = {}) => {
    const url = new URL(String(input), 'http://localhost');
    const key = `${init.method ?? 'GET'} ${url.pathname}`;
    if (!(key in routes)) {
      return new Response(JSON.stringify({ error: { code: 'not_found', message: `no mock for ${key}` } }), {
        status: 404,
        headers: { 'content-type': 'application/json' },
      });
    }
    const route = routes[key];
    const body = typeof route === 'function' ? (route as Route)(url, init) : route;
    return new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } });
  });
  vi.stubGlobal('fetch', fetchMock);
  return fetchMock;
}

/** The /api/v1/me answer for a caller with these permissions. */
export function me(permissions: string[]) {
  return {
    id: 'u-me',
    display_name: 'Me',
    email: 'me@example.test',
    issuer: 'dev',
    subject: 'me',
    memberships: [],
    permissions,
  };
}

/** Renders ui inside the providers every page has (React Query, i18n). */
export function renderWithProviders(ui: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider>{ui}</I18nProvider>
    </QueryClientProvider>
  );
}
