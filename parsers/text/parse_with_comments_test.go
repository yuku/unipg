package text

import (
	"testing"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/stretchr/testify/require"
)

func TestParser_Parse_Comments(t *testing.T) {
	t.Run("comments enabled by default", func(t *testing.T) {
		p := New()
		input := "/** comment */ CREATE TABLE users (id int);"
		result, err := p.Parse(input)
		require.NoError(t, err)

		// Should have 2 statements: [0] CommentStmt, [1] CreateStmt
		require.Len(t, result.Stmts, 2)

		c1 := result.Stmts[0].Stmt.GetCommentStmt()
		require.NotNil(t, c1)
		require.Equal(t, "/** comment */", c1.Comment)
		require.Equal(t, pg_query.ObjectType_OBJECT_TYPE_UNDEFINED, c1.Objtype)

		require.NotNil(t, result.Stmts[1].Stmt.GetCreateStmt())
	})

	t.Run("with comments disabled via WithoutComments", func(t *testing.T) {
		p := New(WithoutComments())
		input := "/* table comment */ CREATE TABLE users (id int); -- end"
		result, err := p.Parse(input)
		require.NoError(t, err)

		// Should only have 1 statement: CreateStmt
		require.Len(t, result.Stmts, 1)
		require.NotNil(t, result.Stmts[0].Stmt.GetCreateStmt())
	})
}
