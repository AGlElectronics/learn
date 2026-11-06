import { useQuery } from '@tanstack/react-query';
import { useParams } from '@tanstack/react-router';
import { ArrowLeft, Printer } from 'lucide-react';
import { QRCodeSVG } from 'qrcode.react';
import { Button, LinkButton } from '@/components/ui/Button';
import { PageContainer } from '@/components/ui/PageHeader';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { formatDate, getCertificate, STATUS_LABEL, verifyLink } from '@/api/qualifications';
import { useT } from '@/i18n';
import { cn } from '@/lib/utils';

// Print only the certificate sheet, on a light background, whatever the
// app chrome or theme around it.
const PRINT_CSS = `
@media print {
  @page { size: A4 landscape; margin: 12mm; }
  body * { visibility: hidden !important; }
  #certificate, #certificate * { visibility: visible !important; }
  #certificate {
    position: absolute; inset: 0; margin: 0 !important;
    box-shadow: none !important; background: #fff !important; color: #111 !important;
  }
  #certificate .cert-muted { color: #555 !important; }
}
`;

/** Printable certificate with a QR code to the public verify page. Holder or staff. */
export function CertificatePage() {
  const { certificateId } = useParams({ from: '/app/certificates/$certificateId' });
  const t = useT();
  const q = useQuery({
    queryKey: ['certificate', certificateId],
    queryFn: ({ signal }) => getCertificate(certificateId, signal),
    retry: false,
  });

  if (q.isLoading) {
    return (
      <PageContainer>
        <LoadingRow message={t('cert.loading')} />
      </PageContainer>
    );
  }
  if (q.isError || !q.data) {
    return (
      <PageContainer>
        <ErrorRow message={t('cert.loadError')} />
      </PageContainer>
    );
  }
  const c = q.data;
  const url = verifyLink(c);

  return (
    <PageContainer>
      <style>{PRINT_CSS}</style>
      <div className="flex flex-wrap items-center justify-between gap-2 mb-6 print:hidden">
        <LinkButton to="/courses" variant="ghost" size="sm">
          <ArrowLeft className="w-4 h-4" />
          {t('nav.myCourses')}
        </LinkButton>
        <Button size="sm" onClick={() => window.print()}>
          <Printer className="w-4 h-4" />
          {t('cert.print')}
        </Button>
      </div>
      {c.status !== 'valid' ? (
        <p
          className={cn(
            'mb-4 rounded-lg border px-4 py-3 text-sm print:hidden',
            c.status === 'revoked' ? 'border-destructive text-destructive' : 'border-warning text-warning'
          )}
        >
          {c.status === 'revoked' && c.revoked_reason
            ? t('cert.isRevokedReason', { reason: c.revoked_reason })
            : t('cert.isStatus', { status: t(STATUS_LABEL[c.status]).toLowerCase() })}
        </p>
      ) : null}
      <div
        id="certificate"
        className="mx-auto max-w-4xl aspect-[1.414/1] rounded-xl border-4 border-double border-border bg-card p-10 shadow-sm flex flex-col"
      >
        <div className="text-center space-y-1">
          <p className="cert-muted text-xs uppercase tracking-[0.4em] text-muted-foreground">{t('cert.heading')}</p>
          <h1 className="font-heading text-4xl font-extrabold tracking-tight">{c.qualification_name}</h1>
        </div>
        <div className="flex-1 flex flex-col items-center justify-center text-center gap-2">
          <p className="cert-muted text-muted-foreground">{t('cert.certifiesThat')}</p>
          <p className="font-heading text-3xl font-bold">{c.display_name}</p>
          <p className="cert-muted text-muted-foreground max-w-xl">
            {c.course_title ? t('cert.completedCourse', { course: c.course_title }) : t('cert.holdsQualification')}
            {c.qualification_description ? <>. {c.qualification_description}</> : null}
          </p>
        </div>
        <div className="flex items-end justify-between gap-6">
          <dl className="text-sm space-y-1">
            <div>
              <dt className="cert-muted inline text-muted-foreground">{t('cert.issued')} </dt>
              <dd className="inline font-medium">{formatDate(c.issued_at)}</dd>
            </div>
            <div>
              <dt className="cert-muted inline text-muted-foreground">{t('cert.validUntil')} </dt>
              <dd className="inline font-medium">{formatDate(c.expires_at) ?? t('cert.noExpiry')}</dd>
            </div>
            <div>
              <dt className="cert-muted inline text-muted-foreground">{t('cert.code')} </dt>
              <dd className="inline font-mono">{c.verify_code}</dd>
            </div>
          </dl>
          <div className="flex flex-col items-center gap-1">
            <div className="rounded-md bg-white p-2">
              <QRCodeSVG value={url} size={112} level="M" />
            </div>
            <p className="cert-muted text-[10px] text-muted-foreground">{t('cert.scan')}</p>
          </div>
        </div>
      </div>
    </PageContainer>
  );
}
