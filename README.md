# koanf-validate

Validate [koanf](https://github.com/knadh/koanf)-populated structs with errors keyed by **koanf paths** (`server.port`) instead of Go field paths (`Config.Server.Port`) — the same paths your operators edit in YAML and env vars.

[![CI](https://github.com/uded/koanf-validate/actions/workflows/ci.yml/badge.svg)](https://github.com/uded/koanf-validate/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/uded/koanf-validate.svg)](https://pkg.go.dev/github.com/uded/koanf-validate)
[![Go Report Card](https://goreportcard.com/badge/github.com/uded/koanf-validate)](https://goreportcard.com/report/github.com/uded/koanf-validate)
[![Release](https://img.shields.io/github/v/release/uded/koanf-validate)](https://github.com/uded/koanf-validate/releases/latest)
[![License: MIT](https://img.shields.io/github/license/uded/koanf-validate)](./LICENSE)

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

Requires Go 1.25+ (matches the MSRV of `validator/v10`). The only direct dependency is `github.com/go-playground/validator/v10`.

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

`Options.IncludeValues` is `false` by default. A `password` field failing `min=16` produces:

```
password: min(16)
```

…and `FieldError.Value` is `nil`. Opt in with `IncludeValues: true` only in environments where the failing values are not sensitive (e.g. integration tests).

## What it doesn't do

- **Tell you which koanf layer produced the value.** `koanf/v2` exposes no provider-of-origin API; after `Unmarshal` the layers are collapsed. The library validates the resulting struct.
- **Normalize values before validation.** Lowercasing emails, trimming whitespace, etc. — run those yourself before calling `Struct`.
- **Reinvent validator rules.** No new rule grammar, no new tag syntax. `koanf-validate` is a translation layer.

## License

MIT — see [LICENSE](./LICENSE).
