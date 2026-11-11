import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useParams } from '@tanstack/react-router';
import {
  Archive,
  ArchiveRestore,
  ArrowLeft,
  CalendarClock,
  ClipboardCheck,
  Eye,
  EyeOff,
  FilePlus2,
  GraduationCap,
  ListPlus,
  Plus,
  Save,
  Trash2,
  UserPlus,
  X,
} from 'lucide-react';
import { QuestionBank } from '@/components/QuestionBank';
import { QuizEditor } from '@/components/QuizEditor';
import { GroupLinksPanel } from '@/components/GroupLinksPanel';
import { FeedbackPanel } from '@/components/FeedbackPanel';
import { TeamBoardSettingsPanel } from '@/components/TeamBoard';
import { SessionEditor } from '@/components/SessionEditor';
import { MediaLibrary } from '@/components/media/MediaLibrary';
import { ActivityMediaEditor } from '@/components/media/ActivityMedia';
import { DueDaysControl, EnrolmentDueControl, PrerequisitesEditor } from '@/components/Rules';
import { QualificationPicker } from '@/components/QualificationPicker';
import { CourseTranslationsCard } from '@/components/Translations';
import { Button, LinkButton } from '@/components/ui/Button';
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import {
  createActivity,
  createSection,
  deleteActivity,
  deleteSection,
  enrolPerson,
  getCourse,
  listEnrolments,
  personRef,
  setCourseArchived,
  setCoursePublished,
  unenrol,
  updateActivity,
  updateCourse,
  type Activity,
  type Person,
} from '@/api/courses';
import { PeoplePicker } from '@/components/PeoplePicker';
import { PERMS, useMe } from '@/hooks/useMe';
import { getLocale, useT } from '@/i18n';

/**
 * The course editor: course settings (publish, self-enrolment), sections
 * with pages and quizzes, the question bank, and the people on the course.
 * Publishing fails while a quiz isn't ready. Server-side authorization:
 * admins everywhere, trainers only on courses they staff.
 */
export function TeachCoursePage() {
  const { courseId } = useParams({ from: '/app/teach/$courseId' });
  const qc = useQueryClient();
  const t = useT();
  const courseQ = useQuery({
    queryKey: ['course', courseId],
    queryFn: ({ signal }) => getCourse(courseId, signal),
    retry: false,
  });
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['course', courseId] });
    void qc.invalidateQueries({ queryKey: ['courses'] });
  };

  const publish = useMutation({
    mutationFn: (published: boolean) => setCoursePublished(courseId, published),
    onSuccess: refresh,
  });
  const selfEnrolToggle = useMutation({
    mutationFn: (self_enrol: boolean) => updateCourse(courseId, { self_enrol }),
    onSuccess: refresh,
  });
  const archive = useMutation({
    mutationFn: (archived: boolean) => setCourseArchived(courseId, archived),
    onSuccess: refresh,
  });

  const [newSection, setNewSection] = useState('');
  const addSection = useMutation({
    mutationFn: () => createSection(courseId, newSection),
    onSuccess: () => {
      setNewSection('');
      refresh();
    },
  });
  const removeSection = useMutation({
    mutationFn: (id: string) => deleteSection(id),
    onSuccess: refresh,
  });

  if (courseQ.isLoading) {
    return (
      <PageContainer>
        <LoadingRow message={t('course.loading')} />
      </PageContainer>
    );
  }
  if (courseQ.isError) {
    return (
      <PageContainer>
        <ErrorRow message={t('teach.courseLoadError')} />
        <div className="text-center">
          <LinkButton to="/teach" variant="secondary" size="sm">
            <ArrowLeft className="w-4 h-4" />
            {t('teach.back')}
          </LinkButton>
        </div>
      </PageContainer>
    );
  }
  const course = courseQ.data!;
  const archived = course.archived_at != null;
  const mutationError = (publish.error ??
    selfEnrolToggle.error ??
    archive.error ??
    addSection.error ??
    removeSection.error) as Error | null;

  return (
    <PageContainer>
      <PageHeader
        title={course.title}
        description={course.summary || undefined}
        actions={
          <>
            <LinkButton to={`/courses/${courseId}`} variant="ghost" size="sm">
              <Eye className="w-4 h-4" />
              {t('teach.viewAsLearner')}
            </LinkButton>
            <LinkButton to={`/teach/${courseId}/grades`} variant="ghost" size="sm">
              <GraduationCap className="w-4 h-4" />
              {t('teach.grades')}
            </LinkButton>
            {!archived ? (
              <>
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={selfEnrolToggle.isPending}
                  onClick={() => selfEnrolToggle.mutate(!course.self_enrol)}
                >
                  {course.self_enrol ? t('teach.selfEnrolOn') : t('teach.selfEnrolOff')}
                </Button>
                <Button
                  variant={course.published ? 'secondary' : 'primary'}
                  size="sm"
                  disabled={publish.isPending}
                  onClick={() => publish.mutate(!course.published)}
                >
                  {course.published ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
                  {course.published ? t('teach.unpublish') : t('teach.publish')}
                </Button>
              </>
            ) : null}
            <Button
              variant="ghost"
              size="sm"
              disabled={archive.isPending}
              onClick={() => {
                if (archived || confirm(t('teach.confirmArchive', { title: course.title }))) archive.mutate(!archived);
              }}
            >
              {archived ? <ArchiveRestore className="w-4 h-4" /> : <Archive className="w-4 h-4" />}
              {archived ? t('teach.unarchive') : t('teach.archive')}
            </Button>
          </>
        }
      />
      {archived ? (
        <p role="status" className="text-sm rounded-md border border-border bg-muted px-3 py-2 -mt-4 mb-6">
          {t('teach.archivedHint')}
        </p>
      ) : null}
      {!course.published && !archived ? (
        <p className="text-sm text-muted-foreground -mt-4 mb-6">
          {t('teach.draftHint')}
        </p>
      ) : null}
      {mutationError ? <ErrorRow message={mutationError.message} /> : null}
      <div className="mb-6 space-y-2">
        <DueDaysControl courseId={courseId} dueDays={course.due_days} onChanged={refresh} />
        <QualificationPicker
          courseId={courseId}
          organizationId={course.organization_id}
          current={course.awards_qualification}
          onChanged={refresh}
        />
      </div>

      <div className="grid gap-6 lg:grid-cols-[1fr_340px]">
        <div className="space-y-4">
          {course.sections.map((section) => (
            <SectionEditor
              key={section.id}
              courseId={courseId}
              sectionId={section.id}
              title={section.title}
              activities={section.activities}
              courseActivities={course.sections.flatMap((s) => s.activities)}
              onChanged={refresh}
              onDelete={() => {
                if (confirm(t('teach.confirmDeleteSection', { title: section.title }))) {
                  removeSection.mutate(section.id);
                }
              }}
            />
          ))}

          <form
            className="flex gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (newSection.trim()) addSection.mutate();
            }}
          >
            <Input
              value={newSection}
              onChange={(e) => setNewSection(e.target.value)}
              placeholder={t('teach.newSectionPlaceholder')}
            />
            <Button type="submit" variant="secondary" disabled={!newSection.trim() || addSection.isPending}>
              <ListPlus className="w-4 h-4" />
              {t('teach.addSection')}
            </Button>
          </form>

          <QuestionBank courseId={courseId} />
          <MediaLibrary courseId={courseId} />
          <CourseTranslationsCard
            courseId={courseId}
            languages={course.languages ?? [course.default_lang]}
            defaultLang={course.default_lang}
            onChanged={refresh}
          />
        </div>

        <div className="space-y-6">
          <EnrolmentsPanel courseId={courseId} archived={archived} />
          <GroupLinksPanel courseId={courseId} archived={archived} />
          <FeedbackPanel courseId={courseId} />
          <TeamBoardSettingsPanel target="course" id={courseId} />
        </div>
      </div>
    </PageContainer>
  );
}

function SectionEditor({
  courseId,
  sectionId,
  title,
  activities,
  courseActivities,
  onChanged,
  onDelete,
}: {
  courseId: string;
  sectionId: string;
  title: string;
  activities: Activity[];
  courseActivities: Activity[];
  onChanged: () => void;
  onDelete: () => void;
}) {
  const t = useT();
  const [adding, setAdding] = useState(false);
  const [pageTitle, setPageTitle] = useState('');
  const [pageBody, setPageBody] = useState('');
  const addPage = useMutation({
    mutationFn: () => createActivity(sectionId, { title: pageTitle, body: pageBody }),
    onSuccess: () => {
      setPageTitle('');
      setPageBody('');
      setAdding(false);
      onChanged();
    },
  });
  const [addingQuiz, setAddingQuiz] = useState(false);
  const [quizTitle, setQuizTitle] = useState('');
  const addQuiz = useMutation({
    mutationFn: () => createActivity(sectionId, { kind: 'quiz', title: quizTitle, body: '' }),
    onSuccess: () => {
      setQuizTitle('');
      setAddingQuiz(false);
      onChanged();
    },
  });
  const [addingSession, setAddingSession] = useState(false);
  const [sessionTitle, setSessionTitle] = useState('');
  const addSession = useMutation({
    mutationFn: () => createActivity(sectionId, { kind: 'session', title: sessionTitle, body: '' }),
    onSuccess: () => {
      setSessionTitle('');
      setAddingSession(false);
      onChanged();
    },
  });
  const removePage = useMutation({
    mutationFn: (id: string) => deleteActivity(id),
    onSuccess: onChanged,
  });

  return (
    <Card>
      <CardHeader className="flex-row items-center justify-between">
        <CardTitle>{title}</CardTitle>
        <div className="flex gap-1">
          <Button variant="ghost" size="sm" onClick={() => setAdding((v) => !v)}>
            {adding ? <X className="w-4 h-4" /> : <FilePlus2 className="w-4 h-4" />}
            {adding ? t('common.cancel') : t('teach.addPage')}
          </Button>
          <Button variant="ghost" size="sm" onClick={() => setAddingQuiz((v) => !v)}>
            {addingQuiz ? <X className="w-4 h-4" /> : <ClipboardCheck className="w-4 h-4" />}
            {addingQuiz ? t('common.cancel') : t('teach.addQuiz')}
          </Button>
          <Button variant="ghost" size="sm" onClick={() => setAddingSession((v) => !v)}>
            {addingSession ? <X className="w-4 h-4" /> : <CalendarClock className="w-4 h-4" />}
            {addingSession ? t('common.cancel') : t('teach.addSession')}
          </Button>
          <Button variant="ghost" size="sm" className="text-destructive" onClick={onDelete}>
            <Trash2 className="w-4 h-4" />
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        {activities.length === 0 && !adding && !addingQuiz && !addingSession ? (
          <p className="text-sm text-muted-foreground">{t('teach.noPages')}</p>
        ) : null}
        {activities.map((a) => (
          <div key={a.id} className="space-y-1">
            {a.kind === 'quiz' ? (
              <QuizEditor
                courseId={courseId}
                activity={a}
                onChanged={onChanged}
                onDelete={() => removePage.mutate(a.id)}
              />
            ) : a.kind === 'session' ? (
              <SessionEditor activity={a} onChanged={onChanged} onDelete={() => removePage.mutate(a.id)} />
            ) : (
              <PageEditor
                courseId={courseId}
                activity={a}
                onChanged={onChanged}
                onDelete={() => removePage.mutate(a.id)}
              />
            )}
            <div className="pl-3">
              <PrerequisitesEditor activity={a} courseActivities={courseActivities} onChanged={onChanged} />
            </div>
          </div>
        ))}
        {addingQuiz ? (
          <form
            className="flex gap-2 rounded-lg border border-border p-3"
            onSubmit={(e) => {
              e.preventDefault();
              if (quizTitle.trim()) addQuiz.mutate();
            }}
          >
            <Input
              value={quizTitle}
              onChange={(e) => setQuizTitle(e.target.value)}
              placeholder={t('teach.quizTitle')}
              autoFocus
            />
            <Button type="submit" size="sm" disabled={!quizTitle.trim() || addQuiz.isPending}>
              <Plus className="w-4 h-4" />
              {t('teach.addQuiz')}
            </Button>
            {addQuiz.isError ? <p className="text-sm text-destructive">{(addQuiz.error as Error).message}</p> : null}
          </form>
        ) : null}
        {addingSession ? (
          <form
            className="flex gap-2 rounded-lg border border-border p-3"
            onSubmit={(e) => {
              e.preventDefault();
              if (sessionTitle.trim()) addSession.mutate();
            }}
          >
            <Input
              value={sessionTitle}
              onChange={(e) => setSessionTitle(e.target.value)}
              placeholder={t('teach.sessionTitlePlaceholder')}
              autoFocus
            />
            <Button type="submit" size="sm" disabled={!sessionTitle.trim() || addSession.isPending}>
              <Plus className="w-4 h-4" />
              {t('teach.addSession')}
            </Button>
            {addSession.isError ? (
              <p className="text-sm text-destructive">{(addSession.error as Error).message}</p>
            ) : null}
          </form>
        ) : null}
        {adding ? (
          <form
            className="space-y-2 rounded-lg border border-border p-3"
            onSubmit={(e) => {
              e.preventDefault();
              if (pageTitle.trim()) addPage.mutate();
            }}
          >
            <div>
              <Label htmlFor={`new-page-title-${sectionId}`}>{t('teach.pageTitle')}</Label>
              <Input
                id={`new-page-title-${sectionId}`}
                value={pageTitle}
                onChange={(e) => setPageTitle(e.target.value)}
                placeholder={t('teach.pageTitlePlaceholder')}
                autoFocus
              />
            </div>
            <div>
              <Label htmlFor={`new-page-body-${sectionId}`}>{t('teach.content')}</Label>
              <textarea
                id={`new-page-body-${sectionId}`}
                value={pageBody}
                onChange={(e) => setPageBody(e.target.value)}
                rows={5}
                className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:border-primary"
                placeholder={t('teach.contentPlaceholder')}
              />
            </div>
            <Button type="submit" size="sm" disabled={!pageTitle.trim() || addPage.isPending}>
              <Plus className="w-4 h-4" />
              {t('teach.addPage')}
            </Button>
            {addPage.isError ? <p className="text-sm text-destructive">{(addPage.error as Error).message}</p> : null}
          </form>
        ) : null}
      </CardContent>
    </Card>
  );
}

function PageEditor({
  courseId,
  activity,
  onChanged,
  onDelete,
}: {
  courseId: string;
  activity: Activity;
  onChanged: () => void;
  onDelete: () => void;
}) {
  const t = useT();
  const [editing, setEditing] = useState(false);
  const [title, setTitle] = useState(activity.title);
  const [body, setBody] = useState(activity.body);
  const save = useMutation({
    mutationFn: () => updateActivity(activity.id, { title, body }),
    onSuccess: () => {
      setEditing(false);
      onChanged();
    },
  });

  if (!editing) {
    return (
      <div className="flex items-center justify-between gap-2 rounded-lg border border-border px-3 py-2">
        <span className="text-sm font-medium truncate">{activity.title}</span>
        <div className="flex gap-1 shrink-0">
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              setTitle(activity.title);
              setBody(activity.body);
              setEditing(true);
            }}
          >
            {t('common.edit')}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="text-destructive"
            onClick={() => {
              if (confirm(t('teach.confirmDeletePage', { title: activity.title }))) onDelete();
            }}
          >
            <Trash2 className="w-4 h-4" />
          </Button>
        </div>
      </div>
    );
  }
  return (
    <div className="space-y-2 rounded-lg border border-primary/40 p-3">
      <form
        className="space-y-2"
        onSubmit={(e) => {
          e.preventDefault();
          if (title.trim()) save.mutate();
        }}
      >
        <Input value={title} onChange={(e) => setTitle(e.target.value)} />
        <textarea
          value={body}
          onChange={(e) => setBody(e.target.value)}
          rows={6}
          className="w-full rounded-md border border-input bg-background px-3 py-2 text-sm focus-visible:outline-none focus-visible:border-primary"
        />
        <div className="flex gap-2">
          <Button type="submit" size="sm" disabled={!title.trim() || save.isPending}>
            <Save className="w-4 h-4" />
            {t('common.save')}
          </Button>
          <Button type="button" variant="ghost" size="sm" onClick={() => setEditing(false)}>
            {t('common.cancel')}
          </Button>
        </div>
        {save.isError ? <p className="text-sm text-destructive">{(save.error as Error).message}</p> : null}
      </form>
      <div className="border-t border-border pt-2">
        <ActivityMediaEditor courseId={courseId} activityId={activity.id} />
      </div>
    </div>
  );
}

/**
 * People on the course. Trainers add and remove learners; only training
 * managers (staff.manage) add or remove trainers, and the last trainer
 * always stays (the server answers 409).
 */
function EnrolmentsPanel({ courseId, archived }: { courseId: string; archived: boolean }) {
  const qc = useQueryClient();
  const t = useT();
  const { can } = useMe();
  const manageStaff = can(PERMS.staffManage);
  const enrolmentsQ = useQuery({
    queryKey: ['enrolments', courseId],
    queryFn: ({ signal }) => listEnrolments(courseId, signal),
    retry: false,
  });
  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ['enrolments', courseId] });
  };
  const add = useMutation({
    mutationFn: ({ person, role }: { person: Person; role: 'learner' | 'trainer' }) =>
      enrolPerson(courseId, { ...personRef(person), role }),
    onSuccess: refresh,
  });
  const remove = useMutation({
    mutationFn: (enrolmentId: string) => unenrol(enrolmentId),
    onSuccess: refresh,
  });

  const enrolments = enrolmentsQ.data?.enrolments ?? [];
  const enrolledIds = new Set(enrolments.map((e) => e.user_id));
  const activeTrainers = enrolments.filter((e) => e.role === 'trainer' && e.status === 'active').length;

  return (
    <Card className="h-fit">
      <CardHeader>
        <CardTitle>{t('people.title')}</CardTitle>
        <CardDescription>
          {t('people.description')}
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {enrolmentsQ.isLoading ? (
          <LoadingRow />
        ) : enrolmentsQ.isError ? (
          <ErrorRow message={t('people.loadError')} />
        ) : (
          <ul className="space-y-2">
            {enrolments.map((e) => {
              const canRemove =
                !archived && (e.role === 'learner' || (manageStaff && !(e.status === 'active' && activeTrainers <= 1)));
              return (
                <li key={e.id} className="flex items-center justify-between gap-2 text-sm">
                  <div className="min-w-0">
                    <p className="font-medium truncate">{e.display_name}</p>
                    <p className="text-xs text-muted-foreground">
                      {e.role === 'trainer' ? t('role.trainer') : t('role.learner')}
                      {e.email ? ` · ${e.email}` : ''}
                      {e.role === 'learner' && e.progress.total > 0
                        ? ` · ${t('people.done', { done: e.progress.completed, total: e.progress.total })}`
                        : ''}
                    </p>
                    {e.role === 'learner' && e.status === 'active' && !archived ? (
                      <EnrolmentDueControl enrolment={e} onChanged={refresh} />
                    ) : null}
                    {e.source === 'group' || e.status === 'inactive' ? (
                      <div className="flex flex-wrap gap-1 mt-0.5">
                        {e.source === 'group' ? (
                          <span className="rounded-full bg-primary-soft text-primary px-2 py-0.5 text-[11px] font-semibold">
                            {t('people.synced')}
                          </span>
                        ) : null}
                        {e.status === 'inactive' ? (
                          <span
                            className="rounded-full bg-muted text-muted-foreground px-2 py-0.5 text-[11px] font-semibold"
                            title={e.left_group_at ? t('people.leftAt', { date: new Date(e.left_group_at).toLocaleString(getLocale()) }) : undefined}
                          >
                            {t('people.inactive')}
                          </span>
                        ) : null}
                      </div>
                    ) : null}
                  </div>
                  {canRemove ? (
                    <Button
                      variant="ghost"
                      size="sm"
                      className="text-destructive shrink-0"
                      title={t('people.remove')}
                      disabled={remove.isPending}
                      onClick={() => {
                        if (confirm(t('people.confirmRemove', { name: e.display_name }))) remove.mutate(e.id);
                      }}
                    >
                      <X className="w-4 h-4" />
                    </Button>
                  ) : null}
                </li>
              );
            })}
          </ul>
        )}
        {!manageStaff ? <p className="text-xs text-muted-foreground">{t('people.trainersByManager')}</p> : null}
        {remove.isError ? <ErrorRow message={(remove.error as Error).message} /> : null}

        {!archived ? (
          <div className="border-t border-border pt-3">
            <PeoplePicker
              id="person-search"
              label={t('people.add')}
              exclude={enrolledIds}
              actions={(p) => (
                <>
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={add.isPending}
                    onClick={() => add.mutate({ person: p, role: 'learner' })}
                  >
                    <UserPlus className="w-4 h-4" />
                    {t('role.learner')}
                  </Button>
                  {manageStaff ? (
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={add.isPending}
                      onClick={() => add.mutate({ person: p, role: 'trainer' })}
                    >
                      {t('role.trainer')}
                    </Button>
                  ) : null}
                </>
              )}
            />
            {add.isError ? <p className="text-sm text-destructive mt-2">{(add.error as Error).message}</p> : null}
          </div>
        ) : null}
      </CardContent>
    </Card>
  );
}
