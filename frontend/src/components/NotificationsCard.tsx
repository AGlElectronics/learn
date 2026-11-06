import { useEffect, useState } from 'react';
import { Bell } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { useMyChannels, useSaveMyChannels } from '@/api/channels';
import { useT } from '@/i18n';

/** Where reminders go: email today, Telegram (chat id saved now) later. */
export function NotificationsCard() {
  const t = useT();
  const q = useMyChannels();
  const save = useSaveMyChannels();
  const [chatId, setChatId] = useState('');
  useEffect(() => {
    if (q.data) setChatId(q.data.telegram_chat_id);
  }, [q.data]);
  const c = q.data;

  return (
    <Card className="mt-10">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Bell className="w-4 h-4 text-primary" />
          {t('notify.title')}
        </CardTitle>
        <CardDescription>{t('notify.description')}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {q.isLoading ? (
          <LoadingRow />
        ) : q.isError || !c ? (
          <ErrorRow message={t('notify.loadError')} />
        ) : (
          <>
            <p className="text-sm">
              {t('notify.email')}{' '}
              {c.email_enabled ? (
                <span className="font-medium">{c.email || t('notify.noAddress')}</span>
              ) : (
                <span className="text-muted-foreground">{t('notify.emailOff')}</span>
              )}
            </p>
            <form
              className="space-y-2 max-w-sm"
              onSubmit={(e) => {
                e.preventDefault();
                save.mutate({ telegram_chat_id: chatId.trim() });
              }}
            >
              <Label htmlFor="telegram-chat-id">{t('notify.telegramId')}</Label>
              <div className="flex gap-2">
                <Input
                  id="telegram-chat-id"
                  inputMode="numeric"
                  placeholder={t('notify.telegramPlaceholder')}
                  value={chatId}
                  onChange={(e) => setChatId(e.target.value)}
                />
                <Button type="submit" size="sm" disabled={save.isPending || chatId.trim() === c.telegram_chat_id}>
                  {t('common.save')}
                </Button>
              </div>
              <p className="text-xs text-muted-foreground">
                {t('notify.telegramHint')}
              </p>
              {save.isError ? <ErrorRow message={(save.error as Error).message} /> : null}
              {save.isSuccess ? <p className="text-xs text-success">{t('common.saved')}</p> : null}
            </form>
          </>
        )}
      </CardContent>
    </Card>
  );
}
