# Contributing to koanf-validate

Thanks for considering a contribution. This document covers the practical
mechanics; if anything here gets in your way, open an issue and we'll fix
the document, not the obstacle.

## Filing issues

- **Bug reports**: include a minimal Go reproducer (struct definition,
  validation call, observed error vs expected error). Race-detector traces
  are especially welcome.
- **Feature requests**: describe the use case before the API. The library's
  guiding principle is to surface koanf-path-keyed errors over an existing
  validator/v10 instance — proposals that fit that scope ship faster.
- **Security**: see [SECURITY.md](./SECURITY.md). Do **not** open public
  issues for vulnerability reports.

## Development setup

Requires Go 1.23+ (tracks koanf v2's MSRV — see [README's dependency-pinning
notice](./README.md#-dependency-pinning-notice) for the rationale) and
[Task](https://taskfile.dev) for the build harness. Any newer Go release
works locally; CI verifies the full supported range.

```bash
git clone https://github.com/uded/koanf-validate.git
cd koanf-validate
task                  # default → task test
```

Useful task targets:

| Task | What it runs |
|---|---|
| `task fmt` | `go fmt ./...` |
| `task vet` | `go vet ./...` |
| `task test` | `go test -race ./...` |
| `task cover` | race tests + HTML coverage report at `coverage.html` |
| `task bench` | benchmarks (currently a placeholder) |
| `task lint` | `vet` + `gofmt` check + `golangci-lint run` |
| `task vuln` | `govulncheck ./...` |
| `task tidy` | `go mod tidy` |
| `task ci` | full pipeline: `lint` → `test` → `vuln` |

`task ci` must be green before opening a pull request. CI runs the same
pipeline against the full supported Go matrix (1.23, 1.24, 1.25, 1.26).

## Pull-request flow

1. Open an issue first for non-trivial changes so we can agree on the
   design before code lands.
2. Branch from `main`, keep commits focused — one logical change per commit.
3. Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/):
   `feat:`, `fix:`, `refactor:`, `perf:`, `test:`, `docs:`, `chore:`, `ci:`.
   The subject line is a short imperative (≤ 72 chars); the body explains
   the *why* and the trade-offs.
4. Tests accompany every behavioral change. The repo follows TDD: write a
   failing test that names the new contract, then add the code that makes
   it pass.
5. Update [CHANGELOG.md](./CHANGELOG.md) under `[Unreleased]` with a one-
   line description of the user-facing impact.
6. Run `task ci` locally and resolve everything before pushing.
7. Open the PR against `main`. A maintainer reviews; review may include
   suggested rebases.

## Coding conventions

- **Public API**: every exported identifier has godoc that names the
  contract, not just the type. Sentinel errors document when they are
  returned. Document anything `errors.Is` / `errors.As` is meant to reach.
- **Comments**: explain *why*, not *what*. The code says what; comments
  describe the constraint, invariant, or motivation. Avoid references to
  internal review IDs, PR numbers, or other artifacts that rot.
- **Tests**: table-driven where the same shape covers many cases; explicit
  fixture types where each case has its own data shape. Every test uses
  `t.Parallel()` unless it depends on shared mutable state (and even then,
  prefer not).
- **Concurrency**: the library is consumed from many goroutines. Any new
  cache must be safe for concurrent use; any new method that mutates
  shared state must be either documented as not-concurrent-safe or
  protected.
- **Reflection**: walker output is keyed on `(reflect.Type, pathTag,
  delim)` and treated as immutable post-construction. Don't store
  `reflect.Value` in cached structures — values belong to a particular
  caller's struct instance.

## License & DCO

By contributing you agree that your contribution is licensed under the
project's [MIT license](./LICENSE).
