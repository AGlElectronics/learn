# Learn

> [!WARNING]
> ## **NOT READY FOR PRODUCTION**
>
> Con Services is under active development. Expect breaking changes, missing
> features and bugs. Do not use it for real events or real data yet.

Training module for the Con Services stack — the suite's own course package,
not a Moodle embed: courses of pages, quizzes and in-person sessions,
qualifications with printable certificates, learning paths, reminders and
reports. The domain rules live in [docs/domain.md](docs/domain.md).

- **Backend:** Go 1.27, chi, pgx (hand-written queries; sqlc comes with a
  later pass, same as HR).
- **Frontend:** React 19 + Rsbuild + TanStack Router/Query + Tailwind,
  English and Dutch UI (`src/i18n`).
- **Database:** PostgreSQL 16, migrated via Goose (`cmd/migrate` one-shot).
- **Object storage:** course media (pictures, audio, video) go to the
  standard `S3_*` bucket (`learn-data-dev` in the suite); without it,
  uploads answer 503 and everything else works.

## Features

- **Courses**: sections of pages, quizzes and sessions; publish/unpublish,
  self-enrolment catalog, staff (trainer) and learner enrolments, progress
  per learner, course copy (content, settings and translations; no learner
  data).
- **Quizzes**: per-course question bank (single, multiple choice and
  hotspot-on-a-picture), categories with random draws, attempts frozen
  server-side, pass mark and attempt limits, gradebook, question statistics,
  import from Moodle XML, GIFT, Aiken and CSV.
- **In-person sessions**: dated slots with capacity and waitlist, rotating
  QR check-in at the venue, walk-ins, roster with manual attendance.
- **Rules**: prerequisites between activities, due dates (days after
  enrolment, per-learner override), overdue tracking.
- **Group sync**: courses and paths linked to Keycloak groups enrol (and
  deactivate, never delete) members automatically.
- **Media**: uploads straight to object storage, alt text required for
  pictures, YouTube/Vimeo embeds, unused-file cleanup by hand.
- **Qualifications**: awarded by finishing a course or a path, expiry and
  renewal cycles, printable certificates with a public QR verify page,
  holdings for Den.
- **Learning paths**: ordered (optionally sequential) course bundles with
  their own enrolments and qualification.
- **Engagement**: email reminders (Telegram stub), team progress boards
  (counts only; teams under 3 people hidden), course and session feedback
  (optionally anonymous), to-dos for Den's portal.
- **Reports**: completion and qualification status with CSV export.
- **Languages**: courses offered in several languages with a translation
  editor; learners get their language (`?lang=`, saved preference, then
  `Accept-Language`) with per-field fallback to the original.

## API overview

All under `/api/v1` with a bearer token (or dev headers) unless noted.
Authorization is enforced server-side on every route; see
[docs/domain.md](docs/domain.md) for who may call what.

| Area | Routes |
| ---- | ------ |
| Public | `GET /livez`, `/readyz`, `/api/role-catalog`, `/api/qualification-catalog`, `/api/public/verify/{code}`, `/api/brand`; `GET /api/internal/holdings` (service token); `GET /api/portal/todos` (Den portal) |
| Me | `GET /me`, `GET/PUT /me/preferences` (`theme`, `language`), `GET/PUT /me/channels`, `GET /me/qualifications`, `GET /me/paths`, `GET /me/teams` |
| Courses | `GET /courses/mine`, `/courses/catalog`, `/courses/teach`; `POST /courses`; `GET/PATCH /courses/{id}` (incl. `languages`, `default_lang`); `POST /courses/{id}/publish`, `/unpublish`, `/copy`, `/enrol`, `/renew`; `GET/POST /courses/{id}/enrolments`; `PATCH/DELETE /enrolments/{id}` |
| Content | `POST /courses/{id}/sections`; `PATCH/DELETE /sections/{id}`; `POST /sections/{id}/activities`; `PATCH/DELETE /activities/{id}`; `PUT /activities/{id}/completion`, `/prerequisites` |
| Translations | `GET/PUT /courses/{id}/translations?lang=`, `/qualifications/{id}/translations?lang=`, `/paths/{id}/translations?lang=` |
| Quizzes | `GET/POST /courses/{id}/question-categories`; `PATCH/DELETE /question-categories/{id}`; `GET/POST /question-categories/{id}/questions`; `PATCH/DELETE /questions/{id}`; `PUT /questions/{id}/regions`; `POST /questions/{id}/options`; `PATCH/DELETE /options/{id}`; `GET/POST /activities/{id}/slots`; `PATCH/DELETE /slots/{id}`; `GET /activities/{id}/quiz`; `POST/DELETE /activities/{id}/attempts`; `GET /attempts/{id}`; `POST /attempts/{id}/submit`; `GET /courses/{id}/gradebook`, `/question-stats`; `POST /courses/{id}/question-import?step=preview/commit`; `GET /question-import/csv-template` |
| Sessions | `GET /activities/{id}/session`; `POST /activities/{id}/session-slots`; `PATCH/DELETE /session-slots/{id}`; `POST/DELETE /slots/{id}/booking`; `GET /slots/{id}/checkin-code`, `/roster`; `POST /slots/{id}/checkin`; `PUT /signups/{id}` |
| Media | `GET /courses/{id}/media`; `POST /courses/{id}/media/uploads`; `POST /media/{id}/complete`; `GET/PATCH/DELETE /media/{id}`; `GET /media/{id}/url`; `GET/POST /activities/{id}/media`; `PATCH/DELETE /activity-media/{id}` |
| Groups | `GET /groups`; `GET/POST/DELETE /courses/{id}/group-links`, `POST …/group-links/sync` (same under `/paths/{id}`) |
| Qualifications | `GET/POST /qualifications`; `PATCH/DELETE /qualifications/{id}`; `GET /qualifications/{id}/certificates`; `PUT /courses/{id}/qualification`; `GET /certificates/{id}`; `POST /certificates/{id}/revoke` |
| Paths | `GET/POST /paths`; `GET/PATCH/DELETE /paths/{id}`; `PUT/POST /paths/{id}/courses`; `DELETE /paths/{id}/courses/{course}`; `GET/POST /paths/{id}/enrolments`; `POST /paths/{id}/enrol`; `DELETE /path-enrolments/{id}` |
| Engagement | `GET /courses/{id}/team-board`, `GET/PUT …/team-board/settings` (same under `/qualifications/{id}`); `GET /courses/{id}/feedback`, `/feedback/mine`; `POST /courses/{id}/feedback`; `PUT /courses/{id}/feedback-settings`; `POST /reminders/run` |
| Reports | `GET /reports/scope`, `/reports/completion`, `/reports/qualifications` (`&format=csv`) |
| Admin | `GET /users`; Den link under `/den` (`GET /`, `POST /pair`, `POST /sync`, `DELETE /pair`) |

## Reserved ports

| Service         | Host port | Container port |
| --------------- | --------- | -------------- |
| backend (API)   | **8120**  | 8080           |
| frontend (SPA)  | **3005**  | 80             |
| postgres        | **5437**  | 5432           |

Every module in this ecosystem binds `:8080` *inside* its container; the
host-side port is reserved per-module (Learn = 8120). Ports avoid colliding
with stash (8088/3000), den (8090/3001), events (8100/3002) and hr
(8110/3003) on the same dev machine.

## Access model

Roles come from IdP groups via `infra/access-config.toml`, checked on every
request:

| Keycloak group  | Role                 | May do                                            |
| --------------- | -------------------- | ------------------------------------------------- |
| `learn-admin`   | `organization_admin` | Everything: every course, enrolments, settings.   |
| `learn-trainer` | `trainer`            | Create courses; edit/enrol on courses they staff. |
| `learn-coordinator` | `coordinator`    | Read every report.                                |

**There is no learner group.** Reading a published course requires an
*enrolment* (created by course staff, or by the person themselves when the
course allows self-enrolment). Unpublished courses are invisible to
everyone but their staff. Users are provisioned on first login, keyed by
the OIDC `(issuer, subject)` pair; Learn never reads the HR database.

The public role catalog is at `GET /api/role-catalog` so HR's access picker
can assign these groups.

## First-time boot (standalone)

Requirements: Docker Desktop (or equivalent) with Compose v2, and no local
processes on the host ports above.

```sh
cd learn
docker compose -f infra/docker-compose.yml up --build
```

What happens:

1. `postgres` boots and reports healthy.
2. `migrate` (one-shot) applies all pending Goose migrations and exits 0.
3. `backend` starts on container `:8080`, published on host `:8120`. It
   does **not** auto-migrate.
4. `frontend` serves the SPA from nginx on `:3005` and proxies `/api/...`
   + `/livez` + `/readyz` to the backend.

Standalone defaults to `AUTH_MODE=dev`: the SPA sends `X-Dev-User` (pick
any name on the login screen) and the backend grants the groups from
`X-Dev-Groups` (default `DEV_DEFAULT_GROUPS=learn-admin`). In the suite
compose it runs with real OIDC against the shared Keycloak instead.

Health probes:

```sh
curl -s http://localhost:8120/livez     # backend direct
curl -s http://localhost:3005/livez     # through the frontend proxy
curl -s http://localhost:8120/api/role-catalog
```

## In the suite

From the repository root (see the root README):

```powershell
docker compose --profile den --profile events --profile learn up --build -d
```

Learn then runs with OIDC against the shared Keycloak (`learn-spa` client,
groups `learn-admin` / `learn-trainer`), its own `learn` database on the
shared Postgres, and https://learn.conservices.test behind the proxy. Pair
it with Den under **Settings → Den connection** (mint the URL in Den admin
→ Modules), same as HR, Stash and Rally.

## Standalone image build

The Dockerfiles are context-agnostic — a plain

```sh
cd learn
docker build -f infra/Dockerfile.backend  -t learn-backend  .
docker build -f infra/Dockerfile.frontend -t learn-frontend .
docker build -f infra/Dockerfile.migrate  -t learn-migrate  .
```

works from the `learn/` directory. All three expect the build context to be
`learn/` (not `learn/backend` or `learn/frontend`).

## Backups

In the full Con Services stack, Den's operator backs up Learn's database
every night along with everything else (see
[docs/operations.md](../docs/operations.md)). The state is the database
plus the media bucket (`S3_BUCKET`).

**Run standalone, or on a database server Den's operator doesn't manage,
Learn is not backed up by anything. That is the administrator's job:**
schedule a `pg_dump --format=custom` of the Learn database and a copy of the
media bucket, encrypt them, and keep them somewhere other than the server.

## Working locally without Docker

```sh
# Backend
cd learn/backend
go mod tidy
DATABASE_URL='postgres://learn:learn@localhost:5437/learn?sslmode=disable' \
  go run ./cmd/migrate      # applies pending migrations
DATABASE_URL='postgres://learn:learn@localhost:5437/learn?sslmode=disable' \
HTTP_ADDR=:8120 AUTH_MODE=dev \
  go run ./cmd/server

# Frontend (proxies /api to localhost:8120)
cd learn/frontend
npm install
npm run dev
```

## Tests

Backend tests run against a real PostgreSQL (`-p 1`: the packages share the
test database):

```sh
cd learn/backend
TEST_DATABASE_URL='postgres://learn:learn@localhost:5437/learn_test?sslmode=disable' go test -p 1 ./...
```

Tests are skipped when `TEST_DATABASE_URL` is unset. Each run migrates the
test database from scratch (drop + recreate the schema), so point it at a
**disposable** database, never a real one.

Frontend: `npm run typecheck`, `npm test` (vitest; checks the English and
Dutch message files match) and `npm run build`.

From the suite root, `scripts/smoke-learn.ps1` runs the whole loop against
the live stack: create course → add content → publish → enrol → complete →
check progress, including the negative authorization checks.
