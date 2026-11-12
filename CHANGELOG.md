# Changelog

All notable changes to Learn are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [1.0.0] - 2026-11-12

Needs Den: 1.0.0 or later (Den MAJOR 1).

First release, `1.0.0`. Released versions are tagged `vMAJOR.MINOR.PATCH`;
the images `ghcr.io/aglelectronics/learn-backend`, `learn-frontend` and
`learn-migrate` are published as `MAJOR.MINOR.PATCH-BUILD`, where `BUILD`
counts rebuilds of the same version. Rebuilds get no entry here.

### Added

- Courses made of pages, quizzes and in-person sessions; unpublished
  courses are hidden from everyone but their staff. Courses linked to a
  Keycloak group enrol its members automatically.
- Qualifications with printable certificates and a public verify code,
  learning paths, reminders, reports and team boards (counts per group,
  never names). Staff roles: `learn-admin`, `learn-trainer`,
  `learn-manager`, `learn-coordinator`.
- HR can require a qualification before it grants a role: it reads the
  qualification catalog and asks for holdings with a shared service token.
  Den's portal shows Learn to-dos.
- `DEN_MODE=required` keeps the module locked until it is paired with Den:
  the API answers `503 den_pairing_required` (except health, brand, sign-in
  and the Den connection routes) and the public `GET /api/den/state`
  (also at `/api/v1/den/state`) reports `{"mode","paired"}`. The default
  `standalone` runs without Den; any other value stops the backend at
  startup.
- The web app shows a "Connect this module to Den" page (admins) or
  "Waiting for Den" (everyone else) while the backend runs with
  `DEN_MODE=required` and is not paired yet.
- `TRUSTED_PROXY_CIDRS` (optional): comma-separated CIDRs of the proxies in
  front of the backend whose `X-Forwarded-For` entries are believed. The
  default, `10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,127.0.0.0/8,::1/128,fc00::/7`,
  covers the Gateway, nginx and Caddy; change it only for a proxy outside
  the private ranges, or set `none` when no proxy is in front. See
  [Client IP addresses](https://aglelectronics.github.io/con-services/operations/production/#client-ip-addresses).
- The backend can serve HTTPS. Set `TLS_CERT_FILE` and `TLS_KEY_FILE` to a
  PEM certificate and private key; the backend re-reads both every minute,
  so a certificate renewed by cert-manager is used without a restart. With
  neither set it serves plain HTTP, which is what Docker Compose
  development uses. See
  [Internal TLS](https://aglelectronics.github.io/con-services/operations/internal-tls/).
- The web app reads its installation settings (sign-in mode, issuer, client
  id, scopes, API address and product name) from `/config.json` when it
  starts, so the published `learn-frontend` image works for every
  installation. nginx serves the file from `/etc/conservices/config.json`
  with `Cache-Control: no-store`, so a change shows on the next page load.
  Without a mounted file the web app shows "Not configured yet"; a broken
  file gives a clear error page. See
  [Web app settings](https://aglelectronics.github.io/con-services/modules/learn/configuration/#web-app-settings-configjson).
- Kubernetes: Helm chart `deploy/charts/learn` `1.0.0` in `con-services`,
  released with the tag `learn/v1.0.0`. Internal TLS is on by default:
  cert-manager issues `learn-backend-tls` and `learn-frontend-tls`, the
  backend and the web app serve HTTPS inside the cluster (web app Service
  on port 443, backend on 8080, both named `https`), and every in-cluster
  address uses `https://` (Keycloak's admin API on port 8443). The cluster
  needs cert-manager, trust-manager and the platform's
  `conservices-internal-ca` issuer; without them set
  `internalTLS.enabled: false`. See
  [Set up Learn](https://aglelectronics.github.io/con-services/modules/learn/setup/).
- Development: `infra/docker-compose.yml` with SeaweedFS as the S3 server
  (`localhost:9030`, bucket `learn-data-dev`), and
  `infra/frontend-config/compose.json` (dev sign-in) mounted as the web
  app's `/config.json`; set `FRONTEND_CONFIG` in `.env` to use another
  file. Building from source needs Go 1.27.3 or newer.
- CI on every pull request (tests, lint, Trivy scans) and signed images with
  an SBOM published to GHCR on version tags (linux/amd64; linux/arm64
  optional).
- Training managers (`learn-manager` group, role `training_manager`,
  permission `staff.manage`) decide who teaches: they add and remove trainers
  on every course under **Course roles** (`GET /api/v1/course-staff`).
  Trainers manage learners only. The last trainer of a course can never be
  removed (`409`).
- The training office (`learn-coordinator`, permission
  `qualification.manage`) creates, edits and deletes qualifications, sees
  every certificate and revokes certificates, on the new **Qualifications**
  page. Module settings stay with `settings.manage`.
- Courses can be archived and unarchived (`POST /api/v1/courses/{id}/archive`,
  `/unarchive`): an archived course is hidden from learners and every list,
  and read-only for its staff (`409 archived`). Nothing is deleted; there is
  no course delete.
- Audit log of enrolments, course roles, publishing, archiving, quiz
  grading and resets, certificate revokes, qualification changes, group
  links, session sign-up status, Den pairing, settings and erasure. Admins
  read it on the **Audit log** page (`GET /api/v1/audit`, `settings.manage`).
- Group links (courses and learning paths) name a group by its full path
  (`/learn/cohort-a`) and match the token's groups claim by full path,
  ignoring case and the leading `/`. Links made by plain name are looked up
  once at start-up and get their path; a name that is missing or used by
  several groups is reported on the link. Configure Keycloak's group mapper
  with **Full group path** on.
- `LEARN_LINKABLE_GROUPS` (default `/learn/*`): the groups trainers may see
  and link; course managers may link any group.
- `GROUP_DIRECTORY` (`keycloak`, the default, or `off`): with `off` Learn
  never calls the Keycloak Admin API, and the group-link panels warn that
  members only appear as they sign in.
- The people picker also finds people in the Keycloak directory who never
  signed in (enrol them with `directory_id`), and Learn keeps their
  Keycloak id apart from the token's `sub`: a first sign-in with a different
  `sub` takes over the synced person by username instead of creating a
  second one.
- Telegram reminders are delivered through the Bot API. Admins switch
  Telegram on and off in **Settings** without removing
  `TELEGRAM_BOT_TOKEN`; while it is off, and for people without a chat id, a
  reminder does not count as delivered. `TELEGRAM_API_URL` (default
  `https://api.telegram.org`) points elsewhere for a proxy.
- Helm chart: `networkPolicy.telegram` (off by default) lets the backend
  reach the Telegram Bot API, as the `CiliumNetworkPolicy` `learn-telegram`
  (`fqdns`) or `ipBlock` rules (`cidrs`). `config` now sets
  `GROUP_DIRECTORY` (`keycloak`; anything but `keycloak` or `off` stops the
  chart), `LEARN_LINKABLE_GROUPS` (`/learn/*`) and `TELEGRAM_API_URL`, and
  `existingSecret` must hold `LEARN_FEEDBACK_PEPPER`.
- GDPR: `POST /api/v1/users/{id}/erase` (and **Settings → Erase a
  person**) anonymises a person; their progress, attempts and certificates
  stay, and certificates keep verifying with the name "Former learner". A
  retention job anonymises people inactive for `inactive_users_days` and
  deletes audit entries older than `audit_log_days`. Den's module settings
  (`retention.inactive_users_days`, `learn.audit_log_days`) decide the values
  while paired; standalone, admins set them in **Settings**. The defaults
  (1095 and 730 days) are pending legal review.
- `monitorSession` in the web app's `/config.json` (default `true`) turns
  OIDC session monitoring off for identity providers without
  `check_session_iframe`.
- Uploads still pending after 24 hours are deleted automatically, hourly.

### Changed

- `GET /api/den/state` and `GET /api/v1/den/state` are documented. Anyone may call them. The body is `mode`, `paired` and `revoked`.
- The people picker still searches Learn's own people and, when the directory is on, the identity provider. HR has no people search for other modules.
- CI passes `den-major: "1"`. A release whose MAJOR is not 1 is refused once the shared `ci-v1` tag points at the workflow that has that input.
- Standalone Compose uses PostgreSQL `postgres:16.15-alpine`.
- The Helm chart description says CI renders and lints the chart; the first production install is still pending.
- Quiz reset is `POST /api/v1/activities/{id}/reset` with
  `{"confirm": "<quiz title>"}` and needs `course.manage`. It deletes the
  attempts and completions of each learner's current cycle only; earlier
  cycles and every certificate already issued stay.
- **BREAKING:** `LEARN_FEEDBACK_PEPPER` is required outside
  `APP_ENV=development`. Generate it with `openssl rand -hex 32` and store it
  in `learn-secrets`; see
  [Secrets](https://aglelectronics.github.io/con-services/operations/secrets/).
- **BREAKING:** outside `APP_ENV=development` the backend refuses to start
  when `LEARN_SERVICE_TOKEN` is shorter than 32 characters, has fewer than
  10 different characters or looks like a placeholder. Generate it with
  `openssl rand -hex 32` and set the same value on HR.
- People search (`GET /api/v1/users`) only finds people of the caller's
  organizations and needs `enrol.manage` or `staff.manage`; trainers see
  names only, never email addresses (also in enrolment lists).
- **BREAKING:** the API checks **access tokens** for audience `learn-api`
  (`OIDC_AUDIENCE`, default `learn-api`) and refuses ID tokens. Add a
  Keycloak audience mapper on `learn-spa` that puts `learn-api` on access
  tokens only. The web app keeps tokens in memory and signs in again
  silently after a reload; it no longer sends the ID token.
- **BREAKING:** `AUTH_MODE` defaults to `oidc` and `APP_ENV` to
  `production`. `AUTH_MODE=dev` stops the backend unless
  `APP_ENV=development`. Local setups (`.env.example`, Compose) set
  `APP_ENV=development` explicitly. Outside development the `X-Dev-User`
  and `X-Dev-Groups` headers are ignored and are not allowed through CORS.
- **BREAKING:** group links and role bindings match with the shared rule
  (`OIDC_GROUP_MATCH=full` or `last`, optional `OIDC_GROUP_PREFIX`). Groups
  are read only from `OIDC_GROUPS_CLAIM` (default `groups`); the `roles`
  claim is no longer merged in. Turn **Full group path** on in Keycloak's
  groups mapper so nested groups such as `/learn/demo-cohort` match. A
  top-level group still matches its short name.
- **BREAKING:** the stored Den credential is encrypted with
  `DEN_LINK_ENCRYPTION_KEY` (32 random bytes, base64; required unless
  `APP_ENV=development`). Add it to `learn-secrets` (and
  `DEN_LINK_ENCRYPTION_KEY_PREVIOUS` only while rotating). A credential
  stored in plain text is encrypted on the first read. When Den rejects
  the credential the Den connection card shows the revoked state and a
  **Pair again** form; Learn does not pair again by itself.
- **BREAKING:** retention is stored in days
  (`inactive_users_days`, `audit_log_days`), not months. Existing rows are
  converted (a month is 365/12 days). While paired, Den's
  `retention.inactive_users_days` and `learn.audit_log_days` win. Set those
  keys in Den instead of the old month fields.
- **BREAKING:** `GET /api/role-catalog` needs `HR_SERVICE_TOKEN` (at least
  32 characters, the same value as on HR) or a staff sign-in. The response
  adds `revision` (also the `ETag`; a matching `If-None-Match` returns
  `304`), `group_display`, `privileged` and `dynamic`.
- Mail uses Den's SMTP settings when Den sets `smtp.host`, and Learn's
  `SMTP_*` settings only when Den does not. When Den sets `smtp.password_set`,
  Learn fetches the password from Den (`SMTPPassword`) and keeps it in
  memory; a failure to fetch it does not fall back to `SMTP_PASSWORD`.
  Save the password on Den's Module settings page. You can leave
  `SMTP_PASSWORD` unset once Den holds it.
- **BREAKING:** `GET /api/portal/todos` no longer accepts a person's access
  token or an extra audience (`OIDC_EXTRA_AUDIENCES`, for example
  `den-spa`). That setting is no longer read; remove it. Den's backend
  calls the route with Learn's pairing credential and the headers
  `X-Den-User-Issuer` and `X-Den-User-Subject`. Upgrade to a Den that
  fetches to-dos that way (Den 1.0.0). On Kubernetes the Learn chart
  admits `den-backend` from `conservices-den` on the backend port
  (`networkPolicy.den`); a hand-written NetworkPolicy must allow the same.
  The chart's `OIDC_AUDIENCE` is `learn-api` and no longer sets
  `OIDC_EXTRA_AUDIENCES`.
- The web app's nginx allows Learn itself to frame `/silent-callback.html`
  (`frame-ancestors 'self'`, `X-Frame-Options: SAMEORIGIN`) so silent
  sign-in keeps working. Every other page stays unframeable.
  `EXTRA_FRAME_SRC` is empty. The copy baked into the image for a
  read-only snippets directory strips every `${NAME}` placeholder,
  including `S3_ORIGIN`, so nginx still starts.

### Security

- `/silent-callback.html` may be framed by Learn's own origin only. Every
  other response still sends `frame-ancestors 'none'` and
  `X-Frame-Options: DENY`.
- Rate limits cannot be bypassed with a fake `X-Forwarded-For` header: the
  backend reads the client address from it only when the connection comes
  from a trusted proxy (`TRUSTED_PROXY_CIDRS`), and never reads
  `X-Real-IP` or `True-Client-IP`.
- On Kubernetes the database URL Den shows ends in
  `?sslmode=verify-full&sslrootcert=/etc/conservices-internal-ca/ca.crt`;
  the platform's PostgreSQL refuses connections without TLS.
- No image runs as root. The web app runs on
  `nginxinc/nginx-unprivileged:1.30.6-alpine` as UID 101 and listens on
  port 8080. All images are built with Go 1.27.3 and pass the CI image
  scan (Trivy, HIGH and CRITICAL issues that have a fix).
- A production build never falls back to dev sign-in: a `/config.json`
  without `authMode` means OIDC.
- Trainers can no longer make other people trainers or remove co-trainers.
- Uploaded media are checked by their contents (file signature), not only
  by the type the browser declares; a rejected upload is deleted from
  storage at once.
- Rate limits per person on self-enrolment, check-in, feedback and the
  people search (`429 rate_limited` with `Retry-After`).
- The anonymous-feedback dedupe hash is an HMAC keyed with a secret
  (`LEARN_FEEDBACK_PEPPER`) instead of a constant in the code.
- `.env.example` no longer contains a service token; the dev value stays in
  `infra/docker-compose.yml` only.
- Safe defaults and the shared error codes (`bad_request`,
  `request_too_large`, `unsupported_media_type`, `rate_limited`) from
  [Shared code](https://aglelectronics.github.io/con-services/concepts/shared-code/#error-codes).
  JSON request bodies are limited to 1 MiB (`413 request_too_large`).
  Every API response carries `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: no-referrer`, `X-Frame-Options: DENY`,
  `Content-Security-Policy: default-src 'none'; frame-ancestors 'none'`
  and exposes `X-Request-ID` to the browser.
- The web app's nginx sends the shared security headers (Content Security
  Policy, `X-Frame-Options`, `Referrer-Policy`, `Permissions-Policy`) only
  on the locations that serve the app. The allowed origins come from
  `KEYCLOAK_ORIGIN`, `DEN_ORIGIN`, `S3_ORIGIN` and `EXTRA_CONNECT_SRC`.
- Helm chart: `existingSecret` must hold `DEN_LINK_ENCRYPTION_KEY` (the
  backend pod does not start without it unless `APP_ENV` is `development`)
  and may hold `DEN_LINK_ENCRYPTION_KEY_PREVIOUS` and `HR_SERVICE_TOKEN`.
  `config.OIDC_AUDIENCE` is `learn-api`. The web app container gets the
  Content Security Policy origins from `frontend.securityHeaders`.
