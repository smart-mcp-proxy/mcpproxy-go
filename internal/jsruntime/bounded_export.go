package jsruntime

import (
	"errors"
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

var exportMapType = reflect.TypeOf(map[string]interface{}{})

var (
	// errExportTooDeep: the value is nested deeper than the native depth
	// budget allows.
	errExportTooDeep = errors.New("nested too deeply to export")
	// errExportCycle: a script result refers to itself.
	errExportCycle = errors.New("contains a circular reference")
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
	if err != nil {
		panic(g.newRangeError(stackOverflowMessage))
	}
	return out
}

// exportArgs is export for tool-call arguments, which must be JSON data: a
// cycle throws a TypeError instead of becoming a cyclic Go map that Go code
// downstream (activity redaction, logging) would walk without end.
func (g *nativeGuard) exportArgs(v goja.Value) interface{} {
	out, err := g.exportMode(v, cyclesThrow)
	if err != nil {
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
}

// value exports v at the given level and returns it with its height.
func (e *exporter) value(v goja.Value, level int) (interface{}, int) {
	obj, ok := v.(*goja.Object)
	if !ok {
		if v == nil {
			return nil, 0
		}
		return v.Export(), 0
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
		return out, h
	}
	if e.base+level >= e.g.max {
		panic(exportError{errExportTooDeep})
	}
	if e.onPath != nil {
		e.onPath[obj] = true
		defer delete(e.onPath, obj)
	}
	out, below := e.object(obj, level)
	e.height[obj] = below + 1
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
		arr := make([]interface{}, internalLength(obj))
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
		m := make([][2]interface{}, len(entries)/2)
		e.seen[obj] = m
		for i := range m {
			m[i][0] = elem(entries[2*i+1]) // forEach passes (value, key)
			m[i][1] = elem(entries[2*i])
		}
		return m, below
	case "setObject":
		entries := e.g.collect(e.g.setForEach, obj)
		a := make([]interface{}, len(entries)/2)
		e.seen[obj] = a
		for i := range a {
			a[i] = elem(entries[2*i])
		}
		return a, below
	case "proxyObject":
		return obj.Export(), 0 // a goja.Proxy; goja does not descend into it
	}
	if len(kind) >= len("objectGo") && kind[:len("objectGo")] == "objectGo" {
		return obj.Export(), 0 // Go-backed: Export hands back the Go value itself
	}
	if obj.ExportType() != exportMapType {
		return obj.Export(), 0 // functions, dates, wrapped primitives, typed arrays, ...
	}
	// An ordinary object: goja exports its own enumerable string-keyed
	// properties (a String object's character indices are not among them),
	// each looked up by its JS key.
	names, exact := e.g.ownKeys(obj)
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

// toJS converts JSON-shaped Go data (maps, slices, primitives) into plain JS
// objects and arrays, the way JSON.parse would build them, so the script
// gets ordinary objects to read and write. Other Go values are wrapped by
// goja as before. Object keys are added in sorted order.
func (g *nativeGuard) toJS(v interface{}) goja.Value {
	switch t := v.(type) {
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
	return g.vm.ToValue(v)
}
