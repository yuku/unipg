## Project Context
- **Name:** `unipg` (unified.js for PostgreSQL DDL)
- **Language:** Go (1.25+)
- **Goal:** Provide a robust, strongly-typed, AST-based pipeline for transforming PostgreSQL DDL statements.

## Architectural Rules
1. **Generics & Type Safety:** The core `Processor` must use Go Generics (`[I any, O any]`) to infer input and output types from the `Parser[I]` and `Compiler[O]`. Ensure strict type safety.
2. **Separation of Concerns:** Always adhere to the `Parse -> Transform -> Compile` pipeline architecture.
3. **AST Manipulation:** All SQL mutations MUST occur at the AST level. Do not use regular expressions to parse or manipulate SQL logic.
4. **Core Parser:** Use `github.com/pganalyze/pg_query_go` for parsing and deparsing. Avoid reinventing these wheels.
5. **Dependency Inversion Principle (DIP):** - Define interfaces (`Parser`, `Transformer`, `Compiler`) in the root directory.
   - Place concrete implementations in the `parsers/`, `transformers/`, and `compilers/` directories.
   - The root package MUST NOT depend on packages inside these implementation directories.
6. **In-Place Mutation:** Transformers MUST mutate the `*pg_query.ParseResult` directly (in-place). Do not attempt to deep-copy the AST, as it introduces severe performance overhead. Configuration options for plugins should be passed via their constructors, keeping the `Transform` signature simple: `Transform(ast *pg_query.ParseResult) error`.

## Coding Standards & Libraries
1. **Dependencies:** Keep third-party dependencies to an absolute minimum. Use the standard library wherever possible.
2. **Testing:** - Use the standard `testing` package.
   - Use `github.com/stretchr/testify/require` strictly for assertions.
   - Write tests for every AST transformation plugin to ensure they handle edge cases cleanly.
3. **Error Handling:** - Use `fmt.Errorf` to wrap errors with context (e.g., `fmt.Errorf("parsing sql: %w", err)`).
   - Ensure errors are propagated up the pipeline cleanly.
4. **Style:** Follow standard `gofmt` styling and Go idiomatic practices (e.g., package-level constructors like `text.New()` or `stringify.New()`).

## Developer Workflow
- Users construct their CLI by importing `unipg` as a library and initializing the pipeline via `unipg.New(parser, transformers, compiler)`. When writing plugin examples or scaffolding, assume this generic-based workflow.
