package unipg

import (
	"errors"
	"fmt"
	"reflect"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// Parser takes an input of type I and returns a PostgreSQL AST.
type Parser[I any] interface {
	Parse(input I) (*pg_query.ParseResult, error)
}

// Transformer mutates a PostgreSQL AST in place.
type Transformer interface {
	Transform(tree *pg_query.ParseResult) error
}

// Compiler takes a PostgreSQL AST and converts it to output of type O.
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

	if isNil(p.parser) {
		return zero, errors.New("parser is nil")
	}
	if isNil(p.compiler) {
		return zero, errors.New("compiler is nil")
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
		if isNil(t) {
			return zero, fmt.Errorf("transformer at index %d is nil", i)
		}
		if err := t.Transform(ast); err != nil {
			return zero, fmt.Errorf("transforming AST at index %d: %w", i, err)
		}
	}

	// 3. Cleanup virtual nodes before compilation to avoid deparser crashes
	p.cleanupVirtualNodes(ast)

	// 4. Compile
	out, err := p.compiler.Compile(ast)
	if err != nil {
		return zero, fmt.Errorf("compiling AST: %w", err)
	}
	return out, nil
}

func (p *Processor[I, O]) cleanupVirtualNodes(tree *pg_query.ParseResult) {
	var cleanStmts []*pg_query.RawStmt
	for _, rawStmt := range tree.Stmts {
		// OBJECT_TYPE_UNDEFINED is used for virtual comment nodes that must not reach the deparser
		if commentStmt := rawStmt.Stmt.GetCommentStmt(); commentStmt != nil && commentStmt.Objtype == pg_query.ObjectType_OBJECT_TYPE_UNDEFINED {
			continue
		}
		cleanStmts = append(cleanStmts, rawStmt)
	}
	tree.Stmts = cleanStmts
}

// isNil checks if an interface value is nil or contains a nil pointer.
func isNil(i any) bool {
	if i == nil {
		return true
	}
	v := reflect.ValueOf(i)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Ptr, reflect.UnsafePointer, reflect.Interface, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
