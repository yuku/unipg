package unipg_test

import (
	"strings"
	"testing"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/stretchr/testify/require"
	"github.com/yuku/unipg"
	"github.com/yuku/unipg/compilers/stringify"
	"github.com/yuku/unipg/parsers/text"
	"github.com/yuku/unipg/transformers/extractfk"
	"github.com/yuku/unipg/transformers/reorder"
)

func TestIntegration(t *testing.T) {
	t.Parallel()

	parser := text.New()
	compiler := stringify.New()

	testCases := []struct {
		name         string
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			processor := unipg.New(parser, tc.transformers, compiler)
			got, err := processor.Process(tc.input)
			require.NoError(t, err)

			require.Equal(t, normalizeSQL(t, tc.want), normalizeSQL(t, got))
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
