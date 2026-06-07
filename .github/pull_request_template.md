<!--
Thanks for opening a PR! A few load-bearing reminders:

- The PR title is enforced by .github/workflows/pr-title.yml as a
  Conventional Commit: `<type>: <short imperative>`. Allowed types are
  feat, fix, docs, refactor, perf, test, build, ci, chore, revert.
  Bad title → red check; rename the PR (not the branch).
- `task ci` mirrors the CI workflow locally. Run it before pushing so
  the CI signal doesn't surprise you.
- The race detector is mandatory: `go test -race ./...` must pass.
- Pre-1.0, but: a breaking change to the public API (Options,
  FieldError, MultiError, StructValidator, exported sentinels) needs
  an explicit note in the CHANGELOG under "### Changed" with the word
  "BREAKING" in the bullet.
-->

## What this changes

<!-- One or two sentences. Lead with the user-visible effect, not the
implementation detail. "Adds Foo so users can Bar" beats "Refactor the
Baz function". -->

## Why

<!-- The motivating bug, finding, or use case. Link the issue if any
(`Fixes #123`). For Principal Review fixes, link the report section. -->

## How

<!-- Implementation notes worth flagging: a tricky reflect path, an
allocation reduction strategy, why a test fixture had to live in a
particular file. Skip for one-liners. -->

## Checklist

- [ ] PR title is a valid Conventional Commit (`feat:`, `fix:`, `docs:`, …)
- [ ] `task ci` passes locally (lint, race tests, govulncheck)
- [ ] Tests added or updated to cover the change
- [ ] CHANGELOG entry added under `## [Unreleased]` for user-visible changes
- [ ] If breaking the public API: bullet starts with `**BREAKING:**` in CHANGELOG

## Scope

<!-- Is this in-scope for the library? koanf-validate is a translation
layer between validator/v10 and koanf paths. New validation rules
generally belong upstream at go-playground/validator or in user code
via Options.Validator. If you're unsure, say so. -->
