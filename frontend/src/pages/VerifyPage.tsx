import { useQuery } from '@tanstack/react-query';
import { useParams } from '@tanstack/react-router';
import { BadgeCheck, CircleAlert, CircleX } from 'lucide-react';
import { Card, CardContent } from '@/components/ui/Card';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { formatDate, STATUS_LABEL, verifyCertificate, type CertificateStatus } from '@/api/qualifications';
import { LanguageSwitcher } from '@/components/LanguageSwitcher';
import { useT } from '@/i18n';
import { cn } from '@/lib/utils';

const STATUS_STYLE: Record<CertificateStatus, string> = {
  valid: 'text-success',
  expired: 'text-warning',
  revoked: 'text-destructive',
};

const STATUS_ICON = {
  valid: BadgeCheck,
  expired: CircleAlert,
  revoked: CircleX,
} as const;

/** Public page behind the certificate QR code: who holds what, and is it still good. */
export function VerifyPage() {
  const { code } = useParams({ from: '/verify/$code' });
  const t = useT();
  const q = useQuery({
    queryKey: ['verify', code],
    queryFn: ({ signal }) => verifyCertificate(code, signal),
    retry: false,
  });

  return (
    <div className="min-h-screen flex flex-col items-center justify-center gap-3 bg-background px-4 py-10">
      <Card className="w-full max-w-md">
        <CardContent className="py-8 space-y-4 text-center">
          <p className="text-xs uppercase tracking-widest text-muted-foreground">{t('verify.heading')}</p>
          {q.isLoading ? (
            <LoadingRow message={t('verify.checking')} />
          ) : q.isError ? (
            <ErrorRow message={(q.error as Error).message} />
          ) : !q.data ? (
            <>
              <CircleX className="w-12 h-12 mx-auto text-destructive" />
              <p className="font-heading text-xl font-bold">{t('verify.notFound')}</p>
              <p className="text-sm text-muted-foreground">
                {t('verify.notFoundHint', { code })}
              </p>
            </>
          ) : (
            (() => {
              const v = q.data;
              const Icon = STATUS_ICON[v.status];
              return (
                <>
                  <Icon className={cn('w-12 h-12 mx-auto', STATUS_STYLE[v.status])} />
                  <p className={cn('font-heading text-2xl font-extrabold', STATUS_STYLE[v.status])}>
                    {t(STATUS_LABEL[v.status])}
                  </p>
                  <div>
                    <p className="text-lg font-semibold">{v.display_name}</p>
                    <p className="text-muted-foreground">{v.qualification}</p>
                  </div>
                  <dl className="grid grid-cols-2 gap-2 text-sm text-left max-w-xs mx-auto">
                    <dt className="text-muted-foreground">{t('cert.issued')}</dt>
                    <dd>{formatDate(v.issued_at)}</dd>
                    <dt className="text-muted-foreground">{v.status === 'expired' ? t('cert.status.expired') : t('cert.validUntil')}</dt>
                    <dd>{formatDate(v.expires_at) ?? t('cert.noExpiry')}</dd>
                  </dl>
                  <p className="text-xs text-muted-foreground font-mono">{code.toUpperCase()}</p>
                </>
              );
            })()
          )}
        </CardContent>
      </Card>
      <LanguageSwitcher className="w-48" />
    </div>
  );
}
