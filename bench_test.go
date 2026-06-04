package koanfvalidate_test

import (
	"testing"
	"time"

	"github.com/go-playground/validator/v10"

	koanfvalidate "github.com/uded/koanf-validate"
)

// Benchmarks demonstrate the caching boundary lines:
//   - DefaultPath: nil Validator + default ValidateTag — hits both the
//     sync.Once default-validator cache and the walker sync.Map cache.
//   - CustomValidator: caller-supplied *validator.Validate — bypasses the
//     default-validator cache but still hits the walker cache.
//   - NonDefaultTag: forces a fresh validator.New() per call — measures the
//     cold path where neither cache helps.
//   - DeepNested / FailingValidation: stress paths that exercise the
//     visitor recipes and error-translation routines respectively.

type benchNested struct {
	Server struct {
		Host    string        `koanf:"host"     koanf-validate:"required,hostname"`
		Port    int           `koanf:"port"     koanf-validate:"required,min=1,max=65535"`
		MinPort int           `koanf:"min_port" koanf-validate:"required,ltefield=Port"`
		Timeout time.Duration `koanf:"timeout"  koanf-validate:"required"`
	} `koanf:"server"`
	LogLevel string `koanf:"log_level" koanf-validate:"required,oneof=debug info warn error"`
}

func newValidNested() *benchNested {
	b := &benchNested{}
	b.Server.Host = "example.com"
	b.Server.Port = 8080
	b.Server.MinPort = 80
	b.Server.Timeout = time.Second
	b.LogLevel = "info"
	return b
}

// BenchmarkStruct_DefaultPath measures the hot path used by every caller
// who passes Options{} — both caches active.
func BenchmarkStruct_DefaultPath(b *testing.B) {
	cfg := newValidNested()
	b.ReportAllocs()
	for b.Loop() {
		if err := koanfvalidate.Struct(cfg, koanfvalidate.Options{}); err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}

// BenchmarkStruct_CustomValidator measures the caller-shared-validator
// path: skip the default-validator cache, keep the walker cache.
func BenchmarkStruct_CustomValidator(b *testing.B) {
	v := validator.New()
	v.SetTagName("koanf-validate")
	cfg := newValidNested()
	opts := koanfvalidate.Options{Validator: v}
	b.ReportAllocs()
	for b.Loop() {
		if err := koanfvalidate.Struct(cfg, opts); err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}

// BenchmarkStruct_NonDefaultTag forces a fresh validator.New() inside the
// library on every call (the default-validator cache only applies when
// ValidateTag matches the default). Walker cache still hits because the
// (rootType, pathTag, delim) tuple is constant across iterations.
func BenchmarkStruct_NonDefaultTag(b *testing.B) {
	type cfgT struct {
		X int `koanf:"x" custom-validate:"required,min=1"`
	}
	cfg := &cfgT{X: 5}
	opts := koanfvalidate.Options{ValidateTag: "custom-validate"}
	b.ReportAllocs()
	for b.Loop() {
		if err := koanfvalidate.Struct(cfg, opts); err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}

// BenchmarkStruct_FailingValidation measures the error path: tag rules
// fail on every iteration; the translator allocates a *FieldError per
// failure plus the wrapping *MultiError.
func BenchmarkStruct_FailingValidation(b *testing.B) {
	cfg := &benchNested{} // every required field empty
	b.ReportAllocs()
	for b.Loop() {
		if err := koanfvalidate.Struct(cfg, koanfvalidate.Options{}); err == nil {
			b.Fatal("expected error, got nil")
		}
	}
}

// BenchmarkStruct_DeepNested measures path-map cache amortisation on a
// wider struct (5 nested levels × 4 fields each), the realistic shape of
// a service config tree.
func BenchmarkStruct_DeepNested(b *testing.B) {
	type leaf struct {
		A string `koanf:"a" koanf-validate:"required"`
		B string `koanf:"b" koanf-validate:"required"`
		C string `koanf:"c" koanf-validate:"required"`
		D string `koanf:"d" koanf-validate:"required"`
	}
	type l4 struct {
		Leaf leaf `koanf:"leaf"`
	}
	type l3 struct {
		Down l4 `koanf:"down"`
	}
	type l2 struct {
		Down l3 `koanf:"down"`
	}
	type l1 struct {
		Down l2 `koanf:"down"`
	}
	type root struct {
		Down l1 `koanf:"down"`
	}

	cfg := &root{}
	cfg.Down.Down.Down.Down.Leaf = leaf{A: "x", B: "y", C: "z", D: "w"}
	b.ReportAllocs()
	for b.Loop() {
		if err := koanfvalidate.Struct(cfg, koanfvalidate.Options{}); err != nil {
			b.Fatalf("unexpected error: %v", err)
		}
	}
}
