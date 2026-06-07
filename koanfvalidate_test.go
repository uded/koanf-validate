package koanfvalidate_test

import (
	"errors"
	"fmt"
	"reflect"
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

// Custom rule fixture
type customRuleCfg struct {
	Port int `koanf:"port" koanf-validate:"required,company_port"`
}

// =============================================================================
// Category 1: Input validation
// =============================================================================

func TestStruct_OptionsValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		opts koanfvalidate.Options
	}{
		{"PathTag equals ValidateTag", koanfvalidate.Options{PathTag: "same", ValidateTag: "same"}},
		{"PathTag contains comma", koanfvalidate.Options{PathTag: "ko,nf"}},
		{"PathTag contains whitespace", koanfvalidate.Options{PathTag: "koanf tag"}},
		{"PathTag contains quote", koanfvalidate.Options{PathTag: `bad"tag`}},
		{"ValidateTag contains comma", koanfvalidate.Options{ValidateTag: "vali,date"}},
		{"ValidateTag contains whitespace", koanfvalidate.Options{ValidateTag: "vali date"}},
		{"ValidateTag contains quote", koanfvalidate.Options{ValidateTag: `vali"date`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := koanfvalidate.Struct(&simpleCfg{Name: "x"}, tc.opts)
			if !errors.Is(err, koanfvalidate.ErrInvalidConfig) {
				t.Fatalf("expected ErrInvalidConfig, got %v", err)
			}
		})
	}
}

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
