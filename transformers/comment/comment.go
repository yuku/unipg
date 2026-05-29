package comment

import (
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// Transformer associates virtual comment nodes with subsequent DDL statements.
type Transformer struct{}

// New creates a new Comment Transformer.
func New() *Transformer {
	return &Transformer{}
}

// Transform implements unipg.Transformer.
func (t *Transformer) Transform(tree *pg_query.ParseResult) error {
	var processedStmts []*pg_query.RawStmt
	var pendingComments []string

	for i := 0; i < len(tree.Stmts); i++ {
		rawStmt := tree.Stmts[i]

		// 1. Check if it's a virtual comment node
		if commentStmt := rawStmt.Stmt.GetCommentStmt(); commentStmt != nil && commentStmt.Objtype == pg_query.ObjectType_OBJECT_TYPE_UNDEFINED {
			commentText := t.cleanComment(commentStmt.Comment)
			if commentText != "" {
				pendingComments = append(pendingComments, commentText)
			}
			continue
		}

		// 2. Try to attach pending comments to the current statement
		if len(pendingComments) > 0 {
			if targetStmt := t.getCommentTarget(rawStmt); targetStmt != nil {
				formalComment := &pg_query.CommentStmt{
					Objtype: targetStmt.objType,
					Object:  targetStmt.object,
					Comment: strings.Join(pendingComments, "\n"),
				}

				processedStmts = append(processedStmts, rawStmt)
				processedStmts = append(processedStmts, &pg_query.RawStmt{
					Stmt: &pg_query.Node{
						Node: &pg_query.Node_CommentStmt{
							CommentStmt: formalComment,
						},
					},
				})

				pendingComments = nil
				continue
			}
			// If statement is not a comment target, discard pending comments
			pendingComments = nil
		}

		processedStmts = append(processedStmts, rawStmt)
	}

	tree.Stmts = processedStmts
	return nil
}

type commentTarget struct {
	objType pg_query.ObjectType
	object  *pg_query.Node
}

func (t *Transformer) getCommentTarget(rawStmt *pg_query.RawStmt) *commentTarget {
	node := rawStmt.Stmt.GetNode()
	if node == nil {
		return nil
	}

	switch n := node.(type) {
	case *pg_query.Node_CreateStmt:
		return &commentTarget{
			objType: pg_query.ObjectType_OBJECT_TABLE,
			object:  t.rangeVarToNode(n.CreateStmt.Relation),
		}
	case *pg_query.Node_ViewStmt:
		return &commentTarget{
			objType: pg_query.ObjectType_OBJECT_VIEW,
			object:  t.rangeVarToNode(n.ViewStmt.View),
		}
	case *pg_query.Node_CreateTableAsStmt:
		if n.CreateTableAsStmt.Objtype == pg_query.ObjectType_OBJECT_MATVIEW && n.CreateTableAsStmt.Into != nil {
			return &commentTarget{
				objType: pg_query.ObjectType_OBJECT_MATVIEW,
				object:  t.rangeVarToNode(n.CreateTableAsStmt.Into.Rel),
			}
		}
	case *pg_query.Node_CompositeTypeStmt:
		return &commentTarget{
			objType: pg_query.ObjectType_OBJECT_TYPE,
			object:  t.rangeVarToNode(n.CompositeTypeStmt.Typevar),
		}
	case *pg_query.Node_CreateEnumStmt:
		return &commentTarget{
			objType: pg_query.ObjectType_OBJECT_TYPE,
			object:  t.namesToNode(n.CreateEnumStmt.TypeName),
		}
	case *pg_query.Node_IndexStmt:
		// Not typically commented via this transformer yet, but easily extensible
	}
	return nil
}

func (t *Transformer) rangeVarToNode(rv *pg_query.RangeVar) *pg_query.Node {
	if rv == nil {
		return nil
	}
	var items []*pg_query.Node
	if rv.Schemaname != "" {
		items = append(items, t.makeStringNode(rv.Schemaname))
	}
	items = append(items, t.makeStringNode(rv.Relname))

	return &pg_query.Node{
		Node: &pg_query.Node_List{
			List: &pg_query.List{
				Items: items,
			},
		},
	}
}

func (t *Transformer) namesToNode(names []*pg_query.Node) *pg_query.Node {
	if len(names) == 0 {
		return nil
	}
	return &pg_query.Node{
		Node: &pg_query.Node_List{
			List: &pg_query.List{
				Items: names,
			},
		},
	}
}

func (t *Transformer) makeStringNode(s string) *pg_query.Node {
	return &pg_query.Node{
		Node: &pg_query.Node_String_{
			String_: &pg_query.String{
				Sval: s,
			},
		},
	}
}

func (t *Transformer) cleanComment(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "/*") && strings.HasSuffix(s, "*/") {
		s = s[2 : len(s)-2]
	} else if strings.HasPrefix(s, "--") {
		s = s[2:]
	}
	return strings.TrimSpace(s)
}
