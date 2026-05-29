package unipg_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yuku/unipg"
	"github.com/yuku/unipg/compilers/stringify"
	"github.com/yuku/unipg/parsers/text"
)

func TestIntegration_ParseCompile(t *testing.T) {
	parser := text.New()
	compiler := stringify.New()
	processor := unipg.New(parser, nil, compiler)

	t.Run("basic passthrough", func(t *testing.T) {
		input := "CREATE TABLE users (id INT PRIMARY KEY);"
		output, err := processor.Process(input)
		require.NoError(t, err)
		// pg_query.Deparse might normalize the SQL (e.g., adding/removing whitespace or changing casing)
		// but for simple cases it should be equivalent.
		require.Contains(t, output, "CREATE TABLE users")
		require.Contains(t, output, "id")
		require.Contains(t, output, "int")
		require.Contains(t, output, "PRIMARY KEY")
	})

	t.Run("multiple statements", func(t *testing.T) {
		input := "CREATE TABLE a (id int); CREATE TABLE b (id int);"
		output, err := processor.Process(input)
		require.NoError(t, err)
		require.Contains(t, output, "CREATE TABLE a")
		require.Contains(t, output, "CREATE TABLE b")
	})
}
