# Changelog

All notable changes to `koanf-validate` are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.9.2] — 2026-06-07

**Third release candidate for 1.0.** Supersedes v0.9.1. Bundles a
principal-review pass: one new validation behavior, several godoc
corrections lifting truthful contracts onto the soon-to-freeze surface,
plus a foot-gun removal on `MultiError`.

### Added

- **Options validation.** `Struct()` now rejects malformed `Options` at
  the start of the call with wrapped `ErrInvalidConfig` when `PathTag`
  equals `ValidateTag`, or when either tag name contains whitespace,
  a comma, or a double quote. Surfaces what was previously a cryptic
  failure inside validator/v10 or reflect.StructTag as a clear,
  early diagnostic.

### Changed

- **`MultiError.Unwrap` no longer caches.** The previous `sync.Once`
  cache made `Errors` an irrevocable snapshot after the first
  `errors.Is`/`errors.As` walk — mutating the slice afterwards left
  the cache stale (silent foot-gun). `Unwrap` now allocates fresh per
  call. One allocation per traversal, but the foot-gun is gone.
- **`ErrInvalidConfig` godoc rewritten** to describe the two distinct
  cases it actually fires for: malformed `Options` (per the Added
  entry above) and malformed cfg struct shape (sibling collision,
  depth excess).
- **`Options.Delim` godoc** no longer mentions absolute-path detection
  that was removed earlier — paths from `Validate()` methods are
  unconditionally prefixed with `receiver+Delim`.
- **`Struct()` godoc** lists every possible return shape — the
  previous "nil or `*MultiError`" wording missed `ErrInvalidInput`,
  `ErrInvalidConfig`, `ErrCyclicType`, and the propagated
  `*validator.InvalidValidationError`.
- **`StructValidator` godoc** explicitly warns that `Param` on a
  returned `*FieldError` must be the unqualified sibling-field name —
  pre-qualifying it (e.g. `"server.min_port"` from a receiver mounted
  at `"server"`) double-prefixes silently.
- **`crossFieldTags` is now derived from `tagToSentinel`** at init
  rather than hand-maintained as a parallel set — single source of
  truth, drift-proof.
- **CONTRIBUTING.md** corrected to state Go 1.23+ (tracking koanf v2)
  instead of the stale Go 1.25+ claim.

### Internal

- `validateCalled` test-only flag promoted to `atomic.Bool` so the race
  detector stays silent and the fixture survives any future
  `t.Parallel()` addition.

### Proposed stable public API (locked unless an issue surfaces)

Same surface as v0.9.1 — the breaking removals from v0.9.0 → v0.9.1
hold. Three behavioral invariants are now explicitly part of the
freeze (previously only documented in godoc):

- **`MultiError.Errors` ordering.** Sorted by `(Path, Tag)` ascending.
  Snapshot tests, log dedup, and structured-error consumers may rely on
  this ordering. Re-sorting or shuffling the slice yields a MultiError
  that no longer satisfies the contract.
- **`Options.Validator` non-mutation.** The library does not call
  `SetTagName`, `RegisterValidation`, or any other mutator on a
  caller-supplied `*validator.Validate`. Sharing one validator across
  goroutines via `Options.Validator` is safe.
- **`ErrPathUnresolved` chain rule.** Whenever the walker cannot map a
  validator namespace to a koanf path (dive, map values, slice
  elements, rules from `RegisterStructValidation`), the returned
  `*FieldError` carries the category sentinel AND `ErrPathUnresolved`
  in its `Unwrap` chain. `errors.Is(fe, ErrPathUnresolved)` is the
  documented way to detect a degraded path without losing the
  category match.

(The proposed-stable surface itself — `Struct`, `Options`, `*FieldError`,
`*MultiError`, `StructValidator`, all 12 sentinels — carries over
unchanged from v0.9.1.)

### MSRV policy (proposed for 1.x)

Unchanged from v0.9.1.

## [0.9.1] — 2026-06-07

**Second release candidate for 1.0.** Supersedes v0.9.0. The stabilization
window caught one breaking change worth landing before the v1.0.0 freeze.

### Removed (breaking)

- **`slog.LogValuer` implementations on `*FieldError` and `*MultiError`**,
  along with the `log/slog` import they pulled into the public surface.
  A library has no business picking a logging framework on consumers'
  behalf — slog, zerolog, zap, logrus, and logr all coexist in the Go
  ecosystem, and forcing slog penalises every consumer who chose
  otherwise. The structured-rendering need is met by `MarshalJSON`
  below.

### Added

- **`MarshalJSON` on `*FieldError` and `*MultiError`.** Emits a stable
  snake_case JSON envelope (`{count, errors:[{path, tag, param, …}]}`)
  suitable for structured logs, JSONL audit trails, or HTTP API
  responses. Honors the same redaction contract `Value` followed: the
  failing value is included only when the originating `Struct` call set
  `IncludeValues=true`. Optional fields (`param`, `raw_param`, `value`,
  `path_unresolved`, `cause`) are omitted when empty. Consumers on any
  logging framework can `json.Marshal` the error and feed the bytes
  into their pipeline of choice.

### Fixed

- `MultiError.LogValue` (now removed) was silently emitting children as
  Pascal-case struct fields via the JSON handler's `json.Marshal`
  fallback for `KindAny` slices, completely bypassing per-element
  `LogValuer` resolution. `MarshalJSON` on each child fixes this for
  the JSON path that supersedes it. Permanent regression tests at
  `TestFieldError_MarshalJSON_StructuredShape` and
  `TestMultiError_MarshalJSON_StructuredShape`.

### Proposed stable public API (locked unless an issue surfaces)

The following will be frozen for the entire v1.x line once v1.0.0 ships;
during the 0.9.x window they are committed only with the asterisk above:

- `koanfvalidate.Struct(cfg any, opts Options) error` — the entry point.
- `Options{Validator, PathTag, ValidateTag, IncludeValues, Delim}` —
  caller-facing knobs. New fields may be added (zero values must stay
  meaningful); existing fields will not be removed or repurposed.
- `*FieldError` — `Path`, `Tag`, `Param`, `RawParam`, `Value` fields;
  `Error()`, `Unwrap() []error`, `MarshalJSON()` methods.
- `*MultiError` — `Errors` slice (sort + length invariants documented in
  the type's godoc); `Error()`, `Unwrap() []error`, `MarshalJSON()` methods.
- `StructValidator` interface — the type-anchored `Validate() error`
  auto-discovery contract.
- Sentinel errors: `ErrInvalidInput`, `ErrInvalidConfig`, `ErrCyclicType`,
  `ErrValidation`, `ErrRequired`, `ErrOutOfRange`, `ErrNotInSet`,
  `ErrBadFormat`, `ErrFieldMismatch`, `ErrInvariant`, `ErrPanic`,
  `ErrPathUnresolved`. New sentinels may be added; existing ones will
  not change identity or move out of the public surface.

### Internal surface — not covered

Anything in `walker.go`, `translate.go`, and unexported identifiers in
`koanfvalidate.go` / `errors.go` is implementation. Cache layouts,
recursion bounds, sort algorithms, the `redactedFieldError` wrapper —
all remain free to change within the 1.x line once cut.

### MSRV policy (proposed for 1.x)

MSRV tracks koanf v2's `go` directive. When koanf raises its minimum,
this library will follow in the next minor (1.1, 1.2, …); the floor
will not jump within a patch series. The transitive pins on
`validator/v10` and `golang.org/x/{crypto,sys,text}` continue to follow
the rationale spelled out in the README's dependency-pinning notice.

## [0.9.0] — 2026-06-07

**Initial release candidate for 1.0.** Spelled out the proposed v1.0 API
surface (including `slog.LogValuer` implementations that v0.9.1 later
removed). Reorganized tests along the source-layout axis (`walker_test.go`,
`translate_test.go`, `errors_test.go`, `validate_method_test.go`,
`secrets_test.go`, `helpers_test.go` — main test file dropped from 1380
to 188 LOC across six commits). Hardened CI (vuln job uses
`go-version: 'stable'`; `ossf/scorecard-action` pinned to `v2.4.3`).
Added GitHub issue and pull-request templates. README gained an explicit
scope note ruling out adapters for gookit/validate, ozzo-validation, and
govalidator.

## [0.2.0] — 2026-06-05

### Changed
- **Restored MSRV to Go 1.23.0 to match koanf v2.** `validator/v10` is pinned
  to `v10.27.0` (the last release supporting Go ≤ 1.23) and the
  `golang.org/x/{crypto,sys,text}` transitives are pinned to the last
  versions that compile on Go 1.23. Without these pins, the latest
  `validator/v10` line would silently force MSRV 1.25 and lock out any
  koanf user still on Go 1.23 or 1.24. The pins are minimums — consumers
  on newer Go whose other dependencies pull in newer `x/*` versions get
  those upgraded versions through MVS. Maintainer policy: bump together
  with koanf when [knadh/koanf](https://github.com/knadh/koanf) raises its
  own `go` directive.

### Added
- `ErrPathUnresolved` sentinel, appended to `FieldError.Unwrap` whenever the
  walker cannot map a validator namespace to a koanf path (validator features
  the walker does not model: `dive`, map values, slice elements, rules from
  `RegisterStructValidation`). Consumers can detect degraded paths via
  `errors.Is` without losing the category sentinel.
- `ErrPanic` sentinel, wrapped into the cause chain when a user's `Validate()`
  method panics. The library recovers and surfaces the panic as a
  `*FieldError`; the host process is never crashed by a buggy validator.
- `Options.Delim` — configurable koanf path separator (default `.`) threaded
  through walker path joining, validator-error translation, and
  `Validate()`-error rebasing. Matches whatever delim was passed to
  `koanf.New`.
- `slog.LogValuer` implementations on `FieldError` and `MultiError`.
  Structured loggers now see typed attributes (`path`, `tag`, `param`,
  `raw_param`, `value`, `cause`, `path_unresolved`) instead of the
  `Error()` string. The `value` attribute respects the `IncludeValues`
  redaction contract.

### Changed
- **Path returned from `Validate()` is always relative to the receiver.**
  The previous "contains delim → absolute" heuristic was replaced with
  explicit rebase rules: any non-empty `Path` is unconditionally prefixed
  with the receiver's koanf path. Literal-dot segments are preserved.
- `Param` returned from `Validate()` is rebased only when `Tag` names a
  known cross-field rule (`gtefield`, `eqfield`, …); literal scalar `Param`
  values (e.g. `"10"` from `min=10`) survive verbatim.
- `Struct()` no longer mutates a caller-supplied `*validator.Validate`.
  Callers passing `Options.Validator` must `SetTagName` the instance
  themselves before passing it in; the library only configures
  internally-constructed validators. This is a breaking change for code
  that relied on auto-tag-name configuration on shared validators.
- Errors at degraded paths now carry both the category sentinel
  (`ErrRequired`, etc.) AND `ErrPathUnresolved`. The Path field falls back
  to the raw validator namespace.
- `FieldError.Error()` appends the cause's message for `invariant` errors
  so `fmt.Errorf("…: %w", inner)` wrapping context returned from a
  `Validate()` method reaches operator logs.

### Fixed
- Secret leak via the cause chain when `IncludeValues=false`. A consumer
  calling `errors.As(err, &validator.FieldError).Value()` previously
  recovered the failing value despite the documented secret-safety
  default. A redacting wrapper now silences `Value()` on the cause-side
  while every other `validator.FieldError` method continues to delegate.
- `flattenValidateError` previously recursed into any single-unwrap and
  discarded the wrapping message. It now only follows the wrap when the
  chain leads to a buried `*FieldError`; otherwise the full wrapped error
  is preserved as the cause.
- `Validate()` is no longer called on nil `*struct` fields. The walker
  previously synthesized a zero value receiver, producing validation
  errors against a struct the user never instantiated.
- Sibling `koanf` tag collisions (two fields claiming the same segment,
  including via anonymous-embedded squash) now return `ErrInvalidConfig`
  instead of silently overwriting the path map.
- `ErrCyclicType` messages now include the koanf path where the cycle was
  triggered, not just the offending Go type name.
- Struct nesting deeper than 64 levels now returns `ErrInvalidConfig`
  naming the offending koanf path, defending against pathological inputs
  that would otherwise drive the walker into stack exhaustion.
- Typed-nil errors returned from `Validate()` (e.g. `var e *MyErr;
  return e` where the concrete type is non-nil but the value is) are
  now normalized as "no failure" instead of surfacing as a meaningless
  invariant error pointing at `<nil>`.
- `flattenValidateError` recursion is bounded at depth 64; adversarial
  `errors.Join` chains returned from `Validate()` surface as a single
  truncation invariant rather than blowing the stack.

### Performance
- Walker output memoized via `sync.Map` keyed on
  `(rootType, pathTag, delim)`. Path tables and visitor recipes are
  computed once per unique key.
- Default `*validator.Validate` memoized via `sync.Once` — used when
  `Options.Validator` is nil AND `Options.ValidateTag` is the default.
  Preserves validator/v10's per-type reflection cache across calls.
- `isOpaqueLeaf` decisions cached per `reflect.Type`.
- Visitor recipes cache the resolved `Validate()` method index so
  invocation avoids a method-by-name lookup per Struct call.
- `MultiError.Unwrap` returns a cached `[]error` view of `Errors` via
  `sync.Once`, amortizing the allocation across repeated `errors.Is` /
  `errors.As` traversal.
- `slices.SortStableFunc` + `cmp.Compare` replace the prior
  `sort.SliceStable` + closure capture, removing the per-element
  function-call indirection on the hot ordering path.
- `flattenValidateError` pre-sizes its multi-error output slice using
  the child count as a hint, eliminating slice growth in the common
  `errors.Join` shape.

## [0.1.0] — 2026-06-04

Initial release. Adapter over `github.com/go-playground/validator/v10` that
re-keys validation errors by koanf paths instead of Go field paths. Includes
type-anchored `Validate() error` auto-discovery, cycle guard,
anonymous-embedded squash, `koanf:"-"` skip, custom rule passthrough via
`Options.Validator`, and `Options.IncludeValues` for secret-safe defaults.

See the [v0.1.0 release notes](https://github.com/uded/koanf-validate/releases/tag/v0.1.0)
for the full feature list.

[Unreleased]: https://github.com/uded/koanf-validate/compare/v0.9.2...HEAD
[0.9.2]: https://github.com/uded/koanf-validate/compare/v0.9.1...v0.9.2
[0.9.1]: https://github.com/uded/koanf-validate/compare/v0.9.0...v0.9.1
[0.9.0]: https://github.com/uded/koanf-validate/compare/v0.2.0...v0.9.0
[0.2.0]: https://github.com/uded/koanf-validate/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/uded/koanf-validate/releases/tag/v0.1.0
