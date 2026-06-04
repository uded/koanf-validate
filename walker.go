package koanfvalidate

import (
	"encoding"
	"fmt"
	"reflect"
	"sync"
	"time"
)

// walkResult is what the walker produces: a map from Go field paths (matching
// validator/v10's Namespace() shape) to koanf paths, plus type-level recipes
// for invoking Validate() on any reachable struct that implements it. The
// result is a pure function of (rootType, pathTag, delim) and is cached.
type walkResult struct {
	// paths maps a Go field path like "Cfg.Server.Port" to a koanf path
	// like "server.port". Includes every field at every depth, plus every
	// intermediate struct path, so sibling-relative cross-field references
	// (gtefield=MinPort) resolve correctly.
	paths map[string]string

	// skippedPrefixes lists Go field paths whose koanf tag is "-". The walker
	// itself does not descend into these subtrees, but validator/v10 sees
	// no koanf tag and will still validate them if they carry validation
	// rules. The translator uses these prefixes to drop those errors.
	skippedPrefixes []string

	// visitorRecipes describes how to reach each Validate()-implementing
	// struct from the root value. Stored type-side only; resolved into a
	// reflect.Value receiver per call so the cached result never references
	// a particular caller's struct instance.
	visitorRecipes []visitorRecipe

	// rootGoPath is the Go field path prefix used for the root. For named
	// types validator/v10 prepends the type name (e.g. "Cfg.Field"); for
	// anonymous struct literals it omits the prefix entirely (just "Field").
	rootGoPath string
}

// visitorRecipe records the path from the root struct value down to a field
// whose type implements the StructValidator interface. Resolving a recipe
// against a particular root value yields the receiver to call Validate() on.
type visitorRecipe struct {
	koanfPath string
	steps     []fieldStep
}

// fieldStep is one hop along a recipe: pick a field by index, and (if it is
// a pointer-to-struct) dereference it. A nil pointer is replaced with a
// freshly-allocated zero value so Validate() runs against well-defined state
// — matching the walker's value-tree-traversal behavior.
type fieldStep struct {
	index int
	deref bool
}

// resolve walks the recipe's steps from rootValue down to the receiver.
// rootValue must be the dereferenced root struct (an addressable struct
// value) — the same shape resolveInput returns.
func (r visitorRecipe) resolve(rootValue reflect.Value) reflect.Value {
	cur := rootValue
	for _, step := range r.steps {
		cur = cur.Field(step.index)
		if step.deref {
			if cur.IsNil() {
				cur = reflect.New(cur.Type().Elem()).Elem()
			} else {
				cur = cur.Elem()
			}
		}
	}
	return cur
}

// errorType is cached so hasValidate doesn't allocate a TypeFor[error] on
// every check.
var errorType = reflect.TypeFor[error]()

// textUnmarshalerType is the reflect.Type of encoding.TextUnmarshaler, used
// to short-circuit descent into types that handle their own text parsing.
var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

// durationType is treated as an opaque leaf even though it is technically a
// named integer type. It is the most common leaf type users hit and naive
// descent would still terminate immediately (it has no fields), but listing
// it explicitly makes intent clear.
var durationType = reflect.TypeFor[time.Duration]()

// walkCache memoizes walk results across Struct() calls. Keyed on the tuple
// the walker actually depends on: (rootType, pathTag, delim). Values are
// immutable *walkResult — the visitor recipes hold no per-call state, so
// reading the cached entry from many goroutines is safe.
var walkCache sync.Map

type walkCacheKey struct {
	rootType reflect.Type
	pathTag  string
	delim    string
}

// walkStruct validates that target is a non-nil pointer to a struct, then
// returns the cached walkResult for its type (computing it on first sight).
// Returns ErrInvalidInput for bad inputs and ErrCyclicType if a struct type
// recursively references itself. Cycle errors are not cached because the
// returned error already references the offending Go type by name, which
// is sufficient diagnostic on the next call.
func walkStruct(target any, pathTag, delim string) (*walkResult, error) {
	v, err := resolveInput(target)
	if err != nil {
		return nil, err
	}

	key := walkCacheKey{rootType: v.Type(), pathTag: pathTag, delim: delim}
	if cached, ok := walkCache.Load(key); ok {
		return cached.(*walkResult), nil
	}

	wr, err := walkType(v.Type(), pathTag, delim)
	if err != nil {
		return nil, err
	}

	// Store and return: in case two goroutines lost the race to populate
	// the same key, both copies are semantically identical so we don't
	// care which wins.
	walkCache.Store(key, wr)
	return wr, nil
}

// walkType performs the cold-path traversal of a struct type and produces
// the walkResult. It only reads reflect.Type — no value is needed, since
// the walker discovers Validate() implementations from method sets and
// records recipes (not receiver values).
func walkType(rootType reflect.Type, pathTag, delim string) (*walkResult, error) {
	w := &walker{
		pathTag:  pathTag,
		delim:    delim,
		paths:    map[string]string{},
		visiting: map[reflect.Type]struct{}{},
	}

	// validator/v10 uses the type name as the root Namespace segment for
	// named types but omits it entirely for anonymous struct literals.
	// Type.Name() returns "" for the anonymous case, which is exactly the
	// rootGoPath we want.
	rootGoPath := rootType.Name()
	if rootGoPath != "" {
		w.paths[rootGoPath] = ""
	}
	if hasValidate(rootType) {
		w.visitorRecipes = append(w.visitorRecipes, visitorRecipe{koanfPath: ""})
	}

	if err := w.walkType(rootType, rootGoPath, "", nil); err != nil {
		return nil, err
	}

	return &walkResult{
		paths:           w.paths,
		skippedPrefixes: w.skippedPrefixes,
		visitorRecipes:  w.visitorRecipes,
		rootGoPath:      rootGoPath,
	}, nil
}

// walker carries immutable configuration and mutable per-walk state through
// the recursive descent. Keeping state on the walker (rather than the recursive
// signature) keeps walkType's argument list small.
type walker struct {
	pathTag, delim  string
	paths           map[string]string
	skippedPrefixes []string
	visitorRecipes  []visitorRecipe
	visiting        map[reflect.Type]struct{}
}

// walkType recurses through t accumulating path mappings, skip prefixes, and
// visitor recipes. parentSteps is the field-step chain from the root to t;
// child recipes extend it by one fieldStep.
func (w *walker) walkType(t reflect.Type, goPath, koanfPath string, parentSteps []fieldStep) error {
	// Cycle guard. Two values of the same Go type share an identical
	// reflect.Type, so this catches both self-reference (Node.Next *Node)
	// and mutual recursion (A→B→A).
	if _, on := w.visiting[t]; on {
		return fmt.Errorf("%w: %s", ErrCyclicType, t.String())
	}
	w.visiting[t] = struct{}{}
	defer delete(w.visiting, t)

	for i := range t.NumField() {
		f := t.Field(i)

		childGoPath := f.Name
		if goPath != "" {
			childGoPath = goPath + "." + f.Name
		}

		ptag := f.Tag.Get(w.pathTag)
		if ptag == "-" {
			// We don't descend, but validator/v10 doesn't know about the
			// koanf tag — it may still emit errors for fields below this
			// node. Record the prefix so the translator can drop them.
			w.skippedPrefixes = append(w.skippedPrefixes, childGoPath)
			continue
		}

		childKoanfPath := w.childKoanfPath(koanfPath, f, ptag)

		// Record the mapping for every field, including intermediate
		// structs, so cross-field Param resolution can look up siblings.
		w.paths[childGoPath] = childKoanfPath

		ft := f.Type
		isPointer := ft.Kind() == reflect.Pointer
		if isPointer {
			ft = ft.Elem()
		}

		// Only descend into struct or *struct fields. Leaves end here.
		if ft.Kind() != reflect.Struct {
			continue
		}
		// Treat encoding.TextUnmarshaler and time.Duration as opaque
		// leaves — validator/v10 sees them as scalars and so do we.
		if isOpaqueLeaf(f.Type) {
			continue
		}

		childSteps := append(append([]fieldStep(nil), parentSteps...), fieldStep{index: i, deref: isPointer})

		if hasValidate(ft) {
			w.visitorRecipes = append(w.visitorRecipes, visitorRecipe{
				koanfPath: childKoanfPath,
				steps:     childSteps,
			})
		}

		if err := w.walkType(ft, childGoPath, childKoanfPath, childSteps); err != nil {
			return err
		}
	}
	return nil
}

// childKoanfPath returns the koanf path for a field. Honors the koanf squash
// convention for anonymous embedded structs without an explicit path tag.
func (w *walker) childKoanfPath(parent string, f reflect.StructField, ptag string) string {
	if f.Anonymous && ptag == "" {
		return parent
	}
	seg := ptag
	if seg == "" {
		seg = f.Name
	}
	if parent == "" {
		return seg
	}
	return parent + w.delim + seg
}

// hasValidate reports whether t (or *t) has a method `Validate() error`.
// Pointer-receiver methods cover both — *T's method set includes T's value
// methods — so checking *T is sufficient.
func hasValidate(t reflect.Type) bool {
	return hasValidateMethod(reflect.PointerTo(t))
}

// hasValidateMethod checks for a method named Validate with signature
// `func() error`. The reflect.Method's Type includes the receiver as the
// first input, so a valid match has NumIn==1, NumOut==1, Out(0)==error.
func hasValidateMethod(t reflect.Type) bool {
	m, ok := t.MethodByName("Validate")
	if !ok {
		return false
	}
	mt := m.Type
	return mt.NumIn() == 1 && mt.NumOut() == 1 && mt.Out(0) == errorType
}

// callValidate invokes Validate() on receiver and returns whatever error it
// produced. Tries pointer receiver first (more permissive — covers value
// receiver methods too via Go's promoted method set). Returns nil if no
// Validate method is callable on either form.
//
// Any panic that escapes the user's Validate() method is recovered and
// converted into an error wrapping ErrPanic, so a buggy Validate() never
// crashes the host process.
func callValidate(receiver reflect.Value) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", ErrPanic, r)
		}
	}()

	if !receiver.IsValid() {
		return nil
	}

	var fn reflect.Value
	if receiver.CanAddr() {
		fn = receiver.Addr().MethodByName("Validate")
	}
	if !fn.IsValid() {
		fn = receiver.MethodByName("Validate")
	}
	if !fn.IsValid() {
		return nil
	}
	out := fn.Call(nil)
	if out[0].IsNil() {
		return nil
	}
	return out[0].Interface().(error)
}

// isOpaqueLeaf reports whether t should be treated as a leaf during the walk.
// Currently: encoding.TextUnmarshaler implementations and time.Duration.
func isOpaqueLeaf(t reflect.Type) bool {
	if t == durationType {
		return true
	}
	if t.Kind() == reflect.Pointer && t.Elem() == durationType {
		return true
	}
	return t.Implements(textUnmarshalerType) || reflect.PointerTo(t).Implements(textUnmarshalerType)
}

// resolveInput validates that target is a non-nil pointer to a struct and
// returns the dereferenced reflect.Value (which is addressable, so methods
// with pointer receivers are callable).
func resolveInput(target any) (reflect.Value, error) {
	if target == nil {
		return reflect.Value{}, fmt.Errorf("%w: got nil", ErrInvalidInput)
	}
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Pointer {
		return reflect.Value{}, fmt.Errorf("%w: got %s (not a pointer)", ErrInvalidInput, v.Kind())
	}
	if v.IsNil() {
		return reflect.Value{}, fmt.Errorf("%w: got nil pointer", ErrInvalidInput)
	}
	elem := v.Elem()
	if elem.Kind() != reflect.Struct {
		return reflect.Value{}, fmt.Errorf("%w: got pointer to %s (not a struct)", ErrInvalidInput, elem.Kind())
	}
	return elem, nil
}
