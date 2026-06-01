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

	t.Run("extract column-level foreign key with deferrable", func(t *testing.T) {
		input := "CREATE TABLE users (id INT PRIMARY KEY, organization_id INT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE);"
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expect 2 statements: [0] CREATE TABLE, [1] ALTER TABLE
		require.Len(t, result.Stmts, 2)

		// 1. Verify CREATE TABLE (FK and its attributes should be removed)
		createStmt := result.Stmts[0].Stmt.GetCreateStmt()
		require.NotNil(t, createStmt)
		columnDef := createStmt.TableElts[1].GetColumnDef()
		require.NotNil(t, columnDef)
		// Only NOT NULL constraint should remain (contype 2)
		require.Len(t, columnDef.Constraints, 1)
		require.Equal(t, pg_query.ConstrType_CONSTR_NOTNULL, columnDef.Constraints[0].GetConstraint().Contype)

		// 2. Verify ALTER TABLE
		alterStmt := result.Stmts[1].Stmt.GetAlterTableStmt()
		require.NotNil(t, alterStmt)
		constraint := alterStmt.Cmds[0].GetAlterTableCmd().Def.GetConstraint()
		require.True(t, constraint.Deferrable, "Deferrable should be moved to ALTER TABLE")
		require.False(t, constraint.Initdeferred, "Initdeferred should be false (INITIALLY IMMEDIATE)")
	})

	t.Run("extract column-level foreign key with initially deferred", func(t *testing.T) {
		input := "CREATE TABLE users (id INT PRIMARY KEY, organization_id INT REFERENCES organizations(id) DEFERRABLE INITIALLY DEFERRED);"
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		alterStmt := result.Stmts[1].Stmt.GetAlterTableStmt()
		constraint := alterStmt.Cmds[0].GetAlterTableCmd().Def.GetConstraint()
		require.True(t, constraint.Deferrable)
		require.True(t, constraint.Initdeferred, "Initdeferred should be true (INITIALLY DEFERRED)")
	})

	t.Run("extract named column-level foreign key", func(t *testing.T) {
		input := "CREATE TABLE users (id INT PRIMARY KEY, team_id INT CONSTRAINT fk_users_teams REFERENCES teams(id));"
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		require.Len(t, result.Stmts, 2)
		alterStmt := result.Stmts[1].Stmt.GetAlterTableStmt()
		constraint := alterStmt.Cmds[0].GetAlterTableCmd().Def.GetConstraint()
		require.Equal(t, "fk_users_teams", constraint.Conname)
	})

	t.Run("reveal constraint name", func(t *testing.T) {
		transformer := New(WithRevealConstraintName())
		input := "CREATE TABLE users (id INT PRIMARY KEY, team_id INT REFERENCES teams(id));"
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		alterStmt := result.Stmts[1].Stmt.GetAlterTableStmt()
		constraint := alterStmt.Cmds[0].GetAlterTableCmd().Def.GetConstraint()
		require.Equal(t, "users_team_id_fkey", constraint.Conname)
	})

	t.Run("reveal constraint name with long identifiers", func(t *testing.T) {
		transformer := New(WithRevealConstraintName())
		// 30 chars + 30 chars -> should be balanced
		input := "CREATE TABLE t123456789012345678901234567890 (c123456789012345678901234567890 INT REFERENCES teams(id));"
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		alterStmt := result.Stmts[1].Stmt.GetAlterTableStmt()
		constraint := alterStmt.Cmds[0].GetAlterTableCmd().Def.GetConstraint()
		// targetTotal = 63 - 5 - 1 = 57. half = 28.
		// table: 30 -> 28. cols: 30 -> 29.
		require.Equal(t, "t123456789012345678901234567_c1234567890123456789012345678_fkey", constraint.Conname)
		require.LessOrEqual(t, len(constraint.Conname), 63)
	})

	t.Run("reveal constraint name with multi-byte characters", func(t *testing.T) {
		transformer := New(WithRevealConstraintName())
		input := "CREATE TABLE テーブル名がとても長い場合にどうなるかテストするためのテーブル (カラム名もとても長い場合にどうなるかテストするためのカラム INT REFERENCES t1(id));"
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		alterStmt := result.Stmts[1].Stmt.GetAlterTableStmt()
		constraint := alterStmt.Cmds[0].GetAlterTableCmd().Def.GetConstraint()
		// Matches the behavior observed in real Postgres
		require.Equal(t, "テーブル名がとても_カラム名もとても長_fkey", constraint.Conname)
		require.LessOrEqual(t, len(constraint.Conname), 63)
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
