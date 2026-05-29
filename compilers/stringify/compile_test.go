package stringify

import (
	"testing"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/stretchr/testify/require"
)

func TestCompiler_Compile(t *testing.T) {
	c := New()

	t.Run("valid AST", func(t *testing.T) {
		// Create a simple AST for "SELECT 1"
		result, err := pg_query.Parse("SELECT 1")
		require.NoError(t, err)

		sql, err := c.Compile(result)
		require.NoError(t, err)
		require.Equal(t, "SELECT 1", sql)
	})

	t.Run("nil tree", func(t *testing.T) {
		sql, err := c.Compile(nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "tree is nil")
		require.Empty(t, sql)
	})
}
