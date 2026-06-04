package koanfvalidate

import (
	"encoding"
	"fmt"
	"reflect"
	"time"
)

// walkResult is what the walker produces: a map from Go field paths (matching
// validator/v10's Namespace() shape) to koanf paths, plus a list of any
// StructValidator instances encountered (with the koanf path of their
// receiver) so the call site can invoke Validate() on each.
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

	// visitors lists every struct (at any depth) whose type implements
	// StructValidator. Order is depth-first, matching the walk.
	visitors []structValidatorVisitor

	// rootGoPath is the Go field path prefix used for the root. For named
	// types validator/v10 prepends the type name (e.g. "Cfg.Field"); for
	// anonymous struct literals it omits the prefix entirely (just "Field").
	// We mirror that convention so map keys match validator's Namespace.
	rootGoPath string
}

type structValidatorVisitor struct {
	koanfPath string
	receiver  reflect.Value // addressable, so pointer-receiver methods work
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
// it explicitly makes intent clear and matches sister repo's convention.
var durationType = reflect.TypeFor[time.Duration]()

// walkStruct validates that target is a non-nil pointer to a struct, then
// walks it to produce a walkResult. Returns ErrInvalidInput for bad inputs
// and ErrCyclicType if a struct type recursively references itself.
func walkStruct(target any, pathTag, delim string) (*walkResult, error) {
	v, err := resolveInput(target)
	if err != nil {
		return nil, err
	}

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
	rootGoPath := v.Type().Name()
	if rootGoPath != "" {
		w.paths[rootGoPath] = ""
	}
	if hasValidate(v.Type()) {
		w.visitors = append(w.visitors, structValidatorVisitor{
			koanfPath: "",
			receiver:  v,
		})
	}

	if err := w.walkStruct(v, rootGoPath, ""); err != nil {
		return nil, err
	}

	return &walkResult{
		paths:           w.paths,
		skippedPrefixes: w.skippedPrefixes,
		visitors:        w.visitors,
		rootGoPath:      rootGoPath,
	}, nil
}

// walker carries immutable configuration and mutable per-walk state through
// the recursive descent. Keeping state on the walker (rather than the recursive
// signature) keeps walkStruct's argument list small.
type walker struct {
	pathTag, delim  string
	paths           map[string]string
	skippedPrefixes []string
	visitors        []structValidatorVisitor
	visiting        map[reflect.Type]struct{}
}

func (w *walker) walkStruct(v reflect.Value, goPath, koanfPath string) error {
	t := v.Type()

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

		fv := v.Field(i)
		if isPointer {
			if fv.IsNil() {
				// Use a fresh zero value for the walk; we don't allocate
				// or mutate the user's input. The cycle guard still fires
				// on type identity, so pointer-recursive types terminate.
				fv = reflect.New(ft).Elem()
			} else {
				fv = fv.Elem()
			}
		}

		if hasValidate(ft) {
			w.visitors = append(w.visitors, structValidatorVisitor{
				koanfPath: childKoanfPath,
				receiver:  fv,
			})
		}

		if err := w.walkStruct(fv, childGoPath, childKoanfPath); err != nil {
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
func callValidate(receiver reflect.Value) error {
	// Prefer pointer receiver: its method set is a superset of the value
	// receiver's, so pointer-receiver Validate methods are reachable.
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
