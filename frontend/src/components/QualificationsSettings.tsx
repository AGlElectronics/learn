import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Award, Ban, BarChart3, Languages, Pencil, Plus, Trash2, Users } from 'lucide-react';
import { Button, LinkButton } from '@/components/ui/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { TeamBoardSettingsPanel } from '@/components/TeamBoard';
import {
  createQualification,
  deleteQualification,
  formatDate,
  listQualifications,
  qualificationCertificates,
  revokeCertificate,
  STATUS_LABEL,
  updateQualification,
  type Qualification,
} from '@/api/qualifications';
import { TranslationEditor } from '@/components/Translations';
import { LANGUAGES, useT } from '@/i18n';
import { cn } from '@/lib/utils';

type Draft = { key: string; name: string; description: string; validMonths: string };

const EMPTY: Draft = { key: '', name: '', description: '', validMonths: '' };

function slug(name: string) {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '');
}

function QualificationForm({
  initial,
  editing,
  onDone,
}: {
  initial: Draft;
  editing?: Qualification;
  onDone: () => void;
}) {
  const qc = useQueryClient();
  const t = useT();
  const [d, setD] = useState<Draft>(initial);
  const save = useMutation({
    mutationFn: () => {
      const months = d.validMonths.trim() === '' ? null : Number(d.validMonths);
      const body = { key: d.key.trim(), name: d.name.trim(), description: d.description.trim(), valid_months: months };
      return editing ? updateQualification(editing.id, body) : createQualification(body);
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['qualifications'] });
      onDone();
    },
  });
  const id = editing?.id ?? 'new';

  return (
    <form
      className="space-y-3 rounded-lg border border-border p-3"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <div>
          <Label htmlFor={`qn-${id}`}>{t('tr.field.name')}</Label>
          <Input
            id={`qn-${id}`}
            value={d.name}
            required
            onChange={(e) =>
              setD((p) => ({
                ...p,
                name: e.target.value,
                key: editing || (p.key && p.key !== slug(p.name)) ? p.key : slug(e.target.value),
              }))
            }
          />
        </div>
        <div>
          <Label htmlFor={`qk-${id}`}>{t('quals.key')}</Label>
          <Input
            id={`qk-${id}`}
            value={d.key}
            required
            pattern="[a-z0-9][a-z0-9_.\-]*"
            title="Lower-case letters, digits, _ . -"
            onChange={(e) => setD((p) => ({ ...p, key: e.target.value }))}
          />
        </div>
      </div>
      <div>
        <Label htmlFor={`qd-${id}`}>{t('tr.field.description')}</Label>
        <Input
          id={`qd-${id}`}
          value={d.description}
          onChange={(e) => setD((p) => ({ ...p, description: e.target.value }))}
        />
      </div>
      <div className="max-w-xs">
        <Label htmlFor={`qv-${id}`}>{t('quals.validMonths')}</Label>
        <Input
          id={`qv-${id}`}
          type="number"
          min={1}
          placeholder={t('quals.neverExpires')}
          value={d.validMonths}
          onChange={(e) => setD((p) => ({ ...p, validMonths: e.target.value }))}
        />
        <p className="text-xs text-muted-foreground mt-1">
          {t('quals.validHint')}
        </p>
      </div>
      {save.isError ? <ErrorRow message={(save.error as Error).message} /> : null}
      <div className="flex gap-2">
        <Button type="submit" size="sm" disabled={save.isPending}>
          {editing ? t('common.save') : t('common.create')}
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onDone}>
          {t('common.cancel')}
        </Button>
      </div>
    </form>
  );
}

function Holders({ qualification }: { qualification: Qualification }) {
  const qc = useQueryClient();
  const t = useT();
  const q = useQuery({
    queryKey: ['qualifications', qualification.id, 'certificates'],
    queryFn: ({ signal }) => qualificationCertificates(qualification.id, signal),
  });
  const revoke = useMutation({
    mutationFn: ({ id, reason }: { id: string; reason: string }) => revokeCertificate(id, reason),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['qualifications'] }),
  });

  if (q.isLoading) return <LoadingRow message={t('quals.loadingHolders')} />;
  if (q.isError) return <ErrorRow message={t('quals.holdersError')} />;
  const certs = q.data?.certificates ?? [];
  if (certs.length === 0) return <p className="text-sm text-muted-foreground">{t('quals.noHolders')}</p>;

  return (
    <ul className="divide-y divide-border text-sm">
      {certs.map((c) => (
        <li key={c.id} className="flex items-center justify-between gap-2 py-2">
          <div className="min-w-0">
            <p className="font-medium truncate">{c.display_name}</p>
            <p
              className={cn(
                'text-xs',
                c.status === 'valid' ? 'text-muted-foreground' : c.status === 'revoked' ? 'text-destructive' : 'text-warning'
              )}
            >
              {t(STATUS_LABEL[c.status])} · {t('quals.issuedOn', { date: formatDate(c.issued_at) ?? '' })}
              {c.expires_at ? ` · ${t('quals.until', { date: formatDate(c.expires_at) ?? '' })}` : ''}
              {c.revoked_reason ? ` · ${c.revoked_reason}` : ''}
            </p>
          </div>
          <div className="flex gap-1 shrink-0">
            <LinkButton to={`/certificates/${c.id}`} variant="ghost" size="sm" title={t('quals.openCertificate')}>
              <Award className="w-4 h-4" />
            </LinkButton>
            {c.status !== 'revoked' ? (
              <Button
                variant="ghost"
                size="sm"
                title={t('quals.revoke')}
                disabled={revoke.isPending}
                onClick={() => {
                  const reason = window.prompt(t('quals.revokeReason', { name: c.display_name }));
                  if (reason && reason.trim()) revoke.mutate({ id: c.id, reason: reason.trim() });
                }}
              >
                <Ban className="w-4 h-4" />
              </Button>
            ) : null}
          </div>
        </li>
      ))}
      {revoke.isError ? <ErrorRow message={(revoke.error as Error).message} /> : null}
    </ul>
  );
}

/** Settings card: create/edit/delete qualifications and see or revoke issued certificates. */
export function QualificationsSettings() {
  const qc = useQueryClient();
  const t = useT();
  const q = useQuery({
    queryKey: ['qualifications', 'all'],
    queryFn: ({ signal }) => listQualifications(signal),
  });
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [board, setBoard] = useState<string | null>(null);
  const [translating, setTranslating] = useState<string | null>(null);
  const remove = useMutation({
    mutationFn: (id: string) => deleteQualification(id),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['qualifications'] }),
  });

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between gap-2">
          <div>
            <CardTitle>{t('quals.title')}</CardTitle>
            <CardDescription>
              {t('quals.description')}
            </CardDescription>
          </div>
          {!creating ? (
            <Button size="sm" onClick={() => setCreating(true)}>
              <Plus className="w-4 h-4" />
              {t('common.new')}
            </Button>
          ) : null}
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        {creating ? <QualificationForm initial={EMPTY} onDone={() => setCreating(false)} /> : null}
        {q.isLoading ? (
          <LoadingRow />
        ) : q.isError ? (
          <ErrorRow message={t('quals.loadAllError')} />
        ) : (q.data?.qualifications ?? []).length === 0 && !creating ? (
          <p className="text-sm text-muted-foreground">{t('quals.none')}</p>
        ) : (
          <ul className="space-y-3">
            {(q.data?.qualifications ?? []).map((item) =>
              editing === item.id ? (
                <li key={item.id}>
                  <QualificationForm
                    editing={item}
                    initial={{
                      key: item.key,
                      name: item.name,
                      description: item.description,
                      validMonths: item.valid_months ? String(item.valid_months) : '',
                    }}
                    onDone={() => setEditing(null)}
                  />
                </li>
              ) : (
                <li key={item.id} className="rounded-lg border border-border p-3 space-y-2">
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0">
                      <p className="font-semibold">{item.name}</p>
                      <p className="text-xs text-muted-foreground">
                        <span className="font-mono">{item.key}</span> ·{' '}
                        {item.valid_months ? t('quals.validFor', { count: item.valid_months }) : t('quals.neverExpiresLower')} ·{' '}
                        {t('paths.courseCount', { count: item.courses })} · {t('quals.certCount', { count: item.certificates })}
                      </p>
                      {item.description ? <p className="text-sm mt-1">{item.description}</p> : null}
                    </div>
                    <div className="flex gap-1 shrink-0">
                      <Button
                        variant="ghost"
                        size="sm"
                        title={t('team.board')}
                        onClick={() => setBoard(board === item.id ? null : item.id)}
                      >
                        <BarChart3 className="w-4 h-4" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        title={t('quals.holders')}
                        onClick={() => setOpen(open === item.id ? null : item.id)}
                      >
                        <Users className="w-4 h-4" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        title={t('tr.title')}
                        onClick={() => setTranslating(translating === item.id ? null : item.id)}
                      >
                        <Languages className="w-4 h-4" />
                      </Button>
                      <Button variant="ghost" size="sm" title={t('common.edit')} onClick={() => setEditing(item.id)}>
                        <Pencil className="w-4 h-4" />
                      </Button>
                      {item.certificates === 0 ? (
                        <Button
                          variant="ghost"
                          size="sm"
                          title={t('common.delete')}
                          disabled={remove.isPending}
                          onClick={() => {
                            if (window.confirm(t('quals.confirmDelete', { name: item.name }))) remove.mutate(item.id);
                          }}
                        >
                          <Trash2 className="w-4 h-4" />
                        </Button>
                      ) : null}
                    </div>
                  </div>
                  {board === item.id ? (
                    <div className="rounded-md border border-border p-3">
                      <p className="text-sm font-semibold mb-1">{t('team.board')}</p>
                      <p className="text-xs text-muted-foreground mb-2">
                        {t('quals.boardHint')}
                      </p>
                      <TeamBoardSettingsPanel target="qualification" id={item.id} bare />
                    </div>
                  ) : null}
                  {translating === item.id ? (
                    <div className="rounded-md border border-border p-3">
                      <p className="text-sm font-semibold mb-2">{t('tr.title')}</p>
                      <TranslationEditor
                        scope="qualification"
                        id={item.id}
                        languages={LANGUAGES.map((l) => l.code).filter((l) => l !== 'en')}
                      />
                    </div>
                  ) : null}
                  {open === item.id ? <Holders qualification={item} /> : null}
                </li>
              )
            )}
          </ul>
        )}
        {remove.isError ? <ErrorRow message={(remove.error as Error).message} /> : null}
      </CardContent>
    </Card>
  );
}
