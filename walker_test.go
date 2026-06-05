package koanfvalidate_test

import (
	"errors"
	"reflect"
	"testing"

	koanfvalidate "github.com/uded/koanf-validate"
)

// =============================================================================
// Fixtures — used only by walker-shape tests
// =============================================================================

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

// Options.Delim threads through walker path joining, validator-error
// translation, and Validate()-error rebasing. Using "/" produces koanf
// paths like "server/port" everywhere — including when a Validate()
// method emits an already-absolute path.
type slashDelimCfg struct {
	Server struct {
		Port int `koanf:"port" koanf-validate:"required,min=1"`
	} `koanf:"server"`
}

// Two siblings claiming the same koanf segment is a developer error — they
// silently aliased the same config key. The walker must detect this and
// return ErrInvalidConfig instead of letting the path map be quietly
// overwritten.
type siblingCollisionCfg struct {
	A string `koanf:"host" koanf-validate:"required"`
	B string `koanf:"host" koanf-validate:"required"`
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

// =============================================================================
// Walker behavior — leaf treatment, skipping, collisions, depth, cycles
// =============================================================================

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

func TestStruct_DashTagSkipsEntireSubtree(t *testing.T) {
	t.Parallel()
	cfg := &withSkippedSubtree{Visible: "ok"}
	if err := koanfvalidate.Struct(cfg, koanfvalidate.Options{}); err != nil {
		t.Fatalf("expected nil (Hidden subtree should be skipped including required children), got %v", err)
	}
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

// A zero-valued Options must resolve PathTag → "koanf", ValidateTag →
// "koanf-validate", and Delim → ".". The fixture is the same one used by
// the custom-delim test so any drift in default-resolution surfaces here
// as the dot-delimited counterpart. All three defaults are asserted by
// observable behavior — never through reflection or package-level
// constants — because the defaults are an internal implementation detail
// that consumers only see through validation results.
func TestStruct_ZeroOptions_AppliesAllDefaults(t *testing.T) {
	t.Parallel()
	cfg := &slashDelimCfg{}
	err := koanfvalidate.Struct(cfg, koanfvalidate.Options{})
	me := requireMultiError(t, err)
	fe := findByPath(me, "server.port")
	if fe == nil {
		t.Fatalf("expected path 'server.port' (default PathTag + default Delim), got %v", pathsOf(me))
	}
	if fe.Tag != "required" {
		t.Errorf("expected Tag %q (default ValidateTag resolved the koanf-validate:\"required\" rule), got %q", "required", fe.Tag)
	}
}

func TestStruct_SiblingTagCollision_ReturnsErrInvalidConfig(t *testing.T) {
	t.Parallel()
	err := koanfvalidate.Struct(&siblingCollisionCfg{}, koanfvalidate.Options{})
	if !errors.Is(err, koanfvalidate.ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestStruct_SquashedEmbeddedCollision_ReturnsErrInvalidConfig(t *testing.T) {
	t.Parallel()
	err := koanfvalidate.Struct(&embeddedCollisionCfg{}, koanfvalidate.Options{})
	if !errors.Is(err, koanfvalidate.ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig from embedded-squash collision, got %v", err)
	}
}

// A struct nested deeper than the walker's depth cap must return
// ErrInvalidConfig naming the offending koanf path, not a stack overflow.
// reflect.StructOf builds a fresh type at each level so the cycle guard
// (which compares by reflect.Type identity) is not what trips the limit.
func TestStruct_DepthExceeded_ReturnsErrInvalidConfig(t *testing.T) {
	t.Parallel()
	build := func(depth int) reflect.Type {
		// Innermost leaf is a struct with a scalar so validator/v10 has
		// something to traverse if the walker permitted descent.
		cur := reflect.TypeFor[struct {
			X int `koanf:"x"`
		}]()
		for range depth {
			cur = reflect.StructOf([]reflect.StructField{{
				Name: "Down",
				Type: cur,
				Tag:  `koanf:"down"`,
			}})
		}
		return cur
	}
	deep := build(80) // > maxStructDepth (64)
	cfg := reflect.New(deep).Interface()
	err := koanfvalidate.Struct(cfg, koanfvalidate.Options{})
	if !errors.Is(err, koanfvalidate.ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig from depth excess; got %v", err)
	}
}

// =============================================================================
// Path translation — koanf paths derived from struct tags
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
// Cycle guard — direct and mutual recursion
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
