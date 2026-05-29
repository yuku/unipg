package unipg

import (
	"errors"
	"testing"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/stretchr/testify/require"
)

func TestProcessor_Process(t *testing.T) {
	t.Run("successful passthrough without transformers", func(t *testing.T) {
		parser := &mockParser{result: &pg_query.ParseResult{}}
		compiler := &mockCompiler{result: "output"}
		processor := New(parser, nil, compiler)

		got, err := processor.Process("input")
		require.NoError(t, err)
		require.Equal(t, "output", got)
	})

	t.Run("successful transformation", func(t *testing.T) {
		parser := &mockParser{result: &pg_query.ParseResult{}}
		compiler := &mockCompiler{result: "transformed"}

		transformed := false
		transformer := &mockTransformer{
			fn: func(tree *pg_query.ParseResult) {
				transformed = true
			},
		}

		processor := New(parser, []Transformer{transformer}, compiler)

		got, err := processor.Process("input")
		require.NoError(t, err)
		require.Equal(t, "transformed", got)
		require.True(t, transformed)
	})

	t.Run("parser error", func(t *testing.T) {
		errParser := errors.New("parse error")
		parser := &mockParser{err: errParser}
		processor := New(parser, nil, &mockCompiler{})

		_, err := processor.Process("input")
		require.ErrorIs(t, err, errParser)
		require.Contains(t, err.Error(), "parsing input")
	})

	t.Run("transformer error", func(t *testing.T) {
		errTransform := errors.New("transform error")
		transformer := &mockTransformer{err: errTransform}
		processor := New(&mockParser{result: &pg_query.ParseResult{}}, []Transformer{transformer}, &mockCompiler{})

		_, err := processor.Process("input")
		require.ErrorIs(t, err, errTransform)
		require.Contains(t, err.Error(), "transforming AST at index 0")
	})

	t.Run("compiler error", func(t *testing.T) {
		errStringify := errors.New("stringify error")
		compiler := &mockCompiler{err: errStringify}
		processor := New(&mockParser{result: &pg_query.ParseResult{}}, nil, compiler)

		_, err := processor.Process("input")
		require.ErrorIs(t, err, errStringify)
		require.Contains(t, err.Error(), "stringifying AST")
	})

	t.Run("nil parser error", func(t *testing.T) {
		processor := New[string, string](nil, nil, &mockCompiler{})
		_, err := processor.Process("input")
		require.Error(t, err)
		require.Contains(t, err.Error(), "parser is not initialized")
	})

	t.Run("nil compiler error", func(t *testing.T) {
		processor := New[string, string](&mockParser{result: &pg_query.ParseResult{}}, nil, nil)
		_, err := processor.Process("input")
		require.Error(t, err)
		require.Contains(t, err.Error(), "compiler is not initialized")
	})

	t.Run("parser returns nil result", func(t *testing.T) {
		parser := &mockParser{result: nil}
		processor := New(parser, nil, &mockCompiler{})

		_, err := processor.Process("input")
		require.Error(t, err)
		require.Contains(t, err.Error(), "parser returned nil result")
	})

	t.Run("nil transformer in slice", func(t *testing.T) {
		parser := &mockParser{result: &pg_query.ParseResult{}}
		processor := New(parser, []Transformer{nil}, &mockCompiler{})

		_, err := processor.Process("input")
		require.Error(t, err)
		require.Contains(t, err.Error(), "transformer at index 0 is nil")
	})
}
