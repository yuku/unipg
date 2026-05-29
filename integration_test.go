package unipg_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yuku/unipg"
	"github.com/yuku/unipg/compilers/stringify"
	"github.com/yuku/unipg/parsers/text"
	"github.com/yuku/unipg/transformers/extractfk"
	"github.com/yuku/unipg/transformers/reorder"
)

func TestIntegration_ParseCompile(t *testing.T) {
	parser := text.New()
	compiler := stringify.New()

	t.Run("basic passthrough", func(t *testing.T) {
		processor := unipg.New(parser, nil, compiler)
		input := "CREATE TABLE users (id INT PRIMARY KEY);"
		output, err := processor.Process(input)
		require.NoError(t, err)

		normalizedOutput := strings.ToUpper(output)
		require.Contains(t, normalizedOutput, "CREATE TABLE USERS")
		require.Contains(t, normalizedOutput, "ID")
		require.Contains(t, normalizedOutput, "INT")
		require.Contains(t, normalizedOutput, "PRIMARY KEY")
	})

	t.Run("multiple statements", func(t *testing.T) {
		processor := unipg.New(parser, nil, compiler)
		input := "CREATE TABLE a (id int); CREATE TABLE b (id int);"
		output, err := processor.Process(input)
		require.NoError(t, err)

		normalizedOutput := strings.ToUpper(output)
		require.Contains(t, normalizedOutput, "CREATE TABLE A")
		require.Contains(t, normalizedOutput, "CREATE TABLE B")
	})

	t.Run("extractfk transformer", func(t *testing.T) {
		processor := unipg.New(parser, []unipg.Transformer{extractfk.New()}, compiler)
		input := "CREATE TABLE users (id INT PRIMARY KEY, team_id INT REFERENCES teams(id));"
		output, err := processor.Process(input)
		require.NoError(t, err)

		normalizedOutput := strings.ToUpper(output)
		// Should have CREATE TABLE and ALTER TABLE
		require.Contains(t, normalizedOutput, "CREATE TABLE USERS")
		require.Contains(t, normalizedOutput, "ALTER TABLE USERS")
		require.Contains(t, normalizedOutput, "ADD FOREIGN KEY (TEAM_ID) REFERENCES TEAMS (ID)")
	})

	t.Run("reorder transformer", func(t *testing.T) {
		processor := unipg.New(parser, []unipg.Transformer{reorder.New()}, compiler)
		input := "CREATE VIEW v1 AS SELECT * FROM users; CREATE TABLE users (id INT);"
		output, err := processor.Process(input)
		require.NoError(t, err)

		normalizedOutput := strings.ToUpper(output)
		// CREATE TABLE should come before CREATE VIEW
		tablePos := strings.Index(normalizedOutput, "CREATE TABLE USERS")
		viewPos := strings.Index(normalizedOutput, "CREATE VIEW V1")
		require.True(t, tablePos < viewPos, "Table should be defined before view")
	})

	t.Run("full pipeline (extractfk + reorder)", func(t *testing.T) {
		processor := unipg.New(parser, []unipg.Transformer{
			extractfk.New(),
			reorder.New(),
		}, compiler)

		input := `
			CREATE TABLE users (
				id INT PRIMARY KEY,
				team_id INT REFERENCES teams(id)
			);
			CREATE TABLE teams (
				id INT PRIMARY KEY
			);
		`
		output, err := processor.Process(input)
		require.NoError(t, err)

		normalizedOutput := strings.ToUpper(output)
		// Order should be: CREATE TABLE users, CREATE TABLE teams, ALTER TABLE users
		// (Actually, reorder moves all ALTER to the very end)
		usersTablePos := strings.Index(normalizedOutput, "CREATE TABLE USERS")
		teamsTablePos := strings.Index(normalizedOutput, "CREATE TABLE TEAMS")
		alterPos := strings.Index(normalizedOutput, "ALTER TABLE USERS")

		require.True(t, usersTablePos < alterPos)
		require.True(t, teamsTablePos < alterPos)
	})
}
