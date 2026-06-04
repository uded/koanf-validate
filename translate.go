package koanfvalidate

import (
	"strings"

	"github.com/go-playground/validator/v10"
)

// tagToSentinel maps a validator/v10 rule tag to the categorical sentinel
// errors.Is should match. Tags not listed here map to ErrValidation (the
// generic parent). Adding a new tag here is the only step needed to teach
// koanfvalidate about it.
var tagToSentinel = map[string]error{
	// Presence
	"required":             ErrRequired,
	"required_if":          ErrRequired,
	"required_unless":      ErrRequired,
	"required_with":        ErrRequired,
	"required_with_all":    ErrRequired,
	"required_without":     ErrRequired,
	"required_without_all": ErrRequired,

	// Magnitude / size
	"min": ErrOutOfRange,
	"max": ErrOutOfRange,
	"gte": ErrOutOfRange,
	"lte": ErrOutOfRange,
	"gt":  ErrOutOfRange,
	"lt":  ErrOutOfRange,
	"len": ErrOutOfRange,
	"eq":  ErrOutOfRange,
	"ne":  ErrOutOfRange,

	// Enumeration
	"oneof":     ErrNotInSet,
	"not_oneof": ErrNotInSet,

	// Format / pattern
	"email":            ErrBadFormat,
	"url":              ErrBadFormat,
	"uri":              ErrBadFormat,
	"uuid":             ErrBadFormat,
	"uuid3":            ErrBadFormat,
	"uuid4":            ErrBadFormat,
	"uuid5":            ErrBadFormat,
	"hostname":         ErrBadFormat,
	"hostname_rfc1123": ErrBadFormat,
	"hostname_port":    ErrBadFormat,
	"fqdn":             ErrBadFormat,
	"ip":               ErrBadFormat,
	"ipv4":             ErrBadFormat,
	"ipv6":             ErrBadFormat,
	"cidr":             ErrBadFormat,
	"cidrv4":           ErrBadFormat,
	"cidrv6":           ErrBadFormat,
	"mac":              ErrBadFormat,
	"datetime":         ErrBadFormat,
	"alpha":            ErrBadFormat,
	"alphanum":         ErrBadFormat,
	"alphaunicode":     ErrBadFormat,
	"alphanumunicode":  ErrBadFormat,
	"ascii":            ErrBadFormat,
	"printascii":       ErrBadFormat,
	"numeric":          ErrBadFormat,
	"number":           ErrBadFormat,
	"boolean":          ErrBadFormat,
	"lowercase":        ErrBadFormat,
	"uppercase":        ErrBadFormat,
	"base64":           ErrBadFormat,
	"base64url":        ErrBadFormat,
	"hexadecimal":      ErrBadFormat,
	"json":             ErrBadFormat,
	"jwt":              ErrBadFormat,
	"semver":           ErrBadFormat,

	// Cross-field
	"eqfield":    ErrFieldMismatch,
	"nefield":    ErrFieldMismatch,
	"gtfield":    ErrFieldMismatch,
	"gtefield":   ErrFieldMismatch,
	"ltfield":    ErrFieldMismatch,
	"ltefield":   ErrFieldMismatch,
	"eqcsfield":  ErrFieldMismatch,
	"necsfield":  ErrFieldMismatch,
	"gtcsfield":  ErrFieldMismatch,
	"gtecsfield": ErrFieldMismatch,
	"ltcsfield":  ErrFieldMismatch,
	"ltecsfield": ErrFieldMismatch,
}

// crossFieldTags is the subset of rule tags whose Param() value is a Go field
// path (sibling-relative) rather than a literal scalar. Used to decide whether
// to translate Param via the goPath→koanfPath map.
var crossFieldTags = map[string]struct{}{
	"eqfield": {}, "nefield": {}, "gtfield": {}, "gtefield": {},
	"ltfield": {}, "ltefield": {},
	"eqcsfield": {}, "necsfield": {}, "gtcsfield": {}, "gtecsfield": {},
	"ltcsfield": {}, "ltecsfield": {},
}

// translateFieldError converts a single validator.FieldError into our
// *FieldError, remapping Namespace → koanf path and (for cross-field tags)
// Param → koanf path of the sibling. Returns nil when the namespace falls
// under a koanf:"-" skip prefix — the field was intentionally excluded.
func translateFieldError(vfe validator.FieldError, paths map[string]string, skippedPrefixes []string, includeValues bool) *FieldError {
	ns := vfe.Namespace()

	if isUnderSkippedPrefix(ns, skippedPrefixes) {
		return nil
	}

	koanfPath, ok := paths[ns]
	if !ok {
		// Defensive fallback: keep the raw namespace if the walker didn't
		// cover this path. This should not happen for well-formed structs;
		// returning a usable error is better than dropping it silently.
		koanfPath = ns
	}

	tag := vfe.Tag()
	rawParam := vfe.Param()
	param := translateParam(tag, rawParam, ns, paths)

	sentinel := tagToSentinel[tag]
	if sentinel == nil {
		sentinel = ErrValidation
	}

	fe := &FieldError{
		Path:     koanfPath,
		Tag:      tag,
		Param:    param,
		RawParam: rawParam,
		sentinel: sentinel,
		cause:    vfe,
	}
	if includeValues {
		fe.Value = vfe.Value()
	}
	return fe
}

// isUnderSkippedPrefix reports whether ns equals or sits under any of the
// prefixes recorded by the walker for koanf:"-" fields. A field at exactly
// the prefix path matches; so does any deeper field within that subtree.
func isUnderSkippedPrefix(ns string, prefixes []string) bool {
	for _, p := range prefixes {
		if ns == p {
			return true
		}
		if len(ns) > len(p) && ns[len(p)] == '.' && ns[:len(p)] == p {
			return true
		}
	}
	return false
}

// translateParam resolves cross-field Param values to koanf paths via the
// goPath map. Returns the original Param for non-cross-field tags or when
// the sibling lookup misses.
func translateParam(tag, rawParam, ns string, paths map[string]string) string {
	if rawParam == "" {
		return rawParam
	}
	if _, isCross := crossFieldTags[tag]; !isCross {
		return rawParam
	}
	sibling := siblingGoPath(ns, rawParam)
	if sibling == "" {
		return rawParam
	}
	if translated, ok := paths[sibling]; ok {
		return translated
	}
	return rawParam
}

// siblingGoPath constructs the Go field path of a sibling field. For
// ns="Cfg.Server.Port" and sibling="MinPort", returns "Cfg.Server.MinPort".
// Returns "" if ns has no parent (top-level field, sibling not addressable).
func siblingGoPath(ns, sibling string) string {
	idx := strings.LastIndexByte(ns, '.')
	if idx < 0 {
		return ""
	}
	return ns[:idx] + "." + sibling
}
