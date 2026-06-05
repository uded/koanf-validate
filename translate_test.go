package koanfvalidate_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-playground/validator/v10"

	koanfvalidate "github.com/uded/koanf-validate"
)

// =============================================================================
// Fixtures — used only by translator-layer tests
// =============================================================================

// Triggers validator/v10's dive rule, whose namespaces (e.g. "diveCfg.Tags[key]")
// fall outside the walker's path map. Used to assert that the resulting
// FieldError exposes ErrPathUnresolved through its Unwrap chain.
type diveCfg struct {
	Tags map[string]string `koanf:"tags" koanf-validate:"dive,required"`
}

// =============================================================================
// Tag → sentinel mapping
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

// =============================================================================
// Path resolution — degraded vs normal
// =============================================================================

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

// =============================================================================
// Cross-field Param translation
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
