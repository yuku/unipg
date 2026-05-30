package file

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/yuku/unipg/parsers/text"
)

// Parser reads SQL from files and parses it into a PostgreSQL AST.
type Parser struct {
	textParser *text.Parser
}

// New creates a new Parser.
// Options are passed to the underlying text.Parser.
func New(opts ...text.Option) *Parser {
	return &Parser{
		textParser: text.New(opts...),
	}
}

// Parse implements unipg.Parser[[]string].
// It expands glob patterns in the input paths, reads the contents of all matching files,
// and parses the concatenated content. Duplicate files are automatically excluded
// based on their absolute paths.
func (p *Parser) Parse(paths []string) (*pg_query.ParseResult, error) {
	var allFiles []string
	seen := make(map[string]bool)

	for _, path := range paths {
		matches, err := filepath.Glob(path)
		if err != nil {
			return nil, fmt.Errorf("expanding glob %q: %w", path, err)
		}

		// If path doesn't contain glob characters and no match found,
		// it's likely a missing file that the user expected to exist.
		if len(matches) == 0 && !strings.ContainsAny(path, "*?[") {
			if _, err := os.Stat(path); err != nil {
				return nil, fmt.Errorf("file not found: %w", err)
			}
		}

		for _, match := range matches {
			abs, err := filepath.Abs(match)
			if err != nil {
				return nil, fmt.Errorf("getting absolute path for %q: %w", match, err)
			}
			if !seen[abs] {
				seen[abs] = true
				allFiles = append(allFiles, abs)
			}
		}
	}

	var totalSize int64
	for _, file := range allFiles {
		info, err := os.Stat(file)
		if err != nil {
			return nil, fmt.Errorf("stat file %q: %w", file, err)
		}
		totalSize += info.Size()
	}

	var sb strings.Builder
	sb.Grow(int(totalSize) + len(allFiles))
	for _, file := range allFiles {
		content, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("reading file %q: %w", file, err)
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.Write(content)
	}

	return p.textParser.Parse(sb.String())
}
