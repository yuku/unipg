# unipg

`unipg` is a strongly-typed, plugin-based text processing interface for PostgreSQL DDL, inspired by [unified.js](https://unifiedjs.com/). 

It parses PostgreSQL SQL into an Abstract Syntax Tree (AST) using [pg_query_go](https://github.com/pganalyze/pg_query_go), applies a pipeline of user-defined plugins (transformers) to mutate the AST, and compiles the result back into the desired output format.

## Why unipg?

Managing complex SQL DDL files often involves manual formatting, resolving dependency order (e.g., `CREATE TABLE` before `ALTER TABLE`), or converting arbitrary comments into standardized DDL comments. `unipg` solves these issues reliably at the AST level, avoiding the fragility of regular expressions.

## Features

- **PostgreSQL Native:** Exclusively designed for PostgreSQL AST.
- **Type-Safe Pipeline:** Leverages Go Generics to infer Input and Output types based on the selected Parser and Compiler.
- **Plugin Architecture (DIP):** Core interfaces are defined in the root, making it easy to plug in custom implementations. Build custom pipelines using the `Parse -> Transform -> Compile` paradigm.
- **Go-Native:** Fast execution with minimal dependencies.

## CLI

`unipg` provides a command-line tool that applies a standard set of transformations to your DDL. 

By default, the CLI enables:
- **comment**: Converts `/** ... */` doc-style comments into formal `COMMENT ON` statements.
- **extractfk**: Extracts inline foreign keys from `CREATE TABLE` to standalone `ALTER TABLE` statements.
- **reorder**: Moves `ALTER TABLE` and `CREATE VIEW` statements to the end of the document, ensuring correct dependency order.

### Running without installation

You can process SQL files instantly using `go run`:

```bash
# Process a file
go run github.com/yuku/unipg/cmd/unipg@latest schema.sql > output.sql

# Read from stdin
cat schema.sql | go run github.com/yuku/unipg/cmd/unipg@latest - > output.sql
```

### Installation

To install the `unipg` binary to your `$GOPATH/bin`:

```bash
go install github.com/yuku/unipg/cmd/unipg@latest
```

Once installed, you can use it simply as:

```bash
unipg schema.sql
```

## Library Usage

You can also use `unipg` as a Go library to build custom DDL processing pipelines.

```go
package main

import (
	"fmt"
	"log"

	"github.com/yuku/unipg"
	"github.com/yuku/unipg/compilers/stringify"
	"github.com/yuku/unipg/parsers/text"
	"github.com/yuku/unipg/transformers/comment"
	"github.com/yuku/unipg/transformers/extractfk"
	"github.com/yuku/unipg/transformers/reorder"
)

func main() {
	input := `
        /** 
         * This is a comment that should be converted 
         * to a DDL comment on the users table 
         */
		CREATE TABLE users (
            /** This is a comment that should be converted to a DDL comment on the id column */
			id INT PRIMARY KEY,
			team_id INT REFERENCES teams(id)
		);
		CREATE TABLE teams (
			id INT PRIMARY KEY
		);
	`

	// Create a new pipeline
    // The types of `input` and `output` are inferred by the Parser and Compiler.
	processor := unipg.New(
        text.New(),          // Parser[string]
        []unipg.Transformer{
            comment.New(),   // Convert comments to DDL comments
            extractfk.New(), // Extract inline FKs to standalone ALTER TABLEs
            reorder.New(),   // Reorder ALTER TABLEs to the bottom
        },
        stringify.New(),   // Compiler[string]
    )

	// Process the SQL
	output, err := processor.Process(input)
	if err != nil {
		log.Fatalf("Failed to process SQL: %v", err)
	}

    // Print the transformed SQL
    fmt.Println(output)
}
```
