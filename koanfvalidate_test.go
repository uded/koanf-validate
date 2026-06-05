package koanfvalidate_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
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

// Custom rule fixture
type customRuleCfg struct {
	Port int `koanf:"port" koanf-validate:"required,company_port"`
}

// Secret-safe fixture
type secretCfg struct {
	Password string `koanf:"password" koanf-validate:"required,min=16"`
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
