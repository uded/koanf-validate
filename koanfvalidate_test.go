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
