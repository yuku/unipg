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
	checkBodies := true
	for i := 0; i <= len(stmts); i++ {
		if i < len(stmts) && stmts[i].Stmt.GetVariableSetStmt() == nil {
			continue
		}
		region, err := t.reorderRegion(stmts[start:i], checkBodies)
		if err != nil {
			return err
		}
		result = append(result, region...)
		if i < len(stmts) {
			result = append(result, stmts[i])
			checkBodies = checksFunctionBodies(stmts[i].Stmt.GetVariableSetStmt(), checkBodies)
		}
		start = i + 1
	}

	tree.Stmts = result
	return nil
}

// reorderRegion reorders the statements between two SET statements. Views and the
// statements that depend on them, directly or through each other, are sorted and
// placed after the remaining statements, followed by ALTER TABLE statements.
// checkBodies reports whether check_function_bodies is on in this region.
func (t *Transformer) reorderRegion(stmts []*pg_query.RawStmt, checkBodies bool) ([]*pg_query.RawStmt, error) {
	defs := make([][]string, len(stmts))
	refs := make([][]string, len(stmts))
	for i, stmt := range stmts {
		defs[i] = t.definitions(stmt)
		refs[i] = t.references(stmt, checkBodies)
	}

	late := make([]bool, len(stmts))
	firstLateDef := make(map[string]int)
	markLate := func(i int) {
		late[i] = true
		for _, name := range defs[i] {
			if j, ok := firstLateDef[name]; !ok || i < j {
				firstLateDef[name] = i
			}
		}
	}
	for i, stmt := range stmts {
		if isView(stmt) {
			markLate(i)
		}
	}

	// A statement must stay after the late statements it references, and after
	// earlier late definitions of a name it redefines (e.g. CREATE OR REPLACE).
	for changed := true; changed; {
		changed = false
		for i, stmt := range stmts {
			if late[i] || stmt.Stmt.GetAlterTableStmt() != nil {
				continue
			}
			referencesLate := slices.ContainsFunc(refs[i], func(name string) bool {
				_, ok := firstLateDef[name]
				return ok
			})
			redefinesLate := slices.ContainsFunc(defs[i], func(name string) bool {
				j, ok := firstLateDef[name]
				return ok && j < i
			})
			if referencesLate || redefinesLate {
				markLate(i)
				changed = true
			}
		}
	}

	var regularStmts []*pg_query.RawStmt
	var alterStmts []*pg_query.RawStmt
	for i, stmt := range stmts {
		switch {
		case stmt.Stmt.GetAlterTableStmt() != nil:
			alterStmts = append(alterStmts, stmt)
		case !late[i]:
			regularStmts = append(regularStmts, stmt)
		}
	}

	sortedLateStmts, err := t.sortLate(stmts, late, defs, refs)
	if err != nil {
		return nil, err
	}

	result := append(regularStmts, sortedLateStmts...)
	return append(result, alterStmts...), nil
}

// checksFunctionBodies returns whether check_function_bodies is on after set, given
// whether it was on before. When it is off, PostgreSQL does not resolve string bodies
// of SQL functions at creation time.
func checksFunctionBodies(set *pg_query.VariableSetStmt, current bool) bool {
	switch {
	case set.Kind == pg_query.VariableSetKind_VAR_RESET_ALL:
		return true
	case set.Name != "check_function_bodies":
		return current
	case set.Kind != pg_query.VariableSetKind_VAR_SET_VALUE || len(set.Args) == 0:
		return true
	}
	value := set.Args[0].GetAConst()
	if value.GetIval() != nil {
		return value.GetIval().GetIval() != 0
	}
	// PostgreSQL accepts any unambiguous prefix of a boolean keyword.
	v := strings.ToLower(value.GetSval().GetSval())
	off := v == "0" || v != "" && (strings.HasPrefix("false", v) || strings.HasPrefix("no", v) || (len(v) >= 2 && strings.HasPrefix("off", v)))
	return !off
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
	case stmt.Stmt.GetCreateTableAsStmt() != nil:
		return []string{relPrefix + t.formatRangeVar(stmt.Stmt.GetCreateTableAsStmt().GetInto().GetRel())}
	case stmt.Stmt.GetCompositeTypeStmt() != nil:
		return []string{relPrefix + t.formatRangeVar(stmt.Stmt.GetCompositeTypeStmt().Typevar)}
	case stmt.Stmt.GetCreateDomainStmt() != nil:
		return []string{relPrefix + t.formatNameList(stmt.Stmt.GetCreateDomainStmt().Domainname)}
	case stmt.Stmt.GetCreateEnumStmt() != nil:
		return []string{relPrefix + t.formatNameList(stmt.Stmt.GetCreateEnumStmt().TypeName)}
	case stmt.Stmt.GetIndexStmt().GetIdxname() != "":
		index := stmt.Stmt.GetIndexStmt()
		return []string{relPrefix + t.formatRangeVar(&pg_query.RangeVar{
			Schemaname: index.GetRelation().GetSchemaname(),
			Relname:    index.Idxname,
		})}
	case stmt.Stmt.GetCreateFunctionStmt() != nil:
		return []string{funcPrefix + t.formatNameList(stmt.Stmt.GetCreateFunctionStmt().Funcname)}
	}
	return nil
}

// references returns the names of the relations, types and functions stmt refers to,
// including those in a SQL function body that PostgreSQL resolves at creation time.
// String bodies are resolved only when checkBodies is set, while SQL-standard bodies
// are always resolved.
func (t *Transformer) references(stmt *pg_query.RawStmt, checkBodies bool) []string {
	roots := []any{stmt.Stmt}
	if fn := stmt.Stmt.GetCreateFunctionStmt(); fn != nil && fn.SqlBody == nil && checkBodies {
		if body := t.parseSQLBody(fn); body != nil {
			roots = append(roots, body)
		}
	}

	var refs []string
	for _, root := range roots {
		// A CTE shadows a relation of the same unqualified name. Scopes are not
		// tracked, so a CTE name hides that relation anywhere in the statement.
		ctes := collectNames(root, func(cte *pg_query.CommonTableExpr) string { return cte.Ctename })
		for _, name := range collectNames(root, t.formatRangeVar) {
			if !slices.Contains(ctes, name) {
				refs = append(refs, relPrefix+name)
			}
		}
		for _, name := range collectNames(root, t.typeKey) {
			refs = append(refs, relPrefix+name)
		}
		for _, name := range t.findFuncCalls(root) {
			refs = append(refs, funcPrefix+name)
		}
		for _, name := range collectNames(root, func(o *pg_query.ObjectWithArgs) string { return t.formatNameList(o.Objname) }) {
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
		case pg_query.ObjectType_OBJECT_TABLE, pg_query.ObjectType_OBJECT_VIEW, pg_query.ObjectType_OBJECT_MATVIEW,
			pg_query.ObjectType_OBJECT_INDEX, pg_query.ObjectType_OBJECT_TYPE, pg_query.ObjectType_OBJECT_DOMAIN:
			if len(names) > 0 {
				refs = append(refs, relPrefix+t.formatNameList(names))
			}
		}
	}

	return refs
}

// typeKey returns the key of the relation or type tn refers to. A %TYPE reference
// names a column, so its last component is dropped.
func (t *Transformer) typeKey(tn *pg_query.TypeName) string {
	names := tn.Names
	if tn.PctType && len(names) > 1 {
		names = names[:len(names)-1]
	}
	return t.formatNameList(names)
}

// nameSep joins the components of a qualified name in keys. PostgreSQL identifiers
// cannot contain NUL, so app.f and "app.f" never share a key.
const nameSep = "\x00"

// formatNameList formats a qualified name as a key, the same way as formatRangeVar.
func (t *Transformer) formatNameList(nodes []*pg_query.Node) string {
	parts := make([]string, 0, len(nodes))
	for _, node := range nodes {
		parts = append(parts, node.GetString_().GetSval())
	}
	return strings.Join(parts, nameSep)
}

// displayName turns a key back into a qualified name for messages.
func displayName(key string) string {
	return strings.ReplaceAll(key, nameSep, ".")
}

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
				if !strings.Contains(name, nameSep) {
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
	return t.formatNameList(fn.Funcname)
}

// sortLate topologically sorts the late statements of a region, which are views and
// the statements that depend on them. defs and refs hold the names each statement of
// the region defines and references. Views that are ready at the same time are
// ordered by name, and other statements keep their relative order after them, so the
// output is deterministic.
func (t *Transformer) sortLate(stmts []*pg_query.RawStmt, late []bool, defs, refs [][]string) ([]*pg_query.RawStmt, error) {
	keys := make([]string, len(stmts))
	definers := make(map[string][]int)
	lateViews := make(map[string]bool)
	for i, stmt := range stmts {
		for _, name := range defs[i] {
			definers[name] = append(definers[name], i)
		}
		if !late[i] {
			continue
		}
		if isView(stmt) {
			keys[i] = t.getViewName(stmt)
			if lateViews[keys[i]] {
				return nil, fmt.Errorf("duplicate view statement for %s", displayName(keys[i]))
			}
			lateViews[keys[i]] = true
		} else {
			// UTF-8 never contains 0xff, so these keys sort after every view name.
			keys[i] = fmt.Sprintf("\xff%08d", i)
		}
	}

	inDegree := make([]int, len(stmts))
	dependents := make([][]int, len(stmts))
	addEdge := func(from, to int) {
		dependents[from] = append(dependents[from], to)
		inDegree[to]++
	}
	for i := range stmts {
		if !late[i] {
			continue
		}
		for _, ref := range refs[i] {
			for _, j := range t.prerequisites(definers[ref], i) {
				if j != i && late[j] {
					addEdge(j, i)
				}
			}
		}
		for _, name := range defs[i] {
			if j := lastLateBefore(definers[name], late, i); j >= 0 {
				addEdge(j, i)
			}
		}
	}

	var ready []int
	count := 0
	for i := range stmts {
		if late[i] {
			count++
			if inDegree[i] == 0 {
				ready = append(ready, i)
			}
		}
	}

	byKey := func(a, b int) int { return strings.Compare(keys[a], keys[b]) }
	result := make([]*pg_query.RawStmt, 0, count)
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

	if len(result) != count {
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

// lastLateBefore returns the last late definer in defs before index i, or -1. Successive
// definitions of a name, such as CREATE OR REPLACE, must keep their order.
func lastLateBefore(defs []int, late []bool, i int) int {
	last := -1
	for _, j := range defs {
		if j < i && late[j] {
			last = j
		}
	}
	return last
}

// prerequisites returns the definitions among defs that the statement at index i
// depends on. When a name has several definitions, such as function overloads, a
// reference following some of them was resolved against those in the original order,
// so only they are prerequisites. Otherwise every definition is.
func (t *Transformer) prerequisites(defs []int, i int) []int {
	if preceding := slices.DeleteFunc(slices.Clone(defs), func(j int) bool { return j >= i }); len(preceding) > 0 {
		return preceding
	}
	return defs
}

// describe names stmt for error messages, preferring the view name in key.
func (t *Transformer) describe(stmt *pg_query.RawStmt, key string) string {
	if isView(stmt) {
		return displayName(key)
	}
	if defs := t.definitions(stmt); len(defs) > 0 {
		return displayName(strings.TrimPrefix(strings.TrimPrefix(defs[0], relPrefix), funcPrefix))
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
		return rv.Schemaname + nameSep + rv.Relname
	}
	return rv.Relname
}

func (t *Transformer) findFuncCalls(node any) []string {
	return collectNames(node, func(fc *pg_query.FuncCall) string {
		return t.formatNameList(fc.Funcname)
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
