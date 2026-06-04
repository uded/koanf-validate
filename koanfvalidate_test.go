package koanfvalidate_test

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"

	koanfvalidate "github.com/uded/koanf-validate"
)

// =============================================================================
// Fixture types
// =============================================================================

type simpleCfg struct {
	Name string `koanf:"name" koanf-validate:"required"`
	Age  int    `koanf:"age"  koanf-validate:"min=0,max=120"`
}

type nestedCfg struct {
	Server struct {
		Host    string        `koanf:"host"     koanf-validate:"required,hostname"`
		Port    int           `koanf:"port"     koanf-validate:"required,min=1,max=65535"`
		MinPort int           `koanf:"min_port" koanf-validate:"required,ltefield=Port"`
		Timeout time.Duration `koanf:"timeout"  koanf-validate:"required"`
	} `koanf:"server"`
	LogLevel string `koanf:"log_level" koanf-validate:"required,oneof=debug info warn error"`
}

type dashTagCfg struct {
	Visible string `koanf:"visible" koanf-validate:"required"`
	Hidden  string `koanf:"-"       koanf-validate:"required"` // must be skipped
}

type embeddedBase struct {
	ID string `koanf:"id" koanf-validate:"required,uuid"`
}

type embeddedCfg struct {
	embeddedBase        // anonymous — squashed into parent
	Specific     string `koanf:"specific" koanf-validate:"required"`
}

type customPathTagCfg struct {
	Name string `mapstructure:"renamed" koanf-validate:"required"`
}

// Cyclic types
type cycleNode struct {
	Name string     `koanf:"name" koanf-validate:"required"`
	Next *cycleNode `koanf:"next"`
}

type mutualA struct {
	B *mutualB `koanf:"b"`
}
type mutualB struct {
	A *mutualA `koanf:"a"`
}

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

// Triggers validator/v10's dive rule, whose namespaces (e.g. "diveCfg.Tags[key]")
// fall outside the walker's path map. Used to assert that the resulting
// FieldError exposes ErrPathUnresolved through its Unwrap chain.
type diveCfg struct {
	Tags map[string]string `koanf:"tags" koanf-validate:"dive,required"`
}

// cyclicTU has both a self-reference AND a TextUnmarshaler implementation.
// If the walker treats TextUnmarshaler types as opaque leaves, it never
// descends into cyclicTU and so never observes the cycle. If the walker
// descended (a regression), the cycle guard would fire and Struct would
// return ErrCyclicType — making this fixture a precise distinguisher.
type cyclicTU struct {
	raw  string
	Self *cyclicTU
}

func (t *cyclicTU) UnmarshalText(b []byte) error { t.raw = string(b); return nil }

type cyclicTUCfg struct {
	Addr cyclicTU `koanf:"addr"`
}

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

// TextUnmarshaler implementations are opaque leaves — the walker must NOT
// descend into them. cyclicTU embeds a self-reference that would trip the
// cycle guard if the walker recursed in.
func TestWalker_TextUnmarshaler_IsLeaf(t *testing.T) {
	t.Parallel()
	cfg := &cyclicTUCfg{}
	err := koanfvalidate.Struct(cfg, koanfvalidate.Options{})
	if errors.Is(err, koanfvalidate.ErrCyclicType) {
		t.Fatal("walker descended into a TextUnmarshaler type; opaque-leaf treatment regressed")
	}
	if err != nil {
		t.Fatalf("expected nil (no rules on cfg), got %v", err)
	}
}

// Rules outside the tag→sentinel table map to ErrValidation (the generic
// parent) — not to a specific category sentinel. Custom rules registered
// by the caller exercise this fallback.
func TestStruct_UnknownTag_MapsToErrValidation(t *testing.T) {
	t.Parallel()
	v := validator.New()
	v.SetTagName("koanf-validate")
	if err := v.RegisterValidation("not_in_sentinel_table", func(fl validator.FieldLevel) bool {
		return false
	}); err != nil {
		t.Fatalf("RegisterValidation: %v", err)
	}
	type cfg struct {
		X string `koanf:"x" koanf-validate:"not_in_sentinel_table"`
	}
	me := requireMultiError(t, koanfvalidate.Struct(&cfg{X: "anything"}, koanfvalidate.Options{Validator: v}))
	fe := findByPath(me, "x")
	if fe == nil {
		t.Fatalf("no FieldError at x")
	}
	if !errors.Is(fe, koanfvalidate.ErrValidation) {
		t.Error("errors.Is(fe, ErrValidation) should be true for unmapped tag")
	}
	if errors.Is(fe, koanfvalidate.ErrRequired) {
		t.Error("custom rule unmapped tag must not match ErrRequired")
	}
}

// When the walker cannot map a validator namespace to a koanf path
// (e.g. dive, maps, slice elements), the FieldError must include
// ErrPathUnresolved in its Unwrap chain so consumers can detect the
// degradation. The Path field falls back to the raw Go namespace.
func TestStruct_DegradedPath_AddsErrPathUnresolved(t *testing.T) {
	t.Parallel()
	cfg := &diveCfg{Tags: map[string]string{"k": ""}}
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	var degraded *koanfvalidate.FieldError
	for _, fe := range me.Errors {
		if errors.Is(fe, koanfvalidate.ErrPathUnresolved) {
			degraded = fe
			break
		}
	}
	if degraded == nil {
		t.Fatalf("expected a FieldError with ErrPathUnresolved; got paths %v", pathsOf(me))
	}
	// Category sentinel must still apply on the same FieldError.
	if !errors.Is(degraded, koanfvalidate.ErrRequired) {
		t.Error("errors.Is(degraded, ErrRequired) = false — category sentinel lost")
	}
	// Path falls back to the raw namespace (some kind of [key] reference).
	if !strings.Contains(degraded.Path, "Tags") {
		t.Errorf("Path: got %q, want a fallback containing Tags", degraded.Path)
	}
}

// Well-modeled paths must NOT carry ErrPathUnresolved — guard against
// adding it spuriously to non-degraded errors.
func TestStruct_NormalPath_DoesNotHaveErrPathUnresolved(t *testing.T) {
	t.Parallel()
	cfg := &simpleCfg{} // Name is required and missing
	err := koanfvalidate.Struct(cfg, koanfvalidate.Options{})
	if errors.Is(err, koanfvalidate.ErrPathUnresolved) {
		t.Error("errors.Is(err, ErrPathUnresolved) = true on a fully-mapped struct")
	}
}

// koanf:"-" on a struct field must skip not just the field but its entire
// subtree. The walker records the skip prefix; the translator drops any
// validator/v10 error whose namespace falls under that prefix, even when
// the children carry their own koanf-validate tags.
type skippedSubtreeChildren struct {
	ID    string `koanf-validate:"required"`
	Inner string `koanf-validate:"required,uuid"`
}

type withSkippedSubtree struct {
	Visible string                 `koanf:"visible" koanf-validate:"required"`
	Hidden  skippedSubtreeChildren `koanf:"-"`
}

func TestStruct_DashTagSkipsEntireSubtree(t *testing.T) {
	t.Parallel()
	cfg := &withSkippedSubtree{Visible: "ok"}
	if err := koanfvalidate.Struct(cfg, koanfvalidate.Options{}); err != nil {
		t.Fatalf("expected nil (Hidden subtree should be skipped including required children), got %v", err)
	}
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

func TestStruct_ValidatePath_AlwaysRelativeEvenWithDelim(t *testing.T) {
	t.Parallel()
	cfg := &alwaysRelativeCfg{}
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	if findByPath(me, "server.child.field") == nil {
		t.Fatalf("expected server.child.field (relative rebase), got %v", pathsOf(me))
	}
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

// Options.Delim threads through walker path joining, validator-error
// translation, and Validate()-error rebasing. Using "/" produces koanf
// paths like "server/port" everywhere — including when a Validate()
// method emits an already-absolute path.
type slashDelimCfg struct {
	Server struct {
		Port int `koanf:"port" koanf-validate:"required,min=1"`
	} `koanf:"server"`
}

func TestStruct_CustomDelim_RoutesThroughEverything(t *testing.T) {
	t.Parallel()
	cfg := &slashDelimCfg{}
	err := koanfvalidate.Struct(cfg, koanfvalidate.Options{Delim: "/"})
	me := requireMultiError(t, err)
	if fe := findByPath(me, "server/port"); fe == nil {
		t.Fatalf("expected path 'server/port', got %v", pathsOf(me))
	}
}

// A nil *struct field whose type implements Validate() must NOT have
// Validate() called on a synthetic zero value. The library walks the schema
// at type-level for path mapping, but skips visitor execution when the
// user's pointer is nil — the sub-config does not exist.
type instrumentedValidate struct {
	X int `koanf:"x"`
}

// validateCalled tracks invocation of instrumentedValidate.Validate. Mutated
// only from a t.Cleanup-guarded test that resets it; not concurrency-safe
// by design.
var validateCalled bool

func (o *instrumentedValidate) Validate() error {
	validateCalled = true
	return nil
}

type withOptionalSubCfg struct {
	Server *instrumentedValidate `koanf:"server"`
}

func TestStruct_NilPointerStructValidator_IsSkipped(t *testing.T) {
	// Not parallel — toggles a package-level flag observed by the assertion.
	t.Cleanup(func() { validateCalled = false })
	validateCalled = false

	cfg := &withOptionalSubCfg{Server: nil}
	if err := koanfvalidate.Struct(cfg, koanfvalidate.Options{}); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if validateCalled {
		t.Error("Validate() was called on a nil *struct field — must be skipped")
	}
}

// Sanity: a non-nil pointer must still trigger Validate().
func TestStruct_NonNilPointerStructValidator_IsCalled(t *testing.T) {
	t.Cleanup(func() { validateCalled = false })
	validateCalled = false

	cfg := &withOptionalSubCfg{Server: &instrumentedValidate{X: 1}}
	if err := koanfvalidate.Struct(cfg, koanfvalidate.Options{}); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !validateCalled {
		t.Error("Validate() was not called on a non-nil *struct field")
	}
}

// Two siblings claiming the same koanf segment is a developer error — they
// silently aliased the same config key. The walker must detect this and
// return ErrInvalidConfig instead of letting the path map be quietly
// overwritten.
type siblingCollisionCfg struct {
	A string `koanf:"host" koanf-validate:"required"`
	B string `koanf:"host" koanf-validate:"required"`
}

func TestStruct_SiblingTagCollision_ReturnsErrInvalidConfig(t *testing.T) {
	t.Parallel()
	err := koanfvalidate.Struct(&siblingCollisionCfg{}, koanfvalidate.Options{})
	if !errors.Is(err, koanfvalidate.ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}

// Anonymous-embedded structs squash up into the parent namespace, so a tag
// in the parent that collides with one in the embedded struct must also be
// detected.
type embeddedCollisionBase struct {
	ID string `koanf:"id" koanf-validate:"required"`
}
type embeddedCollisionCfg struct {
	embeddedCollisionBase
	ID string `koanf:"id" koanf-validate:"required"` // collides with embedded
}

func TestStruct_SquashedEmbeddedCollision_ReturnsErrInvalidConfig(t *testing.T) {
	t.Parallel()
	err := koanfvalidate.Struct(&embeddedCollisionCfg{}, koanfvalidate.Options{})
	if !errors.Is(err, koanfvalidate.ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig from embedded-squash collision, got %v", err)
	}
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

// Custom rule fixture
type customRuleCfg struct {
	Port int `koanf:"port" koanf-validate:"required,company_port"`
}

// Secret-safe fixture
type secretCfg struct {
	Password string `koanf:"password" koanf-validate:"required,min=16"`
}

// =============================================================================
// Helpers
// =============================================================================

// requireMultiError asserts err is a *MultiError and returns it for inspection.
func requireMultiError(t *testing.T, err error) *koanfvalidate.MultiError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	var me *koanfvalidate.MultiError
	if !errors.As(err, &me) {
		t.Fatalf("expected *MultiError, got %T: %v", err, err)
	}
	return me
}

// pathsOf returns the sorted list of FieldError.Path values for inspection.
func pathsOf(me *koanfvalidate.MultiError) []string {
	out := make([]string, len(me.Errors))
	for i, fe := range me.Errors {
		out[i] = fe.Path
	}
	sort.Strings(out)
	return out
}

// findByPath returns the first FieldError matching path, or nil.
func findByPath(me *koanfvalidate.MultiError, path string) *koanfvalidate.FieldError {
	for _, fe := range me.Errors {
		if fe.Path == path {
			return fe
		}
	}
	return nil
}

// =============================================================================
// Category 1: Input validation
// =============================================================================

func TestStruct_InputValidation(t *testing.T) {
	cases := []struct {
		name  string
		input any
	}{
		{"nil interface", nil},
		{"non-pointer struct", simpleCfg{Name: "x"}},
		{"nil pointer to struct", (*simpleCfg)(nil)},
		{"pointer to non-struct", new(int)},
		{"pointer to slice", &[]string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := koanfvalidate.Struct(tc.input, koanfvalidate.Options{})
			if !errors.Is(err, koanfvalidate.ErrInvalidInput) {
				t.Fatalf("got %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestStruct_ValidStructNoErrors(t *testing.T) {
	t.Parallel()
	cfg := &simpleCfg{Name: "alice", Age: 30}
	if err := koanfvalidate.Struct(cfg, koanfvalidate.Options{}); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// =============================================================================
// Category 2: Path translation
// =============================================================================

func TestStruct_PathTranslation_Flat(t *testing.T) {
	t.Parallel()
	cfg := &simpleCfg{} // Name empty, Age 0 (valid for min=0)
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	got := pathsOf(me)
	want := []string{"name"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths: got %v, want %v", got, want)
	}
}

func TestStruct_PathTranslation_Nested(t *testing.T) {
	t.Parallel()
	cfg := &nestedCfg{} // every required field empty
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	got := pathsOf(me)
	want := []string{
		"log_level",
		"server.host",
		"server.min_port",
		"server.port",
		"server.timeout",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths: got %v, want %v", got, want)
	}
}

func TestStruct_PathTranslation_DashTagSkipsField(t *testing.T) {
	t.Parallel()
	cfg := &dashTagCfg{Visible: "ok", Hidden: ""} // Hidden is required but tag says skip
	if err := koanfvalidate.Struct(cfg, koanfvalidate.Options{}); err != nil {
		t.Fatalf("expected nil (Hidden should be skipped), got %v", err)
	}
}

func TestStruct_PathTranslation_AnonymousEmbeddedSquashed(t *testing.T) {
	t.Parallel()
	cfg := &embeddedCfg{} // ID and Specific both empty
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	got := pathsOf(me)
	want := []string{"id", "specific"} // not "embeddedBase.id"
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths: got %v, want %v", got, want)
	}
}

func TestStruct_PathTranslation_CustomPathTag(t *testing.T) {
	t.Parallel()
	cfg := &customPathTagCfg{} // Name empty
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{PathTag: "mapstructure"}))
	got := pathsOf(me)
	want := []string{"renamed"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths: got %v, want %v", got, want)
	}
}

// =============================================================================
// Category 3: Rule → sentinel mapping
// =============================================================================

func TestStruct_SentinelMapping(t *testing.T) {
	cases := []struct {
		name         string
		factory      func() any
		path         string
		wantTag      string
		wantSentinel error
	}{
		{
			"required",
			func() any {
				return &struct {
					X string `koanf:"x" koanf-validate:"required"`
				}{}
			},
			"x", "required", koanfvalidate.ErrRequired,
		},
		{
			"min",
			func() any {
				return &struct {
					X int `koanf:"x" koanf-validate:"min=10"`
				}{X: 5}
			},
			"x", "min", koanfvalidate.ErrOutOfRange,
		},
		{
			"max",
			func() any {
				return &struct {
					X int `koanf:"x" koanf-validate:"max=10"`
				}{X: 20}
			},
			"x", "max", koanfvalidate.ErrOutOfRange,
		},
		{
			"gte",
			func() any {
				return &struct {
					X int `koanf:"x" koanf-validate:"gte=10"`
				}{X: 5}
			},
			"x", "gte", koanfvalidate.ErrOutOfRange,
		},
		{
			"lte",
			func() any {
				return &struct {
					X int `koanf:"x" koanf-validate:"lte=10"`
				}{X: 20}
			},
			"x", "lte", koanfvalidate.ErrOutOfRange,
		},
		{
			"len",
			func() any {
				return &struct {
					X string `koanf:"x" koanf-validate:"len=5"`
				}{X: "ab"}
			},
			"x", "len", koanfvalidate.ErrOutOfRange,
		},
		{
			"oneof",
			func() any {
				return &struct {
					X string `koanf:"x" koanf-validate:"oneof=a b c"`
				}{X: "z"}
			},
			"x", "oneof", koanfvalidate.ErrNotInSet,
		},
		{
			"email",
			func() any {
				return &struct {
					X string `koanf:"x" koanf-validate:"email"`
				}{X: "not-an-email"}
			},
			"x", "email", koanfvalidate.ErrBadFormat,
		},
		{
			"url",
			func() any {
				return &struct {
					X string `koanf:"x" koanf-validate:"url"`
				}{X: "not a url"}
			},
			"x", "url", koanfvalidate.ErrBadFormat,
		},
		{
			"uuid",
			func() any {
				return &struct {
					X string `koanf:"x" koanf-validate:"uuid"`
				}{X: "not-a-uuid"}
			},
			"x", "uuid", koanfvalidate.ErrBadFormat,
		},
		{
			"hostname",
			func() any {
				return &struct {
					X string `koanf:"x" koanf-validate:"hostname"`
				}{X: "no spaces allowed"}
			},
			"x", "hostname", koanfvalidate.ErrBadFormat,
		},
		{
			"gtefield",
			func() any {
				return &struct {
					A int `koanf:"a" koanf-validate:"gtefield=B"`
					B int `koanf:"b"`
				}{A: 1, B: 10}
			},
			"a", "gtefield", koanfvalidate.ErrFieldMismatch,
		},
		{
			"eqfield",
			func() any {
				return &struct {
					A int `koanf:"a" koanf-validate:"eqfield=B"`
					B int `koanf:"b"`
				}{A: 1, B: 2}
			},
			"a", "eqfield", koanfvalidate.ErrFieldMismatch,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			me := requireMultiError(t, koanfvalidate.Struct(tc.factory(), koanfvalidate.Options{}))
			fe := findByPath(me, tc.path)
			if fe == nil {
				t.Fatalf("no FieldError at path %q; got paths %v", tc.path, pathsOf(me))
			}
			if fe.Tag != tc.wantTag {
				t.Errorf("Tag: got %q, want %q", fe.Tag, tc.wantTag)
			}
			if !errors.Is(fe, tc.wantSentinel) {
				t.Errorf("sentinel: errors.Is(fe, %v) = false", tc.wantSentinel)
			}
			// errors.As must reach the underlying validator.FieldError for tag rules.
			var vfe validator.FieldError
			if !errors.As(fe, &vfe) {
				t.Errorf("errors.As to validator.FieldError failed")
			}
		})
	}
}

// =============================================================================
// Category 4: Cross-field Param translation
// =============================================================================

func TestStruct_CrossFieldParamTranslation(t *testing.T) {
	t.Parallel()
	type cfg struct {
		Server struct {
			Port    int `koanf:"port"     koanf-validate:"gtefield=MinPort"`
			MinPort int `koanf:"min_port"`
		} `koanf:"server"`
	}
	c := &cfg{}
	c.Server.Port = 1
	c.Server.MinPort = 10
	me := requireMultiError(t, koanfvalidate.Struct(c, koanfvalidate.Options{}))
	fe := findByPath(me, "server.port")
	if fe == nil {
		t.Fatalf("no FieldError at server.port; paths=%v", pathsOf(me))
	}
	if fe.Tag != "gtefield" {
		t.Fatalf("Tag: got %q, want gtefield", fe.Tag)
	}
	if fe.Param != "server.min_port" {
		t.Errorf("Param: got %q, want server.min_port", fe.Param)
	}
	if fe.RawParam != "MinPort" {
		t.Errorf("RawParam: got %q, want MinPort", fe.RawParam)
	}
}

func TestStruct_LiteralParamUntranslated(t *testing.T) {
	t.Parallel()
	c := &struct {
		X int `koanf:"x" koanf-validate:"min=10"`
	}{X: 5}
	me := requireMultiError(t, koanfvalidate.Struct(c, koanfvalidate.Options{}))
	fe := findByPath(me, "x")
	if fe == nil {
		t.Fatalf("no FieldError at x")
	}
	if fe.Param != "10" {
		t.Errorf("Param: got %q, want 10", fe.Param)
	}
	if fe.RawParam != "10" {
		t.Errorf("RawParam: got %q, want 10", fe.RawParam)
	}
}

// =============================================================================
// Category 5: Validate() method auto-discovery
// =============================================================================

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

// =============================================================================
// Category 6: Options.Validator passthrough
// =============================================================================

func TestStruct_CustomValidatorRule(t *testing.T) {
	t.Parallel()
	v := validator.New()
	v.SetTagName("koanf-validate") // caller-supplied validators must be pre-configured
	if err := v.RegisterValidation("company_port", func(fl validator.FieldLevel) bool {
		p := fl.Field().Int()
		return p >= 8000 && p <= 9000
	}); err != nil {
		t.Fatalf("RegisterValidation: %v", err)
	}
	cfg := &customRuleCfg{Port: 80}
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{Validator: v}))
	fe := findByPath(me, "port")
	if fe == nil || fe.Tag != "company_port" {
		t.Fatalf("expected company_port at port; got %v", pathsOf(me))
	}
}

// =============================================================================
// Category 7: Cycle guard
// =============================================================================

func TestStruct_CycleGuard_SelfReference(t *testing.T) {
	t.Parallel()
	cfg := &cycleNode{Name: "ok"}
	err := koanfvalidate.Struct(cfg, koanfvalidate.Options{})
	if !errors.Is(err, koanfvalidate.ErrCyclicType) {
		t.Fatalf("got %v, want ErrCyclicType", err)
	}
}

func TestStruct_CycleGuard_MutualRecursion(t *testing.T) {
	t.Parallel()
	cfg := &mutualA{}
	err := koanfvalidate.Struct(cfg, koanfvalidate.Options{})
	if !errors.Is(err, koanfvalidate.ErrCyclicType) {
		t.Fatalf("got %v, want ErrCyclicType", err)
	}
}

// =============================================================================
// Category 8: Secret safety (IncludeValues)
// =============================================================================

func TestStruct_IncludeValues_DefaultOff(t *testing.T) {
	t.Parallel()
	cfg := &secretCfg{Password: "short"}
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	fe := findByPath(me, "password")
	if fe == nil {
		t.Fatalf("no FieldError at password")
	}
	if fe.Value != nil {
		t.Errorf("Value: got %v, want nil (secrets must not leak by default)", fe.Value)
	}
}

// When IncludeValues=false (the default), the validator.FieldError reachable
// via the cause chain must not leak the failing value via .Value(). Without
// redaction, errors.As(fe, &validator.FieldError) returns the live vfe whose
// .Value() exposes the secret — bypassing the documented secret-safety
// promise.
func TestStruct_SecretSafety_ValidatorCauseIsRedacted(t *testing.T) {
	t.Parallel()
	const secret = "hunter2" // 7 chars, fails min=16
	cfg := &secretCfg{Password: secret}
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	fe := findByPath(me, "password")
	if fe == nil {
		t.Fatalf("no FieldError at password")
	}

	// High-level redaction.
	if fe.Value != nil {
		t.Errorf("FieldError.Value: got %v, want nil", fe.Value)
	}

	// Cause-chain redaction — the bypass vector.
	var vfe validator.FieldError
	if !errors.As(fe, &vfe) {
		t.Fatal("errors.As to validator.FieldError failed — cause chain broken")
	}
	if v := vfe.Value(); v != nil {
		t.Errorf("validator.FieldError.Value() through cause chain: got %v, want nil — secret leaked", v)
	}
	// Sanity-check the other methods still delegate (must not redact tag/namespace/etc).
	if vfe.Tag() != "min" {
		t.Errorf("delegated Tag: got %q, want min", vfe.Tag())
	}
	if vfe.Param() != "16" {
		t.Errorf("delegated Param: got %q, want 16", vfe.Param())
	}
}

// Opt-in path must still expose the real value through both surfaces.
func TestStruct_SecretSafety_OptInExposesValueEverywhere(t *testing.T) {
	t.Parallel()
	const v = "short" // fails min=16
	cfg := &secretCfg{Password: v}
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{IncludeValues: true}))
	fe := findByPath(me, "password")
	if fe.Value != v {
		t.Errorf("FieldError.Value: got %v, want %q", fe.Value, v)
	}
	var vfe validator.FieldError
	if !errors.As(fe, &vfe) {
		t.Fatal("errors.As failed")
	}
	if got := vfe.Value(); got != v {
		t.Errorf("validator.FieldError.Value() through cause chain: got %v, want %q", got, v)
	}
}

func TestStruct_IncludeValues_Opted(t *testing.T) {
	t.Parallel()
	cfg := &secretCfg{Password: "short"}
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{IncludeValues: true}))
	fe := findByPath(me, "password")
	if fe == nil {
		t.Fatalf("no FieldError at password")
	}
	if fe.Value != "short" {
		t.Errorf("Value: got %v, want %q", fe.Value, "short")
	}
}

// =============================================================================
// Category 9: Determinism
// =============================================================================

func TestStruct_DeterministicOrdering(t *testing.T) {
	t.Parallel()
	cfg := &nestedCfg{}
	const runs = 20
	var first []string
	for i := range runs {
		me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
		this := make([]string, len(me.Errors))
		for j, fe := range me.Errors {
			this[j] = fe.Path + ":" + fe.Tag
		}
		if i == 0 {
			first = this
			continue
		}
		if !reflect.DeepEqual(first, this) {
			t.Fatalf("run %d differs:\n  first: %v\n  got:   %v", i, first, this)
		}
	}
}

func TestStruct_ConcurrentSafety(t *testing.T) {
	t.Parallel()
	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			cfg := &nestedCfg{}
			_ = koanfvalidate.Struct(cfg, koanfvalidate.Options{})
		}()
	}
	wg.Wait()
}

// A shared *validator.Validate passed via Options.Validator must be race-free
// under concurrent Struct() calls. The library must not mutate the caller's
// validator (no SetTagName, no RegisterValidation, no pool tweaking) — those
// mutations would race against any goroutine concurrently calling
// val.Struct(...) on the same instance.
func TestStruct_ConcurrentSafety_SharedValidator(t *testing.T) {
	t.Parallel()
	v := validator.New()
	v.SetTagName("koanf-validate")
	if err := v.RegisterValidation("company_port", func(fl validator.FieldLevel) bool {
		p := fl.Field().Int()
		return p >= 8000 && p <= 9000
	}); err != nil {
		t.Fatalf("RegisterValidation: %v", err)
	}

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			cfg := &customRuleCfg{Port: 8500}
			_ = koanfvalidate.Struct(cfg, koanfvalidate.Options{Validator: v})
		}()
	}
	wg.Wait()
}

// =============================================================================
// Category 10: Error rendering
// =============================================================================

func TestFieldError_RenderTerseFormat(t *testing.T) {
	t.Parallel()
	cases := []struct {
		fe   *koanfvalidate.FieldError
		want string
	}{
		{&koanfvalidate.FieldError{Path: "x", Tag: "required"}, "x: required"},
		{&koanfvalidate.FieldError{Path: "x", Tag: "min", Param: "10"}, "x: min(10)"},
		{&koanfvalidate.FieldError{Path: "server.port", Tag: "gtefield", Param: "server.min_port"}, "server.port: gtefield(server.min_port)"},
	}
	for _, tc := range cases {
		if got := tc.fe.Error(); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

func TestMultiError_RenderShowsAllErrors(t *testing.T) {
	t.Parallel()
	cfg := &simpleCfg{} // Name required, Age min=0 satisfied
	err := koanfvalidate.Struct(cfg, koanfvalidate.Options{})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "name: required") {
		t.Errorf("rendered message missing 'name: required':\n%s", msg)
	}
}

// =============================================================================
// Category 11: Sentinel traversal via errors.Is on MultiError
// =============================================================================

// Covers the documented errors.Is(koanfvalidate.Struct(...), Sentinel)
// contract. MultiError.Unwrap returns []*FieldError; each *FieldError unwraps
// to {sentinel, cause}. errors.Is must walk both layers to reach the sentinel.
func TestMultiError_ErrorsIs_TraversesToSentinels(t *testing.T) {
	t.Parallel()
	type cfg struct {
		Name  string `koanf:"name"  koanf-validate:"required"`    // ErrRequired
		Port  int    `koanf:"port"  koanf-validate:"min=1"`       // ErrOutOfRange
		Mode  string `koanf:"mode"  koanf-validate:"oneof=a b c"` // ErrNotInSet
		Email string `koanf:"email" koanf-validate:"email"`       // ErrBadFormat
	}
	err := koanfvalidate.Struct(&cfg{Email: "not-an-email"}, koanfvalidate.Options{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	cases := []struct {
		name     string
		sentinel error
		want     bool
	}{
		{"required matches", koanfvalidate.ErrRequired, true},
		{"out_of_range matches", koanfvalidate.ErrOutOfRange, true},
		{"not_in_set matches", koanfvalidate.ErrNotInSet, true},
		{"bad_format matches", koanfvalidate.ErrBadFormat, true},
		{"field_mismatch absent", koanfvalidate.ErrFieldMismatch, false},
		{"invariant absent", koanfvalidate.ErrInvariant, false},
		{"cyclic_type absent", koanfvalidate.ErrCyclicType, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := errors.Is(err, tc.sentinel); got != tc.want {
				t.Errorf("errors.Is(err, %v) = %v, want %v", tc.sentinel, got, tc.want)
			}
		})
	}
}

// =============================================================================
// Example for pkg.go.dev
// =============================================================================

func Example() {
	type Config struct {
		Server struct {
			Host string `koanf:"host" koanf-validate:"required,hostname"`
			Port int    `koanf:"port" koanf-validate:"required,min=1,max=65535"`
		} `koanf:"server"`
	}

	cfg := &Config{}
	cfg.Server.Host = "not a host name"
	cfg.Server.Port = 70000

	err := koanfvalidate.Struct(cfg, koanfvalidate.Options{})
	fmt.Println(err)

	// Output:
	// koanfvalidate: 2 validation error(s)
	//   - server.host: hostname
	//   - server.port: max(65535)
}
