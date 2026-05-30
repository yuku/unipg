package stringify

import (
	"errors"
	"fmt"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

const indentWidth = "  "

var keywords = map[string]bool{
	"CREATE": true, "TABLE": true, "PRIMARY": true, "KEY": true,
	"NOT": true, "NULL": true, "UNIQUE": true, "CHECK": true,
	"DEFAULT": true, "REFERENCES": true, "ON": true, "DELETE": true,
	"RESTRICT": true, "CASCADE": true, "DEFERRABLE": true, "INITIALLY": true,
	"IMMEDIATE": true, "DEFERRED": true, "INDEX": true, "USING": true,
	"VIEW": true, "AS": true, "SELECT": true, "FROM": true,
	"WHERE": true, "AND": true, "OR": true, "IN": true,
	"IS": true, "TIMESTAMP": true, "TIMESTAMPTZ": true, "VARCHAR": true,
	"CHAR": true, "INT": true, "DATE": true, "BOOLEAN": true,
	"FALSE": true, "TRUE": true, "ALTER": true, "ADD": true,
	"CONSTRAINT": true, "EXTENSION": true, "IF": true, "EXISTS": true,
	"BTREE": true, "CURRENT_TIMESTAMP": true, "INSERT": true, "UPDATE": true,
	"DROP": true, "GROUP": true, "ORDER": true, "HAVING": true,
	"LIMIT": true, "OFFSET": true, "UNION": true, "VALUES": true,
	"RETURNING": true, "DISTINCT": true, "LEFT": true, "RIGHT": true,
	"FULL": true, "INNER": true, "JOIN": true, "BY": true,
	"MAX": true, "MIN": true, "COUNT": true, "SUM": true, "AVG": true,
}

var majorKeywords = map[string]bool{
	"CREATE": true, "ALTER": true, "DROP": true, "SELECT": true,
	"FROM": true, "WHERE": true, "GROUP": true, "ORDER": true,
	"HAVING": true, "LIMIT": true, "OFFSET": true, "UNION": true,
	"VALUES": true, "INSERT": true, "UPDATE": true, "RETURNING": true,
	"JOIN": true, "LEFT": true, "RIGHT": true, "FULL": true, "INNER": true, "CROSS": true,
}

var spaceBeforeParen = map[string]bool{
	"CHECK": true, "UNIQUE": true, "REFERENCES": true, "IN": true,
	"VALUES": true, "TABLE": true, "KEY": true, "USING": true,
}

var joinModifiers = map[string]bool{
	"LEFT": true, "RIGHT": true, "FULL": true, "INNER": true, "CROSS": true, "OUTER": true,
}

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

		if keywords[upperT] {
			t = upperT
		}

		if rootKeyword == "" && keywords[upperT] {
			rootKeyword = upperT
		}

		if t == "(" {
			if lastChar != 0 && lastChar != '\n' && lastChar != ' ' {
				if indent == 0 || spaceBeforeParen[prevToken] {
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
				if spaceBeforeParen[prevToken] {
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

		if majorKeywords[upperT] {
			isJoinAfterModifier := (upperT == "JOIN" || upperT == "OUTER") && joinModifiers[prevToken]
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
