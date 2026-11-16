package jsruntime

import (
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dop251/goja"
)

// Exporting a JS value to Go (the script's result, call_tool / call_tools
// arguments) and writing a JS value into a Go-backed object are native
// recursions too: goja's Value.Export descends one Go call per nesting level
// and runs no counted builtin on the way. A cyclic Map or Set (goja's Map and
// Set export has no cycle check) recursed forever without the heap budget
// ever applying, and a nested array of a depth the heap budget still allows
// overflows the Go stack on export. So:
//
//   - the guard exports through exportValue below, which follows goja's
//     Export value for value but counts each level against the native depth
//     budget, and
//   - Go data the script may write into (input, tool results, call_tools
//     slots) is handed to it as plain JS objects and arrays (toJS) instead of
//     goja's Go-backed wrappers, whose property writes call Export natively.
//
// The Go value keeps goja's sharing: an object that appears twice is exported
// once and referenced twice. Go code downstream (json.Marshal of the result,
// of tool arguments, activity logging) walks every reference, so a small
// acyclic graph that shares objects at every level
// (x = {a: x, b: x}, sixty times) is 2^60 work there, in Go code no timeout
// interrupts. The JSON data exports (a result, tool arguments) are therefore
// bounded by their expanded size too: every value counts each time it
// appears, at a lower bound of the bytes its JSON encoding takes, against
// maxExportSize.

// maxExportSize bounds the expanded size of a script result or of tool-call
// arguments, in (a lower bound of) JSON-encoded bytes. Distinct data that
// large would already exceed the default memory budget; the bound is reached
// in practice only through shared references.
const maxExportSize int64 = 64 << 20

// maxExportHoles bounds, per export, the array slots the exporter allocates
// for elements that do not exist (holes): an array's length is a number the
// script sets for free (new Array(67108861) is one small object), but the
// []interface{} goja's export builds for it is 16 bytes a slot, allocated in
// one native step that the memory watchdog cannot interrupt. Slots for
// elements that exist cost about what the script already holds for them.
// 1M holes is 16 MiB, a sixteenth of the default memory budget.
const maxExportHoles int64 = 1 << 20

var exportMapType = reflect.TypeOf(map[string]interface{}{})

var (
	// errExportTooDeep: the value is nested deeper than the native depth
	// budget allows.
	errExportTooDeep = errors.New("nested too deeply to export")
	// errExportCycle: a script result refers to itself.
	errExportCycle = errors.New("contains a circular reference")
	// errExportTooLarge: the value's expanded size exceeds maxExportSize.
	errExportTooLarge = fmt.Errorf("too large to serialize (more than %d MiB as JSON, counting every reference to a shared value)", maxExportSize>>20)
)

// exportError is how the exporter unwinds with one of the errors above.
type exportError struct{ err error }

// cycleMode selects what the exporter does with a value that refers to
// itself.
type cycleMode int

const (
	// cyclesKept: goja's semantics, the Go value refers to itself too.
	cyclesKept cycleMode = iota
	// cyclesThrow: a TypeError, as JSON.stringify throws (tool arguments).
	cyclesThrow
	// cyclesFail: errExportCycle (a script result, which must be JSON).
	cyclesFail
)

// export converts v to Go the way v.Export() does. It panics with a
// RangeError when v is nested deeper than the native depth budget allows;
// script exceptions thrown by getters propagate as from Export.
func (g *nativeGuard) export(v goja.Value) interface{} {
	out, err := g.exportMode(v, cyclesKept)
	switch {
	case errors.Is(err, errExportTooLarge):
		panic(g.newRangeError("value is " + errExportTooLarge.Error()))
	case err != nil:
		panic(g.newRangeError(stackOverflowMessage))
	}
	return out
}

// exportArgs is export for tool-call arguments, which must be JSON data: a
// cycle throws a TypeError instead of becoming a cyclic Go map that Go code
// downstream (activity redaction, logging) would walk without end.
func (g *nativeGuard) exportArgs(v goja.Value) interface{} {
	out, err := g.exportMode(v, cyclesThrow)
	switch {
	case errors.Is(err, errExportTooLarge):
		panic(g.newRangeError("tool arguments are " + errExportTooLarge.Error()))
	case err != nil:
		panic(g.newRangeError(stackOverflowMessage))
	}
	return out
}

// exportResult is export for the script's result, which is JSON-encoded
// next: a cycle is reported (errExportCycle) instead of handed on, so that
// every path through the Go value is bounded by the depth budget.
func (g *nativeGuard) exportResult(v goja.Value) (interface{}, error) {
	return g.exportMode(v, cyclesFail)
}

// tryExport is export (or, with rejectCycles, exportArgs) reporting a
// too-deep value as ok=false.
func (g *nativeGuard) tryExport(v goja.Value, rejectCycles bool) (out interface{}, ok bool) {
	mode := cyclesKept
	if rejectCycles {
		mode = cyclesThrow
	}
	out, err := g.exportMode(v, mode)
	return out, err == nil
}

func (g *nativeGuard) exportMode(v goja.Value, mode cycleMode) (out interface{}, err error) {
	saved := g.enter()
	defer g.restore(saved)
	defer func() {
		if r := recover(); r != nil {
			ee, ours := r.(exportError)
			if !ours {
				panic(r)
			}
			out, err = nil, ee.err
		}
	}()
	e := &exporter{g: g, base: saved + 1, mode: mode, seen: make(map[*goja.Object]interface{}), height: make(map[*goja.Object]int)}
	if mode != cyclesKept {
		e.onPath = make(map[*goja.Object]bool)
		e.size = make(map[*goja.Object]int64)
	}
	out, _ = e.value(v, 0)
	return out, nil
}

type exporter struct {
	g    *nativeGuard
	base int
	mode cycleMode
	// seen is goja's export context: an object exported once is the same Go
	// value wherever it appears again (which also ends cycles).
	seen map[*goja.Object]interface{}
	// height is, for every object whose export is complete, the number of
	// object levels of its Go value (1 for an object holding no objects).
	// An object reused at a level counts with its height, so the longest
	// path through the Go graph, and not only the path the export walked,
	// stays within the budget (an acyclic graph that reuses exported
	// objects can otherwise be arbitrarily deep).
	height map[*goja.Object]int
	// onPath, when cycles are refused, holds the objects being exported
	// above the current one.
	onPath map[*goja.Object]bool
	// size, when cycles are refused (JSON data), is the expanded size of
	// every object whose export is complete, and used the expanded size of
	// everything exported so far; see maxExportSize.
	size map[*goja.Object]int64
	used int64
	// holes counts the slots allocated for missing array elements; see
	// maxExportHoles.
	holes int64
}

// charge counts n more bytes of expanded size. Every mode counts: the
// charges made before an allocation (an array's length, a Map's entries) are
// what keeps a script from making the exporter allocate a slice of any length
// it likes (new Array(4294967295) is one small object). Where cycles are kept
// (export), references to an exported object are not counted again: its Go
// value is shared, and nothing encodes it.
func (e *exporter) charge(n int64) {
	e.used += n
	if e.used > maxExportSize || n < 0 {
		panic(exportError{errExportTooLarge})
	}
}

// leafSize is a lower bound of the JSON size of a value exported as is, and
// within a constant factor of the work encoding it takes:
//
//   - a string: its bytes and quotes (escaping only adds to that, at most
//     six bytes per byte);
//   - a BigInt (*big.Int, also from a BigInt object): the digits of its
//     decimal text, which json.Marshal writes in full, bounded from below
//     by its bit length without converting it;
//   - a typed array ([]uint8 is encoded as base64, the others element by
//     element) and other Go slices, arrays and maps: a byte per element;
//   - numbers, booleans, a Date's time.Time, functions, an ArrayBuffer, a
//     Promise, a goja.Proxy: a byte (each encodes to a few dozen bytes at
//     most, "{}", or an error).
//
// Go-backed values (objectGo*) never reach a script: Go data is handed to it
// through toJS as plain JS objects.
func leafSize(out interface{}) int64 {
	switch t := out.(type) {
	case nil:
		return 4
	case string:
		return int64(len(t)) + 2
	case *big.Int:
		return bigIntDigits(t)
	}
	rv := reflect.ValueOf(out)
	switch rv.Kind() {
	case reflect.Slice, reflect.Array, reflect.Map, reflect.String:
		return int64(rv.Len()) + 2 // a typed array's elements, ...
	}
	return 1
}

// bigIntDigits is a lower bound of the length of x's decimal text: a number
// of bit length b >= 1 is at least 2^(b-1), so it has at least
// floor((b-1) * log10(2)) + 1 digits (30102/100000 < log10(2)), plus a sign.
func bigIntDigits(x *big.Int) int64 {
	if x == nil {
		return 4
	}
	n := int64(1)
	if b := int64(x.BitLen()); b > 1 {
		n += (b - 1) * 30102 / 100000
	}
	if x.Sign() < 0 {
		n++
	}
	return n
}

// value exports v at the given level and returns it with its height.
func (e *exporter) value(v goja.Value, level int) (interface{}, int) {
	obj, ok := v.(*goja.Object)
	if !ok {
		if v == nil {
			e.charge(4)
			return nil, 0
		}
		out := v.Export()
		e.charge(leafSize(out))
		return out, 0
	}
	if out, done := e.seen[obj]; done {
		if e.onPath[obj] {
			if e.mode == cyclesFail {
				panic(exportError{errExportCycle})
			}
			panic(e.g.vm.NewTypeError("Converting circular structure to JSON"))
		}
		// An object still being exported (a cycle, kept) has no height yet:
		// cycles are only kept by export, whose Go value nothing walks.
		h := e.height[obj]
		if h > 0 && e.base+level+h-1 >= e.g.max {
			panic(exportError{errExportTooDeep})
		}
		e.charge(e.size[obj]) // each reference is encoded in full
		return out, h
	}
	if e.base+level >= e.g.max {
		panic(exportError{errExportTooDeep})
	}
	if e.onPath != nil {
		e.onPath[obj] = true
		defer delete(e.onPath, obj)
	}
	before := e.used
	out, below := e.object(obj, level)
	e.height[obj] = below + 1
	if e.size != nil {
		e.size[obj] = e.used - before
	}
	return out, below + 1
}

// object exports one object and returns it with the height of its tallest
// element.
func (e *exporter) object(obj *goja.Object, level int) (interface{}, int) {
	// Script code a getter runs at this level sees the depth it runs at.
	e.g.depth = e.base + level

	below := 0
	elem := func(v goja.Value) interface{} {
		out, h := e.value(v, level+1)
		if h > below {
			below = h
		}
		return out
	}

	kind := ""
	if s := objectImpl(obj); s.IsValid() {
		kind = s.Type().Name()
	}
	switch kind {
	case "arrayObject", "sparseArrayObject", "argumentsObject":
		n := internalLength(obj)
		e.charge(int64(n) + 2) // "[]" and a byte per element, before allocating
		if missing := int64(n) - int64(existingElements(obj)); missing > 0 {
			e.holes += missing
			if e.holes > maxExportHoles {
				panic(exportError{errExportTooLarge})
			}
		}
		arr := make([]interface{}, n)
		e.seen[obj] = arr
		for i := range arr {
			e.g.depth = e.base + level
			if el := obj.Get(strconv.Itoa(i)); el != nil {
				arr[i] = elem(el)
			}
		}
		return arr, below
	case "mapObject":
		entries := e.g.collect(e.g.mapForEach, obj)
		e.charge(int64(len(entries)) + 2)
		m := make([][2]interface{}, len(entries)/2)
		e.seen[obj] = m
		for i := range m {
			m[i][0] = elem(entries[2*i+1]) // forEach passes (value, key)
			m[i][1] = elem(entries[2*i])
		}
		return m, below
	case "setObject":
		entries := e.g.collect(e.g.setForEach, obj)
		e.charge(int64(len(entries)/2) + 2)
		a := make([]interface{}, len(entries)/2)
		e.seen[obj] = a
		for i := range a {
			a[i] = elem(entries[2*i])
		}
		return a, below
	case "proxyObject":
		e.charge(2)
		return obj.Export(), 0 // a goja.Proxy; goja does not descend into it
	}
	if len(kind) >= len("objectGo") && kind[:len("objectGo")] == "objectGo" {
		out := obj.Export() // Go-backed: Export hands back the Go value itself
		e.charge(leafSize(out))
		return out, 0
	}
	if obj.ExportType() != exportMapType {
		out := obj.Export() // functions, dates, wrapped primitives, typed arrays, ...
		e.charge(leafSize(out))
		return out, 0
	}
	// An ordinary object: goja exports its own enumerable string-keyed
	// properties (a String object's character indices are not among them),
	// each looked up by its JS key.
	names, exact := e.g.ownKeys(obj)
	e.charge(2)
	m := make(map[string]interface{}, len(names))
	e.seen[obj] = m
	strLen := -1
	if kind == "stringObject" {
		strLen = int(jsonToLength(obj.Get("length")))
	}
	for i, k := range names {
		if strLen >= 0 {
			if n, err := strconv.Atoi(k); err == nil && n >= 0 && n < strLen && strconv.Itoa(n) == k {
				continue
			}
		}
		e.g.depth = e.base + level
		e.charge(int64(len(k)) + 3) // "k":
		var el goja.Value
		if exact != nil {
			el = e.g.reflectGet(goja.FunctionCall{Arguments: []goja.Value{obj, exact[i]}})
		} else {
			el = obj.Get(k)
		}
		if el != nil {
			m[k] = elem(el)
		} else {
			m[k] = nil
		}
	}
	return m, below
}

// exportsAsMap reports whether the exporter turns obj into a
// map[string]interface{}: an ordinary object, as opposed to an array, a Map
// or Set, a proxy or a value goja exports as itself.
func exportsAsMap(obj *goja.Object) bool {
	switch objectKind(obj) {
	case "arrayObject", "sparseArrayObject", "argumentsObject", "mapObject", "setObject", "proxyObject":
		return false
	}
	return obj.ExportType() == exportMapType
}

// ownKeys returns obj's own enumerable string keys as Go strings, the way
// goja's export names them. A lone surrogate in a key has become U+FFFD
// there, so a lookup by that string would miss the property: when a key
// holds U+FFFD, the keys are also returned as the JS strings they are, read
// through the original Object.keys, to look the properties up by.
func (g *nativeGuard) ownKeys(obj *goja.Object) (names []string, exact []goja.Value) {
	names = obj.Keys()
	for _, n := range names {
		if !strings.ContainsRune(n, utf8.RuneError) {
			continue
		}
		arr, ok := g.objectKeys(goja.FunctionCall{Arguments: []goja.Value{obj}}).(*goja.Object)
		if !ok {
			break
		}
		exact = make([]goja.Value, internalLength(arr))
		names = make([]string, len(exact))
		for i := range exact {
			exact[i] = arr.Get(strconv.Itoa(i))
			names[i] = exact[i].String()
		}
		break
	}
	return names, exact
}

// internalLength is the length goja's export uses for an array or an
// arguments object (its internal length, not a script-writable property).
func internalLength(obj *goja.Object) int {
	f, ok := fieldByName(objectImpl(obj), "length")
	if !ok {
		return 0
	}
	switch f.Kind() {
	case reflect.Int, reflect.Int64, reflect.Int32:
		return int(f.Int())
	case reflect.Uint32, reflect.Uint, reflect.Uint64:
		return int(f.Uint())
	}
	return 0
}

// existingElements is the number of elements an array or arguments object
// actually stores (its length may be far larger), or 0 when unknown.
func existingElements(obj *goja.Object) int {
	s := objectImpl(obj)
	if !s.IsValid() {
		return 0
	}
	field := "values" // arrayObject: []Value; argumentsObject: its property map
	if s.Type().Name() == "sparseArrayObject" {
		field = "items"
	}
	f, ok := fieldByName(s, field)
	if !ok {
		return 0
	}
	switch f.Kind() {
	case reflect.Slice, reflect.Map:
		return f.Len()
	}
	return 0
}

// collect runs the original Map/Set forEach over obj and returns what it
// passed to the callback, two values per entry. No script code runs.
func (g *nativeGuard) collect(forEach nativeCallFunc, obj *goja.Object) []goja.Value {
	var out []goja.Value
	cb := g.vm.ToValue(func(call goja.FunctionCall) goja.Value {
		out = append(out, call.Argument(0), call.Argument(1))
		return goja.Undefined()
	})
	forEach(goja.FunctionCall{This: obj, Arguments: []goja.Value{cb}})
	return out
}

// toJS converts Go data into plain JS objects and arrays, the way JSON.parse
// would build them, so the script gets ordinary objects to read and write.
// JSON-shaped data (map[string]interface{}, []interface{}, primitives) is
// converted directly; any other Go value (a typed map or slice, a struct, a
// pointer, which goja would wrap as a Go-backed object whose writes export
// natively and which JSON.stringify serializes without the guard's
// bookkeeping) is first reduced to its JSON shape. Only a value JSON cannot
// encode is still wrapped by goja. Object keys are added in sorted order.
func (g *nativeGuard) toJS(v interface{}) goja.Value {
	switch t := v.(type) {
	case nil, bool, string, float64, float32, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, goja.Value:
		return g.vm.ToValue(v)
	case map[string]interface{}:
		obj := g.vm.NewObject()
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			// Defined, not set: a "__proto__" key is data, as in JSON.parse.
			if err := obj.DefineDataProperty(k, g.toJS(t[k]), goja.FLAG_TRUE, goja.FLAG_TRUE, goja.FLAG_TRUE); err != nil {
				panic(err)
			}
		}
		return obj
	case []interface{}:
		items := make([]interface{}, len(t))
		for i, el := range t {
			items[i] = g.toJS(el)
		}
		return g.vm.NewArray(items...)
	}
	if plain, err := normalizeToolResult(v); err == nil {
		return g.toJS(plain)
	}
	return g.vm.ToValue(v)
}
