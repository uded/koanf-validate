# koanf-validate

Validate [koanf](https://github.com/knadh/koanf)-populated structs with errors keyed by **koanf paths** (`server.port`) instead of Go field paths (`Config.Server.Port`) — the same paths your operators edit in YAML and env vars.

[![CI](https://github.com/uded/koanf-validate/actions/workflows/ci.yml/badge.svg)](https://github.com/uded/koanf-validate/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/uded/koanf-validate.svg)](https://pkg.go.dev/github.com/uded/koanf-validate)
[![Go Report Card](https://goreportcard.com/badge/github.com/uded/koanf-validate)](https://goreportcard.com/report/github.com/uded/koanf-validate)
[![Release](https://img.shields.io/github/v/release/uded/koanf-validate)](https://github.com/uded/koanf-validate/releases/latest)
[![License: MIT](https://img.shields.io/github/license/uded/koanf-validate)](./LICENSE)

> ## ⚠️ Dependency pinning notice — read before adopting
>
> This library **deliberately pins `validator/v10` to `v10.27.0`** and its
> `golang.org/x/{crypto,sys,text}` transitives to the versions that compile on
> Go 1.23, so koanf-validate stays usable on **every Go release koanf itself
> supports**.
>
> **Why the pin exists**
>
> koanf v2 advertises Go 1.23+ as its MSRV. Latest `validator/v10` (v10.30.x)
> and its transitive `x/*` deps quietly require Go 1.25, which would silently
> lock out any koanf user still on Go 1.23 or 1.24 from adopting this
> companion library. Pinning is the only way to honor the koanf-companion
> framing this library is named after.
>
> **What it means for you**
>
> - **You get a working library on every Go version koanf supports** — Go 1.23, 1.24, 1.25, 1.26.
> - The pin is a *minimum*, not a ceiling. Go's Minimum Version Selection (MVS) picks the highest required version across your full dependency tree. If your application pulls in a newer `x/crypto`, `x/sys`, or `x/text` through any other dependency, your build resolves to that newer version — we don't cap you.
> - **You can opt up to the latest `validator/v10` yourself** at any time:
>   ```bash
>   go get github.com/go-playground/validator/v10@latest
>   ```
>   Our public usage is API-compatible across the v10 line; the only reason we don't take that bump in our own `go.mod` is the MSRV constraint above.
> - **Heads-up on inherited CVEs.** The pinned `x/crypto@v0.40.0` and `x/sys@v0.35.0` carry a handful of CVEs fixed only in versions requiring Go 1.24+. `govulncheck` against koanf-validate's own symbol graph reports zero reachable vulnerabilities — none of the vulnerable paths are called from this library. Downstream security tooling that scans your full binary will still flag the modules regardless, since transitive presence is hard to distinguish from actual reachability. If you're already on Go 1.24+ and want a clean scan, pull the fixes into your build via MVS:
>   ```bash
>   go get golang.org/x/crypto@latest golang.org/x/sys@latest
>   ```
>   This is exactly what the "minimum, not ceiling" point above is for.
>
> **Maintainer policy**
>
> These pins track koanf's MSRV. When [knadh/koanf](https://github.com/knadh/koanf) raises the `go` directive in its own `go.mod`, we raise ours and drop the pins in the same release. Until then, `.github/dependabot.yml` has explicit ignore rules so Dependabot doesn't keep proposing bumps we'd have to reject.

## Related projects

This validator composes with two sibling packages in the same family:

- **[koanf-structdefaults](https://github.com/uded/koanf-structdefaults)** — populate a koanf instance from `koanf-default:"…"` struct tags. The natural *floor* layer below file, env, and remote providers in the load order.
- **[koanf-etcd](https://github.com/uded/koanf-etcd)** — production-grade koanf v2 Provider for etcd v3: auth/TLS, nested output, watch with reconnect/resume/resync, debounce, BYO `clientv3.Client`.

Typical pipeline: load with `structdefaults` (and/or `koanf-etcd`, file, env) → `k.Unmarshal(&cfg)` → `koanfvalidate.Struct(&cfg, …)` as the post-load gate.

## Why

The conventional `validator/v10` setup gives you Go-field paths in errors:

```
Config.Server.Port: required
Config.Server.MinPort: ltefield
```

Useful at a Go REPL, useless at 3am when the on-call engineer is looking at `server.port:` in `config.yaml` and can't find `Config.Server.Port` anywhere. This library translates every failure path through the same `koanf:"…"` tag your config layer uses, so errors look like:

```
server.port: required
server.min_port: ltefield(server.port)
```

The same translation applies to `gtefield`/`eqfield`/etc. cross-field references, so sibling-relative rules stay readable.

## Install

```bash
go get github.com/uded/koanf-validate
```

Requires Go 1.23+ (matches koanf v2's MSRV). The only direct dependency is `github.com/go-playground/validator/v10`. See the [dependency-pinning notice](#%EF%B8%8F-dependency-pinning-notice--read-before-adopting) above for the version policy.

> **Transitive footprint note.** `validator/v10` pulls `github.com/gabriel-vasile/mimetype` (~2 MB) as a transitive dependency for its `file` rules. The full set is `mimetype`, `go-playground/locales`, `go-playground/universal-translator`, `leodido/go-urn`, and the standard `golang.org/x/{crypto,sys,text}` chain — your consumer binary inherits all of them whether or not your config uses the rules that require them. This is upstream and outside our control; if it matters for your binary size, raise it with `validator/v10`.

## Usage

```go
package main

import (
    "log"
    "time"

    "github.com/knadh/koanf/parsers/yaml"
    "github.com/knadh/koanf/providers/env"
    "github.com/knadh/koanf/providers/file"
    "github.com/knadh/koanf/v2"

    "github.com/uded/koanf-validate"
)

type Config struct {
    Server struct {
        Host    string        `koanf:"host"     koanf-validate:"required,hostname"`
        Port    int           `koanf:"port"     koanf-validate:"required,min=1,max=65535"`
        MinPort int           `koanf:"min_port" koanf-validate:"required,ltefield=Port"`
        Timeout time.Duration `koanf:"timeout"  koanf-validate:"required"`
    } `koanf:"server"`
    LogLevel string `koanf:"log_level" koanf-validate:"required,oneof=debug info warn error"`
}

func main() {
    k := koanf.New(".")
    _ = k.Load(file.Provider("config.yaml"), yaml.Parser())
    _ = k.Load(env.Provider("APP_", ".", nil), nil)

    var cfg Config
    if err := k.Unmarshal("", &cfg); err != nil {
        log.Fatal(err)
    }

    if err := koanfvalidate.Struct(&cfg, koanfvalidate.Options{}); err != nil {
        log.Fatal(err)
    }
}
```

A failed validation prints, for example:

```
koanfvalidate: 2 validation error(s)
  - server.port: required
  - log_level: oneof(debug info warn error)
```

## Options

```go
type Options struct {
    Validator     *validator.Validate // nil → constructed internally
    PathTag       string              // default: "koanf"
    ValidateTag   string              // default: "koanf-validate"
    IncludeValues bool                // default: false (secret-safe)
}
```

| Field | Effect |
|---|---|
| `Validator` | Pre-configured `*validator.Validate`. Use it to register custom rules, aliases, struct-level validators, or translations. When nil, a fresh instance is constructed for the call. `Struct` calls `SetTagName(ValidateTag)` on it. |
| `PathTag` / `ValidateTag` | Override the struct tags read for path segments and rule lists. Empty values use the library defaults. |
| `IncludeValues` | When `true`, `FieldError.Value` carries the actual failing field value. Off by default to avoid leaking secrets through logs. |

## Tag semantics

Path derivation reuses the existing `koanf:"…"` tag with the same semantics as `koanf-structdefaults`:

| Tag | Behavior |
|---|---|
| `koanf:"name"` | path segment is `name` |
| `koanf:"-"` | field is skipped entirely — no validation runs |
| `koanf:""` or absent | path segment is the Go field name |
| anonymous embedded, no `koanf` tag | squashed into the parent's path |
| anonymous embedded, `koanf:"name"` | namespaced under `name` |

Validation rules live in a separate `koanf-validate:"…"` tag and follow `validator/v10`'s syntax verbatim:

```go
Port int `koanf:"port" koanf-validate:"required,min=1,max=65535"`
```

See the [`validator/v10` docs](https://pkg.go.dev/github.com/go-playground/validator/v10) for the complete list of built-in rules.

## Type-anchored validation: the `Validate()` method

For cross-field invariants or domain-specific rules, implement `Validate() error` on any struct type the walker visits — both value and pointer receivers are honored:

```go
type ServerConfig struct {
    Port    int `koanf:"port"     koanf-validate:"required"`
    MinPort int `koanf:"min_port" koanf-validate:"required"`
}

func (s *ServerConfig) Validate() error {
    if s.Port < s.MinPort {
        return &koanfvalidate.FieldError{
            Path:  "port",
            Tag:   "gtefield",
            Param: "min_port",
        }
    }
    return nil
}
```

`Validate()` may return:

| Return | Result |
|---|---|
| `nil` | no failure from this struct |
| plain `error` | one `*FieldError` at the receiver's koanf path, `Tag="invariant"`, `ErrInvariant` sentinel |
| `*koanfvalidate.FieldError` | used as-is; `Path` and (cross-field) `Param` are interpreted **relative to the receiver** — `{Path: "port"}` on a struct mounted at `server` becomes `server.port` |
| `errors.Join(…)` of any of the above | each leaf is added to the resulting `MultiError` |

No registration. No globals. The validation moves with the type.

## Custom rules via `Options.Validator`

Build a `*validator.Validate` with whatever rules, aliases, or struct-level validators you want, then pass it in:

```go
v := validator.New()
v.RegisterValidation("company_port", func(fl validator.FieldLevel) bool {
    p := fl.Field().Int()
    return p >= 8000 && p <= 9000
})
v.RegisterAlias("shortid", "len=8,alphanum")

err := koanfvalidate.Struct(&cfg, koanfvalidate.Options{Validator: v})
```

`koanfvalidate` reuses the instance you supply, including all of `validator/v10`'s extension points (`RegisterValidation`, `RegisterAlias`, `RegisterStructValidation`, `RegisterTagNameFunc`, `RegisterTranslation`).

## Errors

All errors are sentinel-wrapped via `%w`; match with `errors.Is` or `errors.As`:

| Sentinel | When |
|---|---|
| `ErrInvalidInput` | target is `nil`, a non-pointer, a nil pointer, or a pointer to a non-struct. |
| `ErrInvalidConfig` | `Options` carries an invalid setting. |
| `ErrCyclicType` | walker encountered a struct type that recursively references itself. |
| `ErrValidation` | generic parent for any rule failure not classified into a specific sentinel below. |
| `ErrRequired` | `required`, `required_if`, `required_with`, `required_without`, … |
| `ErrOutOfRange` | `min`, `max`, `gte`, `lte`, `gt`, `lt`, `len`, `eq`, `ne` |
| `ErrNotInSet` | `oneof`, `not_oneof` |
| `ErrBadFormat` | `email`, `url`, `uuid`, `hostname`, `ip`, `cidr`, `datetime`, `alpha`, `numeric`, `base64`, `jwt`, … |
| `ErrFieldMismatch` | `eqfield`, `gtefield`, `ltefield`, `nefield`, and the `_cs` variants |
| `ErrInvariant` | failure produced by a `Validate()` method |

A failing rule returns a `*MultiError` whose `Errors` field exposes each `*FieldError`:

```go
type FieldError struct {
    Path     string // koanf path: "server.port"
    Tag      string // rule that failed: "required", "min", "gtefield", "invariant"
    Param    string // translated where possible: "server.min_port" for gtefield, "10" for min
    RawParam string // validator/v10's verbatim Param ("MinPort" before translation)
    Value    any    // populated only when Options.IncludeValues=true
}
```

`errors.As` reaches the underlying `validator.FieldError` for rule failures:

```go
var multi *koanfvalidate.MultiError
if errors.As(err, &multi) {
    for _, fe := range multi.Errors {
        if errors.Is(fe, koanfvalidate.ErrRequired) {
            // …
        }
        var raw validator.FieldError
        if errors.As(fe, &raw) {
            // raw.Namespace(), raw.Kind(), raw.Type(), …
        }
    }
}
```

`MultiError` orders its `Errors` deterministically by `(Path, Tag)` so test snapshots and log output stay stable across runs.

## Default error rendering

Terse and consistent:

```
server.host: required
server.port: min(10)
log_level: oneof(debug info warn error)
server.port: gtefield(server.min_port)
```

The structured `Path` / `Tag` / `Param` fields are exposed so you can build richer messages at your log site without parsing strings.

## Secret safety

`Options.IncludeValues` is `false` by default — every failing value is redacted at every error surface. A `password` field failing `min=16` produces:

```
password: min(16)
```

…and both `FieldError.Value` and the underlying `validator.FieldError.Value()` reached via `errors.As` return `nil`. The cause chain stays intact so `errors.Is`/`errors.As` keep working for everything except the raw value.

### Diagnosability trade-off

The safe default has a cost: SREs cannot see the actual failing value for non-sensitive fields (port numbers, timeouts, enum mismatches). For two reasonable patterns:

**Whole-application opt-in** — fine in environments where config values are not sensitive (CI test logs, local development):

```go
err := koanfvalidate.Struct(&cfg, koanfvalidate.Options{IncludeValues: true})
```

**Per-call opt-in** — re-validate just for diagnostics after a redacted failure:

```go
if err := koanfvalidate.Struct(&cfg, koanfvalidate.Options{}); err != nil {
    debugErr := koanfvalidate.Struct(&cfg, koanfvalidate.Options{IncludeValues: true})
    log.Printf("config rejected (redacted): %v", err)
    log.Debug("config rejected (with values): %v", debugErr)
}
```

A future release may add per-field sensitivity tags so non-sensitive fields can expose values while genuine secrets remain hidden — for now the choice is whole-call.

### Path resolution caveats

Validator features the walker does not model — `dive`, map values, slice elements, and rules registered via `RegisterStructValidation` — produce errors whose `Path` falls back to the raw Go field path (e.g. `Cfg.Tags[key]`). `ErrPathUnresolved` is added to the `Unwrap` chain on those errors:

```go
if errors.Is(err, koanfvalidate.ErrPathUnresolved) {
    // At least one error has a degraded path — surface to your alerting
    // pipeline, or fall back to the raw validator messages.
}
```

Errors at well-modeled paths (the common case — plain struct fields and intermediate structs) do not carry the sentinel.

## What it doesn't do

- **Tell you which koanf layer produced the value.** `koanf/v2` exposes no provider-of-origin API; after `Unmarshal` the layers are collapsed. The library validates the resulting struct.
- **Normalize values before validation.** Lowercasing emails, trimming whitespace, etc. — run those yourself before calling `Struct`.
- **Reinvent validator rules.** No new rule grammar, no new tag syntax. `koanf-validate` is a translation layer.
- **Adapt to validation libraries other than `validator/v10`.** The walker and translator are coupled to validator/v10's `Namespace()`/`Param()`/`Tag()` error surface. Libraries with different error models — [gookit/validate](https://github.com/gookit/validate), [ozzo-validation](https://github.com/go-ozzo/ozzo-validation), [govalidator](https://github.com/asaskevich/govalidator) — would need a parallel adapter, not a shim, and the upstream concerns those libraries own (custom messages, scenes, i18n, filtering) collide with this library's path-translation mandate. Out of scope.

## License

MIT — see [LICENSE](./LICENSE).
