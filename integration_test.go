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

	tests := []struct {
		name         string
		parser       unipg.Parser[string]
		transformers []unipg.Transformer
		input        string
		want         string
	}{
		{
			name:   "basic passthrough",
			parser: text.New(),
			input: `
				CREATE TABLE users (id INT PRIMARY KEY);
			`,
			want: `
				CREATE TABLE users (id int PRIMARY KEY)
			`,
		},
		{
			name:   "multiple statements passthrough",
			parser: text.New(),
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
			parser:       text.New(),
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
			parser:       text.New(),
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
			parser:       text.New(),
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
			parser:       text.New(),
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
			name:   "comments present but no comment transformer",
			parser: text.New(),
			// Note: no comment transformer here
			transformers: nil,
			input: `
				/* this comment should be ignored or safely handled */
				CREATE TABLE users (id int);
			`,
			want: `
				CREATE TABLE users (id int);
			`,
		},
		{
			name:         "comment transformer: table and view comments",
			parser:       text.New(),
			transformers: []unipg.Transformer{comment.New()},
			input: `
				/* users table */
				CREATE TABLE users (id int);
				
				-- active users view
				CREATE VIEW active_users AS SELECT * FROM users;
			`,
			want: `
				CREATE TABLE users (id int);
				COMMENT ON TABLE users IS 'users table';
				CREATE VIEW active_users AS SELECT * FROM users;
				COMMENT ON VIEW active_users IS 'active users view';
			`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := unipg.New(tt.parser, tt.transformers, compiler)
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
	require.NoError(t, err, "failed to parse SQL for normalization")
	out, err := pg_query.Deparse(result)
	require.NoError(t, err, "failed to deparse SQL for normalization")
	return strings.TrimSpace(out)
}
