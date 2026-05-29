package unipg

import (
	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// mockParser implements Parser[string]
type mockParser struct {
	err    error
	result *pg_query.ParseResult
}

var _ Parser[string] = (*mockParser)(nil)

func (m *mockParser) Parse(input string) (*pg_query.ParseResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

// mockTransformer implements Transformer
type mockTransformer struct {
	err error
	fn  func(tree *pg_query.ParseResult)
}

var _ Transformer = (*mockTransformer)(nil)

func (m *mockTransformer) Transform(tree *pg_query.ParseResult) error {
	if m.err != nil {
		return m.err
	}
	if m.fn != nil {
		m.fn(tree)
	}
	return nil
}

// mockCompiler implements Compiler[string]
type mockCompiler struct {
	err    error
	result string
}

var _ Compiler[string] = (*mockCompiler)(nil)

func (m *mockCompiler) Compile(tree *pg_query.ParseResult) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.result, nil
}
