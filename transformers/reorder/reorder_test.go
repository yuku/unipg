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

	t.Run("move function called in check before create table", func(t *testing.T) {
		input := `
			CREATE TABLE users (id INT, name TEXT CHECK (is_valid_name(name)));
			CREATE OR REPLACE FUNCTION is_valid_name(n TEXT) RETURNS BOOLEAN AS $$ SELECT n <> '' $$ LANGUAGE sql;
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		require.Len(t, result.Stmts, 2)
		require.Equal(t, "is_valid_name", funcName(result.Stmts[0]))
		require.NotNil(t, result.Stmts[1].Stmt.GetCreateStmt())
	})

	t.Run("move function right before the first table calling it", func(t *testing.T) {
		input := `
			CREATE SCHEMA app;
			CREATE TABLE app.users (id INT DEFAULT app.f1());
			CREATE TABLE app.posts (id INT CHECK (id > app.f2()));
			CREATE TABLE app.tags (id INT DEFAULT app.f1());
			CREATE OR REPLACE FUNCTION app.f1() RETURNS INT AS $$ SELECT 1 $$ LANGUAGE sql;
			CREATE OR REPLACE FUNCTION app.f2() RETURNS INT AS $$ SELECT 2 $$ LANGUAGE sql;
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expected: CREATE SCHEMA, f1, users, f2, posts, tags
		require.Len(t, result.Stmts, 6)
		require.NotNil(t, result.Stmts[0].Stmt.GetCreateSchemaStmt())
		require.Equal(t, "f1", funcName(result.Stmts[1]))
		require.Equal(t, "users", result.Stmts[2].Stmt.GetCreateStmt().Relation.Relname)
		require.Equal(t, "f2", funcName(result.Stmts[3]))
		require.Equal(t, "posts", result.Stmts[4].Stmt.GetCreateStmt().Relation.Relname)
		require.Equal(t, "tags", result.Stmts[5].Stmt.GetCreateStmt().Relation.Relname)
	})

	t.Run("move nested function calls and keep their relative order", func(t *testing.T) {
		input := `
			CREATE TABLE users (id INT CHECK (f2(f1(id))));
			CREATE FUNCTION f1(n INT) RETURNS INT AS $$ SELECT n $$ LANGUAGE sql;
			CREATE OR REPLACE FUNCTION f2(n INT) RETURNS BOOLEAN AS $$ SELECT n > 0 $$ LANGUAGE sql;
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expected: f1, f2, users
		require.Len(t, result.Stmts, 3)
		require.Equal(t, "f1", funcName(result.Stmts[0]))
		require.Equal(t, "f2", funcName(result.Stmts[1]))
		require.NotNil(t, result.Stmts[2].Stmt.GetCreateStmt())
	})

	t.Run("move function called in generated column", func(t *testing.T) {
		input := `
			CREATE TABLE users (id INT, doubled INT GENERATED ALWAYS AS (double_it(id)) STORED);
			CREATE FUNCTION double_it(n INT) RETURNS INT IMMUTABLE AS $$ SELECT n * 2 $$ LANGUAGE sql;
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		require.Len(t, result.Stmts, 2)
		require.Equal(t, "double_it", funcName(result.Stmts[0]))
		require.NotNil(t, result.Stmts[1].Stmt.GetCreateStmt())
	})

	t.Run("do not move functions not called by any table", func(t *testing.T) {
		input := `
			CREATE TABLE users (id INT);
			CREATE FUNCTION all_users() RETURNS SETOF users AS $$ SELECT * FROM users $$ LANGUAGE sql;
			CREATE FUNCTION count_users() RETURNS BIGINT BEGIN ATOMIC SELECT count(*) FROM users; END;
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		require.Len(t, result.Stmts, 3)
		require.NotNil(t, result.Stmts[0].Stmt.GetCreateStmt())
		require.Equal(t, "all_users", funcName(result.Stmts[1]))
		require.Equal(t, "count_users", funcName(result.Stmts[2]))
	})

	t.Run("do not move function already defined before the table", func(t *testing.T) {
		input := `
			CREATE TYPE mood AS ENUM ('happy', 'sad');
			CREATE FUNCTION default_mood() RETURNS mood AS $$ SELECT 'happy'::mood $$ LANGUAGE sql;
			CREATE TABLE users (id INT, m mood DEFAULT default_mood());
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		require.Len(t, result.Stmts, 3)
		require.NotNil(t, result.Stmts[0].Stmt.GetCreateEnumStmt())
		require.Equal(t, "default_mood", funcName(result.Stmts[1]))
		require.NotNil(t, result.Stmts[2].Stmt.GetCreateStmt())
	})

	t.Run("do not move function across set statement", func(t *testing.T) {
		input := `
			CREATE TABLE users (id INT DEFAULT f());
			SET search_path = app;
			CREATE FUNCTION f() RETURNS INT AS $$ SELECT 1 $$ LANGUAGE sql;
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		require.Len(t, result.Stmts, 3)
		require.NotNil(t, result.Stmts[0].Stmt.GetCreateStmt())
		require.NotNil(t, result.Stmts[1].Stmt.GetVariableSetStmt())
		require.Equal(t, "f", funcName(result.Stmts[2]))
	})

	t.Run("match schema-qualified function names exactly", func(t *testing.T) {
		input := `
			CREATE TABLE users (id INT DEFAULT app.f());
			CREATE FUNCTION other.f() RETURNS INT AS $$ SELECT 1 $$ LANGUAGE sql;
			CREATE FUNCTION app.f() RETURNS INT AS $$ SELECT 1 $$ LANGUAGE sql;
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expected: app.f, users, other.f
		require.Len(t, result.Stmts, 3)
		require.Equal(t, "app", result.Stmts[0].Stmt.GetCreateFunctionStmt().Funcname[0].GetString_().Sval)
		require.NotNil(t, result.Stmts[1].Stmt.GetCreateStmt())
		require.Equal(t, "other", result.Stmts[2].Stmt.GetCreateFunctionStmt().Funcname[0].GetString_().Sval)
	})

	t.Run("do not move function already defined before its caller", func(t *testing.T) {
		input := `
			CREATE FUNCTION f() RETURNS INT AS $$ SELECT 1 $$ LANGUAGE sql;
			CREATE TABLE t (x INT DEFAULT f());
			CREATE OR REPLACE FUNCTION f() RETURNS INT BEGIN ATOMIC SELECT count(*)::int FROM t; END;
			CREATE FUNCTION f(arg t) RETURNS INT AS $$ SELECT 1 $$ LANGUAGE sql;
		`
		requireUnchanged(t, transformer, input)
	})

	t.Run("move only the first definition after the caller", func(t *testing.T) {
		input := `
			CREATE TABLE t (x INT DEFAULT f());
			CREATE FUNCTION f() RETURNS INT AS $$ SELECT 1 $$ LANGUAGE sql;
			CREATE FUNCTION f(arg t) RETURNS INT AS $$ SELECT 1 $$ LANGUAGE sql;
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		require.Len(t, result.Stmts, 3)
		require.Empty(t, result.Stmts[0].Stmt.GetCreateFunctionStmt().Parameters)
		require.NotNil(t, result.Stmts[1].Stmt.GetCreateStmt())
		require.Len(t, result.Stmts[2].Stmt.GetCreateFunctionStmt().Parameters, 1)
	})

	t.Run("move functions within each set statement region", func(t *testing.T) {
		input := `
			SET search_path = a;
			CREATE TABLE t1 (x INT DEFAULT f());
			CREATE FUNCTION f() RETURNS INT AS $$ SELECT 1 $$ LANGUAGE sql;
			SET search_path = b;
			CREATE TABLE t2 (x INT DEFAULT f());
			CREATE FUNCTION f() RETURNS INT AS $$ SELECT 2 $$ LANGUAGE sql;
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expected: SET a, f, t1, SET b, f, t2
		require.Len(t, result.Stmts, 6)
		require.NotNil(t, result.Stmts[0].Stmt.GetVariableSetStmt())
		require.Equal(t, "f", funcName(result.Stmts[1]))
		require.Equal(t, "t1", result.Stmts[2].Stmt.GetCreateStmt().Relation.Relname)
		require.NotNil(t, result.Stmts[3].Stmt.GetVariableSetStmt())
		require.Equal(t, "f", funcName(result.Stmts[4]))
		require.Equal(t, "t2", result.Stmts[5].Stmt.GetCreateStmt().Relation.Relname)
	})

	t.Run("move functions called from the body of a moved function", func(t *testing.T) {
		for _, f := range []string{
			"CREATE FUNCTION f() RETURNS INT LANGUAGE sql RETURN helper();",
			"CREATE FUNCTION f() RETURNS INT AS $$ SELECT helper() $$ LANGUAGE sql;",
		} {
			input := `
				CREATE TABLE t (x INT DEFAULT f());
				CREATE FUNCTION helper() RETURNS INT LANGUAGE sql RETURN 1;
			` + f
			result, err := pg_query.Parse(input)
			require.NoError(t, err)

			err = transformer.Transform(result)
			require.NoError(t, err)

			// Expected: helper, f, t
			require.Len(t, result.Stmts, 3)
			require.Equal(t, "helper", funcName(result.Stmts[0]))
			require.Equal(t, "f", funcName(result.Stmts[1]))
			require.NotNil(t, result.Stmts[2].Stmt.GetCreateStmt())
		}
	})

	t.Run("move functions called from parameter defaults of a moved function", func(t *testing.T) {
		input := `
			CREATE TABLE t (x INT DEFAULT f());
			CREATE FUNCTION helper() RETURNS INT LANGUAGE sql RETURN 1;
			CREATE FUNCTION f(x INT DEFAULT helper()) RETURNS INT LANGUAGE sql RETURN x;
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expected: helper, f, t
		require.Len(t, result.Stmts, 3)
		require.Equal(t, "helper", funcName(result.Stmts[0]))
		require.Equal(t, "f", funcName(result.Stmts[1]))
		require.NotNil(t, result.Stmts[2].Stmt.GetCreateStmt())
	})

	t.Run("move an already moved dependency before an earlier caller", func(t *testing.T) {
		input := `
			CREATE TABLE t1 (x INT DEFAULT f());
			CREATE TABLE t2 (x INT DEFAULT helper());
			CREATE FUNCTION helper() RETURNS INT LANGUAGE sql RETURN 1;
			CREATE FUNCTION f() RETURNS INT LANGUAGE sql RETURN helper();
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expected: helper, f, t1, t2
		require.Len(t, result.Stmts, 4)
		require.Equal(t, "helper", funcName(result.Stmts[0]))
		require.Equal(t, "f", funcName(result.Stmts[1]))
		require.Equal(t, "t1", result.Stmts[2].Stmt.GetCreateStmt().Relation.Relname)
		require.Equal(t, "t2", result.Stmts[3].Stmt.GetCreateStmt().Relation.Relname)
	})

	t.Run("do not confuse qualified names with quoted names containing a dot", func(t *testing.T) {
		input := `
			CREATE TABLE t (x INT DEFAULT app.f());
			CREATE FUNCTION "app.f"() RETURNS INT AS $$ SELECT 1 $$ LANGUAGE sql;
		`
		requireUnchanged(t, transformer, input)
	})

	t.Run("place views after functions they call", func(t *testing.T) {
		input := `
			CREATE VIEW v AS SELECT f() AS x;
			CREATE MATERIALIZED VIEW mv AS SELECT f() AS x;
			CREATE FUNCTION f() RETURNS INT LANGUAGE sql RETURN 1;
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expected: f, mv, v
		require.Len(t, result.Stmts, 3)
		require.Equal(t, "f", funcName(result.Stmts[0]))
		require.Equal(t, "mv", result.Stmts[1].Stmt.GetCreateTableAsStmt().Into.Rel.Relname)
		require.Equal(t, "v", result.Stmts[2].Stmt.GetViewStmt().View.Relname)
	})

	t.Run("keep statements that depend on a view after it", func(t *testing.T) {
		for _, input := range []string{
			`CREATE VIEW v AS SELECT 1 AS x;
			CREATE FUNCTION g() RETURNS SETOF v LANGUAGE sql BEGIN ATOMIC SELECT * FROM v; END;`,
			`CREATE VIEW v AS SELECT 1 AS x;
			CREATE FUNCTION g() RETURNS INT AS $$ SELECT x FROM v $$ LANGUAGE sql;`,
			`CREATE TABLE t (id INT);
			CREATE MATERIALIZED VIEW mv AS SELECT id FROM t;
			CREATE UNIQUE INDEX mv_id ON mv (id);`,
			`CREATE VIEW v AS SELECT 1 AS x;
			COMMENT ON VIEW v IS 'hello';
			COMMENT ON COLUMN v.x IS 'world';`,
			`CREATE VIEW v AS SELECT 1 AS x;
			GRANT SELECT ON v TO r;`,
			`CREATE VIEW v AS SELECT 1 AS x;
			CREATE TABLE t (y v);`,
			`CREATE FUNCTION tf() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NULL; END $$;
			CREATE VIEW v AS SELECT 1 AS x;
			CREATE TRIGGER tr INSTEAD OF INSERT ON v FOR EACH ROW EXECUTE FUNCTION tf();`,
		} {
			requireUnchanged(t, transformer, input)
		}
	})

	t.Run("move statements that depend on a view together with it", func(t *testing.T) {
		input := `
			CREATE MATERIALIZED VIEW mv AS SELECT id FROM t;
			CREATE INDEX mv_id ON mv (id);
			CREATE TABLE t (id INT);
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expected: t, mv, index
		require.Len(t, result.Stmts, 3)
		require.Equal(t, "t", result.Stmts[0].Stmt.GetCreateStmt().Relation.Relname)
		require.Equal(t, "mv", result.Stmts[1].Stmt.GetCreateTableAsStmt().Into.Rel.Relname)
		require.Equal(t, "mv_id", result.Stmts[2].Stmt.GetIndexStmt().Idxname)
	})

	t.Run("sort views and their dependents together", func(t *testing.T) {
		input := `
			CREATE VIEW v2 AS SELECT * FROM g();
			CREATE VIEW v1 AS SELECT 1 AS x;
			CREATE FUNCTION g() RETURNS SETOF v1 LANGUAGE sql BEGIN ATOMIC SELECT * FROM v1; END;
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expected: v1, g, v2
		require.Len(t, result.Stmts, 3)
		require.Equal(t, "v1", result.Stmts[0].Stmt.GetViewStmt().View.Relname)
		require.Equal(t, "g", funcName(result.Stmts[1]))
		require.Equal(t, "v2", result.Stmts[2].Stmt.GetViewStmt().View.Relname)
	})

	t.Run("do not move views and alters across set statements", func(t *testing.T) {
		input := `
			SET search_path = a;
			CREATE VIEW v AS SELECT f() AS x;
			ALTER TABLE t ADD COLUMN y INT;
			CREATE TABLE t (id INT);
			CREATE FUNCTION f() RETURNS INT LANGUAGE sql RETURN 1;
			SET search_path = b;
			CREATE TABLE u (id INT);
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		// Expected: SET a, t, f, v, ALTER t, SET b, u
		require.Len(t, result.Stmts, 7)
		require.NotNil(t, result.Stmts[0].Stmt.GetVariableSetStmt())
		require.Equal(t, "t", result.Stmts[1].Stmt.GetCreateStmt().Relation.Relname)
		require.Equal(t, "f", funcName(result.Stmts[2]))
		require.Equal(t, "v", result.Stmts[3].Stmt.GetViewStmt().View.Relname)
		require.NotNil(t, result.Stmts[4].Stmt.GetAlterTableStmt())
		require.NotNil(t, result.Stmts[5].Stmt.GetVariableSetStmt())
		require.Equal(t, "u", result.Stmts[6].Stmt.GetCreateStmt().Relation.Relname)
	})

	t.Run("do not move create or replace procedure", func(t *testing.T) {
		input := `
			CREATE TABLE users (id INT);
			CREATE OR REPLACE PROCEDURE p() AS $$ SELECT 1 $$ LANGUAGE sql;
		`
		result, err := pg_query.Parse(input)
		require.NoError(t, err)

		err = transformer.Transform(result)
		require.NoError(t, err)

		require.Len(t, result.Stmts, 2)
		require.NotNil(t, result.Stmts[0].Stmt.GetCreateStmt())
		require.NotNil(t, result.Stmts[1].Stmt.GetCreateFunctionStmt())
	})
}

// funcName returns the unqualified name of a CREATE FUNCTION statement.
func funcName(stmt *pg_query.RawStmt) string {
	names := stmt.Stmt.GetCreateFunctionStmt().Funcname
	return names[len(names)-1].GetString_().Sval
}

// requireUnchanged asserts that transforming input keeps the statement order.
func requireUnchanged(t *testing.T, transformer *Transformer, input string) {
	t.Helper()

	expected, err := pg_query.Parse(input)
	require.NoError(t, err)
	want, err := pg_query.Deparse(expected)
	require.NoError(t, err)

	result, err := pg_query.Parse(input)
	require.NoError(t, err)
	require.NoError(t, transformer.Transform(result))
	got, err := pg_query.Deparse(result)
	require.NoError(t, err)

	require.Equal(t, want, got)
}
