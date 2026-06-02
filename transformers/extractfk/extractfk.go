// Package extractfk provides a Transformer that extracts inline foreign key constraints
// and moves them into standalone ALTER TABLE ADD CONSTRAINT statements.
package extractfk

import (
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

const pgMaxIdentifierLen = 63

// Transformer extracts inline foreign key constraints from CREATE TABLE statements
// and converts them into standalone ALTER TABLE statements. This helps in managing
// table dependencies by allowing foreign keys to be defined after all tables are created.
type Transformer struct {
	revealConstraintName bool
}

// Option is a functional option for configuring the Transformer.
type Option func(*Transformer)

// WithRevealConstraintName enables automatic generation of constraint names for
// anonymous foreign keys. Names are generated using a PostgreSQL-compatible format:
// {tablename}_{columnname}_fkey. If the generated name exceeds 63 bytes, it is
// automatically truncated while preserving UTF-8 integrity.
func WithRevealConstraintName() Option {
	return func(t *Transformer) {
		t.revealConstraintName = true
	}
}

// New creates a new ExtractFK Transformer with the given options.
func New(opts ...Option) *Transformer {
	t := &Transformer{}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// Transform implements unipg.Transformer. It scans CREATE TABLE statements in the AST,
// extracts foreign key constraints, and appends them as ALTER TABLE statements to the end of the AST.
func (t *Transformer) Transform(tree *pg_query.ParseResult) error {
	var alterStmts []*pg_query.RawStmt

	for _, rawStmt := range tree.Stmts {
		createStmtNode := rawStmt.Stmt.GetCreateStmt()
		if createStmtNode == nil || createStmtNode.Relation == nil {
			continue
		}

		tableName := createStmtNode.Relation.Relname
		extractedConstraints := t.extractForeignKeys(createStmtNode)

		for _, constraint := range extractedConstraints {
			if t.revealConstraintName && constraint.Conname == "" {
				constraint.Conname = t.generateFKName(tableName, constraint)
			}

			alterTableStmt := &pg_query.AlterTableStmt{
				Relation: createStmtNode.Relation,
				Objtype:  pg_query.ObjectType_OBJECT_TABLE,
				Cmds: []*pg_query.Node{
					{
						Node: &pg_query.Node_AlterTableCmd{
							AlterTableCmd: &pg_query.AlterTableCmd{
								Subtype: pg_query.AlterTableType_AT_AddConstraint,
								Def: &pg_query.Node{
									Node: &pg_query.Node_Constraint{
										Constraint: constraint,
									},
								},
							},
						},
					},
				},
			}

			alterStmts = append(alterStmts, &pg_query.RawStmt{
				Stmt: &pg_query.Node{
					Node: &pg_query.Node_AlterTableStmt{
						AlterTableStmt: alterTableStmt,
					},
				},
			})
		}
	}

	tree.Stmts = append(tree.Stmts, alterStmts...)
	return nil
}

func (t *Transformer) extractForeignKeys(stmt *pg_query.CreateStmt) []*pg_query.Constraint {
	var extracted []*pg_query.Constraint
	var remainingElts []*pg_query.Node

	for _, elt := range stmt.TableElts {
		if columnDef := elt.GetColumnDef(); columnDef != nil {
			extracted = append(extracted, t.extractFKsFromColumn(columnDef)...)
			remainingElts = append(remainingElts, elt)
		} else if constraint := elt.GetConstraint(); constraint != nil && constraint.Contype == pg_query.ConstrType_CONSTR_FOREIGN {
			extracted = append(extracted, constraint)
		} else {
			remainingElts = append(remainingElts, elt)
		}
	}

	stmt.TableElts = remainingElts
	return extracted
}

func (t *Transformer) extractFKsFromColumn(columnDef *pg_query.ColumnDef) []*pg_query.Constraint {
	var extracted []*pg_query.Constraint
	var remainingConstraints []*pg_query.Node

	for i := 0; i < len(columnDef.Constraints); i++ {
		consNode := columnDef.Constraints[i]
		constraint := consNode.GetConstraint()
		if constraint != nil && constraint.Contype == pg_query.ConstrType_CONSTR_FOREIGN {
			// Inject column name if it's a column-level FK (FkAttrs is usually empty)
			if len(constraint.FkAttrs) == 0 {
				constraint.FkAttrs = []*pg_query.Node{
					makeStringNode(columnDef.Colname),
				}
			}

			// Consume attributes
		AttrLoop:
			for j := i + 1; j < len(columnDef.Constraints); j++ {
				attrNode := columnDef.Constraints[j].GetConstraint()
				if attrNode == nil {
					break
				}
				switch attrNode.Contype {
				case pg_query.ConstrType_CONSTR_ATTR_DEFERRABLE:
					constraint.Deferrable = true
					i = j // Skip this node in the outer loop
				case pg_query.ConstrType_CONSTR_ATTR_NOT_DEFERRABLE:
					constraint.Deferrable = false
					i = j // Skip this node in the outer loop
				case pg_query.ConstrType_CONSTR_ATTR_DEFERRED:
					constraint.Initdeferred = true
					i = j // Skip this node in the outer loop
				case pg_query.ConstrType_CONSTR_ATTR_IMMEDIATE:
					constraint.Initdeferred = false
					i = j // Skip this node in the outer loop
				default:
					break AttrLoop
				}
			}
			extracted = append(extracted, constraint)
		} else {
			remainingConstraints = append(remainingConstraints, consNode)
		}
	}

	columnDef.Constraints = remainingConstraints
	return extracted
}

func (t *Transformer) generateFKName(tableName string, constraint *pg_query.Constraint) string {
	var cols []string
	for _, attr := range constraint.FkAttrs {
		if s := attr.GetString_(); s != nil {
			cols = append(cols, s.Sval)
		}
	}

	if len(cols) == 0 {
		return ""
	}

	joinedCols := strings.Join(cols, "_")
	return t.postgresStyleName(tableName, joinedCols, "_fkey")
}

func (t *Transformer) postgresStyleName(table, cols, suffix string) string {
	const maxLen = pgMaxIdentifierLen

	if len(table)+1+len(cols)+len(suffix) <= maxLen {
		return table + "_" + cols + suffix
	}

	// Balanced truncation like PostgreSQL's ChooseRelationName
	targetTotal := maxLen - len(suffix) - 1 // -1 for the "_" separator
	if targetTotal < 2 {
		// Fallback for extremely short maxLen (not expected)
		joined := table + "_" + cols
		return truncateToBytes(joined, maxLen-len(suffix)) + suffix
	}

	tLen := len(table)
	cLen := len(cols)

	tTarget := (targetTotal + 1) / 2 // Favor the first part (table name)
	cTarget := targetTotal - tTarget

	if tLen > tTarget && cLen > cTarget {
		tLen = tTarget
		cLen = cTarget
	} else if tLen > tTarget {
		tLen = targetTotal - cLen
	} else if cLen > cTarget {
		cLen = targetTotal - tLen
	}

	table = truncateToBytes(table, tLen)
	cols = truncateToBytes(cols, cLen)

	return table + "_" + cols + suffix
}

// truncateToBytes truncates string s to n bytes, ensuring it doesn't break UTF-8 sequences.
func truncateToBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := n
	// If s[i] is a continuation byte (starts with 10), move back until we find a leading byte.
	for i > 0 && (s[i]&0xC0 == 0x80) {
		i--
	}
	return s[:i]
}

func makeStringNode(s string) *pg_query.Node {
	return &pg_query.Node{
		Node: &pg_query.Node_String_{
			String_: &pg_query.String{
				Sval: s,
			},
		},
	}
}
