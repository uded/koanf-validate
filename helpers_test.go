package koanfvalidate_test

import (
	"errors"
	"sort"
	"testing"

	koanfvalidate "github.com/uded/koanf-validate"
)

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
