import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from '@/api/client';

export type TeamBoardTarget = 'course' | 'qualification';

/**
 * One team on a board: counts only, never names. Teams under three members
 * come back with null numbers and hidden_reason "too_small", so a percentage
 * can't single anyone out.
 */
export type TeamGroupStat = {
  group_name: string;
  members_count: number | null;
  complete_count: number | null;
  percent: number | null;
  hidden_reason?: 'too_small';
};

export type TeamBoard = {
  target_type: TeamBoardTarget;
  target_id: string;
  title: string;
  enabled: boolean;
  groups: TeamGroupStat[];
};

export type TeamBoardSettings = { enabled: boolean; groups: string[] };

function base(target: TeamBoardTarget, id: string) {
  return target === 'course' ? `/api/v1/courses/${id}/team-board` : `/api/v1/qualifications/${id}/team-board`;
}

export function getTeamBoard(target: TeamBoardTarget, id: string, signal?: AbortSignal) {
  return apiRequest<TeamBoard>(base(target, id), { signal });
}

export function getTeamBoardSettings(target: TeamBoardTarget, id: string, signal?: AbortSignal) {
  return apiRequest<TeamBoardSettings>(`${base(target, id)}/settings`, { signal });
}

export function setTeamBoardSettings(target: TeamBoardTarget, id: string, body: Partial<TeamBoardSettings>) {
  return apiRequest<TeamBoardSettings>(`${base(target, id)}/settings`, { method: 'PUT', body });
}

export function getMyTeams(signal?: AbortSignal) {
  return apiRequest<{ teams: TeamBoard[] }>('/api/v1/me/teams', { signal });
}

export const teamBoardKeys = {
  board: (target: TeamBoardTarget, id: string) => ['team-board', target, id] as const,
  settings: (target: TeamBoardTarget, id: string) => ['team-board', target, id, 'settings'] as const,
  mine: ['team-board', 'mine'] as const,
};

export function useTeamBoard(target: TeamBoardTarget, id: string) {
  return useQuery({
    queryKey: teamBoardKeys.board(target, id),
    queryFn: ({ signal }) => getTeamBoard(target, id, signal),
    retry: false,
  });
}

export function useTeamBoardSettings(target: TeamBoardTarget, id: string) {
  return useQuery({
    queryKey: teamBoardKeys.settings(target, id),
    queryFn: ({ signal }) => getTeamBoardSettings(target, id, signal),
    retry: false,
  });
}

export function useSaveTeamBoardSettings(target: TeamBoardTarget, id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<TeamBoardSettings>) => setTeamBoardSettings(target, id, body),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['team-board'] }),
  });
}

export function useMyTeams() {
  return useQuery({
    queryKey: teamBoardKeys.mine,
    queryFn: ({ signal }) => getMyTeams(signal),
    retry: false,
  });
}
