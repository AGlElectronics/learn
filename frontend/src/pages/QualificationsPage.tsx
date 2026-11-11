import { QualificationsSettings } from '@/components/QualificationsSettings';
import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { useT } from '@/i18n';

/** Qualifications and certificates, for the training office (qualification.manage). */
export function QualificationsPage() {
  const t = useT();
  return (
    <PageContainer>
      <PageHeader title={t('nav.qualifications')} description={t('qualifications.pageDescription')} />
      <div className="max-w-2xl">
        <QualificationsSettings />
      </div>
    </PageContainer>
  );
}
