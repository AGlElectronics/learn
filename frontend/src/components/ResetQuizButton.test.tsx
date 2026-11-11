import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { ResetQuizButton } from '@/components/ResetQuizButton';
import { me, mockApi, renderWithProviders } from '@/test/render';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('ResetQuizButton', () => {
  it('is hidden from trainers without course.manage', async () => {
    const fetchMock = mockApi({ 'GET /api/v1/me': me(['course.teach', 'enrol.manage']) });
    renderWithProviders(<ResetQuizButton activityId="q1" title="Final check" onDone={() => undefined} />);
    await waitFor(() => expect(fetchMock).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByRole('button', { name: /Reset this cycle/ })).not.toBeInTheDocument();
  });

  it('needs the quiz title typed before it resets', async () => {
    const onDone = vi.fn();
    const fetchMock = mockApi({
      'GET /api/v1/me': me(['course.manage']),
      'POST /api/v1/activities/q1/reset': { attempts: 3, completions: 2 },
    });
    renderWithProviders(<ResetQuizButton activityId="q1" title="Final check" onDone={onDone} />);

    fireEvent.click(await screen.findByRole('button', { name: /Reset this cycle/ }));
    const confirm = screen.getByRole('button', { name: 'Reset quiz' });
    expect(confirm).toBeDisabled();
    const input = screen.getByLabelText(/Type the quiz title/);
    fireEvent.change(input, { target: { value: 'final check' } });
    expect(confirm).toBeDisabled();
    fireEvent.change(input, { target: { value: 'Final check' } });
    expect(confirm).toBeEnabled();
    fireEvent.click(confirm);

    expect(await screen.findByText('Deleted 3 attempt(s) and 2 completion(s).')).toBeInTheDocument();
    expect(onDone).toHaveBeenCalled();
    const post = fetchMock.mock.calls.find(([u]) => String(u).includes('/reset'))!;
    expect(JSON.parse(String((post[1] as RequestInit).body))).toEqual({ confirm: 'Final check' });
  });
});
