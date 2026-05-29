package unipg_test

import (
	"strings"
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

		normalizedOutput := strings.ToUpper(output)
		require.Contains(t, normalizedOutput, "CREATE TABLE USERS")
		require.Contains(t, normalizedOutput, "ID")
		require.Contains(t, normalizedOutput, "INT")
		require.Contains(t, normalizedOutput, "PRIMARY KEY")
	})

	t.Run("multiple statements", func(t *testing.T) {
		input := "CREATE TABLE a (id int); CREATE TABLE b (id int);"
		output, err := processor.Process(input)
		require.NoError(t, err)

		normalizedOutput := strings.ToUpper(output)
		require.Contains(t, normalizedOutput, "CREATE TABLE A")
		require.Contains(t, normalizedOutput, "CREATE TABLE B")
	})
}
