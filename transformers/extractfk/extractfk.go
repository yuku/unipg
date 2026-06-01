package extractfk

import (
	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// Transformer extracts inline foreign key constraints from CREATE TABLE statements
// and converts them into standalone ALTER TABLE statements.
type Transformer struct{}

// New creates a new ExtractFK Transformer.
func New() *Transformer {
	return &Transformer{}
}

// Transform implements unipg.Transformer.
func (t *Transformer) Transform(tree *pg_query.ParseResult) error {
	var alterStmts []*pg_query.RawStmt

	for _, rawStmt := range tree.Stmts {
		createStmtNode := rawStmt.Stmt.GetCreateStmt()
		if createStmtNode == nil {
			continue
		}

		extractedConstraints := t.extractForeignKeys(createStmtNode)
		for _, constraint := range extractedConstraints {
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

func makeStringNode(s string) *pg_query.Node {
	return &pg_query.Node{
		Node: &pg_query.Node_String_{
			String_: &pg_query.String{
				Sval: s,
			},
		},
	}
}
