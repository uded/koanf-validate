package koanfvalidate_test

import (
	"errors"
	"testing"

	"github.com/go-playground/validator/v10"

	koanfvalidate "github.com/uded/koanf-validate"
)

// Secret-safe fixture
type secretCfg struct {
	Password string `koanf:"password" koanf-validate:"required,min=16"`
}

// =============================================================================
// IncludeValues / cause-chain redaction contract
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
