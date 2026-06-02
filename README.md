# unipg

[![Go Reference](https://pkg.go.dev/badge/github.com/yuku/unipg.svg)](https://pkg.go.dev/github.com/yuku/unipg)

`unipg` is a strongly-typed, plugin-based text processing interface for PostgreSQL DDL, inspired by [unified.js](https://unifiedjs.com/). 

It parses PostgreSQL SQL into an Abstract Syntax Tree (AST) using [pg_query_go](https://github.com/pganalyze/pg_query_go), applies a pipeline of user-defined plugins (transformers) to mutate the AST, and compiles the result back into the desired output format.

## Why unipg?

Managing complex SQL DDL files often involves manual formatting, resolving dependency order (e.g., `CREATE TABLE` before `ALTER TABLE`), or converting arbitrary comments into standardized DDL comments. `unipg` solves these issues reliably at the AST level, avoiding the fragility of regular expressions.

## Features

- **PostgreSQL Native:** Exclusively designed for PostgreSQL AST.
- **Type-Safe Pipeline:** Leverages Go Generics to infer Input and Output types based on the selected Parser and Compiler.
- **Plugin Architecture (DIP):** Core interfaces are defined in the root, making it easy to plug in custom implementations. Build custom pipelines using the `Parse -> Transform -> Compile` paradigm.
- **Go-Native:** Fast execution with minimal dependencies.

## Library Usage

You can use `unipg` as a Go library to build custom DDL processing pipelines.

```go
import (
    "github.com/yuku/unipg"
    "github.com/yuku/unipg/compilers/stringify"
    "github.com/yuku/unipg/parsers/text"
    "github.com/yuku/unipg/transformers/comment"
    "github.com/yuku/unipg/transformers/extractfk"
    "github.com/yuku/unipg/transformers/reorder"
)

var processor = unipg.New(
    text.New(),  // implements unipg.Parser[string]
    []unipg.Transformer{
        comment.New(),
        extractfk.New(extractfk.WithRevealConstraintName()),
        reorder.New(),
    },
    stringify.New(stringify.WithPretty()),  // implements unipg.Compiler[string]
)

// The types of `input` and `output` are inferred by the Parser and Compiler.
var input, output string

// Process the SQL
output, _ = processor.Process(input)
```

## Built-in Parsers and Compilers

### Text Parser

The `text` parser parses PostgreSQL SQL from a string input and produces an AST.

Implements `unipg.Parser[string]`.

### File Parser

The `file` parser reads PostgreSQL SQL from a list of files and produces an AST.
Glob patterns are supported (e.g., `schemas/*.sql`).

Implements `unipg.Parser[[]string]`.

### Stringify Compiler

The `stringify` compiler compiles the AST back into a string output.

Implements `unipg.Compiler[string]`.

## Built-in Transformers

### Comment Transformer

Converts JavaDoc-style comments (`/** ... */`) into PostgreSQL DDL comments (`COMMENT ON TABLE` and `COMMENT ON COLUMN`).

* Input
    ```sql
    /** 
     * This is a comment that should be converted to a DDL comment on the users table.
     * Multiple line comments are supported.
     */
    CREATE TABLE users (
        /** This is a comment that should be converted to a DDL comment on the id column */
        id INT PRIMARY KEY,
        -- This is a comment that will be ignored because it's not a doc comment
        team_id INT
    );
    ```
* Output
    ```sql
    CREATE TABLE users (
        id INT PRIMARY KEY,
        team_id INT
    );

    COMMENT ON TABLE users IS 'This is a comment that should be converted to a DDL comment on the users table.
    Multiple line comments are supported.';

    COMMENT ON COLUMN users.id IS 'This is a comment that should be converted to a DDL comment on the id column';
    ```

### ExtractFK + Reorder Transformers

Extracts inline foreign keys into standalone `ALTER TABLE` statements and reorders them to the bottom.
`reorder` transformer also moves `CREATE VIEW` statements to the bottom.

By using them together, you can ensure that all `CREATE TABLE` statements are defined before any `ADD FOREIGN KEY` statements, so you don't have to worry about dependency order.

* Input
    ```sql
    CREATE VIEW user_teams AS
    SELECT users.id AS user_id, teams.id AS team_id
    FROM users
    JOIN teams ON users.team_id = teams.id;

    CREATE TABLE users (
        id INT PRIMARY KEY,
        team_id INT REFERENCES teams(id)
    );

    CREATE TABLE teams (
        id INT PRIMARY KEY
    );
    ```
* Output
    ```sql
    CREATE TABLE users (
        id INT PRIMARY KEY,
        team_id INT
    );

    CREATE TABLE teams (
        id INT PRIMARY KEY
    );

    CREATE VIEW user_teams AS
    SELECT users.id AS user_id, teams.id AS team_id
    FROM users
    JOIN teams ON users.team_id = teams.id;

    -- Without `extractfk.WithRevealConstraintName` option
    ALTER TABLE users ADD FOREIGN KEY (team_id) REFERENCES teams(id);

    -- With `extractfk.WithRevealConstraintName` option
    ALTER TABLE users ADD CONSTRAINT users_team_id_fkey FOREIGN KEY (team_id) REFERENCES teams(id);
    ```
