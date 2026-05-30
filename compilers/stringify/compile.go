package stringify

import (
	"errors"
	"fmt"
	"strings"

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

var keywords = makeMap(
	"CREATE", "TABLE", "PRIMARY", "KEY",
	"NOT", "NULL", "UNIQUE", "CHECK",
	"DEFAULT", "REFERENCES", "ON", "DELETE",
	"RESTRICT", "CASCADE", "DEFERRABLE", "INITIALLY",
	"IMMEDIATE", "DEFERRED", "INDEX", "USING",
	"VIEW", "AS", "SELECT", "FROM",
	"WHERE", "AND", "OR", "IN",
	"IS", "TIMESTAMP", "TIMESTAMPTZ", "VARCHAR",
	"CHAR", "INT", "DATE", "BOOLEAN",
	"FALSE", "TRUE", "ALTER", "ADD",
	"CONSTRAINT", "EXTENSION", "IF", "EXISTS",
	"BTREE", "CURRENT_TIMESTAMP", "INSERT", "UPDATE",
	"DROP", "GROUP", "ORDER", "HAVING",
	"LIMIT", "OFFSET", "UNION", "VALUES",
	"RETURNING", "DISTINCT", "LEFT", "RIGHT",
	"FULL", "INNER", "JOIN", "BY",
	"MAX", "MIN", "COUNT", "SUM", "AVG",
)

var majorKeywords = makeMap(
	"CREATE", "ALTER", "DROP", "SELECT",
	"FROM", "WHERE", "GROUP", "ORDER",
	"HAVING", "LIMIT", "OFFSET", "UNION",
	"VALUES", "INSERT", "UPDATE", "RETURNING",
	"JOIN", "LEFT", "RIGHT", "FULL", "INNER", "CROSS",
)

var spaceBeforeParen = makeMap(
	"CHECK", "UNIQUE", "REFERENCES", "IN",
	"VALUES", "TABLE", "KEY", "USING",
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
		return c.format(sql), nil
	}

	return sql, nil
}

func (c *Compiler) format(sql string) string {
	result, err := pg_query.Scan(sql)
	if err != nil {
		return sql
	}
	var buf strings.Builder
	indent := 0
	var prevToken string
	var lastChar rune
	var rootKeyword string
	var multilineDepth int

	for i, token := range result.Tokens {
		t := sql[token.Start:token.End]
		upperT := strings.ToUpper(t)

		if _, ok := keywords[upperT]; ok {
			t = upperT
		}

		if rootKeyword == "" {
			if _, ok := keywords[upperT]; ok {
				rootKeyword = upperT
			}
		}

		if t == "(" {
			if lastChar != 0 && lastChar != '\n' && lastChar != ' ' {
				_, isSpaceBeforeParen := spaceBeforeParen[prevToken]
				if indent == 0 || isSpaceBeforeParen {
					buf.WriteByte(' ')
					lastChar = ' '
				}
			}
			buf.WriteString(t)
			lastChar = '('
			indent++

			shouldMultiline := false
			if indent == 1 {
				if rootKeyword == "CREATE" || rootKeyword == "ALTER" || rootKeyword == "INSERT" {
					shouldMultiline = true
				}
				if _, ok := spaceBeforeParen[prevToken]; ok {
					shouldMultiline = true
				}
			}

			if shouldMultiline {
				multilineDepth = indent
				buf.WriteByte('\n')
				buf.WriteString(strings.Repeat(indentWidth, indent))
				lastChar = '\n'
			}
			prevToken = "("
			continue
		}

		if t == ")" {
			if indent == multilineDepth {
				buf.WriteByte('\n')
				buf.WriteString(strings.Repeat(indentWidth, indent-1))
				lastChar = '\n'
				multilineDepth = 0
			}
			indent--
			buf.WriteString(t)
			lastChar = ')'
			prevToken = ")"
			continue
		}

		if t == "," && indent == multilineDepth && multilineDepth > 0 {
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
			multilineDepth = 0
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

		if lastChar != 0 && lastChar != '\n' && lastChar != ' ' && lastChar != '(' &&
			t != "," && t != ";" && t != "." && lastChar != '.' {
			buf.WriteByte(' ')
			lastChar = ' '
		}
		buf.WriteString(t)
		if len(t) > 0 {
			lastChar = rune(t[len(t)-1])
		}
		prevToken = upperT
	}
	return buf.String()
}
