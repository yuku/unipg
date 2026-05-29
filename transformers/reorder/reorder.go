package reorder

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// Transformer moves ALTER TABLE and CREATE VIEW statements to the end of the AST,
// and topologically sorts CREATE VIEW statements based on their dependencies.
type Transformer struct{}

// New creates a new Reorder Transformer.
func New() *Transformer {
	return &Transformer{}
}

// Transform implements unipg.Transformer.
func (t *Transformer) Transform(tree *pg_query.ParseResult) error {
	var regularStmts []*pg_query.RawStmt
	var alterStmts []*pg_query.RawStmt
	var viewStmts []*pg_query.RawStmt

	for _, rawStmt := range tree.Stmts {
		switch {
		case rawStmt.Stmt.GetAlterTableStmt() != nil:
			alterStmts = append(alterStmts, rawStmt)
		case rawStmt.Stmt.GetViewStmt() != nil || (rawStmt.Stmt.GetCreateTableAsStmt() != nil && rawStmt.Stmt.GetCreateTableAsStmt().Objtype == pg_query.ObjectType_OBJECT_MATVIEW):
			viewStmts = append(viewStmts, rawStmt)
		default:
			regularStmts = append(regularStmts, rawStmt)
		}
	}

	sortedViewStmts, err := t.sortViews(viewStmts)
	if err != nil {
		return err
	}

	tree.Stmts = append(regularStmts, sortedViewStmts...)
	tree.Stmts = append(tree.Stmts, alterStmts...)

	return nil
}

func (t *Transformer) sortViews(viewStmts []*pg_query.RawStmt) ([]*pg_query.RawStmt, error) {
	if len(viewStmts) <= 1 {
		return viewStmts, nil
	}

	viewMap := make(map[string]*pg_query.RawStmt)
	var unnamedViews []*pg_query.RawStmt

	for _, stmt := range viewStmts {
		name := t.getViewName(stmt)
		if name != "" {
			viewMap[name] = stmt
		} else {
			unnamedViews = append(unnamedViews, stmt)
		}
	}

	inDegree := make(map[string]int)
	adjList := make(map[string][]string)

	for name := range viewMap {
		inDegree[name] = 0
	}

	for _, stmt := range viewStmts {
		name := t.getViewName(stmt)
		if name == "" {
			continue
		}

		query := t.getViewQuery(stmt)
		deps := t.findDependencies(query)
		for _, dep := range deps {
			if _, isView := viewMap[dep]; isView && dep != name {
				adjList[dep] = append(adjList[dep], name)
				inDegree[name]++
			}
		}
	}

	// For deterministic output, sort names initially
	var allNames []string
	for name := range inDegree {
		allNames = append(allNames, name)
	}
	sort.Strings(allNames)

	var queue []string
	for _, name := range allNames {
		if inDegree[name] == 0 {
			queue = append(queue, name)
		}
	}

	var sortedViewNames []string
	for len(queue) > 0 {
		// Sort queue to maintain deterministic behavior at each step
		sort.Strings(queue)
		curr := queue[0]
		queue = queue[1:]
		sortedViewNames = append(sortedViewNames, curr)

		dependents := adjList[curr]
		sort.Strings(dependents)
		for _, dependent := range dependents {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}

	if len(sortedViewNames) != len(viewMap) {
		var remaining []string
		for name, degree := range inDegree {
			if degree > 0 {
				remaining = append(remaining, name)
			}
		}
		sort.Strings(remaining)
		return nil, fmt.Errorf("circular dependency detected among views: %s", strings.Join(remaining, ", "))
	}

	result := make([]*pg_query.RawStmt, 0, len(sortedViewNames)+len(unnamedViews))
	for _, name := range sortedViewNames {
		result = append(result, viewMap[name])
	}
	result = append(result, unnamedViews...)

	return result, nil
}

func (t *Transformer) getViewName(stmt *pg_query.RawStmt) string {
	if viewNode := stmt.Stmt.GetViewStmt(); viewNode != nil {
		return t.formatRangeVar(viewNode.View)
	}
	if matViewNode := stmt.Stmt.GetCreateTableAsStmt(); matViewNode != nil {
		if matViewNode.Into != nil {
			return t.formatRangeVar(matViewNode.Into.Rel)
		}
	}
	return ""
}

func (t *Transformer) getViewQuery(stmt *pg_query.RawStmt) *pg_query.Node {
	if viewNode := stmt.Stmt.GetViewStmt(); viewNode != nil {
		return viewNode.Query
	}
	if matViewNode := stmt.Stmt.GetCreateTableAsStmt(); matViewNode != nil {
		return matViewNode.Query
	}
	return nil
}

func (t *Transformer) formatRangeVar(rv *pg_query.RangeVar) string {
	if rv == nil {
		return ""
	}
	if rv.Schemaname != "" {
		return rv.Schemaname + "." + rv.Relname
	}
	return rv.Relname
}

func (t *Transformer) findDependencies(node *pg_query.Node) []string {
	var deps []string
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		if v.Kind() == reflect.Ptr {
			if v.IsNil() {
				return
			}
			if rv, ok := v.Interface().(*pg_query.RangeVar); ok {
				name := t.formatRangeVar(rv)
				if name != "" {
					deps = append(deps, name)
				}
				return
			}
			walk(v.Elem())
			return
		}
		switch v.Kind() {
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).PkgPath != "" {
					continue
				}
				walk(v.Field(i))
			}
		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		}
	}
	walk(reflect.ValueOf(node))

	seen := make(map[string]bool, len(deps))
	var uniqueDeps []string
	for _, dep := range deps {
		if !seen[dep] {
			seen[dep] = true
			uniqueDeps = append(uniqueDeps, dep)
		}
	}

	return uniqueDeps
}
