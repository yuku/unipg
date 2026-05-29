package reorder

import (
	"testing"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/stretchr/testify/require"
)

func TestTransformer_Transform(t *testing.T) {
	t.Parallel()

	transformer := New()

	t.Run("basic reordering", func(t *testing.T) {
		input := "ALTER TABLE users ADD COLUMN age INT; CREATE TABLE users (id INT); CREATE VIEW v1 AS SELECT 1;"
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expected order: CREATE TABLE, CREATE VIEW, ALTER TABLE
		require.Len(t, result.Stmts, 3)
		require.NotNil(t, result.Stmts[0].Stmt.GetCreateStmt())
		require.NotNil(t, result.Stmts[1].Stmt.GetViewStmt())
		require.NotNil(t, result.Stmts[2].Stmt.GetAlterTableStmt())
	})

	t.Run("view topological sorting", func(t *testing.T) {
		// v3 depends on v2, v2 depends on v1
		// Input order: v3, v2, v1
		input := `
			CREATE VIEW v3 AS SELECT * FROM v2;
			CREATE VIEW v2 AS SELECT * FROM v1;
			CREATE VIEW v1 AS SELECT * FROM users;
			CREATE TABLE users (id INT);
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expected order: users (table), v1, v2, v3
		require.Len(t, result.Stmts, 4)
		require.Equal(t, "users", result.Stmts[0].Stmt.GetCreateStmt().Relation.Relname)
		require.Equal(t, "v1", result.Stmts[1].Stmt.GetViewStmt().View.Relname)
		require.Equal(t, "v2", result.Stmts[2].Stmt.GetViewStmt().View.Relname)
		require.Equal(t, "v3", result.Stmts[3].Stmt.GetViewStmt().View.Relname)
	})

	t.Run("circular dependency error", func(t *testing.T) {
		input := `
			CREATE VIEW v1 AS SELECT * FROM v2;
			CREATE VIEW v2 AS SELECT * FROM v1;
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.Error(t, err)
		require.Contains(t, err.Error(), "circular dependency detected among views: v1, v2")
	})

	t.Run("preserve relative order of alters", func(t *testing.T) {
		input := `
			ALTER TABLE users ADD COLUMN age INT;
			CREATE TABLE users (id INT);
			ALTER TABLE users ADD COLUMN name TEXT;
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expected: CREATE TABLE, ALTER (age), ALTER (name)
		require.Len(t, result.Stmts, 3)
		require.NotNil(t, result.Stmts[0].Stmt.GetCreateStmt())

		alter1 := result.Stmts[1].Stmt.GetAlterTableStmt()
		require.Equal(t, "age", alter1.Cmds[0].GetAlterTableCmd().Def.GetColumnDef().Colname)

		alter2 := result.Stmts[2].Stmt.GetAlterTableStmt()
		require.Equal(t, "name", alter2.Cmds[0].GetAlterTableCmd().Def.GetColumnDef().Colname)
	})

	t.Run("handle materialized views", func(t *testing.T) {
		input := `
			CREATE MATERIALIZED VIEW mv1 AS SELECT * FROM v1;
			CREATE VIEW v1 AS SELECT 1;
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		require.Len(t, result.Stmts, 2)
		require.Equal(t, "v1", result.Stmts[0].Stmt.GetViewStmt().View.Relname)
		require.Equal(t, "mv1", result.Stmts[1].Stmt.GetCreateTableAsStmt().Into.Rel.Relname)
	})

	t.Run("multi-schema support", func(t *testing.T) {
		input := `
			CREATE VIEW s2.v1 AS SELECT * FROM s1.v1;
			CREATE VIEW s1.v1 AS SELECT 1;
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		require.Len(t, result.Stmts, 2)
		v1 := result.Stmts[0].Stmt.GetViewStmt().View
		require.Equal(t, "s1", v1.Schemaname)
		require.Equal(t, "v1", v1.Relname)

		v2 := result.Stmts[1].Stmt.GetViewStmt().View
		require.Equal(t, "s2", v2.Schemaname)
		require.Equal(t, "v1", v2.Relname)
	})

	t.Run("deterministic output for independent views", func(t *testing.T) {
		input := `
			CREATE VIEW b AS SELECT 1;
			CREATE VIEW a AS SELECT 1;
			CREATE VIEW c AS SELECT 1;
		`
		for i := 0; i < 5; i++ {
			result, err := pg_query.Parse(input)
			require.NoError(t, err)

			err = transformer.Transform(result)
			require.NoError(t, err)

			// Should always be a, b, c (sorted by name)
			require.Equal(t, "a", result.Stmts[0].Stmt.GetViewStmt().View.Relname)
			require.Equal(t, "b", result.Stmts[1].Stmt.GetViewStmt().View.Relname)
			require.Equal(t, "c", result.Stmts[2].Stmt.GetViewStmt().View.Relname)
		}
	})
}
