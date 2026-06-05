module github.com/uded/koanf-validate

// MSRV tracks koanf v2 (currently go 1.23.0). validator/v10 is pinned to
// v10.27.0 because v10.28+ require go 1.24+ and v10.30+ require go 1.25+,
// and the golang.org/x/{crypto,sys,text} transitives are pinned for the
// same reason. These pins are minimums — Go's MVS picks newer versions
// when a consumer's other deps require them. See README's "Dependency
// pinning notice" section for the full rationale and the bump policy.
go 1.23.0

require github.com/go-playground/validator/v10 v10.27.0

require (
	github.com/gabriel-vasile/mimetype v1.4.13 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	golang.org/x/crypto v0.40.0 // indirect; pinned for go 1.23 MSRV
	golang.org/x/sys v0.35.0 // indirect; pinned for go 1.23 MSRV
	golang.org/x/text v0.27.0 // indirect; pinned for go 1.23 MSRV
)
