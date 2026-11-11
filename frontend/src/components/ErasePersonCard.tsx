import { useState } from 'react';
import { useMutation } from '@tanstack/react-query';
import { erasePerson } from '@/api/admin';
import type { Person } from '@/api/courses';
import { PeoplePicker } from '@/components/PeoplePicker';
import { Button } from '@/components/ui/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { ErrorRow } from '@/components/ui/States';
import { useT } from '@/i18n';

/**
 * GDPR erasure: anonymises one person. Their progress, attempts and
 * certificates stay (certificates keep verifying, with the name replaced).
 */
export function ErasePersonCard() {
  const t = useT();
  const [picked, setPicked] = useState<Person | null>(null);
  const [typed, setTyped] = useState('');
  const [done, setDone] = useState<string | null>(null);
  const erase = useMutation({
    mutationFn: () => erasePerson(picked?.id ?? '', typed),
    onSuccess: () => {
      setDone(t('erase.done', { name: picked?.display_name ?? '' }));
      setPicked(null);
      setTyped('');
    },
  });
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t('erase.title')}</CardTitle>
        <CardDescription>{t('erase.description')}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {picked ? (
          <form
            className="space-y-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (typed === 'ERASE') erase.mutate();
            }}
          >
            <p className="text-sm">{t('erase.confirmPerson', { name: picked.display_name })}</p>
            <Label htmlFor="erase-confirm">{t('erase.type')}</Label>
            <Input id="erase-confirm" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" />
            <div className="flex gap-2">
              <Button type="submit" variant="destructive" size="sm" disabled={typed !== 'ERASE' || erase.isPending}>
                {t('erase.button')}
              </Button>
              <Button type="button" variant="ghost" size="sm" onClick={() => setPicked(null)}>
                {t('common.cancel')}
              </Button>
            </div>
          </form>
        ) : (
          <PeoplePicker
            id="erase-search"
            label={t('erase.find')}
            actions={(p) =>
              p.id ? (
                <Button variant="ghost" size="sm" className="text-destructive" onClick={() => setPicked(p)}>
                  {t('erase.pick')}
                </Button>
              ) : null
            }
          />
        )}
        {done ? <p className="text-sm text-muted-foreground">{done}</p> : null}
        {erase.isError ? <ErrorRow message={(erase.error as Error).message} /> : null}
      </CardContent>
    </Card>
  );
}
