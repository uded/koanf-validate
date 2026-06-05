package koanfvalidate_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	koanfvalidate "github.com/uded/koanf-validate"
)

// =============================================================================
// slog.LogValuer — structured rendering
// =============================================================================

// FieldError.LogValue must surface every structured attribute (path, tag,
// param, raw_param, value, cause) so a slog handler emits them as typed
// JSON fields rather than the Error() string.
func TestFieldError_LogValue_RendersStructuredAttrs(t *testing.T) {
	t.Parallel()
	cfg := &simpleCfg{} // Name required, Age min=0 ok
	me := requireMultiError(t, koanfvalidate.Struct(cfg, koanfvalidate.Options{}))
	fe := findByPath(me, "name")
	if fe == nil {
		t.Fatalf("no FieldError at name")
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("validation failed", "err", fe)

	var line map[string]any
	if err := json.Unmarshal(buf.Bytes(), &line); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, buf.String())
	}
	errAttr, ok := line["err"].(map[string]any)
	if !ok {
		t.Fatalf("expected err to be a group; got %T: %v", line["err"], line["err"])
	}
	if errAttr["path"] != "name" {
		t.Errorf("path: got %v, want name", errAttr["path"])
	}
	if errAttr["tag"] != "required" {
		t.Errorf("tag: got %v, want required", errAttr["tag"])
	}
	if _, hasValue := errAttr["value"]; hasValue {
		t.Errorf("value attribute must be absent when IncludeValues=false; got %v", errAttr["value"])
	}
}

// MultiError.LogValue renders as {count, errors:[…]} with each FieldError
// using its own structured attributes.
func TestMultiError_LogValue_RendersGroup(t *testing.T) {
	t.Parallel()
	cfg := &simpleCfg{}
	err := koanfvalidate.Struct(cfg, koanfvalidate.Options{})

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("config rejected", "result", err)

	var line map[string]any
	if jerr := json.Unmarshal(buf.Bytes(), &line); jerr != nil {
		t.Fatalf("not valid JSON: %v", jerr)
	}
	result, ok := line["result"].(map[string]any)
	if !ok {
		t.Fatalf("result not a group: %T", line["result"])
	}
	if got, want := result["count"], float64(1); got != want {
		t.Errorf("count: got %v, want %v", got, want)
	}
	errs, ok := result["errors"].([]any)
	if !ok || len(errs) != 1 {
		t.Fatalf("errors not a 1-element array: %v", result["errors"])
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
