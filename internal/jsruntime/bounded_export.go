package jsruntime

import (
	"reflect"
	"sort"
	"strconv"

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

// exportError is how exportValue unwinds when the value is nested too deeply.
type exportError struct{}

// export converts v to Go the way v.Export() does. It panics with a
// RangeError when v is nested deeper than the native depth budget allows;
// script exceptions thrown by getters propagate as from Export.
func (g *nativeGuard) export(v goja.Value) interface{} {
	out, ok := g.tryExport(v, false)
	if !ok {
		panic(g.newRangeError(stackOverflowMessage))
	}
	return out
}

// exportArgs is export for tool-call arguments, which must be JSON data: a
// cycle throws a TypeError instead of becoming a cyclic Go map that Go code
// downstream (activity redaction, logging) would walk without end.
func (g *nativeGuard) exportArgs(v goja.Value) interface{} {
	out, ok := g.tryExport(v, true)
	if !ok {
		panic(g.newRangeError(stackOverflowMessage))
	}
	return out
}

// tryExport is export reporting a too-deep value as ok=false.
func (g *nativeGuard) tryExport(v goja.Value, rejectCycles bool) (out interface{}, ok bool) {
	saved := g.enter()
	defer g.restore(saved)
	defer func() {
		if r := recover(); r != nil {
			if _, deep := r.(exportError); !deep {
				panic(r)
			}
			out, ok = nil, false
		}
	}()
	e := &exporter{g: g, base: saved + 1, seen: make(map[*goja.Object]interface{})}
	if rejectCycles {
		e.onPath = make(map[*goja.Object]bool)
	}
	return e.value(v, 0), true
}

type exporter struct {
	g    *nativeGuard
	base int
	// seen is goja's export context: an object exported once is the same Go
	// value wherever it appears again (which also ends cycles).
	seen map[*goja.Object]interface{}
	// onPath, when set, holds the objects being exported above the current
	// one, so that a cycle is refused.
	onPath map[*goja.Object]bool
}

func (e *exporter) value(v goja.Value, level int) interface{} {
	obj, ok := v.(*goja.Object)
	if !ok {
		if v == nil {
			return nil
		}
		return v.Export()
	}
	if out, done := e.seen[obj]; done {
		if e.onPath[obj] {
			panic(e.g.vm.NewTypeError("Converting circular structure to JSON"))
		}
		return out
	}
	if e.base+level >= e.g.max {
		panic(exportError{})
	}
	if e.onPath != nil {
		e.onPath[obj] = true
		defer delete(e.onPath, obj)
	}
	// Script code a getter runs at this level sees the depth it runs at.
	e.g.depth = e.base + level

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
				arr[i] = e.value(el, level+1)
			}
		}
		return arr
	case "mapObject":
		entries := e.g.collect(e.g.mapForEach, obj)
		m := make([][2]interface{}, len(entries)/2)
		e.seen[obj] = m
		for i := range m {
			m[i][0] = e.value(entries[2*i+1], level+1) // forEach passes (value, key)
			m[i][1] = e.value(entries[2*i], level+1)
		}
		return m
	case "setObject":
		entries := e.g.collect(e.g.setForEach, obj)
		a := make([]interface{}, len(entries)/2)
		e.seen[obj] = a
		for i := range a {
			a[i] = e.value(entries[2*i], level+1)
		}
		return a
	case "proxyObject":
		return obj.Export() // a goja.Proxy; goja does not descend into it
	}
	if len(kind) >= len("objectGo") && kind[:len("objectGo")] == "objectGo" {
		return obj.Export() // Go-backed: Export hands back the Go value itself
	}
	if obj.ExportType() != exportMapType {
		return obj.Export() // functions, dates, wrapped primitives, typed arrays, ...
	}
	// An ordinary object: goja exports its own enumerable string-keyed
	// properties (a String object's character indices are not among them).
	keys := obj.Keys()
	m := make(map[string]interface{}, len(keys))
	e.seen[obj] = m
	strLen := -1
	if kind == "stringObject" {
		strLen = int(jsonToLength(obj.Get("length")))
	}
	for _, k := range keys {
		if strLen >= 0 {
			if i, err := strconv.Atoi(k); err == nil && i >= 0 && i < strLen && strconv.Itoa(i) == k {
				continue
			}
		}
		e.g.depth = e.base + level
		if el := obj.Get(k); el != nil {
			m[k] = e.value(el, level+1)
		} else {
			m[k] = nil
		}
	}
	return m
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
