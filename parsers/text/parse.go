package text

import (
	"fmt"
	"sort"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// Parser parses SQL strings into a PostgreSQL AST.
type Parser struct {
	excludeComments bool
}

// Option is a functional option for configuring the Parser.
type Option func(*Parser)

// WithoutComments disables extraction of comments from the source SQL.
func WithoutComments() Option {
	return func(p *Parser) {
		p.excludeComments = true
	}
}

// New creates a new Parser with the given options.
func New(opts ...Option) *Parser {
	p := &Parser{}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Parse implements unipg.Parser.
func (p *Parser) Parse(input string) (*pg_query.ParseResult, error) {
	result, err := pg_query.Parse(input)
	if err != nil {
		return nil, fmt.Errorf("parsing SQL: %w", err)
	}

	if !p.excludeComments {
		if err := p.injectComments(input, result); err != nil {
			return nil, fmt.Errorf("injecting comments: %w", err)
		}
	}

	return result, nil
}

func (p *Parser) injectComments(input string, result *pg_query.ParseResult) error {
	scanResult, err := pg_query.Scan(input)
	if err != nil {
		return err
	}

	// 1. Identify actual statement starts and ends (excluding leading/trailing comments and whitespace)
	// We also record the intervals of actual statements to avoid extracting internal comments.
	type interval struct{ start, end int32 }
	var stmtIntervals []interval

	for _, stmt := range result.Stmts {
		stmtStart := int(stmt.StmtLocation)
		stmtEnd := stmtStart + int(stmt.StmtLen)
		if stmtEnd > len(input) {
			stmtEnd = len(input)
		}

		var actualStart, actualEnd int32
		foundStart := false

		for _, token := range scanResult.Tokens {
			if token.Start >= int32(stmtStart) && token.Start < int32(stmtEnd) {
				if token.Token != pg_query.Token_C_COMMENT && token.Token != pg_query.Token_SQL_COMMENT {
					if !foundStart {
						actualStart = token.Start
						foundStart = true
					}
					actualEnd = token.End
				}
			}
		}

		if foundStart {
			stmt.StmtLocation = actualStart
			stmtIntervals = append(stmtIntervals, interval{actualStart, actualEnd})
		}
	}

	var commentStmts []*pg_query.RawStmt
	for _, token := range scanResult.Tokens {
		if token.Token == pg_query.Token_C_COMMENT || token.Token == pg_query.Token_SQL_COMMENT {
			// Check if the comment is inside any statement
			isInternal := false
			for _, inv := range stmtIntervals {
				if token.Start >= inv.start && token.Start < inv.end {
					isInternal = true
					break
				}
			}
			if isInternal {
				continue
			}

			commentText := input[token.Start:token.End]

			virtualComment := &pg_query.CommentStmt{
				Objtype: pg_query.ObjectType_OBJECT_TYPE_UNDEFINED,
				Comment: commentText,
			}

			commentStmts = append(commentStmts, &pg_query.RawStmt{
				Stmt: &pg_query.Node{
					Node: &pg_query.Node_CommentStmt{
						CommentStmt: virtualComment,
					},
				},
				StmtLocation: token.Start,
				StmtLen:      token.End - token.Start,
			})
		}
	}

	if len(commentStmts) == 0 {
		return nil
	}

	// Merge and sort
	allStmts := append(result.Stmts, commentStmts...)
	sort.SliceStable(allStmts, func(i, j int) bool {
		if allStmts[i].StmtLocation != allStmts[j].StmtLocation {
			return allStmts[i].StmtLocation < allStmts[j].StmtLocation
		}
		// Prioritize CommentStmt if they share the same location
		_, iIsComment := allStmts[i].Stmt.GetNode().(*pg_query.Node_CommentStmt)
		_, jIsComment := allStmts[j].Stmt.GetNode().(*pg_query.Node_CommentStmt)
		if iIsComment && !jIsComment {
			return true
		}
		return false
	})

	result.Stmts = allStmts
	return nil
}
