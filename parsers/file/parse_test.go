package file

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/yuku/unipg/parsers/text"
)

func TestParser_Parse(t *testing.T) {
	tmpDir := t.TempDir()

	file1 := filepath.Join(tmpDir, "file1.sql")
	err := os.WriteFile(file1, []byte("SELECT 1;"), 0644)
	require.NoError(t, err)

	file2 := filepath.Join(tmpDir, "file2.sql")
	err = os.WriteFile(file2, []byte("SELECT 2;"), 0644)
	require.NoError(t, err)

	p := New()

	t.Run("single file", func(t *testing.T) {
		result, err := p.Parse([]string{file1})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Stmts, 1)
	})

	t.Run("multiple files", func(t *testing.T) {
		result, err := p.Parse([]string{file1, file2})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Stmts, 2)
	})

	t.Run("glob pattern", func(t *testing.T) {
		pattern := filepath.Join(tmpDir, "*.sql")
		result, err := p.Parse([]string{pattern})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Stmts, 2)
	})

	t.Run("duplicate files", func(t *testing.T) {
		// Same file passed twice should be deduplicated
		result, err := p.Parse([]string{file1, file1})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Stmts, 1)
	})

	t.Run("duplicate via glob and literal", func(t *testing.T) {
		pattern := filepath.Join(tmpDir, "*.sql")
		result, err := p.Parse([]string{pattern, file1})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, result.Stmts, 2) // file1, file2 (file1 is deduplicated)
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := p.Parse([]string{filepath.Join(tmpDir, "missing.sql")})
		require.Error(t, err)
		require.Contains(t, err.Error(), "file not found")
	})

	t.Run("missing file with closing bracket", func(t *testing.T) {
		// ']' alone is not a glob character, so it should trigger "file not found"
		_, err := p.Parse([]string{filepath.Join(tmpDir, "missing].sql")})
		require.Error(t, err)
		require.Contains(t, err.Error(), "file not found")
	})

	t.Run("empty list", func(t *testing.T) {
		result, err := p.Parse([]string{})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Empty(t, result.Stmts)
	})

	t.Run("with comments", func(t *testing.T) {
		file3 := filepath.Join(tmpDir, "file3.sql")
		err := os.WriteFile(file3, []byte("/** comment */\nSELECT 3;"), 0644)
		require.NoError(t, err)

		// Default parser should include comments
		result, err := p.Parse([]string{file3})
		require.NoError(t, err)
		require.Len(t, result.Stmts, 2) // CommentStmt + SelectStmt

		// Parser with WithoutComments should exclude comments
		p2 := New(text.WithoutComments())
		result2, err := p2.Parse([]string{file3})
		require.NoError(t, err)
		require.Len(t, result2.Stmts, 1) // Only SelectStmt
	})
}
