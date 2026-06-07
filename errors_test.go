package koanfvalidate_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	koanfvalidate "github.com/uded/koanf-validate"
)

// =============================================================================
// encoding/json — structured serialization
// =============================================================================

// FieldError.MarshalJSON must emit snake_case structured attributes (path,
// tag, param, raw_param, value, cause, path_unresolved) so consumers can
// feed validation errors into any structured pipeline — log frameworks,
// audit trails, HTTP APIs — without writing a wrapper themselves. The
// library deliberately does not adapt to any specific logging framework.
func TestFieldError_MarshalJSON_StructuredShape(t *testing.T) {
	t.Parallel()
	cfg := &simpleCfg{} // Name required, Age min=0 ok
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	fe := findByPath(me, "name")
	if fe == nil {
		t.Fatalf("no FieldError at name")
	}

	raw, err := json.Marshal(fe)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, raw)
	}
	if out["path"] != "name" {
		t.Errorf("path: got %v, want name", out["path"])
	}
	if out["tag"] != "required" {
		t.Errorf("tag: got %v, want required", out["tag"])
	}
	if _, hasValue := out["value"]; hasValue {
		t.Errorf("value attribute must be absent when IncludeValues=false; got %v", out["value"])
	}
	// Confirm we're producing snake_case via MarshalJSON, not Pascal-case
	// via struct-default JSON encoding.
	if _, pascal := out["Path"]; pascal {
		t.Errorf("emitted Pascal-case 'Path' — MarshalJSON not invoked")
	}
}

// MultiError.MarshalJSON renders as {count, errors:[…]} with each child
// going through FieldError.MarshalJSON. This is the regression guard for
// the bug where a parent serializer bypassed per-element resolution.
func TestMultiError_MarshalJSON_StructuredShape(t *testing.T) {
	t.Parallel()
	cfg := &simpleCfg{}
	err := koanfvalidate.Struct(cfg, koanfvalidate.Options{})

	raw, jerr := json.Marshal(err)
	if jerr != nil {
		t.Fatalf("Marshal: %v", jerr)
	}
	var out map[string]any
	if uerr := json.Unmarshal(raw, &out); uerr != nil {
		t.Fatalf("not valid JSON: %v", uerr)
	}
	if got, want := out["count"], float64(1); got != want {
		t.Errorf("count: got %v, want %v", got, want)
	}
	errs, ok := out["errors"].([]any)
	if !ok || len(errs) != 1 {
		t.Fatalf("errors not a 1-element array: %v", out["errors"])
	}
	child, ok := errs[0].(map[string]any)
	if !ok {
		t.Fatalf("child is %T, not a map — per-element MarshalJSON bypassed", errs[0])
	}
	if child["path"] != "name" {
		t.Errorf("child path: got %v, want name", child["path"])
	}
	if _, pascal := child["Path"]; pascal {
		t.Errorf("child emitted Pascal-case 'Path' — per-element MarshalJSON not invoked")
	}
}

// =============================================================================
// Error() rendering — terse human-readable format
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

// A *FieldError constructed directly by user code (e.g. inside a
// Validate() method, before it goes through the library's flattening
// pipeline) has its internal sentinel field unset. Unwrap MUST substitute
// ErrInvariant so errors.Is still reaches a meaningful category — the
// public Unwrap godoc promises this contract regardless of construction
// path.
func TestFieldError_UserConstructed_UnwrapHasInvariantSentinel(t *testing.T) {
	t.Parallel()
	fe := &koanfvalidate.FieldError{Path: "x", Tag: "custom"}
	if !errors.Is(fe, koanfvalidate.ErrInvariant) {
		t.Errorf("errors.Is(user-constructed FieldError, ErrInvariant) = false; expected true")
	}
	// Negative control — must not match an unrelated sentinel.
	if errors.Is(fe, koanfvalidate.ErrRequired) {
		t.Errorf("errors.Is(user-constructed FieldError, ErrRequired) = true; expected false")
	}
}

// =============================================================================
// Sentinel traversal via errors.Is on MultiError
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
