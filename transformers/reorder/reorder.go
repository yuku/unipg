// Package reorder provides a Transformer that reorganizes SQL statements in the AST.
// CREATE [OR REPLACE] FUNCTION statements called by a CREATE TABLE (e.g. in DEFAULT or
// CHECK expressions) are moved right before that table. CREATE VIEW and ALTER TABLE
// statements are moved after table definitions, and views are topologically sorted
// based on their dependencies. Statements that depend on a view move together with it,
// and no statement is moved across a SET statement.
package reorder

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// Transformer moves CREATE [OR REPLACE] FUNCTION statements right before the first
// CREATE TABLE statement that calls them, moves ALTER TABLE and CREATE VIEW statements
// to the end of the AST, and topologically sorts CREATE VIEW statements based on their
// dependencies to ensure they are created in the correct order. Statements that depend
// on a view, such as an index on a materialized view or a function returning its row
// type, are sorted together with the views. Each region between SET statements is
// reordered separately, since a SET may change how names are resolved.
type Transformer struct{}

// New creates a new Reorder Transformer.
func New() *Transformer {
	return &Transformer{}
}

// Transform implements unipg.Transformer. It reorders the statements in the AST
// and performs topological sorting on view definitions.
func (t *Transformer) Transform(tree *pg_query.ParseResult) error {
	stmts := t.hoistFunctions(tree.Stmts)
	result := make([]*pg_query.RawStmt, 0, len(stmts))

	start := 0
	for i := 0; i <= len(stmts); i++ {
		if i < len(stmts) && stmts[i].Stmt.GetVariableSetStmt() == nil {
			continue
		}
		region, err := t.reorderRegion(stmts[start:i])
		if err != nil {
			return err
		}
		result = append(result, region...)
		if i < len(stmts) {
			result = append(result, stmts[i])
		}
		start = i + 1
	}

	tree.Stmts = result
	return nil
}

// reorderRegion reorders the statements between two SET statements. Views and the
// statements that depend on them, directly or through each other, are sorted and
// placed after the remaining statements, followed by ALTER TABLE statements.
func (t *Transformer) reorderRegion(stmts []*pg_query.RawStmt) ([]*pg_query.RawStmt, error) {
	var regularStmts []*pg_query.RawStmt
	var alterStmts []*pg_query.RawStmt
	var lateStmts []*pg_query.RawStmt

	lateNames := make(map[string]bool)
	for _, stmt := range stmts {
		if isView(stmt) {
			for _, name := range t.definitions(stmt) {
				lateNames[name] = true
			}
		}
	}

	for _, stmt := range stmts {
		switch {
		case stmt.Stmt.GetAlterTableStmt() != nil:
			alterStmts = append(alterStmts, stmt)
		case isView(stmt):
			lateStmts = append(lateStmts, stmt)
		case slices.ContainsFunc(t.references(stmt), func(name string) bool { return lateNames[name] }):
			lateStmts = append(lateStmts, stmt)
			for _, name := range t.definitions(stmt) {
				lateNames[name] = true
			}
		default:
			regularStmts = append(regularStmts, stmt)
		}
	}

	sortedLateStmts, err := t.sortLate(lateStmts)
	if err != nil {
		return nil, err
	}

	result := append(regularStmts, sortedLateStmts...)
	return append(result, alterStmts...), nil
}

func isView(stmt *pg_query.RawStmt) bool {
	if stmt.Stmt.GetViewStmt() != nil {
		return true
	}
	ctas := stmt.Stmt.GetCreateTableAsStmt()
	return ctas != nil && ctas.Objtype == pg_query.ObjectType_OBJECT_MATVIEW
}

// Relations and types share a namespace in PostgreSQL (every table and view has a row
// type of the same name), while functions have their own.
const (
	relPrefix  = "rel:"
	funcPrefix = "func:"
)

// definitions returns the names of the objects stmt creates that other statements
// may depend on.
func (t *Transformer) definitions(stmt *pg_query.RawStmt) []string {
	switch {
	case isView(stmt):
		return []string{relPrefix + t.getViewName(stmt)}
	case stmt.Stmt.GetCreateStmt() != nil:
		return []string{relPrefix + t.formatRangeVar(stmt.Stmt.GetCreateStmt().Relation)}
	case t.getFuncName(stmt) != "":
		return []string{funcPrefix + t.getFuncName(stmt)}
	}
	return nil
}

// references returns the names of the relations, types and functions stmt refers to,
// including those in a SQL function body that PostgreSQL resolves at creation time.
func (t *Transformer) references(stmt *pg_query.RawStmt) []string {
	roots := []any{stmt.Stmt}
	if fn := stmt.Stmt.GetCreateFunctionStmt(); fn != nil && fn.SqlBody == nil {
		if body := t.parseSQLBody(fn); body != nil {
			roots = append(roots, body)
		}
	}

	var refs []string
	for _, root := range roots {
		for _, name := range collectNames(root, t.formatRangeVar) {
			refs = append(refs, relPrefix+name)
		}
		for _, name := range collectNames(root, func(tn *pg_query.TypeName) string { return t.formatNameList(tn.Names) }) {
			refs = append(refs, relPrefix+name)
		}
		for _, name := range t.findFuncCalls(root) {
			refs = append(refs, funcPrefix+name)
		}
	}

	if comment := stmt.Stmt.GetCommentStmt(); comment != nil {
		names := comment.Object.GetList().GetItems()
		switch comment.Objtype {
		case pg_query.ObjectType_OBJECT_COLUMN:
			if len(names) > 0 {
				refs = append(refs, relPrefix+t.formatNameList(names[:len(names)-1]))
			}
		case pg_query.ObjectType_OBJECT_TABLE, pg_query.ObjectType_OBJECT_VIEW, pg_query.ObjectType_OBJECT_MATVIEW:
			refs = append(refs, relPrefix+t.formatNameList(names))
		}
	}

	return refs
}

// formatNameList formats a qualified name the same way as formatRangeVar.
func (t *Transformer) formatNameList(nodes []*pg_query.Node) string {
	parts := make([]string, 0, len(nodes))
	for _, node := range nodes {
		parts = append(parts, node.GetString_().GetSval())
	}
	return strings.Join(parts, ".")
}

// funcNameSep joins the components of a qualified function name. PostgreSQL
// identifiers cannot contain NUL, so app.f and "app.f" never share a key.
const funcNameSep = "\x00"

// hoistFunctions moves a CREATE FUNCTION statement right before the first
// CREATE TABLE that calls it, if that table precedes every definition of the
// function. Only the first such definition is moved, together with the functions
// it calls at creation time that are defined between the table and itself, even if
// they were already moved before a later table. Other functions
// are left in place, since they may depend on tables or types defined before them
// (e.g. RETURNS SETOF <table> or a LANGUAGE sql body reading a table).
//
// A SET statement starts a new region: functions are never moved across it, and
// unqualified names defined before it may resolve to other functions after it.
func (t *Transformer) hoistFunctions(stmts []*pg_query.RawStmt) []*pg_query.RawStmt {
	defined := make(map[string]int)
	firstCaller := make(map[string]int)
	hoisted := make(map[int][]*pg_query.RawStmt)
	dest := make(map[int]int)

	var hoist func(i, target int)
	hoist = func(i, target int) {
		if prev, ok := dest[i]; ok {
			if prev <= target {
				return
			}
			hoisted[prev] = slices.DeleteFunc(hoisted[prev], func(s *pg_query.RawStmt) bool {
				return s == stmts[i]
			})
		}
		dest[i] = target
		for _, dep := range t.findCreationFuncCalls(stmts[i].Stmt.GetCreateFunctionStmt()) {
			if j, ok := defined[dep]; ok && j > target {
				hoist(j, target)
			}
		}
		hoisted[target] = append(hoisted[target], stmts[i])
	}

	for i, stmt := range stmts {
		if stmt.Stmt.GetVariableSetStmt() != nil {
			clear(firstCaller)
			for name := range defined {
				if !strings.Contains(name, funcNameSep) {
					delete(defined, name)
				}
			}
			continue
		}

		if createStmt := stmt.Stmt.GetCreateStmt(); createStmt != nil {
			for _, name := range t.findFuncCalls(createStmt) {
				if _, ok := defined[name]; ok {
					continue
				}
				if _, ok := firstCaller[name]; !ok {
					firstCaller[name] = i
				}
			}
			continue
		}

		name := t.getFuncName(stmt)
		if name == "" {
			continue
		}
		if _, ok := defined[name]; !ok {
			defined[name] = i
		}
		if target, ok := firstCaller[name]; ok {
			delete(firstCaller, name)
			hoist(i, target)
		}
	}

	if len(dest) == 0 {
		return stmts
	}

	result := make([]*pg_query.RawStmt, 0, len(stmts))
	for i, stmt := range stmts {
		if _, ok := dest[i]; ok {
			continue
		}
		result = append(result, hoisted[i]...)
		result = append(result, stmt)
	}
	return result
}

// findCreationFuncCalls returns the functions that PostgreSQL resolves when creating
// fn: those called by parameter defaults and by a SQL function body. Bodies in other
// languages are not checked until they run, so they impose no ordering.
func (t *Transformer) findCreationFuncCalls(fn *pg_query.CreateFunctionStmt) []string {
	calls := t.findFuncCalls(fn.Parameters)
	if fn.SqlBody != nil {
		return append(calls, t.findFuncCalls(fn.SqlBody)...)
	}
	if body := t.parseSQLBody(fn); body != nil {
		return append(calls, t.findFuncCalls(body)...)
	}
	return calls
}

// parseSQLBody parses the string body of a LANGUAGE sql function, or returns nil if
// fn has no such body.
func (t *Transformer) parseSQLBody(fn *pg_query.CreateFunctionStmt) *pg_query.ParseResult {
	var language, body string
	for _, opt := range fn.Options {
		def := opt.GetDefElem()
		switch def.GetDefname() {
		case "language":
			language = def.Arg.GetString_().GetSval()
		case "as":
			if items := def.Arg.GetList().GetItems(); len(items) > 0 {
				body = items[0].GetString_().GetSval()
			}
		}
	}
	if language != "sql" {
		return nil
	}

	tree, err := pg_query.Parse(body)
	if err != nil {
		return nil
	}
	return tree
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
	return strings.Join(parts, funcNameSep)
}

// sortLate topologically sorts views and the statements that depend on them. Views
// that are ready at the same time are ordered by name, and other statements keep their
// relative order after them, so the output is deterministic.
func (t *Transformer) sortLate(stmts []*pg_query.RawStmt) ([]*pg_query.RawStmt, error) {
	keys := make([]string, len(stmts))
	definers := make(map[string][]int)
	for i, stmt := range stmts {
		if isView(stmt) {
			keys[i] = t.getViewName(stmt)
			if _, exists := definers[relPrefix+keys[i]]; exists {
				return nil, fmt.Errorf("duplicate view statement for %s", keys[i])
			}
		} else {
			// UTF-8 never contains 0xff, so these keys sort after every view name.
			keys[i] = fmt.Sprintf("\xff%08d", i)
		}
		for _, name := range t.definitions(stmt) {
			definers[name] = append(definers[name], i)
		}
	}

	inDegree := make([]int, len(stmts))
	dependents := make([][]int, len(stmts))
	for i, stmt := range stmts {
		for _, ref := range t.references(stmt) {
			for _, j := range definers[ref] {
				if j != i {
					dependents[j] = append(dependents[j], i)
					inDegree[i]++
				}
			}
		}
	}

	byKey := func(a, b int) int { return strings.Compare(keys[a], keys[b]) }
	var ready []int
	for i := range stmts {
		if inDegree[i] == 0 {
			ready = append(ready, i)
		}
	}

	result := make([]*pg_query.RawStmt, 0, len(stmts))
	for len(ready) > 0 {
		slices.SortFunc(ready, byKey)
		curr := ready[0]
		ready = ready[1:]
		result = append(result, stmts[curr])

		for _, dependent := range dependents[curr] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
	}

	if len(result) != len(stmts) {
		var remaining []string
		for i, degree := range inDegree {
			if degree > 0 {
				remaining = append(remaining, t.describe(stmts[i], keys[i]))
			}
		}
		sort.Strings(remaining)
		return nil, fmt.Errorf("circular dependency detected among views: %s", strings.Join(remaining, ", "))
	}

	return result, nil
}

// describe names stmt for error messages, preferring the view name in key.
func (t *Transformer) describe(stmt *pg_query.RawStmt, key string) string {
	if isView(stmt) {
		return key
	}
	if defs := t.definitions(stmt); len(defs) > 0 {
		return strings.TrimPrefix(strings.TrimPrefix(defs[0], relPrefix), funcPrefix)
	}
	return "statement at offset " + fmt.Sprint(stmt.StmtLocation)
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

func (t *Transformer) formatRangeVar(rv *pg_query.RangeVar) string {
	if rv == nil {
		return ""
	}
	if rv.Schemaname != "" {
		return rv.Schemaname + "." + rv.Relname
	}
	return rv.Relname
}

func (t *Transformer) findFuncCalls(node any) []string {
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
