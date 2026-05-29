package stringify

import (
	"errors"
	"fmt"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// Compiler compiles a PostgreSQL AST back into a SQL string.
type Compiler struct{}

// New creates a new Compiler.
func New() *Compiler {
	return &Compiler{}
}

// Compile implements unipg.Compiler.
func (c *Compiler) Compile(tree *pg_query.ParseResult) (string, error) {
	if tree == nil {
		return "", errors.New("tree is nil")
	}
	sql, err := pg_query.Deparse(tree)
	if err != nil {
		return "", fmt.Errorf("deparsing AST: %w", err)
	}
	return sql, nil
}
