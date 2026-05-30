package stringify

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

const indentWidth = "  "

func makeMap[T comparable](list ...T) map[T]struct{} {
	m := make(map[T]struct{}, len(list))
	for _, item := range list {
		m[item] = struct{}{}
	}
	return m
}

var majorKeywords = makeMap(
	"CREATE", "ALTER", "DROP", "SELECT", "FROM", "WHERE", "GROUP", "ORDER",
	"HAVING", "LIMIT", "OFFSET", "UNION", "VALUES", "INSERT", "UPDATE",
	"RETURNING", "JOIN", "LEFT", "RIGHT", "FULL", "INNER", "CROSS", "COMMENT",
	"ON", "USING",
)

var extraKeywordsToNormalize = makeMap(
	"ADD", "COLUMN", "CONSTRAINT", "RENAME", "TO", "MATERIALIZED", "FOREIGN",
	"DEFERRABLE", "INITIALLY", "IMMEDIATE", "DEFERRED", "CASCADE", "RESTRICT",
	"NO", "ACTION", "IF", "EXISTS",
)

var multilineStatementKeywords = makeMap(
	"CREATE", "ALTER", "INSERT", "VIEW",
)

var spaceBeforeParen = makeMap(
	"CHECK", "UNIQUE", "REFERENCES", "IN", "VALUES", "TABLE", "KEY", "USING",
	"ON", "SELECT", "FROM",
)

var commonTypesAndFuncs = makeMap(
	"TEXT", "BIGINT", "SMALLINT", "NUMERIC", "JSONB", "JSON", "UUID", "SERIAL",
	"BIGSERIAL", "BYTEA", "REAL", "DOUBLE", "PRECISION", "INTERVAL", "DATE",
	"TIME", "TIMESTAMP", "TIMESTAMPTZ", "BOOLEAN", "INET", "CIDR", "MACADDR",
	"BIT", "VARBIT", "XML", "INT", "INTEGER", "CHAR", "VARCHAR", "CHARACTER",
	"VARYING", "MAX", "MIN", "COUNT", "SUM", "AVG", "NOW", "BTREE", "GIN", "GIST",
	"BRIN",
)

var joinModifiers = makeMap(
	"LEFT", "RIGHT", "FULL", "INNER", "CROSS", "OUTER",
)

// Compiler compiles a PostgreSQL AST back into a SQL string.
type Compiler struct {
	pretty bool
}

// Option is a functional option for the Compiler.
type Option func(*Compiler)

// WithPretty enables pretty printing of the compiled SQL.
func WithPretty() Option {
	return func(c *Compiler) {
		c.pretty = true
	}
}

// New creates a new Compiler.
func New(opts ...Option) *Compiler {
	c := &Compiler{}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Compile implements unipg.Compiler.
func (c *Compiler) Compile(tree *pg_query.ParseResult) (string, error) {
	if tree == nil {
		return "", errors.New("tree is nil")
	}
	sql, err := pg_query.Deparse(tree)
	if err != nil {
		return "", fmt.Errorf("deparsing AST: %w", err)
	}

	if c.pretty {
		formatted, err := c.format(sql)
		if err != nil {
			return "", fmt.Errorf("formatting SQL: %w", err)
		}
		return formatted, nil
	}

	return sql, nil
}

func (c *Compiler) format(sql string) (string, error) {
	result, err := pg_query.Scan(sql)
	if err != nil {
		return "", err
	}
	var buf strings.Builder
	indent := 0
	var prevToken string
	var lastChar rune
	var rootKeyword string
	var multilineStack []int

	for i, token := range result.Tokens {
		t := sql[token.Start:token.End]
		upperT := strings.ToUpper(t)

		// Normalize keywords
		if token.KeywordKind == pg_query.KeywordKind_RESERVED_KEYWORD ||
			token.KeywordKind == pg_query.KeywordKind_TYPE_FUNC_NAME_KEYWORD {
			t = upperT
		} else {
			_, isMajor := majorKeywords[upperT]
			_, isCommon := commonTypesAndFuncs[upperT]
			_, isExtra := extraKeywordsToNormalize[upperT]
			if isMajor || isCommon || isExtra {
				t = upperT
			}
		}

		if rootKeyword == "" {
			if token.KeywordKind != pg_query.KeywordKind_NO_KEYWORD {
				rootKeyword = upperT
			}
		} else if rootKeyword == "CREATE" && upperT == "VIEW" {
			rootKeyword = "VIEW"
		}

		if t == "(" {
			if lastChar != 0 && lastChar != '\n' && lastChar != ' ' {
				if _, ok := spaceBeforeParen[prevToken]; indent == 0 || ok {
					buf.WriteByte(' ')
					lastChar = ' '
				}
			}
			buf.WriteString(t)
			lastChar = '('
			indent++

			shouldMultiline := false
			// Heuristic: top-level parens in specific statements, or following specific keywords
			_, isMultilineStatement := multilineStatementKeywords[rootKeyword]
			if isMultilineStatement {
				if indent == 1 {
					shouldMultiline = true
				} else if _, ok := spaceBeforeParen[prevToken]; ok {
					shouldMultiline = true
				}
			} else if _, ok := spaceBeforeParen[prevToken]; ok {
				// E.g. IN ( ... )
				shouldMultiline = true
			}

			if shouldMultiline {
				multilineStack = append(multilineStack, indent)
				buf.WriteByte('\n')
				buf.WriteString(strings.Repeat(indentWidth, indent))
				lastChar = '\n'
			}
			prevToken = "("
			continue
		}

		if t == ")" {
			if len(multilineStack) > 0 && multilineStack[len(multilineStack)-1] == indent {
				multilineStack = multilineStack[:len(multilineStack)-1]
				buf.WriteByte('\n')
				buf.WriteString(strings.Repeat(indentWidth, indent-1))
				lastChar = '\n'
			}
			indent--
			buf.WriteString(t)
			lastChar = ')'
			prevToken = ")"
			continue
		}

		if t == "," && len(multilineStack) > 0 && multilineStack[len(multilineStack)-1] == indent {
			buf.WriteString(t)
			buf.WriteByte('\n')
			buf.WriteString(strings.Repeat(indentWidth, indent))
			lastChar = '\n'
			prevToken = ","
			continue
		}

		if t == ";" {
			buf.WriteString(t)
			if i < len(result.Tokens)-1 {
				buf.WriteString("\n\n")
			} else {
				buf.WriteByte('\n')
			}
			lastChar = '\n'
			rootKeyword = ""
			multilineStack = nil
			prevToken = ";"
			continue
		}

		if _, ok := majorKeywords[upperT]; ok {
			_, isPrevJoinModifier := joinModifiers[prevToken]
			isJoinAfterModifier := (upperT == "JOIN" || upperT == "OUTER") && isPrevJoinModifier
			if lastChar != 0 && lastChar != '\n' && !isJoinAfterModifier {
				buf.WriteByte('\n')
				buf.WriteString(strings.Repeat(indentWidth, indent))
				lastChar = '\n'
			}
		}

		// Add leading space if needed
		if lastChar != 0 && lastChar != '\n' && lastChar != ' ' && lastChar != '(' &&
			t != "," && t != ";" && t != "." && lastChar != '.' {
			buf.WriteByte(' ')
			lastChar = ' '
		}
		buf.WriteString(t)
		if len(t) > 0 {
			lastChar, _ = utf8.DecodeLastRuneInString(t)
		}
		prevToken = upperT
	}
	return buf.String(), nil
}
