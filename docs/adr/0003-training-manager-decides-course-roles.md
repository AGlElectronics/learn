# ADR 0003 — A training manager decides who teaches

**Status:** accepted · **Date:** 2026-11-11

## Context

Being a trainer on a course is an enrolment with role `trainer` (ADR 0001).
Until now anyone who could manage enrolments on a course — every trainer of
it — could make anyone else a trainer, and remove the other trainers,
including the last one. That is a privilege escalation (a trainer can hand
out edit rights) and a way to orphan a course.

## Decision

- A new role `training_manager` (IdP group `learn-manager`) holds the new
  permission `staff.manage`: add and remove trainers on every course of the
  organization, from the **Course roles** page. It does not include editing
  course content.
- `enrol.manage` (trainers) covers learners only. Granting or removing the
  trainer role needs `staff.manage`; organization admins hold it too.
- The last active trainer of a course is never removed (`409`), whoever
  asks. The check locks the course's trainer rows so two removals at once
  can't both pass.
- People pickers only find people of the caller's organization, and only
  course or training managers see email addresses.

Qualifications get the same treatment: `qualification.manage` (the
coordinator, i.e. the training office) replaces `settings.manage` for
qualification changes and certificate revokes.

## Consequences

- Organisations need someone in `learn-manager` (or an admin) to staff new
  trainers; the course creator stays its first trainer as before.
- HR's access picker offers `learn-manager` through the role catalog.
- A person belongs to an organization when they signed in to it (the access
  config's home organization or a binding) or are enrolled on one of its
  courses; the table `user_organizations` records this.
