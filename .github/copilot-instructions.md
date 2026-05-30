# Copilot Cloud Agent Instructions for `unipg`

## Repository purpose
- `unipg` is a Go library/CLI for PostgreSQL DDL processing with a strict pipeline:
  1. Parse SQL to AST
  2. Transform AST in place via plugins
  3. Compile AST back to output

## High-signal architecture rules (must follow)
- Keep Dependency Inversion intact:
  - Root package (`/tmp/workspace/yuku/unipg`) defines interfaces and core processor.
  - Implementations live under:
    - `/tmp/workspace/yuku/unipg/parsers/`
    - `/tmp/workspace/yuku/unipg/transformers/`
    - `/tmp/workspace/yuku/unipg/compilers/`
  - Do **not** make root depend on those implementation packages.
- Preserve the generic core API in `Processor[I, O]` (`processor.go`).
- AST mutations must be AST-based using `pg_query_go`; do not implement SQL parsing/mutation with regex.
- Transformers should mutate `*pg_query.ParseResult` in place.

## Important code locations
- Core interfaces/pipeline: `/tmp/workspace/yuku/unipg/processor.go`
- CLI entrypoint: `/tmp/workspace/yuku/unipg/cmd/unipg/main.go`
- Parser implementation: `/tmp/workspace/yuku/unipg/parsers/text/`
- Built-in transformers:
  - `/tmp/workspace/yuku/unipg/transformers/comment/`
  - `/tmp/workspace/yuku/unipg/transformers/extractfk/`
  - `/tmp/workspace/yuku/unipg/transformers/reorder/`
- Compiler implementation: `/tmp/workspace/yuku/unipg/compilers/stringify/`

## Validate changes
- Primary validation command:
  - `go test ./...`
- CI also runs tests with `gotestsum` (`.github/workflows/ci.yml`), but local `go test ./...` is the quickest reliable check.

## Change guidance for agents
- Prefer small focused edits in existing packages; mirror existing constructor patterns like `text.New()`, `comment.New()`.
- When adding/changing transformer behavior:
  - update transformer package tests
  - update integration coverage in `/tmp/workspace/yuku/unipg/integration_test.go` when end-to-end behavior changes
- Keep error wrapping style consistent (`fmt.Errorf("...: %w", err)`).
- Use `require` from `testify` for assertions (existing test style).

## Errors encountered during onboarding
- No repository errors were encountered during this onboarding task.
- Baseline validation (`go test ./...`) passed successfully.
