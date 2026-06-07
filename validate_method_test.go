package koanfvalidate_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/go-playground/validator/v10"

	koanfvalidate "github.com/uded/koanf-validate"
)

// =============================================================================
// Fixtures — types implementing the StructValidator interface
// =============================================================================

// Type-anchored Validate() method (value receiver)
type valueValidate struct {
	X int `koanf:"x" koanf-validate:"required"`
}

func (v valueValidate) Validate() error {
	if v.X == 7 {
		return errors.New("x must not be the unlucky number")
	}
	return nil
}

// Type-anchored Validate() method (pointer receiver) returning *FieldError
type pointerValidate struct {
	Port    int `koanf:"port"     koanf-validate:"required"`
	MinPort int `koanf:"min_port" koanf-validate:"required"`
}

func (p *pointerValidate) Validate() error {
	if p.Port < p.MinPort {
		return &koanfvalidate.FieldError{
			Path:  "port",
			Tag:   "gtefield",
			Param: "min_port",
		}
	}
	return nil
}

// Validate() returning errors.Join of multiple *FieldError
type joinedValidate struct {
	A int `koanf:"a" koanf-validate:"required"`
	B int `koanf:"b" koanf-validate:"required"`
}

func (j *joinedValidate) Validate() error {
	return errors.Join(
		&koanfvalidate.FieldError{Path: "a", Tag: "custom_a"},
		&koanfvalidate.FieldError{Path: "b", Tag: "custom_b"},
	)
}

// Container holding a type with Validate() at a nested koanf path
type withNestedValidate struct {
	Server pointerValidate `koanf:"server"`
}

// errors.Join nested inside errors.Join must flatten correctly — every
// leaf *FieldError reaches MultiError without being lost or wrapped.
type nestedJoinValidate struct {
	X int `koanf:"x"`
}

func (n *nestedJoinValidate) Validate() error {
	return errors.Join(
		&koanfvalidate.FieldError{Path: "first", Tag: "outer"},
		errors.Join(
			&koanfvalidate.FieldError{Path: "second", Tag: "inner_a"},
			&koanfvalidate.FieldError{Path: "third", Tag: "inner_b"},
		),
	)
}

// A Validate() method returning a Path that contains the delim is still
// interpreted as relative to the receiver — the library does not attempt
// to detect "absolute" paths via a leading delim or by substring search.
// This preserves literal-dot path segments and keeps the rebasing rule
// uniform.
type alwaysRelativePathInner struct {
	X int `koanf:"x"`
}

func (a *alwaysRelativePathInner) Validate() error {
	return &koanfvalidate.FieldError{Path: "child.field", Tag: "custom_check"}
}

type alwaysRelativeCfg struct {
	Server alwaysRelativePathInner `koanf:"server"`
}

// Param is only rebased when Tag names a cross-field rule. Literal scalar
// Params (e.g. "10" from min=10, or anything a user-defined Tag carries)
// must not be polluted with the receiver path.
type literalParamInner struct {
	X int `koanf:"x"`
}

func (l *literalParamInner) Validate() error {
	return &koanfvalidate.FieldError{Path: "x", Tag: "min_custom", Param: "10"}
}

type literalParamCfg struct {
	Server literalParamInner `koanf:"server"`
}

// A nil *struct field whose type implements Validate() must NOT have
// Validate() called on a synthetic zero value. The library walks the schema
// at type-level for path mapping, but skips visitor execution when the
// user's pointer is nil — the sub-config does not exist.
type instrumentedValidate struct {
	X int `koanf:"x"`
}

// validateCalled tracks invocation of instrumentedValidate.Validate.
// atomic.Bool so the two tests touching it can safely run with
// t.Parallel() if anyone adds it later, and so the race detector stays
// silent if a future test exercises the same fixture concurrently.
var validateCalled atomic.Bool

func (o *instrumentedValidate) Validate() error {
	validateCalled.Store(true)
	return nil
}

type withOptionalSubCfg struct {
	Server *instrumentedValidate `koanf:"server"`
}

// A Validate() method that returns a typed-nil ((*MyErr)(nil) wrapped in an
// error interface) must be treated as "no failure". The Go gotcha: the
// interface compares != nil even though the underlying pointer is nil, so
// without normalisation the library would surface a meaningless invariant
// error pointing at <nil>.
type typedNilErr struct{ msg string }

func (e *typedNilErr) Error() string { return e.msg }

type withTypedNilValidate struct {
	OK bool `koanf:"ok"`
}

func (v *withTypedNilValidate) Validate() error {
	var e *typedNilErr // typed-nil: concrete type non-nil, value nil
	return e
}

// A Validate() method that panics must not crash the host process; the
// library recovers and surfaces the panic as a FieldError whose chain
// matches both ErrPanic and ErrInvariant.
type panicValidate struct {
	X int `koanf:"x"`
}

func (p *panicValidate) Validate() error {
	panic("intentional panic for test")
}

// errReserved is a fixture sentinel that a Validate() method wraps with
// fmt.Errorf("…: %w", errReserved); the library must preserve both the
// wrapping message and the wrapped chain so errors.Is reaches the inner
// sentinel.
var errReserved = errors.New("port is reserved")

type wrapValidate struct {
	Port int `koanf:"port" koanf-validate:"required"`
}

func (w *wrapValidate) Validate() error {
	if w.Port == 22 {
		return fmt.Errorf("port %d is reserved by the OS: %w", w.Port, errReserved)
	}
	return nil
}

// =============================================================================
// Tests — StructValidator auto-discovery behavior
// =============================================================================

// Invariant errors come from a user's Validate() method, not from validator/v10.
// errors.As(fe, &validator.FieldError) must therefore return false — there is
// no underlying validator.FieldError to expose.
func TestStruct_InvariantError_HasNoValidatorCause(t *testing.T) {
	t.Parallel()
	cfg := &valueValidate{X: 7} // Validate() returns a plain error
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	fe := findByPath(me, "")
	if fe == nil {
		t.Fatalf("no FieldError at root; got %v", pathsOf(me))
	}
	if !errors.Is(fe, koanfvalidate.ErrInvariant) {
		t.Error("errors.Is(fe, ErrInvariant) should be true")
	}
	var vfe validator.FieldError
	if errors.As(fe, &vfe) {
		t.Errorf("errors.As to validator.FieldError must fail for invariant errors; got %v", vfe)
	}
}

func TestStruct_NestedErrorsJoin_FlattensAllLeaves(t *testing.T) {
	t.Parallel()
	cfg := &nestedJoinValidate{}
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	got := pathsOf(me)
	want := []string{"first", "second", "third"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("nested errors.Join flatten: got %v, want %v", got, want)
	}
}

func TestStruct_ValidatePath_AlwaysRelativeEvenWithDelim(t *testing.T) {
	t.Parallel()
	cfg := &alwaysRelativeCfg{}
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	if findByPath(me, "server.child.field") == nil {
		t.Fatalf("expected server.child.field (relative rebase), got %v", pathsOf(me))
	}
}

func TestStruct_ValidateParam_LiteralPreservedForNonCrossFieldTags(t *testing.T) {
	t.Parallel()
	cfg := &literalParamCfg{}
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	fe := findByPath(me, "server.x")
	if fe == nil {
		t.Fatalf("expected server.x, got %v", pathsOf(me))
	}
	if fe.Param != "10" {
		t.Errorf("Param: got %q, want %q (literal, no rebase for non-cross-field tags)", fe.Param, "10")
	}
}

func TestStruct_NilPointerStructValidator_IsSkipped(t *testing.T) {
	// Not parallel — observes a package-level atomic shared with
	// TestStruct_NonNilPointerStructValidator_IsCalled.
	t.Cleanup(func() { validateCalled.Store(false) })
	validateCalled.Store(false)

	cfg := &withOptionalSubCfg{Server: nil}
	if err := koanfvalidate.Struct(cfg, koanfvalidate.Options{}); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if validateCalled.Load() {
		t.Error("Validate() was called on a nil *struct field — must be skipped")
	}
}

// Sanity: a non-nil pointer must still trigger Validate().
func TestStruct_NonNilPointerStructValidator_IsCalled(t *testing.T) {
	t.Cleanup(func() { validateCalled.Store(false) })
	validateCalled.Store(false)

	cfg := &withOptionalSubCfg{Server: &instrumentedValidate{X: 1}}
	if err := koanfvalidate.Struct(cfg, koanfvalidate.Options{}); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !validateCalled.Load() {
		t.Error("Validate() was not called on a non-nil *struct field")
	}
}

func TestStruct_TypedNilFromValidate_NormalisedAsNoError(t *testing.T) {
	t.Parallel()
	cfg := &withTypedNilValidate{}
	if err := koanfvalidate.Struct(cfg, koanfvalidate.Options{}); err != nil {
		t.Errorf("typed-nil from Validate must be normalised to no error; got %v", err)
	}
}

func TestStruct_ValidatePanic_RecoveredAsFieldError(t *testing.T) {
	t.Parallel()
	cfg := &panicValidate{}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Struct must recover Validate() panics, but one escaped: %v", r)
		}
	}()
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	fe := findByPath(me, "")
	if fe == nil {
		t.Fatalf("no FieldError at root; got %v", pathsOf(me))
	}
	if !errors.Is(fe, koanfvalidate.ErrPanic) {
		t.Error("errors.Is(fe, ErrPanic) should be true")
	}
	if !errors.Is(fe, koanfvalidate.ErrInvariant) {
		t.Error("errors.Is(fe, ErrInvariant) should be true — panics are still Validate() failures")
	}
	if !strings.Contains(me.Error(), "intentional panic for test") {
		t.Errorf("rendered MultiError missing panic message:\n%s", me.Error())
	}
}

func TestStruct_ValidateMethod_ValueReceiverPlainError(t *testing.T) {
	t.Parallel()
	cfg := &valueValidate{X: 7}
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	// Path is the receiver's koanf path — for root struct it's "" so we expect "".
	fe := findByPath(me, "")
	if fe == nil {
		t.Fatalf("no FieldError at root path; paths=%v", pathsOf(me))
	}
	if fe.Tag != "invariant" {
		t.Errorf("Tag: got %q, want invariant", fe.Tag)
	}
	if !errors.Is(fe, koanfvalidate.ErrInvariant) {
		t.Errorf("errors.Is ErrInvariant = false")
	}
}

func TestStruct_ValidateMethod_PointerReceiverFieldError(t *testing.T) {
	t.Parallel()
	cfg := &pointerValidate{Port: 1, MinPort: 10}
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	fe := findByPath(me, "port")
	if fe == nil {
		t.Fatalf("no FieldError at port; paths=%v", pathsOf(me))
	}
	if fe.Tag != "gtefield" {
		t.Errorf("Tag: got %q, want gtefield", fe.Tag)
	}
	if fe.Param != "min_port" {
		t.Errorf("Param: got %q, want min_port", fe.Param)
	}
}

func TestStruct_ValidateMethod_ReturnsNil(t *testing.T) {
	t.Parallel()
	cfg := &valueValidate{X: 1} // not 7, no error
	if err := koanfvalidate.Struct(cfg, koanfvalidate.Options{}); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestStruct_ValidateMethod_ReturnsJoinedErrors(t *testing.T) {
	t.Parallel()
	cfg := &joinedValidate{A: 1, B: 2}
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	if findByPath(me, "a") == nil || findByPath(me, "b") == nil {
		t.Fatalf("expected FieldErrors at a and b; got %v", pathsOf(me))
	}
}

// A Validate() method returning fmt.Errorf("…: %w", inner) must reach the
// consumer with both the wrapping message AND the chain intact. If
// flattenValidateError recursed unconditionally into the single-wrap Unwrap,
// the wrapping message would be discarded and the operator would never see
// "port 22 is reserved by the OS".
func TestStruct_ValidateMethod_PreservesWrapContext(t *testing.T) {
	t.Parallel()
	cfg := &wrapValidate{Port: 22}
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	fe := findByPath(me, "")
	if fe == nil {
		t.Fatalf("no FieldError at root path; paths=%v", pathsOf(me))
	}
	if fe.Tag != "invariant" {
		t.Errorf("Tag: got %q, want invariant", fe.Tag)
	}
	if !errors.Is(fe, koanfvalidate.ErrInvariant) {
		t.Error("errors.Is(fe, ErrInvariant) = false")
	}
	// The library MUST preserve the wrap chain so consumers can errors.Is
	// against the inner sentinel.
	if !errors.Is(fe, errReserved) {
		t.Error("errors.Is(fe, errReserved) = false — wrap context discarded")
	}
	// The full rendered MultiError must contain the wrapping message; that's
	// the human-readable signal a Validate() author wrote and expects to see.
	if !strings.Contains(me.Error(), "port 22 is reserved by the OS") {
		t.Errorf("rendered MultiError missing wrap context:\n%s", me.Error())
	}
}

func TestStruct_ValidateMethod_NestedReceiver(t *testing.T) {
	t.Parallel()
	cfg := &withNestedValidate{}
	cfg.Server.Port = 1
	cfg.Server.MinPort = 10
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	// Validate() returned FieldError{Path: "port"}. The walker must rewrite
	// that to "server.port" because the receiver lives at koanf path "server".
	fe := findByPath(me, "server.port")
	if fe == nil {
		t.Fatalf("expected server.port; got %v", pathsOf(me))
	}
	if fe.Param != "server.min_port" {
		t.Errorf("Param: got %q, want server.min_port", fe.Param)
	}
}

func TestStruct_TagRulesPlusValidateBothReported(t *testing.T) {
	t.Parallel()
	cfg := &valueValidate{X: 7} // X is set so tag "required" passes; Validate() fires
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	// Only invariant error expected since X=7 is non-zero.
	if len(me.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d: %v", len(me.Errors), pathsOf(me))
	}
}
