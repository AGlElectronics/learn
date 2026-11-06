import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from '@/api/client';

export type MyChannels = {
  email: string;
  telegram_chat_id: string;
  /** Whether the deployment sends email / has a Telegram bot configured. */
  email_enabled: boolean;
  telegram_enabled: boolean;
};

export function getMyChannels(signal?: AbortSignal) {
  return apiRequest<MyChannels>('/api/v1/me/channels', { signal });
}

export function setMyChannels(body: { telegram_chat_id: string }) {
  return apiRequest<MyChannels>('/api/v1/me/channels', { method: 'PUT', body });
}

const KEY = ['me', 'channels'] as const;

export function useMyChannels() {
  return useQuery({ queryKey: KEY, queryFn: ({ signal }) => getMyChannels(signal), retry: false });
}

export function useSaveMyChannels() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: setMyChannels,
    onSuccess: (data) => qc.setQueryData(KEY, data),
  });
}
