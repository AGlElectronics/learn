import { DenConnection } from '@/components/DenConnection';
import { ErasePersonCard } from '@/components/ErasePersonCard';
import { ModuleSettingsCard } from '@/components/ModuleSettingsCard';
import { LinkButton } from '@/components/ui/Button';
import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { useT } from '@/i18n';

/**
 * Module settings (settings.manage): the Den pairing, Telegram, retention,
 * the group directory status and erasure. Qualifications have their own
 * page (qualification.manage).
 */
export function SettingsPage() {
  const t = useT();
  return (
    <PageContainer>
      <PageHeader
        title={t('nav.settings')}
        description={t('settings.description')}
        actions={
          <LinkButton to="/audit" variant="secondary" size="sm">
            {t('nav.audit')}
          </LinkButton>
        }
      />
      <div className="max-w-2xl space-y-6">
        <DenConnection base="/api/v1/den" />
        <ModuleSettingsCard />
        <ErasePersonCard />
      </div>
    </PageContainer>
  );
}
