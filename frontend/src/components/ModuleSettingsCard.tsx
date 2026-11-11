import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { getModuleSettings, saveModuleSettings, type ModuleSettings } from '@/api/admin';
import { GroupDirectoryWarning } from '@/components/GroupLinksPanel';
import { Button } from '@/components/ui/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { useT } from '@/i18n';

/** Telegram switch, retention periods and the group directory status. */
export function ModuleSettingsCard() {
  const t = useT();
  const qc = useQueryClient();
  const settingsQ = useQuery({ queryKey: ['module-settings'], queryFn: ({ signal }) => getModuleSettings(signal) });
  const save = useMutation({
    mutationFn: saveModuleSettings,
    onSuccess: (s) => qc.setQueryData(['module-settings'], s),
  });
  if (settingsQ.isLoading) return <LoadingRow />;
  if (settingsQ.isError) return <ErrorRow message={(settingsQ.error as Error).message} />;
  const s = settingsQ.data!;
  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>{t('settings.telegramTitle')}</CardTitle>
          <CardDescription>
            {s.telegram.configured ? t('settings.telegramConfigured') : t('settings.telegramMissing')}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={s.telegram.enabled}
              disabled={!s.telegram.configured || save.isPending}
              onChange={(e) => save.mutate({ telegram_enabled: e.target.checked })}
            />
            {t('settings.telegramEnabled')}
          </label>
        </CardContent>
      </Card>
      <RetentionCard settings={s} saving={save.isPending} onSave={(retention) => save.mutate({ retention })} />
      {save.isError ? <ErrorRow message={(save.error as Error).message} /> : null}
      <Card>
        <CardHeader>
          <CardTitle>{t('settings.directoryTitle')}</CardTitle>
          <CardDescription>
            {t('settings.directoryMode', { mode: s.group_directory.mode })}
            {' · '}
            {t('settings.directoryLinkable', { patterns: s.group_directory.linkable.join(', ') || '—' })}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <GroupDirectoryWarning mode={s.group_directory.mode} available={s.group_directory.available} />
          {s.group_directory.available ? <p className="text-sm">{t('settings.directoryOn')}</p> : null}
        </CardContent>
      </Card>
    </>
  );
}

function RetentionCard({
  settings,
  saving,
  onSave,
}: {
  settings: ModuleSettings;
  saving: boolean;
  onSave: (r: { inactive_user_months: number; audit_log_months: number }) => void;
}) {
  const t = useT();
  const r = settings.retention;
  const [inactive, setInactive] = useState(String(r.inactive_user_months));
  const [auditMonths, setAuditMonths] = useState(String(r.audit_log_months));
  useEffect(() => {
    setInactive(String(r.inactive_user_months));
    setAuditMonths(String(r.audit_log_months));
  }, [r.inactive_user_months, r.audit_log_months]);
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('settings.retentionTitle')}</CardTitle>
        <CardDescription>
          {r.source === 'den' ? t('settings.retentionFromDen') : t('settings.retentionDescription')}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {r.pending_legal_review ? (
          <p role="note" className="mb-3 rounded-md border border-border bg-muted px-3 py-2 text-xs">
            {t('settings.retentionPending')}
          </p>
        ) : null}
        <form
          className="grid gap-3 sm:grid-cols-2"
          onSubmit={(e) => {
            e.preventDefault();
            onSave({ inactive_user_months: Number(inactive), audit_log_months: Number(auditMonths) });
          }}
        >
          <div>
            <Label htmlFor="retention-inactive">{t('settings.retentionInactive')}</Label>
            <Input
              id="retention-inactive"
              type="number"
              min={0}
              value={inactive}
              disabled={!r.editable}
              onChange={(e) => setInactive(e.target.value)}
            />
          </div>
          <div>
            <Label htmlFor="retention-audit">{t('settings.retentionAudit')}</Label>
            <Input
              id="retention-audit"
              type="number"
              min={0}
              value={auditMonths}
              disabled={!r.editable}
              onChange={(e) => setAuditMonths(e.target.value)}
            />
          </div>
          {r.editable ? (
            <div className="sm:col-span-2">
              <Button type="submit" size="sm" disabled={saving}>
                {t('settings.save')}
              </Button>
            </div>
          ) : null}
        </form>
      </CardContent>
    </Card>
  );
}
