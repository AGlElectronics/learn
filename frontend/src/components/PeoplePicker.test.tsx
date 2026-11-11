import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen } from '@testing-library/react';
import { PeoplePicker } from '@/components/PeoplePicker';
import { mockApi, renderWithProviders } from '@/test/render';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('PeoplePicker', () => {
  it('lists Learn people and directory hits, hides enrolled people, and shows emails only when sent', async () => {
    const fetchMock = mockApi({
      'GET /api/v1/users': {
        source: 'learn+directory',
        people: [
          { id: 'u-ann', display_name: 'Ann Lee' },
          { id: 'u-bob', display_name: 'Bob Enrolled' },
          { id: null, directory_id: 'kc-gil', display_name: 'Gil Dir' },
        ],
      },
    });
    const picked: string[] = [];
    renderWithProviders(
      <PeoplePicker
        id="pick"
        label="Add someone"
        exclude={new Set(['u-bob'])}
        actions={(p) => (
          <button type="button" onClick={() => picked.push(p.id ?? `dir:${p.directory_id}`)}>
            pick {p.display_name}
          </button>
        )}
      />
    );

    fireEvent.change(screen.getByLabelText('Add someone'), { target: { value: 'l' } });
    expect(await screen.findByText('Ann Lee')).toBeInTheDocument();
    expect(screen.queryByText('Bob Enrolled')).not.toBeInTheDocument();
    expect(screen.getByText('Gil Dir')).toBeInTheDocument();
    expect(screen.getByText('not signed in yet')).toBeInTheDocument();
    expect(screen.queryByText(/@/)).not.toBeInTheDocument();

    fireEvent.click(screen.getByText('pick Gil Dir'));
    expect(picked).toEqual(['dir:kc-gil']);
    const url = new URL(String(fetchMock.mock.calls[0][0]));
    expect(url.searchParams.get('q')).toBe('l');
  });

  it('shows emails the server returns (managers)', async () => {
    mockApi({
      'GET /api/v1/users': { source: 'learn', people: [{ id: 'u-ann', display_name: 'Ann Lee', email: 'ann@example.test' }] },
    });
    renderWithProviders(<PeoplePicker id="pick" label="Add someone" actions={() => null} />);
    fireEvent.change(screen.getByLabelText('Add someone'), { target: { value: 'ann' } });
    expect(await screen.findByText(/ann@example\.test/)).toBeInTheDocument();
  });
});
