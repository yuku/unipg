package text

import (
	"fmt"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// Parser parses SQL strings into a PostgreSQL AST.
type Parser struct{}

// New creates a new Parser.
func New() *Parser {
	return &Parser{}
}

// Parse implements unipg.Parser.
func (p *Parser) Parse(input string) (*pg_query.ParseResult, error) {
	result, err := pg_query.Parse(input)
	if err != nil {
		return nil, fmt.Errorf("parsing SQL: %w", err)
	}
	return result, nil
}
