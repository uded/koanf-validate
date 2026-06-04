package koanfvalidate

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidInput is returned when the target passed to Struct is nil, a
// non-pointer, a nil pointer, or a pointer to a non-struct value.
var ErrInvalidInput = errors.New("koanfvalidate: input must be a non-nil pointer to a struct")

// ErrInvalidConfig is returned when the Options struct is invalid — e.g. a
// negative-sized parameter or an unparseable custom tag name.
var ErrInvalidConfig = errors.New("koanfvalidate: invalid Options")

// ErrCyclicType is returned when the walker encounters a struct type that
// recursively references itself (directly or transitively). Validating such
// a type would otherwise recurse without bound.
var ErrCyclicType = errors.New("koanfvalidate: cyclic struct type")

// ErrValidation is the generic parent sentinel for any rule failure whose
// tag is not classified into one of the specific sentinels below. Match it
// when you want to catch "any validation failure" regardless of category.
var ErrValidation = errors.New("koanfvalidate: validation failed")

// ErrRequired is returned for "value must be present" rules: required,
// required_if, required_unless, required_with, required_without.
var ErrRequired = errors.New("koanfvalidate: required")

// ErrOutOfRange is returned for magnitude/size rules: min, max, gte, lte,
// len, eq, ne (when applied to numeric ordering or length).
var ErrOutOfRange = errors.New("koanfvalidate: out of range")

// ErrNotInSet is returned for enumeration rules: oneof, not_oneof.
var ErrNotInSet = errors.New("koanfvalidate: value not in allowed set")

// ErrBadFormat is returned for format/pattern rules: email, url, uri, uuid,
// hostname, hostname_rfc1123, ip, ipv4, ipv6, cidr, datetime, alphanum, etc.
var ErrBadFormat = errors.New("koanfvalidate: format invalid")

// ErrFieldMismatch is returned for cross-field comparison rules: eqfield,
// nefield, gtfield, gtefield, ltfield, ltefield, and the _cs (case-sensitive)
// variants.
var ErrFieldMismatch = errors.New("koanfvalidate: field constraint not satisfied")

// ErrInvariant is returned for failures produced by a type's Validate()
// method (the "type-anchored" validation convention). Match it when you want
// to discriminate user-authored invariant failures from tag-rule failures.
var ErrInvariant = errors.New("koanfvalidate: invariant violated")

// ErrPathUnresolved is added to a FieldError's Unwrap chain whenever the
// underlying validator/v10 namespace could not be mapped to a koanf path.
// This happens for validator features the walker does not model: dive,
// map values, slice elements, and rules registered via
// RegisterStructValidation. In those cases the FieldError's Path field
// holds the raw Go field path (e.g. "Cfg.Tags[key]") rather than a koanf
// path — alert your logging/alerting pipeline by matching this sentinel.
var ErrPathUnresolved = errors.New("koanfvalidate: koanf path could not be resolved from validator namespace")

// FieldError describes a single validation failure keyed by its koanf path
// rather than the underlying Go field path. It satisfies the error interface
// and supports errors.Is / errors.As against both the sentinel category and
// the underlying validator.FieldError (when produced by a tag rule).
type FieldError struct {
	// Path is the koanf path of the failing field, e.g. "server.port".
	Path string

	// Tag is the name of the rule that failed, e.g. "required", "min",
	// "gtefield", or "invariant" for errors returned from a Validate() method.
	Tag string

	// Param is the rule parameter, translated to a koanf path when the
	// underlying parameter resolves as a Go field path. For literal-scalar
	// params (e.g. "10" from min=10) Param equals the literal. For tags
	// without parameters Param is the empty string.
	Param string

	// RawParam is the validator/v10 Param() value verbatim — for cross-field
	// rules this is the raw Go field path (e.g. "MinPort"). Empty for
	// invariant errors and other cases without an upstream Param.
	RawParam string

	// Value is the actual field value that failed validation. Populated only
	// when Options.IncludeValues is true; nil otherwise to avoid accidentally
	// leaking secrets through logs.
	Value any

	// sentinel is the categorical sentinel error (ErrRequired, ErrOutOfRange,
	// etc.) that errors.Is matches against. Always non-nil.
	sentinel error

	// cause is the underlying validator.FieldError when this FieldError was
	// produced by a tag rule. Nil for invariant errors produced by a
	// Validate() method.
	cause error

	// pathUnresolved is set when the walker could not map the validator
	// namespace to a koanf path. ErrPathUnresolved is added to the Unwrap
	// chain so consumers can match it with errors.Is.
	pathUnresolved bool
}

// Error renders the FieldError in a terse, consistent format:
//
//	<path>: <tag>             // tags without a parameter
//	<path>: <tag>(<param>)    // tags with a parameter
//	<path>: invariant: <msg>  // invariant errors include the cause's
//	                          // message so wrapping context added by
//	                          // a Validate() method survives to logs
//
// Examples:
//
//	server.port: required
//	server.port: min(10)
//	server.port: oneof(http https)
//	server.port: gtefield(server.min_port)
//	server: invariant: port 22 is reserved by the OS
func (e *FieldError) Error() string {
	base := e.Path + ": " + e.Tag
	if e.Param != "" {
		base += "(" + e.Param + ")"
	}
	if e.Tag == "invariant" && e.cause != nil {
		base += ": " + e.cause.Error()
	}
	return base
}

// Unwrap returns the chain {sentinel, cause, ErrPathUnresolved?}. The
// sentinel is always present; the cause is the underlying
// validator.FieldError for tag rules or nil for invariant errors;
// ErrPathUnresolved is appended only when the koanf path could not be
// resolved (e.g. for dive, maps, slice elements). This lets callers do:
//
//	errors.Is(err, koanfvalidate.ErrRequired)            // sentinel match
//	errors.As(err, &validator.ValidationErrors{})        // cause match
//	errors.Is(err, koanfvalidate.ErrPathUnresolved)      // degraded path
func (e *FieldError) Unwrap() []error {
	out := make([]error, 0, 3)
	out = append(out, e.sentinel)
	if e.cause != nil {
		out = append(out, e.cause)
	}
	if e.pathUnresolved {
		out = append(out, ErrPathUnresolved)
	}
	return out
}

// MultiError joins per-field validation failures into one error suitable for
// returning from Struct. Errors are deterministically ordered (by Path, then
// Tag) so test output and logs remain stable across runs.
type MultiError struct {
	Errors []*FieldError
}

// Error renders the MultiError as a newline-joined list of FieldError strings,
// prefixed by a one-line summary. Stable across runs.
func (m *MultiError) Error() string {
	if len(m.Errors) == 0 {
		return "koanfvalidate: validation failed"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "koanfvalidate: %d validation error(s)", len(m.Errors))
	for _, fe := range m.Errors {
		b.WriteString("\n  - ")
		b.WriteString(fe.Error())
	}
	return b.String()
}

// Unwrap exposes the individual FieldErrors for errors.Is/errors.As walking.
// errors.Is(multi, ErrRequired) returns true iff any contained FieldError
// has ErrRequired as its sentinel.
func (m *MultiError) Unwrap() []error {
	out := make([]error, len(m.Errors))
	for i, fe := range m.Errors {
		out[i] = fe
	}
	return out
}
