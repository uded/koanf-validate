# Changelog

All notable changes to `koanf-validate` are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

[Unreleased]: https://github.com/uded/koanf-validate/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/uded/koanf-validate/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/uded/koanf-validate/releases/tag/v0.1.0
