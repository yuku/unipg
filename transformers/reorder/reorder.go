// Package reorder provides a Transformer that reorganizes SQL statements in the AST.
// CREATE [OR REPLACE] FUNCTION statements called by a CREATE TABLE (e.g. in DEFAULT or
// CHECK expressions) are moved right before that table. CREATE VIEW and ALTER TABLE
// statements are moved after table definitions, and views are topologically sorted
// based on their dependencies.
package reorder

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// Transformer moves CREATE [OR REPLACE] FUNCTION statements right before the first
// CREATE TABLE statement that calls them, moves ALTER TABLE and CREATE VIEW statements
// to the end of the AST, and topologically sorts CREATE VIEW statements based on their
// dependencies to ensure they are created in the correct order.
type Transformer struct{}

// New creates a new Reorder Transformer.
func New() *Transformer {
	return &Transformer{}
}

// Transform implements unipg.Transformer. It reorders the statements in the AST
// and performs topological sorting on view definitions.
func (t *Transformer) Transform(tree *pg_query.ParseResult) error {
	var regularStmts []*pg_query.RawStmt
	var alterStmts []*pg_query.RawStmt
	var viewStmts []*pg_query.RawStmt

	for _, rawStmt := range t.hoistFunctions(tree.Stmts) {
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

// hoistFunctions moves each CREATE FUNCTION statement right before the first
// CREATE TABLE that calls it, if that table precedes the function. Other functions
// are left in place, since they may depend on tables or types defined before them
// (e.g. RETURNS SETOF <table> or a LANGUAGE sql body reading a table).
// A function is not moved across a SET statement, as that could change the
// search_path or other settings it is created under.
func (t *Transformer) hoistFunctions(stmts []*pg_query.RawStmt) []*pg_query.RawStmt {
	firstCaller := make(map[string]int)
	for i, stmt := range stmts {
		createStmt := stmt.Stmt.GetCreateStmt()
		if createStmt == nil {
			continue
		}
		for _, name := range t.findFuncCalls(createStmt) {
			if _, exists := firstCaller[name]; !exists {
				firstCaller[name] = i
			}
		}
	}

	hoisted := make(map[int][]*pg_query.RawStmt)
	moved := make(map[int]bool)
	for i, stmt := range stmts {
		name := t.getFuncName(stmt)
		if name == "" {
			continue
		}
		target, ok := firstCaller[name]
		if !ok || target > i || containsSetStmt(stmts[target:i]) {
			continue
		}
		hoisted[target] = append(hoisted[target], stmt)
		moved[i] = true
	}

	if len(moved) == 0 {
		return stmts
	}

	result := make([]*pg_query.RawStmt, 0, len(stmts))
	for i, stmt := range stmts {
		if moved[i] {
			continue
		}
		result = append(result, hoisted[i]...)
		result = append(result, stmt)
	}
	return result
}

func containsSetStmt(stmts []*pg_query.RawStmt) bool {
	for _, stmt := range stmts {
		if stmt.Stmt.GetVariableSetStmt() != nil {
			return true
		}
	}
	return false
}

// getFuncName returns the name of a CREATE FUNCTION statement, or "" if stmt is not one.
// Procedures are excluded because they cannot be called from table expressions.
func (t *Transformer) getFuncName(stmt *pg_query.RawStmt) string {
	fn := stmt.Stmt.GetCreateFunctionStmt()
	if fn == nil || fn.IsProcedure {
		return ""
	}
	return t.formatFuncName(fn.Funcname)
}

func (t *Transformer) formatFuncName(nodes []*pg_query.Node) string {
	parts := make([]string, 0, len(nodes))
	for _, node := range nodes {
		parts = append(parts, node.GetString_().GetSval())
	}
	return strings.Join(parts, ".")
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
			if _, exists := viewMap[name]; exists {
				return nil, fmt.Errorf("duplicate view statement for %s", name)
			}
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
	return collectNames(node, t.formatRangeVar)
}

func (t *Transformer) findFuncCalls(node *pg_query.CreateStmt) []string {
	return collectNames(node, func(fc *pg_query.FuncCall) string {
		return t.formatFuncName(fc.Funcname)
	})
}

// collectNames walks the AST under root and returns the unique non-empty names
// produced by nameOf for every node of type T, in order of first appearance.
func collectNames[T any](root any, nameOf func(T) string) []string {
	var names []string
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if !v.IsValid() {
			return
		}
		if v.Kind() == reflect.Ptr {
			if v.IsNil() {
				return
			}
			if n, ok := v.Interface().(T); ok {
				if name := nameOf(n); name != "" {
					names = append(names, name)
				}
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
	walk(reflect.ValueOf(root))

	seen := make(map[string]bool, len(names))
	var uniqueNames []string
	for _, name := range names {
		if !seen[name] {
			seen[name] = true
			uniqueNames = append(uniqueNames, name)
		}
	}

	return uniqueNames
}
