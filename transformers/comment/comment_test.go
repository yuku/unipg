package comment

import (
	"testing"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/stretchr/testify/require"
	"github.com/yuku/unipg/parsers/text"
)

func TestTransformer_Transform(t *testing.T) {
	parser := text.New()
	transformer := New()

	t.Run("table comment", func(t *testing.T) {
		input := "/** users table */ CREATE TABLE users (id int);"
		result, err := parser.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expected 2 statements: [0] CREATE TABLE, [1] COMMENT ON TABLE
		require.Len(t, result.Stmts, 2)
		require.NotNil(t, result.Stmts[0].Stmt.GetCreateStmt())

		commentStmt := result.Stmts[1].Stmt.GetCommentStmt()
		require.NotNil(t, commentStmt)
		require.Equal(t, "users table", commentStmt.Comment)
		require.Equal(t, pg_query.ObjectType_OBJECT_TABLE, commentStmt.Objtype)
	})

	t.Run("view comment", func(t *testing.T) {
		input := "/** user view */\nCREATE VIEW v1 AS SELECT 1;"
		result, err := parser.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		require.Len(t, result.Stmts, 2)
		commentStmt := result.Stmts[1].Stmt.GetCommentStmt()
		require.Equal(t, "user view", commentStmt.Comment)
		require.Equal(t, pg_query.ObjectType_OBJECT_VIEW, commentStmt.Objtype)
	})

	t.Run("merged comments", func(t *testing.T) {
		input := "/** line 1 */ /** line 2 */\nCREATE TABLE t1 (id int);"
		result, err := parser.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		require.Len(t, result.Stmts, 2)
		require.Equal(t, "line 1\nline 2", result.Stmts[1].Stmt.GetCommentStmt().Comment)
	})

	t.Run("discard comment for unsupported target", func(t *testing.T) {
		input := "/** index comment */ CREATE INDEX idx1 ON t1(id); CREATE TABLE t1 (id int);"
		result, err := parser.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Comment should be discarded because CREATE INDEX is not a supported target,
		// and it shouldn't leak to the subsequent CREATE TABLE.
		require.Len(t, result.Stmts, 2)
		require.NotNil(t, result.Stmts[0].Stmt.GetIndexStmt())
		require.NotNil(t, result.Stmts[1].Stmt.GetCreateStmt())
	})

	t.Run("unattached comment (no target)", func(t *testing.T) {
		input := "CREATE TABLE t1 (id int); /** end comment */"
		result, err := parser.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		require.Len(t, result.Stmts, 1)
	})

	t.Run("column comment does not leak to next statement", func(t *testing.T) {
		input := dedent(`
			CREATE TABLE users (
				id int /** user ID */
			);
			CREATE VIEW active_users AS SELECT * FROM users;
		`)
		result, err := parser.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expected 4 statements:
		// [0] CREATE TABLE
		// [1] COMMENT ON COLUMN users.id
		// [2] CREATE VIEW
		// (No comment on view)
		require.Len(t, result.Stmts, 3)
		require.NotNil(t, result.Stmts[0].Stmt.GetCreateStmt())

		c1 := result.Stmts[1].Stmt.GetCommentStmt()
		require.Equal(t, pg_query.ObjectType_OBJECT_COLUMN, c1.Objtype)
		require.Equal(t, "user ID", c1.Comment)

		require.NotNil(t, result.Stmts[2].Stmt.GetViewStmt())
	})
}
