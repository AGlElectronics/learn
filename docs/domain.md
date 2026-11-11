# Learn — domain notes (proof-of-concept slice)

Learn is the ecosystem's training module (the architecture's "Learning"
slot: training, onboarding, courses, assessments and qualifications). This
slice proves one vertical loop of course delivery; everything else is
future work on the same schema.

## The loop this slice proves

1. A **trainer** (IdP group `learn-trainer`) creates a course. They become
   its first staff member (a *trainer enrolment*).
2. They add ordered **sections**, each holding ordered **page activities**
   (title + text body, stored in Postgres).
3. They **publish** the course. Optionally they allow **self-enrolment**,
   which lists it in the catalog.
4. A **learner** is enrolled by staff, or enrols themselves from the
   catalog. The enrolment — not a group — is what grants access to the
   published course.
5. The learner reads pages and **marks each done**; the course shows
   progress as completed pages over total pages.

## Entities

```text
organizations 1─n courses 1─n sections 1─n activities (kind page|quiz|session)
users (issuer+subject unique)
enrolments  (course, user, role learner|trainer; unique per course+user)
completions (activity, user; unique per activity+user)
den_link    (Den pairing, same as every module)
```

## Authorization

Two layers, both enforced server-side on every request:

- **Permissions** come from IdP groups through `infra/access-config.toml`
  (same mechanism as Rally). `organization_admin` holds every permission;
  `trainer` holds `course.teach`, `enrol.manage`; `training_manager` (group
  `learn-manager`) holds `staff.manage`; `coordinator` (group
  `learn-coordinator`) holds `report.view` and `qualification.manage`.
  Permissions are org-scoped; the owning organization is always resolved
  from the resource row.
- **Course staffing** is an enrolment with role `trainer`. `course.teach`
  only has effect on courses the holder staffs; `course.manage` works
  everywhere. `enrol.manage` adds and removes learners only; adding or
  removing a trainer needs `staff.manage`, and the last active trainer of a
  course is never removed (409). See ADR 0003.
- **People** belong to an organization (`user_organizations`) once they
  sign in to it or are enrolled on one of its courses. The picker
  (`GET /users`) only finds people of the caller's organizations; emails
  show only to `course.manage` and `staff.manage` holders.
- **Archived courses** (`archived_at`) are hidden from learners and every
  course list and read-only for staff (409 `archived` on anything but GET).
  Unarchiving restores them; courses are never deleted.

Visibility rules:

- Unpublished courses are invisible to non-staff — reads return the same
  404 a missing course would, never a 403 confirming existence.
- Published courses require an enrolment to read (403 otherwise).
- The catalog lists only published + self-enrol courses.
- Completions can only be written by the enrolled person, for themselves.

## Quizzes

An activity of kind `quiz` carries `pass_percent` (default 50),
`max_attempts` (NULL = unlimited) and `shuffle_options` (default on); its
body is the intro text.

```text
courses 1─n question_categories 1─n questions (kind single|multiple|hotspot) 1─n question_options (correct)
questions(kind='hotspot') 1─n question_regions (shape rect|circle, normalized coords, correct)
activities(kind='quiz') 1─n quiz_slots (one fixed question | draw N from a category)
activities(kind='quiz') 1─n quiz_attempts (user, submitted_at, score, max_score, passed)
quiz_attempts 1─n quiz_attempt_questions (frozen question + option order, chosen ids, correct)
```

- **Bank** (per course, staff only): single-choice questions need exactly
  one correct option, multiple-choice at least one, both at least two
  options. Marking a single-choice option correct unmarks the others.
- **Ready check**: a quiz needs at least one slot, no question used twice,
  enough questions left in each category for its draws, and valid
  questions. Publishing a course answers 422 while any quiz isn't ready;
  `GET /activities/{id}/slots` lists the problems.
- **Attempts**: starting returns the open attempt if there is one;
  otherwise it checks `max_attempts` (409 when used up), draws the
  questions (fixed slots first, random draws never repeat a question) and
  freezes the option order. Correct flags and feedback are only returned
  after submitting.
- **Marking** is all-or-nothing per question (the chosen set must equal the
  correct set). Passed = `score * 100 >= pass_percent * max_score`. The
  first pass writes the completion; manual completion answers 400 for
  quizzes. The grade is the best submitted attempt.
- **Edit lock**: once a question appears in a submitted attempt, its
  options and kind are locked (409); prompt, feedback and points stay
  editable. A course manager can reset a quiz
  (`POST /activities/{id}/reset {"confirm": "<title>"}`), which deletes the
  attempts and completions of each person's current cycle (and open
  attempts); earlier cycles and certificates stay, so questions those
  attempts used stay locked. Resets are audited.
- **Gradebook** (`GET /courses/{id}/gradebook`, staff): learner enrolments
  × quizzes with best %, passed and submitted attempt count.

## In-person sessions

An activity of kind `session` has dated slots; attending completes it.

```text
activities(kind='session') 1─n session_slots (starts_at, ends_at, location, capacity, checkin_secret)
session_slots 1─n session_signups (user, status booked|waitlist|attended|no_show, walk_in; one per activity+user)
```

- **Booking** (`POST/DELETE /slots/{id}/booking`) runs in a transaction
  with the slot row locked `FOR UPDATE`. Booked + attended count against
  capacity; when full the learner joins the waitlist, and the first in line
  is promoted when a seat frees up (cancel, capacity raised, no-show).
  Moving to another slot of the same session is allowed; cancelling after
  the start is not (409). The activity lock applies.
- **Check-in**: staff open `/teach/sessions/{slot}/checkin`, which shows a
  QR for `/checkin/{slot}?code=…`. The code is an HMAC of the slot's
  secret and the 30-second window; the current and previous window are
  accepted. Check-in is open from 30 minutes before the start until the
  end. Enrolled people without a booking get in as walk-ins while seats
  are left.
- **Attendance**: checking in (or staff marking `attended` via
  `PUT /signups/{id}`) writes the completion; setting it back to booked or
  no-show removes it. Manual completion answers 400 for sessions.

## Group sync

Courses can be linked to Keycloak groups (`course_group_links`) by full
path; members are enrolled as learners. Enrolments carry `source`
(manual|group) and `status` (active|inactive).

- Every stored group value (`group_name` in links, `user_groups`, team
  boards) and the token's groups claim are compared in one form: trimmed,
  one leading `/` dropped, lower-case, full path. `group_path` keeps the
  directory's spelling. Old name-only links (`path_resolved = false`) are
  resolved once at start-up. See ADR 0002.
- Trainers may only list and link groups matching `LEARN_LINKABLE_GROUPS`;
  `course.manage` links any group. `GROUP_DIRECTORY=off` never calls
  Keycloak.
- Synced people keep their Keycloak id in `users.idp_user_id`. A first
  sign-in whose `sub` differs takes over a never-signed-in synced row with
  the same `preferred_username`.

- Sync only touches `source='group'` rows. Leaving every linked group sets
  `status='inactive'` and `left_group_at`; nothing is deleted and progress
  stays. Rejoining reactivates. Manually enrolling an inactive person
  turns them into an active manual enrolment.
- Inactive enrolments grant nothing: no access, not staff, not in lists or
  the gradebook.
- A background worker syncs every 5 minutes under an advisory lock (off
  when no Keycloak admin client is configured). Logging in also refreshes
  the person's cached groups (`user_groups`) and applies them at once.
  If a group can't be read, that run enrols but never deactivates.
- Staff API: `GET /groups`, `GET/POST/DELETE /courses/{id}/group-links`,
  `POST /courses/{id}/group-links/sync`.

## Media

```text
courses 1─n media (kind image|audio|video, status pending|ready, alt_text, object_key)
questions → media_id, feedback_media_id; question_options → media_id
activities(kind='page') 1─n activity_media (media_id XOR external_url, caption, position)
```

- **Upload**: `POST /courses/{id}/media/uploads` checks type and size
  (images 10 MB, audio 20 MB, video 200 MB), records a `pending` row and
  returns a presigned POST; the browser uploads straight to object storage
  and calls `POST /media/{id}/complete`, which stats the object, checks its
  first bytes are really the announced type, and marks it `ready`. A file
  that fails a check is deleted (object and row, 422). Without storage
  configured these answer 503.
- **Cleanup**: uploads still `pending` after 24 hours are deleted by an
  hourly sweeper. `GET /courses/{id}/media?filter=unused` lists files no
  content uses; staff delete ready files by hand (`DELETE /media/{id}`,
  409 while in use). Course copies share stored objects (`object_key` is
  not unique); the object is removed only when no other row uses the key.
- **Viewing**: `GET /media/{id}/url` answers a 5-minute presigned link (the
  SPA fetches it with the bearer header and refreshes it; tokens never go
  in URLs); `GET /media/{id}` redirects to the same. Staff see any file of
  their course; learners need an enrolment and a file used by a question
  or by an attachment on a page that isn't locked for them. Everyone else
  gets 404.
- Pictures need alt text before they can be attached. An option's label
  may be blank when it has a picture.
- **Embeds**: external attachments accept YouTube (shown via
  youtube-nocookie.com) and Vimeo (player.vimeo.com, dnt) links only.

### Hotspot questions

A hotspot question has an image and regions in normalized 0..1
coordinates (rect `x,y,w,h`; circle `cx,cy,rx,ry`, an ellipse in
normalized space). `hotspot_clicks` caps the taps (default: one per
correct region). It is correct only if every correct region has exactly
one tap inside it and no tap falls outside all correct regions. Attempts
never include regions until submitted; taps are stored on
`quiz_attempt_questions.taps`. Regions and image lock with the options.

## Prerequisites and due dates

```text
activity_prerequisites (activity, requires; same course, no cycles)
courses.due_days → enrolments.due_at (set at learner enrolment by trigger)
```

- `PUT /activities/{id}/prerequisites {requires}` replaces the list; self,
  cross-course and cyclic rules answer 422.
- An activity is **locked** for a learner until every prerequisite is
  complete: the course detail marks it `locked` with `needs` (titles) and
  withholds a page's body; completing it, starting a quiz, booking a
  session or viewing its media answer 403 `locked`. Staff are never locked.
- A learner enrolment (staff, self-enrol or group sync) gets
  `due_at = now + due_days` when the course has `due_days`. Staff override
  with `PATCH /enrolments/{id} {due_at | null}`. Overdue = past `due_at`
  and not every activity complete; shown in My courses, the player, the
  enrolment list and the gradebook. Only active enrolments count: someone
  who left the synced group is never overdue.

## Qualifications, certificates and renewal

```text
qualifications (org, key, name, description, valid_months null = never expires)
courses.awards_qualification_id → qualifications
enrolments.cycle, completions.cycle, quiz_attempts.cycle
certificates (user, qualification, course, cycle, issued_at, expires_at,
              verify_code, revoked_at/reason/by; unique course+user+cycle)
```

- Qualification CRUD needs `qualification.manage` (deleting one with issued
  certificates answers 409). Course staff pick the awarded qualification
  with `PUT /courses/{id}/qualification {qualification_id | null}`;
  linking awards learners who already finished.
- Progress, completion, best quiz score, attempt limits and session
  attendance count only the learner's **current cycle**. Finishing the last
  activity (page, quiz pass or attendance) awards a certificate once per
  cycle, idempotently, to active learner enrolments only.
- `POST /courses/{id}/renew` (the learner) opens 60 days before expiry or
  any time after it (or after a revoke) and bumps the cycle; older
  completions and certificates are kept as history. Nothing is deleted
  automatically.
- `POST /certificates/{id}/revoke {reason}` needs `qualification.manage`.
  `GET /certificates/{id}` is for the holder or staff; the SPA prints it at
  `/certificates/{id}` with a QR code to `${PUBLIC_BASE_URL}/verify/<code>`.
- `GET /api/public/verify/{code}` (no auth, ~30 requests/min per IP)
  answers display name, qualification, dates and `valid|expired|revoked`.
- For Den: `GET /api/qualification-catalog` (public) lists the keys, and
  `GET /api/internal/holdings?subjects=a,b` with
  `Authorization: Bearer $LEARN_SERVICE_TOKEN` (404 when unset) returns
  each OIDC subject's valid qualifications.

## Reminders

An hourly worker (also run on demand with `POST /reminders/run`,
`settings.manage`) emails active learners: due in 7 days, due in 1 day,
overdue (once), session tomorrow, certificate expiring in 30 days and
certificate expired (once). `reminders_sent` makes each reminder go out
once (a failed delivery is retried next run); a Postgres advisory lock
keeps replicas from double-sending. `user_channels` holds extra notifier
addresses. Without SMTP or Telegram configured the worker does nothing.

Reminders go through `internal/notify`: every enabled channel (email when
Den sets `smtp.host`, or `SMTP_HOST` is set and Den does not, Telegram when
`TELEGRAM_BOT_TOKEN` is) gets each notice
with the recipient's `user_channels` addresses; a reminder counts as sent
when at least one channel really delivered it. People save their Telegram
chat id with `GET|PUT /me/channels {telegram_chat_id}` (the Notifications
card on My courses); Telegram sends with the Bot API's `sendMessage`. The
module setting `telegram_enabled` (`PUT /settings`) switches Telegram off
without removing the token; skipped channels never count as delivered.

## Audit log and privacy

- `audit_log` records who did what (enrolments, course roles, publish,
  archive, quiz grading and resets, revokes, qualification changes, group
  links, sign-up status, Den pairing, settings, erasure). Details hold ids,
  never names or emails. `GET /audit` needs `settings.manage`.
- `POST /users/{id}/erase {"confirm": "ERASE"}` anonymises a person: name
  "Former learner", no email, subject `erased:<id>`, IdP ids, language,
  group cache, channels and organization rows removed, feedback detached.
  Enrolments, attempts, attendance and certificates stay; certificates
  keep verifying. Their audit entries keep the action with the actor name
  "Erased user".
- A retention job (every six hours, advisory lock) anonymises people
  without a sign-in or group sync for `inactive_users_days` and deletes
  audit entries older than `audit_log_days` (0 = never). While paired,
  Den's module settings `retention.inactive_users_days` and
  `learn.audit_log_days` win; otherwise `module_settings`, otherwise 1095
  and 730 days (pending legal review).

## Team progress board

```text
courses.team_board_enabled, qualifications.team_board_enabled (default off)
team_board_groups (course_id | qualification_id, group_name)
```

- Staff pick Keycloak groups (teams) and switch the board on:
  `GET|PUT /courses/{id}/team-board/settings {enabled?, groups?}` (course
  staff) and `GET|PUT /qualifications/{id}/team-board/settings`
  (`settings.manage`).
- `GET /courses/{id}/team-board` (active enrolment or staff) and
  `GET /qualifications/{id}/team-board` (certificate holders, people
  enrolled on a course awarding it, staff): per group `members_count`
  (from the `user_groups` cache), `complete_count` and `percent`. Done means
  every activity complete in the person's current cycle (course) or a valid
  certificate (qualification). Disabled boards are 404 except to staff.
- `GET /me/teams`: for each enabled board the caller may see, only the
  selected groups they belong to ("Your team: 87% trained" on My courses).
- Boards return counts only, never names or per-person rows. Groups with
  fewer than 3 members come back with `members_count`, `complete_count`
  and `percent` null and `hidden_reason: "too_small"` (to everyone,
  staff included), so a percentage can't reveal one person's status.

## Portal to-dos (Den)

`GET /api/portal/todos` implements the suite-wide contract Den's portal
reads: `{"items": [{id, title, detail, due_at, url, severity}]}` for one
person, sorted by `due_at` (none last), URLs absolute on `PUBLIC_BASE_URL`.

- Courses due within 14 days or overdue with work left (active learner
  enrolment): `info`, `warning` within 3 days, `danger` when overdue.
- Booked sessions starting within 7 days: `info`.
- Latest certificate per qualification expiring within 30 days: `warning`
  (skipped while a renewal cycle is under way); expired and renewable on its
  active course: `danger`.
- Quizzes whose attempts this cycle all failed and that have attempts
  left: `info`, no due date.

Auth: every route that takes a person's token, including every `/api/v1`
route, checks an access token whose audience is `learn-api`
(`OIDC_AUDIENCE`) and refuses an ID token. `GET /api/portal/todos` does
not take that token. Den's backend calls it with Learn's pairing
credential (`Authorization: Bearer`) and the person in `X-Den-User-Issuer`
and `X-Den-User-Subject`. A missing or wrong credential is `401`; missing
headers are `400`. Learn looks the person up by issuer and subject and
does not create a user. Someone who has never signed in gets
`{"items":[]}`.

## Question statistics

`GET /courses/{id}/question-stats` (course staff) returns, per bank
question, the submitted attempts (all cycles; open attempts don't count),
how many were right and `percent_correct` (null when unanswered), picks per
option, and for hotspots the hit rate per region plus taps that landed
outside every correct region (`miss_taps`). The question bank shows this
under each question with a "hardest first" sort.

## Course and session feedback

```text
courses.feedback_enabled (default off), courses.feedback_anonymous
feedback (course, activity null = the course, user null when anonymous,
          dedupe_hash unique, rating 1-5, comment)
```

- Staff switch it with `PUT /courses/{id}/feedback-settings
  {enabled?, anonymous?}` and read `GET /courses/{id}/feedback`: average,
  count and comments for the course and each session (names only on
  non-anonymous responses).
- Learners see `GET /courses/{id}/feedback/mine` and post
  `POST /courses/{id}/feedback {activity_id?, rating, comment}`: the course
  once every activity is complete, a session once attended. 403 while off
  or not eligible yet, 409 on a second rating of the same target.
- `dedupe_hash = sha256(course|activity|user|server constant)` enforces one
  response per person per target without storing who gave an anonymous one.
  Switching anonymity applies to new responses only.

## Question import

- `POST /courses/{id}/question-import?step=preview|commit` (staff,
  multipart: `file`, optional `format` = `moodle_xml|gift|aiken|csv`,
  detected when empty; `category_id` for commit). Preview parses and saves
  nothing; the client sends the same file again to commit.
- Commit adds everything in one transaction with the bank's validation;
  a question that fails it is skipped with a warning instead of aborting.
  The first base64 image of a Moodle question is stored in object storage
  as a ready media row (alt text = file name) and becomes the question's
  picture; without storage, images are skipped with a warning.
- `GET /question-import/csv-template` downloads the CSV template
  (`prompt, kind, option1..n, correct, feedback, points`).

## Reports

- `GET /reports/completion?course=&group=&status=done|in_progress|not_started|overdue`
  and `GET /reports/qualifications?qualification=&group=&status=valid|expiring|expired|missing`;
  `&format=csv` downloads CSV (formula-prefixed cells neutralised).
  `GET /reports/scope` tells the SPA what the caller may see.
- Only active learner enrolments (current cycle) count. `report.view` sees
  every course and the qualification report; `course.manage` sees
  completion for its org; trainers see completion of courses they staff.
- Expiring = expires within 30 days. Missing = member of a group linked to
  a course or path awarding the qualification, with no valid certificate.

## Course copy

- `POST /courses/{id}/copy {title, include_group_links}` (whoever may edit
  the source). One transaction: sections, activities, prerequisites,
  questions, categories, quiz slots, media rows (same stored objects) and
  page attachments, with every id remapped. Session activities come without
  slots. Course settings come along too: feedback and team board settings
  (with the board's groups), due days, languages, and every translation
  re-pointed at the new rows (except the course title, which the copier
  just chose). No learner data. The copy starts unpublished and the copier
  is its trainer.

## Languages and translations

```text
courses.default_lang (the language its own columns are written in), courses.languages (offered; always includes the default)
users.language (UI/content preference, null = none)
translations (entity_type, entity_id, field, lang, text; PK of the first four)
```

- Translatable: course title/summary, section title, activity title/body,
  question prompt/feedback, option label, media alt text, attachment
  caption, path name/description, qualification name/description.
  Deleting a row deletes its translations (triggers).
- **Language per request**: `?lang=`, then the saved preference
  (`PUT /me/preferences {language}`), then `Accept-Language`; the first one
  the course offers wins, else its default. Paths and qualifications have no
  language list, so they use the first wish only (else the base text).
  Untranslated fields fall back to the default text field by field.
- Applied to learner payloads: course detail and lists, quiz overview,
  attempts and results, sessions and check-in, attachments, certificates,
  public verify, my qualifications/paths/teams and portal to-dos (whose
  own wording is English or Dutch). Staff get the default text unless they
  pass `?lang=`, so editors never save a translation over the original.
  Marking works on ids and is unaffected.
- **Editor**: `PATCH /courses/{id} {languages?, default_lang?}`;
  `GET /courses/{id}/translations?lang=xx` lists every non-empty field with
  default text, translation and a `missing` flag; `PUT` with
  `{translations: [{entity_type, entity_id, field, text}]}` upserts in bulk
  (empty text deletes; fields outside the course answer 422). The same pair
  exists for `/qualifications/{id}` (`qualification.manage`) and `/paths/{id}`
  (`course.manage`).
- The SPA ships English and Dutch UI text (`src/i18n`); the switcher saves
  the choice to the preference and to localStorage (used on signed-out
  pages such as `/verify`), and every API call sends it as
  `Accept-Language`.

## Learning paths

```text
paths 1─n path_courses (position) ─ courses
paths 1─n path_enrolments (source manual|group, status active|inactive, cycle)
paths 1─n path_group_links; certificates.path_id (path awards)
```

- Managed with `course.manage` (`/paths…`); learners use `GET /me/paths`
  and `POST /paths/{id}/enrol` on self-enrol paths.
- A path enrolment fans out to a learner enrolment on every path course
  with the same source (manual wins). Group sync handles path links like
  course links: leaving makes the path enrolment inactive, and course
  enrolments turn inactive only when no course link or active path covers
  them. Nothing is deleted.
- Sequential paths lock a course until the earlier ones are complete:
  403 `locked` with `needs` for non-staff; lists show `locked`.
- Finishing every course awards the path's qualification once per path
  enrolment cycle.

## Deliberately out of this slice

Forums, cohorts, badges, SCORM, HR person sync, and a shared `denlink`
library.
`activities.kind` is the extension point for further activity types.
