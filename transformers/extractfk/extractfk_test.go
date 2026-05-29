package extractfk

import (
	"testing"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/stretchr/testify/require"
)

func TestTransformer_Transform(t *testing.T) {
	t.Parallel()

	transformer := New()

	t.Run("extract column-level foreign key", func(t *testing.T) {
		input := "CREATE TABLE users (id INT PRIMARY KEY, team_id INT REFERENCES teams(id));"
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expect 2 statements: [0] CREATE TABLE, [1] ALTER TABLE
		require.Len(t, result.Stmts, 2)

		// 1. Verify CREATE TABLE (FK should be removed from column constraints)
		createStmt := result.Stmts[0].Stmt.GetCreateStmt()
		require.NotNil(t, createStmt)
		columnDef := createStmt.TableElts[1].GetColumnDef()
		require.NotNil(t, columnDef)
		require.Empty(t, columnDef.Constraints, "Foreign key constraint should be removed from column definition")

		// 2. Verify ALTER TABLE
		alterStmt := result.Stmts[1].Stmt.GetAlterTableStmt()
		require.NotNil(t, alterStmt)
		require.Equal(t, "users", alterStmt.Relation.Relname)

		cmd := alterStmt.Cmds[0].GetAlterTableCmd()
		require.NotNil(t, cmd)
		require.Equal(t, pg_query.AlterTableType_AT_AddConstraint, cmd.Subtype)

		constraint := cmd.Def.GetConstraint()
		require.NotNil(t, constraint)
		require.Equal(t, pg_query.ConstrType_CONSTR_FOREIGN, constraint.Contype)
		require.Equal(t, "team_id", constraint.FkAttrs[0].GetString_().Sval, "Column name should be injected into standalone FK")
	})

	t.Run("extract table-level foreign key", func(t *testing.T) {
		input := "CREATE TABLE users (id INT PRIMARY KEY, team_id INT, FOREIGN KEY (team_id) REFERENCES teams(id));"
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expect 2 statements: [0] CREATE TABLE (with 2 columns), [1] ALTER TABLE
		require.Len(t, result.Stmts, 2)

		// 1. Verify CREATE TABLE
		createStmt := result.Stmts[0].Stmt.GetCreateStmt()
		require.NotNil(t, createStmt)
		require.Len(t, createStmt.TableElts, 2, "Table-level FK should be removed from TableElts")

		// 2. Verify ALTER TABLE
		alterStmt := result.Stmts[1].Stmt.GetAlterTableStmt()
		require.NotNil(t, alterStmt)
		constraint := alterStmt.Cmds[0].GetAlterTableCmd().Def.GetConstraint()
		require.Equal(t, "team_id", constraint.FkAttrs[0].GetString_().Sval)
	})

	t.Run("multiple foreign keys", func(t *testing.T) {
		input := "CREATE TABLE users (id INT PRIMARY KEY, a_id INT REFERENCES a(id), b_id INT REFERENCES b(id));"
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expect 3 statements: [0] CREATE TABLE, [1] ALTER TABLE (a_id), [2] ALTER TABLE (b_id)
		require.Len(t, result.Stmts, 3)
	})

	t.Run("no foreign keys", func(t *testing.T) {
		input := "CREATE TABLE users (id INT PRIMARY KEY);"
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expect only 1 statement (CREATE TABLE)
		require.Len(t, result.Stmts, 1)
	})
}
