import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { GroupLinksPanel } from '@/components/GroupLinksPanel';
import { mockApi, renderWithProviders } from '@/test/render';

afterEach(() => {
  vi.unstubAllGlobals();
});

const link = {
  course_id: 'c1',
  group_name: 'learn/cohort-a',
  group_path: '/learn/cohort-a',
  path_resolved: true,
  created_at: '2026-11-01T10:00:00Z',
  last_synced_at: null,
  last_sync_error: '',
  members: 2,
  active: 2,
};

describe('GroupLinksPanel', () => {
  it('warns when the group directory is off and accepts a typed full path', async () => {
    const fetchMock = mockApi({
      // No GET /api/v1/groups: without a directory it fails, and the panel offers a text field.
      'GET /api/v1/courses/c1/group-links': { links: [], sync_available: false, directory: 'off' },
      'POST /api/v1/courses/c1/group-links': { link, sync: null },
    });
    renderWithProviders(<GroupLinksPanel courseId="c1" />);

    expect(await screen.findByRole('alert')).toHaveTextContent('GROUP_DIRECTORY=off');
    const input = await screen.findByLabelText('Link a group (full path)');
    fireEvent.change(input, { target: { value: '/learn/cohort-a' } });
    fireEvent.click(screen.getByRole('button', { name: 'Link group' }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.some(([, init]) => (init as RequestInit | undefined)?.method === 'POST')).toBe(true)
    );
    const post = fetchMock.mock.calls.find(([, init]) => (init as RequestInit | undefined)?.method === 'POST')!;
    expect(JSON.parse(String((post[1] as RequestInit).body))).toEqual({ group_path: '/learn/cohort-a' });
  });

  it('shows linked groups by path, the trainer limit and unresolved old links', async () => {
    mockApi({
      'GET /api/v1/courses/c1/group-links': {
        links: [link, { ...link, group_name: 'staff', group_path: '/staff', path_resolved: false }],
        sync_available: true,
        directory: 'keycloak',
      },
      'GET /api/v1/groups': {
        groups: [{ id: 'g1', name: 'cohort-b', path: '/learn/cohort-b' }],
        limited: true,
        linkable: ['/learn/*'],
      },
    });
    renderWithProviders(<GroupLinksPanel courseId="c1" />);

    expect(await screen.findByText('/learn/cohort-a')).toBeInTheDocument();
    expect(screen.getByText('/staff')).toBeInTheDocument();
    expect(screen.getByText(/its path was not found/)).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(await screen.findByText(/groups under \/learn\/\*/)).toBeInTheDocument();
    expect(screen.getByRole('option', { name: '/learn/cohort-b' })).toBeInTheDocument();
  });

  it('is read-only on an archived course', async () => {
    mockApi({
      'GET /api/v1/courses/c1/group-links': { links: [link], sync_available: true, directory: 'keycloak' },
    });
    renderWithProviders(<GroupLinksPanel courseId="c1" archived />);
    expect(await screen.findByText('/learn/cohort-a')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Link group' })).not.toBeInTheDocument();
    expect(screen.queryByTitle('Unlink group')).not.toBeInTheDocument();
  });
});
