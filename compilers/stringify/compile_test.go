package stringify

import (
	"strings"
	"testing"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/stretchr/testify/require"
)

func TestCompiler_Compile(t *testing.T) {
	tests := []struct {
		name    string
		sql     string
		options []Option
		want    string
	}{
		{
			name: "plain DDL",
			sql:  "CREATE TABLE users (id serial PRIMARY KEY, name text NOT NULL)",
			want: "CREATE TABLE users (id serial PRIMARY KEY, name text NOT NULL)",
		},
		{
			name:    "pretty DDL",
			sql:     "CREATE TABLE users (id serial PRIMARY KEY, name text NOT NULL)",
			options: []Option{WithPretty()},
			want: dedent(`
				CREATE TABLE users (
				  id SERIAL PRIMARY KEY,
				  NAME TEXT NOT NULL
				)`),
		},
		{
			name: "complex table",
			sql: dedent(`
				CREATE TABLE customers (
					id int PRIMARY KEY,
					display_name varchar(50) NOT NULL,
					subscription_period int NOT NULL DEFAULT 6 CHECK (subscription_period > 0),
					privacy_policy_id int NOT NULL REFERENCES privacy_policies (id) ON DELETE RESTRICT DEFERRABLE INITIALLY IMMEDIATE,
					created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP
				)`),
			options: []Option{WithPretty()},
			want: dedent(`
				CREATE TABLE customers (
				  id INT PRIMARY KEY,
				  display_name VARCHAR(50) NOT NULL,
				  subscription_period INT NOT NULL DEFAULT 6 CHECK (
				    subscription_period > 0
				  ),
				  privacy_policy_id INT NOT NULL REFERENCES privacy_policies(id)
				  ON DELETE RESTRICT DEFERRABLE INITIALLY IMMEDIATE,
				  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
				)`),
		},
		{
			name:    "unique index with function",
			sql:     "CREATE UNIQUE INDEX customers_metadata_region_uniq ON customers (jsonb_extract_path_text(metadata, 'region', 'id'))",
			options: []Option{WithPretty()},
			want: dedent(`
				CREATE UNIQUE INDEX customers_metadata_region_uniq
				ON customers
				USING BTREE (
				  jsonb_extract_path_text(metadata, 'region', 'id')
				)`),
		},
		{
			name:    "alter table add constraint",
			sql:     "ALTER TABLE pricing_rules ADD CONSTRAINT pricing_rules_pricing_id_rank_unique UNIQUE (pricing_id, \"rank\") DEFERRABLE INITIALLY DEFERRED",
			options: []Option{WithPretty()},
			want: dedent(`
				ALTER TABLE pricing_rules ADD CONSTRAINT pricing_rules_pricing_id_rank_unique UNIQUE (
				  pricing_id,
				  rank
				) DEFERRABLE INITIALLY DEFERRED`),
		},
		{
			name:    "create view",
			sql:     "CREATE VIEW active_customers AS SELECT id, metadata FROM customers WHERE metadata IS NOT NULL",
			options: []Option{WithPretty()},
			want: dedent(`
				CREATE VIEW active_customers AS
				SELECT id, metadata
				FROM customers
				WHERE metadata IS NOT NULL`),
		},
		{
			name:    "create extension",
			sql:     "CREATE EXTENSION IF NOT EXISTS \"uuid-ossp\"",
			options: []Option{WithPretty()},
			want:    "CREATE EXTENSION IF NOT EXISTS \"uuid-ossp\"",
		},
		{
			name: "composite primary key",
			sql: dedent(`
				CREATE TABLE employee_profiles (
					employee_id int REFERENCES employees (id) ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
					profile_key varchar(50),
					created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
					PRIMARY KEY (employee_id, profile_key)
				)`),
			options: []Option{WithPretty()},
			want: dedent(`
				CREATE TABLE employee_profiles (
				  employee_id INT REFERENCES employees(id)
				  ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
				  profile_key VARCHAR(50),
				  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
				  PRIMARY KEY (
				    employee_id,
				    profile_key
				  )
				)`),
		},
		{
			name: "complex table with constraints and types",
			sql: dedent(`
				CREATE TABLE office_locations (
					id int NOT NULL PRIMARY KEY,
					owner_id int NOT NULL REFERENCES owners (id) ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
					address_line1 varchar(50) NOT NULL,
					region_code varchar(9) NOT NULL REFERENCES regions (code) ON DELETE RESTRICT DEFERRABLE INITIALLY IMMEDIATE,
					created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
					updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
					CONSTRAINT office_locations_owner_id_name_unique UNIQUE (owner_id, address_line1),
					CONSTRAINT office_locations_id_owner_id_unique UNIQUE (id, owner_id)
				)`),
			options: []Option{WithPretty()},
			want: dedent(`
				CREATE TABLE office_locations (
				  id INT NOT NULL PRIMARY KEY,
				  owner_id INT NOT NULL REFERENCES owners(id)
				  ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
				  address_line1 VARCHAR(50) NOT NULL,
				  region_code VARCHAR(9) NOT NULL REFERENCES regions(code)
				  ON DELETE RESTRICT DEFERRABLE INITIALLY IMMEDIATE,
				  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
				  updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
				  CONSTRAINT office_locations_owner_id_name_unique UNIQUE (
				    owner_id,
				    address_line1
				  ),
				  CONSTRAINT office_locations_id_owner_id_unique UNIQUE (
				    id,
				    owner_id
				  )
				)`),
		},
		{
			name:    "multiple statements",
			sql:     "CREATE TABLE users (id int); CREATE TABLE posts (id int)",
			options: []Option{WithPretty()},
			want: dedent(`
				CREATE TABLE users (
				  id INT
				);

				CREATE TABLE posts (
				  id INT
				)`),
		},
		{
			name: "create view with joins and subquery",
			sql: dedent(`
				CREATE VIEW users_with_last_activity AS
				SELECT u.user_id, ua.user_activity_type, ua.acted_at
				FROM
					all_users AS u
					LEFT JOIN (
						SELECT
							user_id,
							MAX(acted_at) AS acted_at
						FROM
							user_activities
						WHERE
							user_activity_type = 'suspend'
						GROUP BY
							user_id
					) AS last_user_activities USING (user_id)
					LEFT JOIN user_activities AS ua USING (user_id, acted_at)
				WHERE
					u.is_deleted = false`),
			options: []Option{WithPretty()},
			want: dedent(`
				CREATE VIEW users_with_last_activity AS
				SELECT u.user_id, ua.user_activity_type, ua.acted_at
				FROM all_users u
				LEFT JOIN (
				  SELECT user_id,
				  MAX(acted_at) AS acted_at
				  FROM user_activities
				  WHERE user_activity_type = 'suspend'
				  GROUP BY user_id
				) last_user_activities
				USING (
				  user_id
				)
				LEFT JOIN user_activities ua
				USING (
				  user_id,
				  acted_at
				)
				WHERE u.is_deleted = FALSE`),
		},
		{
			name:    "create view with distinct",
			sql:     "CREATE VIEW organization_tags AS SELECT DISTINCT tag FROM organization_taggings",
			options: []Option{WithPretty()},
			want: dedent(`
				CREATE VIEW organization_tags AS
				SELECT DISTINCT tag
				FROM organization_taggings`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New(tt.options...)
			result, err := pg_query.Parse(tt.sql)
			require.NoError(t, err)

			got, err := c.Compile(result)
			require.NoError(t, err)
			require.Equal(t, strings.TrimSpace(tt.want), strings.TrimSpace(got))
		})
	}

	t.Run("nil tree", func(t *testing.T) {
		c := New()
		sql, err := c.Compile(nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "tree is nil")
		require.Empty(t, sql)
	})
}

// dedent removes the common leading whitespace from every line in s.
func dedent(s string) string {
	s = strings.TrimPrefix(s, "\n")
	s = strings.TrimSuffix(s, "\n")
	lines := strings.Split(s, "\n")
	if len(lines) == 0 {
		return ""
	}

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

	var buf strings.Builder
	for i, line := range lines {
		if i > 0 {
			buf.WriteByte('\n')
		}
		if len(line) > minIndent {
			buf.WriteString(line[minIndent:])
		} else {
			buf.WriteString(strings.TrimSpace(line))
		}
	}
	return buf.String()
}
