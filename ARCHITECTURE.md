# Architecture

`unipg` follows the architectural paradigm of the `unified` ecosystem, dividing the processing pipeline into three distinct phases: **Parse**, **Transform**, and **Compile**.

## Core Paradigm & Generics

The core processor utilizes Go Generics (`Processor[I any, O any]`) to provide a type-safe API. The Input type `I` and Output type `O` are dynamically determined by the `Parser` and `Compiler` implementations provided during initialization.

1. **Parse (`Parser[I]`):** Takes an input of type `I` (e.g., `string`, `[]string`, `io.Reader`) and parses it into a Postgres AST.
2. **Transform (`Transformer`):** Sequentially applies a series of plugins to mutate, inspect, or reorder the AST.
3. **Compile (`Compiler[O]`):** Converts the manipulated AST into the final output of type `O` (e.g., `string`, `[]byte`, `io.Writer`).

## Directory Structure & DIP

The project strictly follows the **Dependency Inversion Principle (DIP)**. The directory structure mirrors the three phases of the pipeline:

- **Root Directory (`/`):** Contains the core generic pipeline logic (`Processor`) and the core Interfaces (`Parser`, `Transformer`, `Compiler`). 
- **`parsers/`:** Concrete implementations of the `Parser` interface (e.g., `parsers/text`).
- **`transformers/`:** Concrete implementations of the `Transformer` interface (the plugins).
- **`compilers/`:** Concrete implementations of the `Compiler` interface (e.g., `compilers/stringify`).

This structure ensures that third-party developers can easily create custom implementations by adhering to the interfaces defined in the root package, while keeping the repository cleanly organized by role.

## Components

### 1. Parser (`unipg.Parser[I]`)
- **Role:** Converts incoming data (`I`) into a Postgres AST node structure (`*pg_query.ParseResult`).
- **Implementation:** Typically wraps `github.com/pganalyze/pg_query_go`.

### 2. Transformer (`unipg.Transformer`)
- **Role:** Iterates over the registered plugins in the exact order they were provided. Each plugin receives the AST, performs its specific mutation, and passes the updated AST to the next plugin.
- **Mutation Strategy:** Plugins must perform **in-place mutations** on the AST (modifying the `*pg_query.ParseResult` directly). Given the deep complexity of the PostgreSQL AST, deep copying is highly inefficient and unnecessary since the pipeline executes sequentially.

### 3. Compiler (`unipg.Compiler[O]`)
- **Role:** Recursively walks the final AST and generates output in the desired format (`O`).
- **Implementation:** Typically utilizes `pg_query_go`'s native Deparse functionality for string outputs.

## Data Flow

```text
Input (Type: I)
      │
      ▼
 [ Parser[I] ]      <- Extracts AST (e.g., pg_query_go.Parse)
      │
      ▼
     AST            <- (*pg_query.ParseResult)
      │
      ▼
 [ Transformer 1 ]  <- In-place mutation (Interface implementation)
      │
      ▼
 [ Transformer 2 ]  <- In-place mutation (Interface implementation)
      │
      ▼
     AST            <- Final state
      │
      ▼
 [ Compiler[O] ]    <- Converts AST to Output (e.g., pg_query_go.Deparse)
      │
      ▼
Output (Type: O)
