// Package koanfvalidate validates a struct populated by koanf.Unmarshal and
// reports failures keyed by koanf paths (e.g. "server.port") rather than Go
// field paths (e.g. "Config.Server.Port"). It wraps github.com/go-playground/
// validator/v10, translating its rule errors and, additionally, auto-calling
// any Validate() error method discovered on walked struct types.
//
// Typical use:
//
//	type Config struct {
//	    Server struct {
//	        Host string `koanf:"host" koanf-validate:"required,hostname"`
//	        Port int    `koanf:"port" koanf-validate:"required,min=1,max=65535"`
//	    } `koanf:"server"`
//	}
//
//	var cfg Config
//	k.Unmarshal("", &cfg)
//	if err := koanfvalidate.Struct(&cfg, koanfvalidate.Options{}); err != nil {
//	    log.Fatal(err)
//	}
package koanfvalidate

import (
	"errors"
	"sort"
	"strings"

	"github.com/go-playground/validator/v10"
)

const (
	defaultPathTag     = "koanf"
	defaultValidateTag = "koanf-validate"
	defaultDelim       = "."
)

// Options configures a validation call. All fields are optional; zero values
// trigger sensible defaults documented per field.
type Options struct {
	// Validator is the underlying *validator.Validate instance used to run
	// tag-based rules. Pass a pre-configured instance to register custom
	// rules (RegisterValidation), aliases (RegisterAlias), struct-level
	// validators (RegisterStructValidation), or translations. When nil, a
	// fresh instance is constructed internally with default settings.
	//
	// Note: Struct calls SetTagName on the validator each invocation to
	// honor Options.ValidateTag; if you share a *validator.Validate across
	// koanf-validate and other validation callers, expect its current tag
	// name to be the one most recently set.
	Validator *validator.Validate

	// PathTag names the struct tag whose value supplies the koanf path
	// segment for each field. Empty → "koanf".
	PathTag string

	// ValidateTag names the struct tag whose value declares validation rules.
	// Empty → "koanf-validate".
	ValidateTag string

	// IncludeValues, when true, populates FieldError.Value with the actual
	// failing field value. Off by default to avoid leaking secrets (e.g. a
	// password field that fails a min=N rule would otherwise dump the
	// password into logs).
	IncludeValues bool
}

// StructValidator is the auto-discovery interface for type-anchored
// validation. Any struct type encountered during the walk (at any depth) that
// implements StructValidator has its Validate() method invoked and the result
// merged into the returned MultiError. Both value and pointer receivers are
// honored.
//
// Validate() may return:
//   - nil — no failure for this struct.
//   - a plain error — attached to the receiver's koanf path with Tag set to
//     "invariant" and the ErrInvariant sentinel.
//   - a *FieldError — used as-is (lets the method pinpoint a specific child
//     field or carry a custom Tag). Paths and cross-field Params are
//     interpreted relative to the receiver's koanf path: writing
//     {Path: "port"} from a Validate method on a struct mounted at "server"
//     produces a FieldError keyed at "server.port".
//   - errors.Join(...) of any combination of the above — each leaf is added
//     to the returned MultiError.
type StructValidator interface {
	Validate() error
}

// Struct validates cfg and returns nil on success or a *MultiError on
// failure. cfg must be a non-nil pointer to a struct; any other input
// produces an error matching ErrInvalidInput.
//
// Behavior:
//   - Tag-based rules from validator/v10 are evaluated first.
//   - Any encountered struct implementing StructValidator has its Validate()
//     method called; resulting errors are merged with tag-rule errors.
//   - Returned FieldErrors are deterministically ordered by (Path, Tag).
func Struct(cfg any, opts Options) error {
	if opts.PathTag == "" {
		opts.PathTag = defaultPathTag
	}
	if opts.ValidateTag == "" {
		opts.ValidateTag = defaultValidateTag
	}

	// walkStruct also validates the input shape and surfaces ErrInvalidInput
	// / ErrCyclicType. Running it first means a bad input is rejected before
	// we touch the validator.
	wr, err := walkStruct(cfg, opts.PathTag, defaultDelim)
	if err != nil {
		return err
	}

	val := opts.Validator
	if val == nil {
		val = validator.New()
	}
	val.SetTagName(opts.ValidateTag)

	var fieldErrors []*FieldError

	if vErr := val.Struct(cfg); vErr != nil {
		var vErrs validator.ValidationErrors
		if !errors.As(vErr, &vErrs) {
			// *InvalidValidationError or similar — propagate verbatim so
			// callers can distinguish library misuse from validation failure.
			return vErr
		}
		for _, vfe := range vErrs {
			fe := translateFieldError(vfe, wr.paths, wr.skippedPrefixes, opts.IncludeValues)
			if fe == nil {
				continue
			}
			fieldErrors = append(fieldErrors, fe)
		}
	}

	for _, vis := range wr.visitors {
		userErr := callValidate(vis.receiver)
		if userErr == nil {
			continue
		}
		fieldErrors = append(fieldErrors, flattenValidateError(userErr, vis.koanfPath)...)
	}

	if len(fieldErrors) == 0 {
		return nil
	}

	sort.SliceStable(fieldErrors, func(i, j int) bool {
		if fieldErrors[i].Path != fieldErrors[j].Path {
			return fieldErrors[i].Path < fieldErrors[j].Path
		}
		return fieldErrors[i].Tag < fieldErrors[j].Tag
	})
	return &MultiError{Errors: fieldErrors}
}

// flattenValidateError turns whatever a Validate() method returned into a
// slice of *FieldError. Walk order:
//   - direct *FieldError → rebased to receiver path
//   - multi-error (errors.Join → Unwrap() []error) → recurse on each leaf
//   - single-wrap (fmt.Errorf %w → Unwrap() error) → recurse on the inner
//   - any other error → one invariant FieldError at receiver path
//
// The direct check must precede the multi-error check because *FieldError
// itself implements Unwrap() []error to expose its (sentinel, cause) chain;
// without ordering we would recurse into that chain and lose the user's
// intent.
func flattenValidateError(err error, receiverPath string) []*FieldError {
	if err == nil {
		return nil
	}

	if fe, ok := err.(*FieldError); ok {
		return []*FieldError{rebaseFieldError(fe, receiverPath)}
	}

	if u, ok := err.(interface{ Unwrap() []error }); ok {
		var out []*FieldError
		for _, sub := range u.Unwrap() {
			out = append(out, flattenValidateError(sub, receiverPath)...)
		}
		return out
	}

	if u, ok := err.(interface{ Unwrap() error }); ok {
		if inner := u.Unwrap(); inner != nil {
			return flattenValidateError(inner, receiverPath)
		}
	}

	return []*FieldError{{
		Path:     receiverPath,
		Tag:      "invariant",
		sentinel: ErrInvariant,
		cause:    err,
	}}
}

// rebaseFieldError produces a copy of fe with Path and Param interpreted
// relative to the Validate() method's receiver:
//   - empty Path → receiverPath.
//   - Path containing the delim is treated as already-absolute and left alone.
//   - Path without the delim is prefixed with receiverPath.
//
// Same rule for Param. The heuristic is conservative: callers who want to
// emit an absolute path can do so by including the delim, and the common
// case ({Path: "port"} from a Validate on a struct at "server") becomes
// "server.port" without ceremony.
//
// Tag, sentinel, and Value are filled in with invariant defaults when the
// user didn't supply them.
func rebaseFieldError(fe *FieldError, receiverPath string) *FieldError {
	out := *fe
	if out.Tag == "" {
		out.Tag = "invariant"
	}
	if out.sentinel == nil {
		out.sentinel = ErrInvariant
	}
	if receiverPath != "" {
		out.Path = prefixIfRelative(out.Path, receiverPath)
		out.Param = prefixIfRelative(out.Param, receiverPath)
	}
	return &out
}

// prefixIfRelative prepends prefix + delim to s when s does not already
// contain delim. An empty s becomes prefix on its own.
func prefixIfRelative(s, prefix string) string {
	if s == "" {
		return prefix
	}
	if strings.Contains(s, defaultDelim) {
		return s
	}
	return prefix + defaultDelim + s
}
