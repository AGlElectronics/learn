# ADR 0001 — Course access is an enrolment, not an IdP role

**Status:** accepted · **Date:** 2026-11-05

## Context

Every Con Services module maps IdP groups to module roles through an access
config (Rally's pattern). Learn needs per-course access: who may read a
course, and who may edit it. Two options:

1. Model learners as an IdP group (`learn-learner`) and keep per-course
   lists in each course's settings.
2. Model per-course access as a database row (`enrolments`), and use IdP
   groups only for the two operator roles (admin, trainer).

## Decision

Option 2. IdP groups answer "may this person use the teaching feature at
all"; an `enrolments` row answers "is this person on this course" — role
`learner` grants reading a published course, role `trainer` makes them
course staff. Being signed in grants nothing; there is no learner group.

## Consequences

- Enrolling someone never touches Keycloak, so trainers don't need (and
  don't get) IdP admin rights. HR assigns only `learn-admin` and
  `learn-trainer` through the role catalog.
- A person must have signed in once before they can be enrolled (their
  users row is created on first login). The enrolment picker says so. A
  later HR integration could pre-provision users; out of scope here.
- Completion history survives unenrolment (completions are keyed by
  activity + user and kept), so re-enrolling restores progress — same
  behaviour Moodle admins expect.
- `course.teach` is deliberately inert without a staff enrolment: trainers
  cannot see or edit each other's draft courses. Admins (`course.manage`)
  can manage everything, matching how the other modules treat their admin
  groups.
