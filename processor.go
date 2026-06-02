// Package unipg provides a type-safe, plugin-based pipeline for processing
// PostgreSQL DDL (Data Definition Language) using an Abstract Syntax Tree (AST).
//
// It allows parsing SQL into an AST, applying a series of transformations (plugins),
// and compiling the result back into a desired output format.
package unipg

import (
	"errors"
	"fmt"
	"reflect"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// Parser takes an input of type I (Input) and parses it into a PostgreSQL AST.
// Implementations are typically found in the parsers/ subdirectory.
type Parser[I any] interface {
	// Parse converts the input into a ParseResult.
	Parse(input I) (*pg_query.ParseResult, error)
}

// Transformer mutates a PostgreSQL AST in place.
// Implementations (plugins) are typically found in the transformers/ subdirectory.
type Transformer interface {
	// Transform applies mutations to the provided AST.
	Transform(tree *pg_query.ParseResult) error
}

// Compiler takes a PostgreSQL AST and converts it to an output of type O (Output).
// Implementations are typically found in the compilers/ subdirectory.
type Compiler[O any] interface {
	// Compile generates the final output from the AST.
	Compile(tree *pg_query.ParseResult) (O, error)
}

// Processor is the core pipeline that coordinates the Parse, Transform, and Compile phases.
// It uses generics to maintain type safety for input (I) and output (O).
type Processor[I any, O any] struct {
	parser       Parser[I]
	transformers []Transformer
	compiler     Compiler[O]
}

// New creates a new Processor with the specified Parser, a sequence of Transformers, and a Compiler.
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

// Process executes the entire pipeline:
//
//  1. Parser.Parse(input)
//  2. Each Transformer.Transform(ast) sequentially
//  3. Cleanup of virtual nodes
//  4. Compiler.Compile(ast)
//
// If any stage returns an error, processing stops and the error is returned.
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

	// 3. Cleanup virtual nodes before compilation to avoid deparser crashes.
	// Virtual nodes (e.g. holding doc-comments) use ObjectType_UNDEFINED which
	// is not valid PostgreSQL DDL and causes the deparser to fail.
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
		// Skip "virtual" comment nodes used to hold doc-style comments.
		// These nodes use OBJECT_TYPE_UNDEFINED as a marker and must not reach the deparser/compiler
		// as they are not valid PostgreSQL DDL.
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
