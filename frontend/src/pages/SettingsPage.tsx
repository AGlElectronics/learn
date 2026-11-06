import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { DenConnection } from '@/components/DenConnection';
import { QualificationsSettings } from '@/components/QualificationsSettings';
import { useT } from '@/i18n';

/**
 * Module settings. The slice only has the Den pairing; organisation-wide
 * settings (name, templates) live in Den once paired.
 */
export function SettingsPage() {
  const t = useT();
  return (
    <PageContainer>
      <PageHeader
        title={t('nav.settings')}
        description={t('settings.description')}
      />
      <div className="max-w-2xl space-y-6">
        <DenConnection base="/api/v1/den" />
        <QualificationsSettings />
      </div>
    </PageContainer>
  );
}
