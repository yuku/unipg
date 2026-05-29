package text

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParser_Parse(t *testing.T) {
	p := New()

	t.Run("valid SQL", func(t *testing.T) {
		input := "SELECT 1;"
		result, err := p.Parse(input)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotEmpty(t, result.Stmts)
	})

	t.Run("invalid SQL", func(t *testing.T) {
		input := "SELECT FROM;"
		result, err := p.Parse(input)
		require.Error(t, err)
		require.Contains(t, err.Error(), "parsing SQL")
		require.Nil(t, result)
	})

	t.Run("empty input", func(t *testing.T) {
		input := ""
		result, err := p.Parse(input)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Empty(t, result.Stmts)
	})
}
