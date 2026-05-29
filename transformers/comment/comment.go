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

	// Track locations of comments that are inside a statement (columns)
	// to avoid re-queuing them as top-level comments.
	internalCommentLocs := make(map[int32]bool)
	for _, rawStmt := range tree.Stmts {
		if createStmt := rawStmt.Stmt.GetCreateStmt(); createStmt != nil {
			stmtEnd := rawStmt.StmtLocation + rawStmt.StmtLen
			for _, vNode := range tree.Stmts {
				if vNode.StmtLocation > rawStmt.StmtLocation && vNode.StmtLocation < stmtEnd {
					if commentStmt := vNode.Stmt.GetCommentStmt(); commentStmt != nil && commentStmt.Objtype == pg_query.ObjectType_OBJECT_TYPE_UNDEFINED {
						internalCommentLocs[vNode.StmtLocation] = true
					}
				}
			}
		}
	}

	for i := 0; i < len(tree.Stmts); i++ {
		rawStmt := tree.Stmts[i]

		// 1. Check if it's a virtual comment node
		if commentStmt := rawStmt.Stmt.GetCommentStmt(); commentStmt != nil && commentStmt.Objtype == pg_query.ObjectType_OBJECT_TYPE_UNDEFINED {
			// Skip comments that are already processed as internal (column) comments
			if internalCommentLocs[rawStmt.StmtLocation] {
				continue
			}

			commentText := t.cleanComment(commentStmt.Comment)
			if commentText != "" {
				pendingComments = append(pendingComments, commentText)
			}
			continue
		}

		// 2. Try to attach pending comments (top-level) to the current statement
		if len(pendingComments) > 0 {
			if target := t.getTopLevelTarget(rawStmt); target != nil {
				processedStmts = append(processedStmts, rawStmt)

				// Add top-level comment
				processedStmts = append(processedStmts, &pg_query.RawStmt{
					Stmt: &pg_query.Node{
						Node: &pg_query.Node_CommentStmt{
							CommentStmt: &pg_query.CommentStmt{
								Objtype: target.objType,
								Object:  target.object,
								Comment: strings.Join(pendingComments, "\n"),
							},
						},
					},
				})

				// Add internal comments (columns) within this statement
				processedStmts = append(processedStmts, t.extractInternalComments(rawStmt, tree.Stmts)...)

				pendingComments = nil
				continue
			}
			// If statement is not a comment target, discard pending comments
			pendingComments = nil
		}

		// Add regular statement and its internal comments
		processedStmts = append(processedStmts, rawStmt)
		processedStmts = append(processedStmts, t.extractInternalComments(rawStmt, tree.Stmts)...)
	}

	tree.Stmts = processedStmts
	return nil
}

type commentTarget struct {
	objType pg_query.ObjectType
	object  *pg_query.Node
}

func (t *Transformer) extractInternalComments(rawStmt *pg_query.RawStmt, allStmts []*pg_query.RawStmt) []*pg_query.RawStmt {
	createStmt := rawStmt.Stmt.GetCreateStmt()
	if createStmt == nil {
		return nil
	}

	var results []*pg_query.RawStmt
	stmtEnd := rawStmt.StmtLocation + rawStmt.StmtLen

	for _, vNode := range allStmts {
		// Only comments strictly INSIDE the statement range
		if vNode.StmtLocation > rawStmt.StmtLocation && vNode.StmtLocation < stmtEnd {
			if commentStmt := vNode.Stmt.GetCommentStmt(); commentStmt != nil && commentStmt.Objtype == pg_query.ObjectType_OBJECT_TYPE_UNDEFINED {
				cleaned := t.cleanComment(commentStmt.Comment)
				if cleaned == "" {
					continue
				}

				if colName := t.findTargetColumn(createStmt, vNode.StmtLocation); colName != "" {
					results = append(results, &pg_query.RawStmt{
						Stmt: &pg_query.Node{
							Node: &pg_query.Node_CommentStmt{
								CommentStmt: &pg_query.CommentStmt{
									Objtype: pg_query.ObjectType_OBJECT_COLUMN,
									Object:  t.columnToNode(createStmt.Relation, colName),
									Comment: cleaned,
								},
							},
						},
					})
				}
			}
		}
	}
	return results
}

func (t *Transformer) getTopLevelTarget(rawStmt *pg_query.RawStmt) *commentTarget {
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
	}
	return nil
}

func (t *Transformer) findTargetColumn(stmt *pg_query.CreateStmt, location int32) string {
	var bestCol string
	var minDiff int32 = -1

	for _, elt := range stmt.TableElts {
		if col := elt.GetColumnDef(); col != nil {
			diff := location - col.Location
			if diff < 0 {
				diff = -diff
			}
			if minDiff == -1 || diff < minDiff {
				minDiff = diff
				bestCol = col.Colname
			}
		}
	}
	return bestCol
}

func (t *Transformer) columnToNode(rv *pg_query.RangeVar, colName string) *pg_query.Node {
	var items []*pg_query.Node
	if rv.Schemaname != "" {
		items = append(items, t.makeStringNode(rv.Schemaname))
	}
	items = append(items, t.makeStringNode(rv.Relname))
	items = append(items, t.makeStringNode(colName))

	return &pg_query.Node{
		Node: &pg_query.Node_List{
			List: &pg_query.List{
				Items: items,
			},
		},
	}
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
	// Only handle /** ... */ style comments
	if strings.HasPrefix(s, "/**") && strings.HasSuffix(s, "*/") {
		s = s[3 : len(s)-2]
	} else {
		return ""
	}

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		// Remove leading asterisk (common in JavaDoc/JSDoc)
		if strings.HasPrefix(line, "*") {
			line = strings.TrimPrefix(line, "*")
			line = strings.TrimSpace(line)
		}
		lines[i] = line
	}

	return dedent(strings.Join(lines, "\n"))
}

func dedent(s string) string {
	lines := strings.Split(s, "\n")
	if len(lines) == 0 {
		return ""
	}

	// Find first non-empty line
	start := 0
	for ; start < len(lines) && strings.TrimSpace(lines[start]) == ""; start++ {
	}
	// Find last non-empty line
	end := len(lines)
	for ; end > start && strings.TrimSpace(lines[end-1]) == ""; end-- {
	}
	lines = lines[start:end]

	if len(lines) == 0 {
		return ""
	}

	// Find minimum indentation
	minIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := 0
		for _, r := range line {
			if r == ' ' || r == '\t' {
				indent++
			} else {
				break
			}
		}
		if minIndent == -1 || indent < minIndent {
			minIndent = indent
		}
	}

	if minIndent <= 0 {
		return strings.Join(lines, "\n")
	}

	// Remove indent
	for i, line := range lines {
		if len(line) >= minIndent {
			lines[i] = line[minIndent:]
		}
	}

	return strings.Join(lines, "\n")
}
