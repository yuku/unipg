package unipg

import (
	"errors"
	"fmt"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// Parser takes an input of type I and returns a Postgres AST.
type Parser[I any] interface {
	Parse(input I) (*pg_query.ParseResult, error)
}

// Transformer mutates a Postgres AST in place.
type Transformer interface {
	Transform(tree *pg_query.ParseResult) error
}

// Compiler takes a Postgres AST and converts it to output of type O.
type Compiler[O any] interface {
	Compile(tree *pg_query.ParseResult) (O, error)
}

// Processor is the core pipeline that chains a Parser, optional Transformers, and a Compiler.
type Processor[I any, O any] struct {
	parser       Parser[I]
	transformers []Transformer
	compiler     Compiler[O]
}

// New creates a new Processor with the given Parser, Transformers, and Compiler.
func New[I any, O any](
	parser Parser[I],
	transformers []Transformer,
	compiler Compiler[O],
) *Processor[I, O] {
	return &Processor[I, O]{
		parser:       parser,
		transformers: transformers,
		compiler:     compiler,
	}
}

// Process executes the pipeline: Parse -> Transform (if any) -> Compile.
func (p *Processor[I, O]) Process(input I) (O, error) {
	var zero O

	if p.parser == nil {
		return zero, errors.New("parser is not initialized")
	}
	if p.compiler == nil {
		return zero, errors.New("compiler is not initialized")
	}

	// 1. Parse
	ast, err := p.parser.Parse(input)
	if err != nil {
		return zero, fmt.Errorf("parsing input: %w", err)
	}
	if ast == nil {
		return zero, errors.New("parser returned nil result")
	}

	// 2. Transform
	for i, t := range p.transformers {
		if t == nil {
			return zero, fmt.Errorf("transformer at index %d is nil", i)
		}
		if err := t.Transform(ast); err != nil {
			return zero, fmt.Errorf("transforming AST at index %d: %w", i, err)
		}
	}

	// 3. Compile
	out, err := p.compiler.Compile(ast)
	if err != nil {
		return zero, fmt.Errorf("Compiling AST: %w", err)
	}
	return out, nil
}
