package unipg_test

import (
	"strings"
	"testing"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/stretchr/testify/require"
	"github.com/yuku/unipg"
	"github.com/yuku/unipg/compilers/stringify"
	"github.com/yuku/unipg/parsers/text"
	"github.com/yuku/unipg/transformers/comment"
	"github.com/yuku/unipg/transformers/extractfk"
	"github.com/yuku/unipg/transformers/reorder"
)

func TestIntegration(t *testing.T) {
	compiler := stringify.New()
	defaultParser := text.New()

	tests := []struct {
		name         string
		parser       unipg.Parser[string] // Optional override
		transformers []unipg.Transformer
		input        string
		want         string
	}{
		{
			name: "basic passthrough",
			input: `
				CREATE TABLE users (id INT PRIMARY KEY);
			`,
			want: `
				CREATE TABLE users (id int PRIMARY KEY)
			`,
		},
		{
			name: "multiple statements passthrough",
			input: `
				CREATE TABLE a (id int);
				CREATE TABLE b (id int);
			`,
			want: `
				CREATE TABLE a (id int);
				CREATE TABLE b (id int);
			`,
		},
		{
			name:         "extractfk: column-level",
			transformers: []unipg.Transformer{extractfk.New()},
			input: `
				CREATE TABLE users (id INT PRIMARY KEY, team_id INT REFERENCES teams(id));
			`,
			want: `
				CREATE TABLE users (id int PRIMARY KEY, team_id int);
				ALTER TABLE users ADD FOREIGN KEY (team_id) REFERENCES teams (id)
			`,
		},
		{
			name:         "reorder: view and table",
			transformers: []unipg.Transformer{reorder.New()},
			input: `
				CREATE VIEW v1 AS SELECT * FROM users;
				CREATE TABLE users (id INT);
			`,
			want: `
				CREATE TABLE users (id int);
				CREATE VIEW v1 AS SELECT * FROM users;
			`,
		},
		{
			name:         "full pipeline: extractfk and reorder",
			transformers: []unipg.Transformer{extractfk.New(), reorder.New()},
			input: `
				CREATE TABLE users (
					id INT PRIMARY KEY,
					team_id INT REFERENCES teams(id)
				);
				CREATE TABLE teams (
					id INT PRIMARY KEY
				);
			`,
			want: `
				CREATE TABLE users (id int PRIMARY KEY, team_id int);
				CREATE TABLE teams (id int PRIMARY KEY);
				ALTER TABLE users ADD FOREIGN KEY (team_id) REFERENCES teams (id);
			`,
		},
		{
			name:         "complex reordering: views with dependencies",
			transformers: []unipg.Transformer{reorder.New()},
			input: `
				CREATE VIEW v3 AS SELECT * FROM v2;
				CREATE TABLE t1 (id int);
				CREATE VIEW v1 AS SELECT * FROM t1;
				CREATE VIEW v2 AS SELECT * FROM v1;
			`,
			want: `
				CREATE TABLE t1 (id int);
				CREATE VIEW v1 AS SELECT * FROM t1;
				CREATE VIEW v2 AS SELECT * FROM v1;
				CREATE VIEW v3 AS SELECT * FROM v2;
			`,
		},
		{
			name: "comments present but no comment transformer",
			input: `
				/** this comment should be ignored or safely handled */
				CREATE TABLE users (id int);
			`,
			want: `
				CREATE TABLE users (id int);
			`,
		},
		{
			name:         "comment transformer: table and view comments",
			transformers: []unipg.Transformer{comment.New()},
			input: `
				/** users table */
				CREATE TABLE users (id int);
				
				/** active users view */
				CREATE VIEW active_users AS SELECT * FROM users;
			`,
			want: `
				CREATE TABLE users (id int);
				COMMENT ON TABLE users IS 'users table';
				CREATE VIEW active_users AS SELECT * FROM users;
				COMMENT ON VIEW active_users IS 'active users view';
			`,
		},
		{
			name:         "comment transformer: JSDoc style and column comments",
			transformers: []unipg.Transformer{comment.New()},
			input: dedent(`
				/**
				 * first line
				 * second line
				 */
				CREATE TABLE users (
					/** user ID */
					id int,
					name text /** user name */
				);
			`),
			want: dedent(`
				CREATE TABLE users (id int, name text);
				COMMENT ON TABLE users IS 'first line
				second line';
				COMMENT ON COLUMN users.id IS 'user ID';
				COMMENT ON COLUMN users.name IS 'user name';
			`),
		},
		{
			name:         "comment transformer: multi-line without asterisks",
			transformers: []unipg.Transformer{comment.New()},
			input: dedent(`
				/**
				  Multi-line comment
				  without leading asterisks
				*/
				CREATE TABLE users (id int);
			`),
			want: dedent(`
				CREATE TABLE users (id int);
				COMMENT ON TABLE users IS 'Multi-line comment
				without leading asterisks';
			`),
		},
		{
			name:         "ignore non-doc comments",
			transformers: []unipg.Transformer{comment.New()},
			input: `
				/* regular comment */
				-- line comment
				CREATE TABLE users (id int);
			`,
			want: `
				CREATE TABLE users (id int);
			`,
		},
		{
			name:         "WithoutComments with comment transformer",
			parser:       text.New(text.WithoutComments()),
			transformers: []unipg.Transformer{comment.New()},
			input: `
				/** this comment should be ignored because parser excludes it */
				CREATE TABLE users (id int);
			`,
			want: `
				CREATE TABLE users (id int);
			`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.parser
			if p == nil {
				p = defaultParser
			}
			processor := unipg.New(p, tt.transformers, compiler)
			got, err := processor.Process(tt.input)
			require.NoError(t, err)

			require.Equal(t, normalizeSQL(t, tt.want), normalizeSQL(t, got))
		})
	}
}

// normalizeSQL uses pg_query to parse and deparse SQL for canonical comparison.
func normalizeSQL(t *testing.T, s string) string {
	t.Helper()
	result, err := pg_query.Parse(s)
	require.NoError(t, err, "failed to parse SQL for normalization: %s", s)
	out, err := pg_query.Deparse(result)
	require.NoError(t, err, "failed to deparse SQL for normalization")
	return strings.TrimSpace(out)
}

// dedent removes the common leading whitespace from every line in s.
func dedent(s string) string {
	s = strings.TrimPrefix(s, "\n")
	s = strings.TrimSuffix(s, "\n")
	lines := strings.Split(s, "\n")
	if len(lines) == 0 {
		return s
	}

	// Find minimum indentation
	minIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := 0
		for _, r := range line {
			if r == ' ' || r == '\t' {
				indent++
			} else {
				break
			}
		}
		if minIndent == -1 || indent < minIndent {
			minIndent = indent
		}
	}

	if minIndent <= 0 {
		return s
	}

	// Remove indent
	for i, line := range lines {
		if len(line) >= minIndent {
			lines[i] = line[minIndent:]
		}
	}

	return strings.Join(lines, "\n")
}
