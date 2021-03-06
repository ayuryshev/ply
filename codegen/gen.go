package codegen

import (
	"go/ast"
	"strconv"
	"strings"

	"github.com/lukechampine/ply/types"
)

type rewriter func(*ast.CallExpr) ast.Node

func rewriteFunc(name string) rewriter {
	return func(c *ast.CallExpr) ast.Node {
		c.Fun = ast.NewIdent(name)
		return c
	}
}

func rewriteMethod(name string) rewriter {
	return func(c *ast.CallExpr) ast.Node {
		fn := c.Fun.(*ast.SelectorExpr)
		c.Fun = &ast.SelectorExpr{
			X: &ast.CallExpr{
				Fun:  ast.NewIdent(name),
				Args: []ast.Expr{fn.X},
			},
			Sel: ast.NewIdent(fn.Sel.Name),
		}
		return c
	}
}

var funcGenerators = map[string]func(*ast.Ident, []ast.Expr, map[ast.Expr]types.TypeAndValue) (string, string, rewriter){
	"enum":  enumGen,
	"max":   maxGen,
	"merge": mergeGen,
	"min":   minGen,
	"not":   notGen,
	"zip":   zipGen,
}

var methodGenerators = map[string]func(*ast.SelectorExpr, []ast.Expr, map[ast.Expr]types.TypeAndValue) (string, string, rewriter){
	"all":       genSliceMethod(allTempl, "all_slice"),
	"any":       genSliceMethod(anyTempl, "any_slice"),
	"contains":  containsGen,
	"drop":      genSliceMethod(dropTempl, "drop_slice"),
	"dropWhile": genSliceMethod(dropWhileTempl, "dropWhile_slice"),
	"elems":     elemsGen,
	"filter":    filterGen,
	"fold":      foldGen,
	"foreach":   genSliceMethod(foreachTempl, "foreach_slice"),
	"keys":      keysGen,
	"morph":     morphGen,
	"reverse":   genSliceMethod(reverseTempl, "reverse_slice"),
	"sort":      sortGen,
	"take":      genSliceMethod(takeTempl, "take_slice"),
	"takeWhile": genSliceMethod(takeWhileTempl, "takeWhile_slice"),
	"tee":       genSliceMethod(teeTempl, "tee_slice"),
	"toMap":     toMapGen,
	"toSet":     genSliceMethod(toSetTempl, "toSet_slice"),
	"uniq":      genSliceMethod(uniqTempl, "uniq_slice"),
}

var safeFnName = func() func(string) string {
	count := 0
	return func(name string) string {
		count++
		return "__plyfn_" + strconv.Itoa(count) + "_" + name
	}
}()

var safeTypeName = func() func(string) string {
	count := 0
	return func(name string) string {
		count++
		return "__plytype_" + strconv.Itoa(count) + "_" + name
	}
}()

func specify(templ, name string, typs ...types.Type) string {
	code := strings.Replace(templ, "#name", name, -1)
	for i, t := range typs {
		typVar := 'T' + byte(i) // T, U, V, etc.
		code = strings.Replace(code, "#"+string(typVar), t.String(), -1)
	}
	return code
}

func genFunc(templ, fnname string, typs ...types.Type) (name, code string, r rewriter) {
	name = safeFnName(fnname)
	code = specify(templ, name, typs...)
	r = rewriteFunc(name)
	return
}

func genMethod(templ, methodname string, typs ...types.Type) (name, code string, r rewriter) {
	name = safeTypeName(methodname)
	code = specify(templ, name, typs...)
	r = rewriteMethod(name)
	return
}

// for slice methods that just need T
func genSliceMethod(templ, methodname string) func(fn *ast.SelectorExpr, args []ast.Expr, exprTypes map[ast.Expr]types.TypeAndValue) (name, code string, r rewriter) {
	return func(fn *ast.SelectorExpr, args []ast.Expr, exprTypes map[ast.Expr]types.TypeAndValue) (name, code string, r rewriter) {
		T := exprTypes[fn.X].Type.Underlying().(*types.Slice).Elem()
		return genMethod(templ, methodname, T)
	}
}

const enumTempl = `
func #name(x, y, s #T) []#T {
	if s == 0 || (x < y && s < 0) || (x > y && s > 0) {
		panic("non-terminating enum")
	}
	e := make([]#T, 0, 1+(y-x)/s)
	for i := x; (x < y && i < y) || (x > y && i > y); i += s {
		e = append(e, i)
	}
	return e
}
`

const enum2Templ = `
func #name(x, y #T) []#T {
	if x > y {
		panic("non-terminating enum")
	}
	e := make([]#T, y-x)
	for i := range e {
		e[i] = x+#T(i)
	}
	return e
}
`

const enum1Templ = `
func #name(x #T) []#T {
	if x < 0 {
		panic("non-terminating enum")
	}
	e := make([]#T, x)
	for i := range e {
		e[i] = #T(i)
	}
	return e
}
`

func enumGen(fn *ast.Ident, args []ast.Expr, exprTypes map[ast.Expr]types.TypeAndValue) (name, code string, r rewriter) {
	T := exprTypes[args[0]].Type
	switch len(args) {
	case 3:
		return genFunc(enumTempl, "enum", T)
	case 2:
		return genFunc(enum2Templ, "enum", T)
	case 1:
		return genFunc(enum1Templ, "enum", T)
	}
	return
}

const maxTempl = `
func #name(a, b #T) #T {
	if a > b {
		return a
	}
	return b
}
`

func maxGen(fn *ast.Ident, args []ast.Expr, exprTypes map[ast.Expr]types.TypeAndValue) (name, code string, r rewriter) {
	T := exprTypes[args[0]].Type
	return genFunc(maxTempl, "max", T)
}

const mergeTempl = `
func #name(recv map[#T]#U, rest ...map[#T]#U) map[#T]#U {
	if len(rest) == 0 {
		return recv
	} else if recv == nil {
		recv = make(map[#T]#U, len(rest[0]))
	}
	for _, m := range rest {
		for k, v := range m {
			recv[k] = v
		}
	}
	return recv
}
`

func mergeGen(fn *ast.Ident, args []ast.Expr, exprTypes map[ast.Expr]types.TypeAndValue) (name, code string, r rewriter) {
	// seek until we find a non-nil arg
	var mt *types.Map
	for _, arg := range args {
		var ok bool
		if mt, ok = exprTypes[arg].Type.(*types.Map); ok {
			break
		}
	}
	return genFunc(mergeTempl, "merge", mt.Key(), mt.Elem())
}

const minTempl = `
func #name(a, b #T) #T {
	if a < b {
		return a
	}
	return b
}
`

func minGen(fn *ast.Ident, args []ast.Expr, exprTypes map[ast.Expr]types.TypeAndValue) (name, code string, r rewriter) {
	T := exprTypes[args[0]].Type
	return genFunc(minTempl, "min", T)
}

const notTempl = `
func #name(fn #T) #T {
	return #T {
		return !fn(#args)
	}
}
`

func notGen(fn *ast.Ident, args []ast.Expr, exprTypes map[ast.Expr]types.TypeAndValue) (name, code string, r rewriter) {
	sig := exprTypes[args[0]].Type.Underlying().(*types.Signature)
	callArgs := make([]string, sig.Params().Len())
	for i := range callArgs {
		callArgs[i] = sig.Params().At(i).Name()
	}
	name, code, r = genFunc(notTempl, "not", sig)
	// not requires an additional rewrite for the arguments
	code = strings.Replace(code, "#args", strings.Join(callArgs, ", "), -1)
	return
}

const zipTempl = `
func #name(fn func(a #T, b #U) #V, a []#T, b []#U) []#V {
	var zipped []#V
	if len(a) < len(b) {
		zipped = make([]#V, len(a))
	} else {
		zipped = make([]#V, len(b))
	}
	for i := range zipped {
		zipped[i] = fn(a[i], b[i])
	}
	return zipped
}
`

func zipGen(fn *ast.Ident, args []ast.Expr, exprTypes map[ast.Expr]types.TypeAndValue) (name, code string, r rewriter) {
	// determine arg types
	sig := exprTypes[args[0]].Type.(*types.Signature)
	T := sig.Params().At(0).Type()
	U := sig.Params().At(1).Type()
	V := sig.Results().At(0).Type()
	return genFunc(zipTempl, "zip", T, U, V)
}

const allTempl = `
type #name []#T

func (xs #name) all(pred func(#T) bool) bool {
	for _, x := range xs {
		if !pred(x) {
			return false
		}
	}
	return true
}
`

const anyTempl = `
type #name []#T

func (xs #name) any(pred func(#T) bool) bool {
	for _, x := range xs {
		if pred(x) {
			return true
		}
	}
	return false
}
`

const containsSliceTempl = `
type #name []#T

func (xs #name) contains(e #T) bool {
	for _, x := range xs {
		if x == e {
			return true
		}
	}
	return false
}
`

const containsSliceNilTempl = `
type #name []#T

func (xs #name) contains(_ #T) bool {
	for _, x := range xs {
		if x == nil {
			return true
		}
	}
	return false
}
`

const containsMapTempl = `
type #name map[#T]#U

func (m #name) contains(e #T) bool {
	_, ok := m[e]
	return ok
}
`

func containsGen(fn *ast.SelectorExpr, args []ast.Expr, exprTypes map[ast.Expr]types.TypeAndValue) (name, code string, r rewriter) {
	switch typ := exprTypes[fn.X].Type.Underlying().(type) {
	case *types.Slice:
		if T := typ.Elem(); !types.Comparable(T) {
			// if type is not comparable, then the argument must be nil
			// (otherwise type-check would have failed)
			return genMethod(containsSliceNilTempl, "contains_slice_nil", T)
		} else {
			return genMethod(containsSliceTempl, "contains_slice", T)
		}
	case *types.Map:
		return genMethod(containsMapTempl, "contains_map", typ.Key(), typ.Elem())
	}
	return
}

const dropTempl = `
type #name []#T

func (xs #name) drop(n int) []#T {
	if n > len(xs) {
		n = len(xs)
	}
	return xs[n:]
}
`

const dropWhileTempl = `
type #name []#T

func (xs #name) dropWhile(pred func(#T) bool) []#T {
	var i int
	for i = range xs {
		if !pred(xs[i]) {
			break
		}
	}
	return append([]#T(nil), xs[i:]...)
}
`

const elemsTempl = `
type #name map[#T]#U

func (m #name) elems() []#U {
	es := make([]#U, 0, len(m))
	for _, e := range m {
		es = append(es, e)
	}
	return es
}
`

func elemsGen(fn *ast.SelectorExpr, args []ast.Expr, exprTypes map[ast.Expr]types.TypeAndValue) (name, code string, r rewriter) {
	mt := exprTypes[fn.X].Type.Underlying().(*types.Map)
	return genMethod(elemsTempl, "elems_map", mt.Key(), mt.Elem())
}

const filterTempl = `
type #name []#T

func (xs #name) filter(pred func(#T) bool) []#T {
	var filtered []#T
	for _, x := range xs {
		if pred(x) {
			filtered = append(filtered, x)
		}
	}
	return filtered
}
`

const filterMapTempl = `
type #name map[#T]#U

func (m #name) filter(pred func(#T, #U) bool) map[#T]#U {
	if m == nil {
		return nil
	}
	filtered := make(map[#T]#U)
	for k, e := range m {
		if pred(k, e) {
			filtered[k] = e
		}
	}
	return filtered
}
`

func filterGen(fn *ast.SelectorExpr, args []ast.Expr, exprTypes map[ast.Expr]types.TypeAndValue) (name, code string, r rewriter) {
	switch typ := exprTypes[fn.X].Type.Underlying().(type) {
	case *types.Slice:
		return genMethod(filterTempl, "filter_slice", typ.Elem())
	case *types.Map:
		return genMethod(filterMapTempl, "filter_map", typ.Key(), typ.Elem())
	}
	return
}

const foldTempl = `
type #name []#T

func (xs #name) fold(fn func(#U, #T) #U, acc #U) #U {
	for _, x := range xs {
		acc = fn(acc, x)
	}
	return acc
}
`

const fold1Templ = `
type #name []#T

func (xs #name) fold(fn func(#U, #T) #U) #U {
	if len(xs) == 0 {
		panic("fold of empty slice")
	}
	acc := xs[0]
	for _, x := range xs[1:] {
		acc = fn(acc, x)
	}
	return acc
}
`

func foldGen(fn *ast.SelectorExpr, args []ast.Expr, exprTypes map[ast.Expr]types.TypeAndValue) (name, code string, r rewriter) {
	// determine arg types
	sig := exprTypes[args[0]].Type.(*types.Signature)
	T := sig.Params().At(1).Type()
	U := sig.Params().At(0).Type()
	if len(args) == 1 {
		return genMethod(fold1Templ, "fold1_slice", T, U)
	} else if len(args) == 2 {
		return genMethod(foldTempl, "fold_slice", T, U)
	}
	return
}

const foreachTempl = `
type #name []#T

func (xs #name) foreach(fn func(#T)) {
	for _, x := range xs {
		fn(x)
	}
}
`

const keysTempl = `
type #name map[#T]#U

func (m #name) keys() []#T {
	ks := make([]#T, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
`

func keysGen(fn *ast.SelectorExpr, args []ast.Expr, exprTypes map[ast.Expr]types.TypeAndValue) (name, code string, r rewriter) {
	mt := exprTypes[fn.X].Type.Underlying().(*types.Map)
	return genMethod(keysTempl, "keys_map", mt.Key(), mt.Elem())
}

const morphTempl = `
type #name []#T

func (xs #name) morph(fn func(#T) #U) []#U {
	morphed := make([]#U, len(xs))
	for i := range xs {
		morphed[i] = fn(xs[i])
	}
	return morphed
}
`

const morphMapTempl = `
type #name map[#T]#U

func (m #name) morph(fn func(#T, #U) (#V, #W)) map[#V]#W {
	if m == nil {
		return nil
	}
	morphed := make(map[#V]#W, len(m))
	for k, e := range m {
		mk, me := fn(k, e)
		morphed[mk] = me
	}
	return morphed
}
`

func morphGen(fn *ast.SelectorExpr, args []ast.Expr, exprTypes map[ast.Expr]types.TypeAndValue) (name, code string, r rewriter) {
	sig := exprTypes[args[0]].Type.Underlying().(*types.Signature)
	switch exprTypes[fn.X].Type.Underlying().(type) {
	case *types.Slice:
		T := sig.Params().At(0).Type()
		U := sig.Results().At(0).Type()
		return genMethod(morphTempl, "morph_slice", T, U)
	case *types.Map:
		T := sig.Params().At(0).Type()
		U := sig.Params().At(1).Type()
		V := sig.Results().At(0).Type()
		W := sig.Results().At(1).Type()
		return genMethod(morphMapTempl, "morph_map", T, U, V, W)
	}
	return
}

const reverseTempl = `
type #name []#T

func (xs #name) reverse() []#T {
	reversed := make([]#T, len(xs))
	for i := range xs {
		reversed[i] = xs[len(xs)-1-i]
	}
	return reversed
}
`
const sortTempl = `
type #name []#T

func (xs #name) Len() int           { return len(xs) }
func (xs #name) Swap(i, j int)      { xs[i], xs[j] = xs[j], xs[i] }
func (xs #name) Less(i, j int) bool { return xs[i] < xs[j] }

func (xs #name) sort() []#T {
	s := make([]#T, len(xs))
	copy(s, xs)
	sort.Sort(#name(s))
	return s
}
`

const sortByTempl = `
type #name []#T

type #namesorter struct {
	data []#T
	less func(#T, #T) bool
}

func (xs #namesorter) Len() int { return len(xs.data) }
func (xs #namesorter) Swap(i, j int) { xs.data[i], xs.data[j] = xs.data[j], xs.data[i] }
func (xs #namesorter) Less(i, j int) bool { return xs.less(xs.data[i], xs.data[j]) }

func (xs #name) sort(less func(#T, #T) bool) []#T {
	s := #namesorter{make([]#T, len(xs)), less}
	copy(s.data, xs)
	sort.Sort(s)
	return s.data
}
`

func sortGen(fn *ast.SelectorExpr, args []ast.Expr, exprTypes map[ast.Expr]types.TypeAndValue) (name, code string, r rewriter) {
	// determine arg types
	T := exprTypes[fn.X].Type.Underlying().(*types.Slice).Elem()
	if len(args) == 0 {
		return genMethod(sortTempl, "sort_slice", T)
	} else if len(args) == 1 {
		return genMethod(sortByTempl, "sortBy_slice", T)
	}
	return
}

const takeTempl = `
type #name []#T

func (xs #name) take(n int) []#T {
	if n > len(xs) {
		n = len(xs)
	}
	return xs[:n]
}
`

const takeWhileTempl = `
type #name []#T

func (xs #name) takeWhile(pred func(#T) bool) []#T {
	var i int
	for i = range xs {
		if !pred(xs[i]) {
			break
		}
	}
	return append([]#T(nil), xs[:i]...)
}
`

const teeTempl = `
type #name []#T

func (xs #name) tee(fn func(#T)) []#T {
	for _, x := range xs {
		fn(x)
	}
	return xs
}
`

const toMapTempl = `
type #name []#T

func (xs #name) toMap(fn func(#T) #U) map[#T]#U {
	m := make(map[#T]#U)
	for _, x := range xs {
		m[x] = fn(x)
	}
	return m
}
`

func toMapGen(fn *ast.SelectorExpr, args []ast.Expr, exprTypes map[ast.Expr]types.TypeAndValue) (name, code string, r rewriter) {
	// determine arg type
	sig := exprTypes[args[0]].Type.(*types.Signature)
	T := sig.Params().At(0).Type()
	U := sig.Results().At(0).Type()
	return genMethod(toMapTempl, "toMap_slice", T, U)
}

const toSetTempl = `
type #name []#T

func (xs #name) toSet() map[#T]struct{} {
	set := make(map[#T]struct{})
	for _, x := range xs {
		set[x] = struct{}{}
	}
	return set
}
`

const uniqTempl = `
type #name []#T

func (xs #name) uniq() []#T {
	set := make(map[#T]struct{})
	var unique []#T
	for _, x := range xs {
		if _, ok := set[x]; !ok {
			unique = append(unique, x)
			set[x] = struct{}{}
		}
	}
	return unique
}
`
