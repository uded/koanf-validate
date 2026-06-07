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
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"
)

// defaultValidatorOnce + defaultValidatorInstance hold a shared
// *validator.Validate configured with the default tag name. Each Struct call
// that takes no caller-supplied validator and leaves Options.ValidateTag at
// its default reuses this instance — preserving validator/v10's per-type
// reflection cache across calls. validator/v10's Struct method is documented
// concurrent-safe; only the mutators (SetTagName, RegisterValidation) are
// not, and we only ever call those during construction.
var (
	defaultValidatorOnce     sync.Once
	defaultValidatorInstance *validator.Validate
)

// defaultValidator returns a *validator.Validate configured for tagName.
// When tagName matches the library default, a package-level singleton is
// reused (the hot path). Custom tag names build a fresh validator since
// SetTagName is not concurrency-safe across many such customizations.
func defaultValidator(tagName string) *validator.Validate {
	if tagName == defaultValidateTag {
		defaultValidatorOnce.Do(func() {
			v := validator.New()
			v.SetTagName(defaultValidateTag)
			defaultValidatorInstance = v
		})
		return defaultValidatorInstance
	}
	v := validator.New()
	v.SetTagName(tagName)
	return v
}

const (
	defaultPathTag     = "koanf"
	defaultValidateTag = "koanf-validate"
	defaultDelim       = "."

	// maxValidateErrorDepth caps how deeply flattenValidateError will
	// recurse when unwrapping errors returned from Validate() methods.
	// A user (or an adversarial caller) returning deeply nested
	// errors.Join chains shouldn't be able to drive the host into stack
	// exhaustion; hitting the cap surfaces as a single invariant
	// FieldError naming the limit.
	maxValidateErrorDepth = 64
)

// invalidTagChars are characters that have special meaning inside a Go
// struct-tag value or that confuse validator/v10's tag parser. PathTag
// and ValidateTag must not contain any of these; the failure mode without
// this check is cryptic errors from deep inside reflection/parsing code.
const invalidTagChars = " \t\n\","

// validateOptions enforces the contract documented on Options and
// surfaces violations as wrapped ErrInvalidConfig. Called after the
// default-filling pass in Struct(), so values are guaranteed non-empty.
func validateOptions(opts Options) error {
	if opts.PathTag == opts.ValidateTag {
		return fmt.Errorf("%w: PathTag and ValidateTag must differ (both are %q)", ErrInvalidConfig, opts.PathTag)
	}
	if strings.ContainsAny(opts.PathTag, invalidTagChars) {
		return fmt.Errorf("%w: PathTag %q contains invalid characters (whitespace, comma, or quote)", ErrInvalidConfig, opts.PathTag)
	}
	if strings.ContainsAny(opts.ValidateTag, invalidTagChars) {
		return fmt.Errorf("%w: ValidateTag %q contains invalid characters (whitespace, comma, or quote)", ErrInvalidConfig, opts.ValidateTag)
	}
	return nil
}

// Options configures a validation call. All fields are optional; zero values
// trigger sensible defaults documented per field.
type Options struct {
	// Validator is the underlying *validator.Validate instance used to run
	// tag-based rules. Pass a pre-configured instance to register custom
	// rules (RegisterValidation), aliases (RegisterAlias), struct-level
	// validators (RegisterStructValidation), or translations.
	//
	// When non-nil, the caller MUST have already called
	//   v.SetTagName(opts.ValidateTag)
	// (or SetTagName("koanf-validate") when ValidateTag is left default).
	// The library does not mutate caller-supplied validators — doing so
	// would race against any goroutine concurrently calling v.Struct(...)
	// on the same instance.
	//
	// When nil, the library constructs and configures a fresh validator
	// per call with the correct tag name; share an instance via this
	// field if you need to amortize validator/v10's reflection cache or
	// register custom rules.
	Validator *validator.Validate

	// PathTag names the struct tag whose value supplies the koanf path
	// segment for each field. Empty → "koanf".
	PathTag string

	// ValidateTag names the struct tag whose value declares validation rules.
	// Empty → "koanf-validate".
	ValidateTag string

	// IncludeValues, when true, populates FieldError.Value with the actual
	// failing field value AND keeps the underlying validator.FieldError's
	// Value() reachable through the cause chain.
	//
	// Off by default to avoid leaking secrets — a password field failing
	// min=N would otherwise dump the password into logs both via
	// FieldError.Value and via errors.As(err, &validator.FieldError).Value().
	//
	// Trade-off: the safe default also hides non-sensitive failing values
	// (port numbers, timeouts, enum mismatches) from SREs. Re-validating
	// with IncludeValues=true at a debug log level is a reasonable pattern
	// when the redacted message is insufficient.
	IncludeValues bool

	// Delim is the path separator joining koanf path segments. Empty → "."
	// (matches koanf's own default). Set this to whatever separator you
	// passed to koanf.New. Paths returned from a Validate() method are
	// always interpreted as relative to the receiver and prefixed with
	// receiver+Delim — there is no absolute-path detection.
	Delim string
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
//     field or carry a custom Tag). Path is always interpreted as relative
//     to the receiver's koanf path: writing {Path: "port"} from a Validate
//     method on a struct mounted at "server" produces a FieldError keyed at
//     "server.port". Param is rebased the same way ONLY when Tag names a
//     known cross-field rule (gtefield, eqfield, …); literal scalar Params
//     (e.g. "10") survive verbatim regardless of the receiver path.
//
//     IMPORTANT: when Tag is a cross-field rule, Param MUST be the
//     UNQUALIFIED name of a sibling field (e.g. "min_port"), NOT a
//     pre-qualified koanf path. The library unconditionally prefixes Param
//     with the receiver path — passing "server.min_port" from a Validate
//     method on a struct mounted at "server" produces "server.server.min_port".
//   - errors.Join(...) of any combination of the above — each leaf is added
//     to the returned MultiError.
type StructValidator interface {
	Validate() error
}

// Struct validates cfg and returns one of:
//
//   - nil — every rule passed.
//   - *MultiError — one or more validation failures; the typical return.
//   - ErrInvalidInput (wrapped) — cfg was nil, a non-pointer, a nil pointer,
//     or a pointer to a non-struct.
//   - ErrInvalidConfig (wrapped) — opts itself is malformed (PathTag equals
//     ValidateTag, tag name contains illegal characters), OR cfg's struct
//     shape is malformed (two sibling fields claim the same koanf segment;
//     nesting exceeds the depth cap).
//   - ErrCyclicType (wrapped) — cfg's type recursively references itself.
//   - *validator.InvalidValidationError — propagated verbatim when
//     validator/v10 itself rejects the input (e.g. passed a non-struct
//     through a custom Validator). Distinct from a validation FAILURE.
//
// Use errors.As(err, &me) where me is *MultiError to discriminate the
// validation-failure case from the other categories. errors.Is against
// any of the sentinels reaches them through their wrap chains.
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
	if opts.Delim == "" {
		opts.Delim = defaultDelim
	}
	if err := validateOptions(opts); err != nil {
		return err
	}

	// Resolve the input first so that bad inputs are rejected before any
	// reflection, cache lookup, or validator work. Holds the receiver for
	// visitor resolution below.
	rootValue, err := resolveInput(cfg)
	if err != nil {
		return err
	}

	wr, err := walkStruct(cfg, opts.PathTag, opts.Delim)
	if err != nil {
		return err
	}

	val := opts.Validator
	if val == nil {
		val = defaultValidator(opts.ValidateTag)
	}

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

	for _, recipe := range wr.visitorRecipes {
		receiver, ok := recipe.resolve(rootValue)
		if !ok {
			// Nil pointer along the recipe's path — the user never set this
			// sub-config. Don't invent a zero value and call Validate() on it.
			continue
		}
		userErr := callValidate(receiver, recipe.methodIndex)
		if userErr == nil {
			continue
		}
		fieldErrors = append(fieldErrors, flattenValidateError(userErr, recipe.koanfPath, opts.Delim, 0)...)
	}

	if len(fieldErrors) == 0 {
		return nil
	}

	slices.SortStableFunc(fieldErrors, func(a, b *FieldError) int {
		if c := cmp.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		return cmp.Compare(a.Tag, b.Tag)
	})
	return &MultiError{Errors: fieldErrors}
}

// flattenValidateError turns whatever a Validate() method returned into a
// slice of *FieldError. Walk order:
//   - direct *FieldError → rebased to receiver path
//   - multi-error (errors.Join → Unwrap() []error) → recurse on each leaf
//   - single-wrap whose chain reaches a *FieldError → recurse so the
//     buried FieldError is used as-is
//   - any other error (including fmt.Errorf("…: %w", plain)) → one invariant
//     FieldError at receiver path with the WHOLE wrapped err as cause, so
//     the user's wrapping message survives and errors.Is reaches every
//     sentinel in the chain
//
// The direct check must precede the multi-error check because *FieldError
// itself implements Unwrap() []error to expose its (sentinel, cause) chain;
// without ordering we would recurse into that chain and lose the user's
// intent.
func flattenValidateError(err error, receiverPath, delim string, depth int) []*FieldError {
	if err == nil {
		return nil
	}

	if depth >= maxValidateErrorDepth {
		return []*FieldError{{
			Path:     receiverPath,
			Tag:      invariantTag,
			sentinel: ErrInvariant,
			cause:    fmt.Errorf("flattenValidateError: max depth %d exceeded; pathological Validate() return chain truncated", maxValidateErrorDepth),
		}}
	}

	if fe, ok := err.(*FieldError); ok {
		return []*FieldError{rebaseFieldError(fe, receiverPath, delim)}
	}

	if u, ok := err.(interface{ Unwrap() []error }); ok {
		children := u.Unwrap()
		// Pre-size the destination using the multi-error's child count as a
		// hint. Each child may produce more than one FieldError (nested
		// errors.Join, *FieldError-with-Param), but the hint keeps the
		// common case allocation-free past initial growth.
		out := make([]*FieldError, 0, len(children))
		for _, sub := range children {
			out = append(out, flattenValidateError(sub, receiverPath, delim, depth+1)...)
		}
		return out
	}

	if u, ok := err.(interface{ Unwrap() error }); ok {
		if inner := u.Unwrap(); inner != nil {
			var fe *FieldError
			if errors.As(inner, &fe) {
				return flattenValidateError(inner, receiverPath, delim, depth+1)
			}
		}
	}

	return []*FieldError{{
		Path:     receiverPath,
		Tag:      invariantTag,
		sentinel: ErrInvariant,
		cause:    err,
	}}
}

// rebaseFieldError produces a copy of fe with Path and Param interpreted
// relative to the Validate() method's receiver:
//   - Path is always relative. An empty Path becomes receiverPath; a non-
//     empty Path is unconditionally prepended with receiverPath+delim so a
//     literal dot inside Path is preserved rather than treated as a path
//     separator.
//   - Param is only rebased when Tag names a known cross-field rule
//     (gtefield, eqfield, …). Literal scalar Params such as "10" from
//     min=10 survive verbatim and never collide with the delim.
//
// Tag, sentinel, and Value are filled in with invariant defaults when the
// user didn't supply them.
func rebaseFieldError(fe *FieldError, receiverPath, delim string) *FieldError {
	out := *fe
	if out.Tag == "" {
		out.Tag = invariantTag
	}
	if out.sentinel == nil {
		out.sentinel = ErrInvariant
	}
	if receiverPath != "" {
		out.Path = prefixPath(out.Path, receiverPath, delim)
		if _, isCross := crossFieldTags[out.Tag]; isCross {
			out.Param = prefixPath(out.Param, receiverPath, delim)
		}
	}
	return &out
}

// prefixPath joins a relative path s to its receiver. An empty s becomes the
// receiver itself; a non-empty s is unconditionally prepended with
// receiver+delim. There is no absolute-path detection — every path returned
// from a Validate() method is interpreted as relative to the receiver.
func prefixPath(s, receiver, delim string) string {
	if s == "" {
		return receiver
	}
	return receiver + delim + s
}
