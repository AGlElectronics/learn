# ADR 0002 — Group links name a group by its full path

**Status:** accepted · **Date:** 2026-11-11

## Context

Courses and learning paths can be linked to identity-provider groups so
their members are enrolled automatically. Until now a link stored the plain
group name (`cohort-a`) and compared it exactly with the token's groups
claim and with the Keycloak directory. That breaks in three ways:

- With nested groups two groups can share a name (`/learn/cohort-a` and
  `/events/cohort-a`); the directory lookup took the first match.
- When Keycloak's group mapper sends full paths (`/learn/cohort-a`), which
  is what the suite's identity setup does, the link `cohort-a` never
  matches.
- Case and a leading `/` made identical groups look different.

The options were: store the full path; store the name and normalise; store
the Keycloak group id and show the path.

## Decision

Store the full path. Every group value Learn keeps (course and path links,
the `user_groups` cache, team boards) is stored in one comparison form —
trimmed, one leading `/` dropped, lower-case — and the token's groups claim
goes through the same function. Links keep the path as the directory spells
it (`group_path`) for display. Matching is by full path only: a plain-name
claim `cohort-a` does not match the link `/learn/cohort-a`.

Links made by name before this change are looked up once at start-up: a
name that matches exactly one directory group gets its path; a missing or
ambiguous name stays as it was and the link shows why.

Trainers may only see and link groups under `LEARN_LINKABLE_GROUPS`
(default `/learn/*`); course managers may link any group.

The normalising function is local for now
(`// TODO(shared/groupmatch)`); it moves to the suite's shared `groupmatch`
package once that exists, with the same rules.

## Consequences

- Keycloak's group mapper must send full paths ("Full group path" on).
  Top-level groups match either way, because `/staff` and `staff` normalise
  to the same value.
- A Keycloak group id would survive renames, a path does not: renaming or
  moving a linked group in Keycloak breaks the link until it is linked
  again. We accept that; paths are what tokens carry, and admins can read
  them.
- Group values are lower-cased in Learn's database, so team boards show
  `learn/cohort-a` rather than the directory's spelling.
