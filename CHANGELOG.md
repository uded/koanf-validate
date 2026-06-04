# Changelog

All notable changes to `koanf-validate` are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

### Performance
- Walker output memoized via `sync.Map` keyed on
  `(rootType, pathTag, delim)`. Path tables and visitor recipes are
  computed once per unique key.
- Default `*validator.Validate` memoized via `sync.Once` — used when
  `Options.Validator` is nil AND `Options.ValidateTag` is the default.
  Preserves validator/v10's per-type reflection cache across calls.
- `isOpaqueLeaf` decisions cached per `reflect.Type`.

## [0.1.0] — 2026-06-04

Initial release. Adapter over `github.com/go-playground/validator/v10` that
re-keys validation errors by koanf paths instead of Go field paths. Includes
type-anchored `Validate() error` auto-discovery, cycle guard,
anonymous-embedded squash, `koanf:"-"` skip, custom rule passthrough via
`Options.Validator`, and `Options.IncludeValues` for secret-safe defaults.

See the [v0.1.0 release notes](https://github.com/uded/koanf-validate/releases/tag/v0.1.0)
for the full feature list.

[Unreleased]: https://github.com/uded/koanf-validate/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/uded/koanf-validate/releases/tag/v0.1.0
