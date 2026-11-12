package jsruntime

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"unsafe"

	"github.com/dop251/goja"
)

// Stack-depth limits for code_execution.
//
// goja runs JS-to-JS calls on its own heap-allocated call stack, but a builtin
// implemented in Go that calls back into the object protocol (ToString,
// valueOf, a getter, a toJSON, Array.prototype.join on each element, ...)
// recurses on the Go stack of the script goroutine. When that goroutine's
// stack passes the runtime's 1 GB maximum the process dies with an
// unrecoverable "fatal error: stack overflow": no recover(), no vm.Interrupt
// (which goja only checks between interpreter instructions) and no memory
// budget (a self-referencing builtin needs no heap at all) can stop it. A
// script could take the whole proxy down with, for example,
//
//	var a = []; for (var i = 0; i < 600000; i++) a = [a]; String(a)
//	var o = {}; o.toString = String.prototype.trim; String(o)
//
// Two limits close this class:
//
//   - maxCallStackDepth bounds JS frames (goja's default is MaxInt32). A JS
//     function that re-enters native code (function f(){[1].forEach(f)},
//     g.apply(this)) costs Go stack per frame, so its depth must be bounded;
//     goja then fails the execution with a *goja.StackOverflowError.
//   - maxNativeDepth bounds how many builtin calls may be nested at once.
//     Every builtin function object of the VM is instrumented in place (see
//     installStackGuard) so any recursion through builtins, however it is
//     wired, counts against it and fails with a catchable RangeError.
//
// JSON.stringify, JSON.parse and Array.prototype.flat recurse inside a single
// builtin call, so they get their own depth checks against the same budget,
// as does exporting a value to Go (bounded_export.go). Two recursions involve
// no builtin call at all and are closed structurally: a property lookup on a
// prototype cycle through a proxy (wrapSetPrototypeOf refuses the cycle) and
// a callable proxy used as a trap (guardedHandler counts every trap call).
//
// What is left is recursion over an acyclic structure that must be built
// first: deep prototype or proxy-target chains. Each link costs far more heap
// than Go stack (measured on darwin/arm64: the memory budget stops the build
// at about 1-2 million links, the Go stack would hold roughly 8-20 million),
// so the memory budget stops those; with the budget disabled
// (MaxMemoryBytes < 0) they are bounded only by the host's memory.
//
// Recursion in goja's parser and compiler, on deeply nested source text, is
// bounded separately by source_guard.go.
const (
	maxCallStackDepth = 4096
	maxNativeDepth    = 2048
)

const (
	stackOverflowMessage = "Maximum call stack size exceeded"
	// cyclicProtoMessage is goja's own error for a prototype cycle.
	cyclicProtoMessage = "Cyclic __proto__ value"
)

// errStackGuardSetup reports that the builtins could not be instrumented. It
// fails the execution closed: running without the guard would put the
// process back within reach of a script.
var errStackGuardSetup = errors.New("code_execution sandbox setup failed: cannot instrument builtins against native stack overflow")

type (
	nativeCallFunc      = func(goja.FunctionCall) goja.Value
	nativeConstructFunc = func([]goja.Value, *goja.Object) *goja.Object
)

var (
	nativeCallType      = reflect.TypeOf((nativeCallFunc)(nil))
	nativeConstructType = reflect.TypeOf((nativeConstructFunc)(nil))
)

// nativeGuard is the per-execution count of nested builtin calls. It is only
// touched from the script goroutine (goja is single-threaded per VM).
type nativeGuard struct {
	vm    *goja.Runtime
	depth int
	max   int

	newRangeError func(string) *goja.Object
	isArray       nativeCallFunc
	funcCall      nativeCallFunc // the original Function.prototype.call
	mapForEach    nativeCallFunc // the original Map.prototype.forEach
	setForEach    nativeCallFunc // the original Set.prototype.forEach

	// patched is every function object instrumented so far.
	patched map[*goja.Object]struct{}
}

// enter counts one more nested builtin call and returns the depth to restore
// when it returns. Past the limit it throws a RangeError instead.
func (g *nativeGuard) enter() int {
	saved := g.depth
	if saved >= g.max {
		panic(g.newRangeError(stackOverflowMessage))
	}
	g.depth = saved + 1
	return saved
}

func (g *nativeGuard) restore(saved int) { g.depth = saved }

func (g *nativeGuard) wrapCall(orig nativeCallFunc) nativeCallFunc {
	return func(call goja.FunctionCall) goja.Value {
		defer g.restore(g.enter())
		return orig(call)
	}
}

func (g *nativeGuard) wrapConstruct(orig nativeConstructFunc) nativeConstructFunc {
	return func(args []goja.Value, newTarget *goja.Object) *goja.Object {
		defer g.restore(g.enter())
		return orig(args, newTarget)
	}
}

// builtinRoots are evaluated before the script runs; every function reachable
// from them through own properties (values and accessors) and prototypes is
// instrumented. globalThis reaches every constructor, prototype and namespace;
// the others reach intrinsics that have no global name. A root goja cannot
// evaluate (unsupported syntax) is skipped.
var builtinRoots = []string{
	`globalThis`,
	`[][Symbol.iterator]()`,
	`""[Symbol.iterator]()`,
	`new Map()[Symbol.iterator]()`,
	`new Set()[Symbol.iterator]()`,
	`/a/[Symbol.matchAll]("")`,
	`(function*(){})`,
	`(function*(){})()`,
	`(async function(){})`,
	`(async function*(){})`,
	`(async function*(){})()`,
	`(function(){ "use strict"; return arguments })()`,
	`(function(){ return arguments })()`,
}

// collectBuiltinsScript walks the object graph from the roots and returns
// every function object it finds. It runs before any script code, so nothing
// it calls can have been tampered with.
const collectBuiltinsScript = `(function (roots) {
	var seen = new Set(), out = [], queue = roots.slice();
	function add(v) {
		if (v !== null && (typeof v === "object" || typeof v === "function")) queue.push(v);
	}
	while (queue.length) {
		var o = queue.pop();
		if (seen.has(o)) continue;
		seen.add(o);
		if (typeof o === "function") out.push(o);
		add(Object.getPrototypeOf(o));
		var keys = Reflect.ownKeys(o);
		for (var i = 0; i < keys.length; i++) {
			var d = Object.getOwnPropertyDescriptor(o, keys[i]);
			if (!d) continue;
			if ("value" in d) { add(d.value); } else { add(d.get); add(d.set); }
		}
	}
	return out;
})`

// installStackGuard sets the JS call-depth limit and instruments every
// builtin of vm against nested native recursion. It must run before any
// script code.
//
// The instrumentation replaces, in place, the Go functions a builtin
// function object dispatches to. Replacing the properties that hold the
// builtins instead would not work: goja keeps fast paths keyed on the
// identity of several builtins (RegExp.prototype.exec, Map.prototype.set,
// Array.prototype.values, eval, ...) and on unmodified prototypes, and the
// originals would stay reachable through other references (the TypedArray
// prototype shares Array.prototype.toString, a [].constructor, a bound
// function's target). Patching the function objects keeps every identity,
// name, length, property and fast path intact, and leaves no unguarded copy
// for a script to find. goja exposes no API for this, so it is done through
// reflection on goja's internal layout; any mismatch with that layout fails
// the execution closed (errStackGuardSetup) and is caught by the tests on a
// goja upgrade.
func installStackGuard(vm *goja.Runtime) (*nativeGuard, error) {
	vm.SetMaxCallStackSize(maxCallStackDepth)

	g := &nativeGuard{vm: vm, max: maxNativeDepth, patched: make(map[*goja.Object]struct{})}

	funcs, err := collectBuiltins(vm)
	if err != nil {
		return nil, err
	}

	// Originals the guard itself needs, captured before anything is patched:
	// the RangeError it throws must not be built through a guarded
	// constructor, or throwing at the limit would recurse into the limit.
	rangeErrorCtor := vm.Get("RangeError").ToObject(vm)
	_, rangeConstruct, ok := nativeFuncFields(rangeErrorCtor)
	if !ok || *rangeConstruct == nil {
		return nil, errStackGuardSetup
	}
	origRangeConstruct := *rangeConstruct
	g.newRangeError = func(msg string) *goja.Object {
		return origRangeConstruct([]goja.Value{vm.ToValue(msg)}, rangeErrorCtor)
	}
	isArrayFn, _, ok := nativeFuncFields(vm.Get("Array").ToObject(vm).Get("isArray").ToObject(vm))
	if !ok || *isArrayFn == nil {
		return nil, errStackGuardSetup
	}
	g.isArray = *isArrayFn

	funcProto := vm.Get("Function").ToObject(vm).Get("prototype").ToObject(vm)
	callFn, _, ok := nativeFuncFields(funcProto.Get("call").ToObject(vm))
	if !ok || *callFn == nil {
		return nil, errStackGuardSetup
	}
	g.funcCall = *callFn
	for _, c := range []struct {
		ctor string
		dst  *nativeCallFunc
	}{{"Map", &g.mapForEach}, {"Set", &g.setForEach}} {
		fn, _, ok := nativeFuncFields(vm.Get(c.ctor).ToObject(vm).Get("prototype").ToObject(vm).Get("forEach").ToObject(vm))
		if !ok || *fn == nil {
			return nil, errStackGuardSetup
		}
		*c.dst = *fn
	}

	protoSetter, err := vm.RunString(`Object.getOwnPropertyDescriptor(Object.prototype, "__proto__").set`)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errStackGuardSetup, err)
	}

	jsonObj := vm.Get("JSON").ToObject(vm)
	proxyCtor := vm.Get("Proxy").ToObject(vm)
	special := map[*goja.Object]func(nativeCallFunc) nativeCallFunc{
		jsonObj.Get("stringify").ToObject(vm):                                               g.wrapStringify,
		jsonObj.Get("parse").ToObject(vm):                                                   g.wrapParse,
		funcProto.Get("bind").ToObject(vm):                                                  g.wrapBind,
		vm.Get("Array").ToObject(vm).Get("prototype").ToObject(vm).Get("flat").ToObject(vm): g.wrapFlat,
		vm.Get("Object").ToObject(vm).Get("setPrototypeOf").ToObject(vm):                    g.wrapSetPrototypeOf(0, false),
		vm.Get("Reflect").ToObject(vm).Get("setPrototypeOf").ToObject(vm):                   g.wrapSetPrototypeOf(0, true),
		protoSetter.ToObject(vm):                                                            g.wrapSetPrototypeOf(-1, false),
		proxyCtor.Get("revocable").ToObject(vm):                                             g.wrapProxyRevocable,
	}

	for _, fn := range funcs {
		if wrap, isSpecial := special[fn]; isSpecial {
			call, _, ok := nativeFuncFields(fn)
			if !ok || *call == nil {
				return nil, errStackGuardSetup
			}
			*call = wrap(*call) // the special wrappers count themselves
			g.patched[fn] = struct{}{}
			delete(special, fn)
			continue
		}
		g.patch(fn)
	}
	// new Proxy hands every handler to goja through guardedHandler. The
	// constructor's plain call (which only throws) was patched above.
	_, proxyConstruct, ok := nativeFuncFields(proxyCtor)
	if _, done := g.patched[proxyCtor]; !done || !ok || *proxyConstruct == nil {
		return nil, errStackGuardSetup
	}
	*proxyConstruct = g.wrapProxyConstruct(*proxyConstruct)
	// Every builtin the guard depends on must have been found and patched.
	if len(special) != 0 || len(g.patched) < 100 {
		return nil, errStackGuardSetup
	}
	if err := g.installSourceGuard(); err != nil {
		return nil, err
	}
	return g, nil
}

// patch instruments one builtin function object; it reports false for any
// other kind of object (a JS function, whose calls goja already counts
// against maxCallStackDepth, or a proxy).
func (g *nativeGuard) patch(fn *goja.Object) bool {
	if _, done := g.patched[fn]; done {
		return true
	}
	if !g.instrument(fn) {
		return false
	}
	g.patched[fn] = struct{}{}
	return true
}

// instrument wraps the call and construct functions of a builtin in place.
func (g *nativeGuard) instrument(fn *goja.Object) bool {
	call, construct, ok := nativeFuncFields(fn)
	if !ok {
		return false
	}
	if *call != nil {
		*call = g.wrapCall(*call)
	}
	if *construct != nil {
		*construct = g.wrapConstruct(*construct)
	}
	return true
}

var (
	collectBuiltinsProgram = goja.MustCompile("collect-builtins", collectBuiltinsScript, true)
	builtinRootPrograms    = compileBuiltinRoots()
)

func compileBuiltinRoots() []*goja.Program {
	progs := make([]*goja.Program, 0, len(builtinRoots))
	for _, src := range builtinRoots {
		if p, err := goja.Compile("builtin-root", src, false); err == nil {
			progs = append(progs, p)
		}
	}
	return progs
}

// collectBuiltins returns every function object reachable from builtinRoots.
func collectBuiltins(vm *goja.Runtime, extraRoots ...goja.Value) ([]*goja.Object, error) {
	collectValue, err := vm.RunProgram(collectBuiltinsProgram)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errStackGuardSetup, err)
	}
	collect, ok := goja.AssertFunction(collectValue)
	if !ok {
		return nil, errStackGuardSetup
	}
	roots := make([]interface{}, 0, len(builtinRootPrograms)+len(extraRoots))
	for _, p := range builtinRootPrograms {
		if v, err := vm.RunProgram(p); err == nil {
			roots = append(roots, v)
		}
	}
	for _, v := range extraRoots {
		roots = append(roots, v)
	}
	listValue, err := collect(goja.Undefined(), vm.ToValue(roots))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errStackGuardSetup, err)
	}
	var funcs []*goja.Object
	if err := vm.ExportTo(listValue, &funcs); err != nil {
		return nil, fmt.Errorf("%w: %v", errStackGuardSetup, err)
	}
	return funcs, nil
}

// nativeFuncFields returns pointers to the call and construct functions of a
// builtin (goja nativeFuncObject or templatedFuncObject, or the
// nativeFuncObject a bound function embeds), or ok=false for any other kind
// of object.
func nativeFuncFields(obj *goja.Object) (call *nativeCallFunc, construct *nativeConstructFunc, ok bool) {
	if obj == nil {
		return nil, nil, false
	}
	self := reflect.ValueOf(obj).Elem().FieldByName("self")
	if !self.IsValid() || self.Kind() != reflect.Interface || self.IsNil() {
		return nil, nil, false
	}
	impl := self.Elem()
	if impl.Kind() != reflect.Pointer || impl.IsNil() || impl.Elem().Kind() != reflect.Struct {
		return nil, nil, false
	}
	s := impl.Elem()
	switch s.Type().Name() {
	case "nativeFuncObject", "templatedFuncObject": // the latter: Object, Number, Date, BigInt, Function.prototype
	case "boundFuncObject":
		s = s.FieldByName("nativeFuncObject")
		if !s.IsValid() || s.Kind() != reflect.Struct {
			return nil, nil, false
		}
	default:
		return nil, nil, false
	}
	f := s.FieldByName("f")
	c := s.FieldByName("construct")
	if !f.IsValid() || !c.IsValid() || f.Type() != nativeCallType || c.Type() != nativeConstructType || !f.CanAddr() || !c.CanAddr() {
		return nil, nil, false
	}
	// #nosec G103 -- the fields' types were checked above; this is the only
	// way to instrument goja's builtins in place (see installStackGuard).
	call = (*nativeCallFunc)(unsafe.Pointer(f.UnsafeAddr()))
	// #nosec G103 -- as above.
	construct = (*nativeConstructFunc)(unsafe.Pointer(c.UnsafeAddr()))
	return call, construct, true
}

// wrapBind instruments the bound functions Function.prototype.bind creates
// at run time, so a bound builtin with a self-referencing bound argument
// (o.toString = String.bind(null, o)) is counted like any other builtin.
func (g *nativeGuard) wrapBind(orig nativeCallFunc) nativeCallFunc {
	return func(call goja.FunctionCall) goja.Value {
		defer g.restore(g.enter())
		res := orig(call)
		if obj, ok := res.(*goja.Object); ok {
			// A fresh object every call: instrumented, not recorded.
			g.instrument(obj)
		}
		return res
	}
}

// wrapFlat bounds Array.prototype.flat, which recurses once per level it
// flattens inside a single builtin call (flattenIntoArray), so the nesting
// counter would not see it and a cyclic array (a[0] = a) needs no heap to go
// arbitrarily deep.
//
// The call is charged the depth it may recurse to, so a getter or proxy trap
// that runs during it and calls flat again works from what is left. A
// request deeper than half of what is left is cut there (half, so that the
// charges of flat calls nested that way add up to at most the budget): goja
// flattens to the cut, and if the result still holds an array, flattening
// it as asked would have gone deeper, which throws the RangeError. The depth
// argument is coerced here, once; goja receives a number.
func (g *nativeGuard) wrapFlat(orig nativeCallFunc) nativeCallFunc {
	return func(call goja.FunctionCall) goja.Value {
		saved := g.enter()
		defer g.restore(saved)
		if call.This == nil || goja.IsUndefined(call.This) || goja.IsNull(call.This) {
			return orig(call) // throws goja's TypeError
		}
		requested := int64(1)
		if len(call.Arguments) > 0 { // goja reads an explicit undefined as 0
			requested = call.Arguments[0].ToInteger()
		}
		depth := requested
		if limit := int64(g.max-saved-1) / 2; depth > limit {
			depth = limit
		}
		if depth > 0 {
			g.depth = saved + 1 + int(depth)
		}
		res := orig(goja.FunctionCall{This: call.This, Arguments: []goja.Value{g.vm.ToValue(depth)}})
		if requested > depth && g.holdsArray(res) {
			panic(g.newRangeError(stackOverflowMessage))
		}
		return res
	}
}

// holdsArray reports whether the array flat returned has an element that is
// an array. A result that is not an ordinary array (an exotic species) is
// assumed to have one: its elements cannot be read without running script
// code.
func (g *nativeGuard) holdsArray(res goja.Value) bool {
	arr, ok := res.(*goja.Object)
	if !ok || arr.ClassName() != "Array" || isProxy(arr) {
		return true
	}
	n := jsonToLength(arr.Get("length"))
	for i := int64(0); i < n; i++ {
		el, ok := arr.Get(strconv.FormatInt(i, 10)).(*goja.Object)
		if ok && g.isArray(goja.FunctionCall{Arguments: []goja.Value{el}}).ToBoolean() {
			return true
		}
	}
	return false
}

// maxProtoWalk bounds the object graph protoCycle walks. A prototype graph
// that large is refused: it is far past anything legitimate, and walking it
// on every change would cost the script goroutine time it cannot be
// interrupted in.
const maxProtoWalk = 1 << 16

const protoChainTooLongMessage = "Maximum prototype chain length exceeded"

// wrapSetPrototypeOf refuses a prototype change that would close a cycle
// through a proxy. goja (like the spec) stops its cycle check at the first
// proxy, but a property lookup on such a cycle (proxy -> target -> prototype
// -> proxy, or through the handler, whose prototype chain every trap lookup
// walks) recurses natively forever without allocating, inside one
// operation the nesting counter does not see. Prototypes, proxy targets and
// handlers can only form a cycle when an existing object's prototype is
// changed, and these three builtins are the only ways a script can do that
// (an object literal's __proto__ and Object.create only set the prototype of
// a new object, and a proxy's target and handler are fixed when it is
// created).
//
// objIdx is the argument holding the object (-1: this) and the prototype is
// the argument after it; reflectAPI selects Reflect.setPrototypeOf's answer to a
// cycle (false instead of a TypeError).
func (g *nativeGuard) wrapSetPrototypeOf(objIdx int, reflectAPI bool) func(nativeCallFunc) nativeCallFunc {
	return func(orig nativeCallFunc) nativeCallFunc {
		return func(call goja.FunctionCall) goja.Value {
			defer g.restore(g.enter())
			obj := call.This
			if objIdx >= 0 {
				obj = call.Argument(objIdx)
			}
			cycle, tooLong := protoCycle(obj, call.Argument(objIdx+1))
			switch {
			case tooLong:
				panic(g.newRangeError(protoChainTooLongMessage))
			case cycle && reflectAPI:
				return g.vm.ToValue(false)
			case cycle:
				panic(g.vm.NewTypeError(cyclicProtoMessage))
			}
			return orig(call)
		}
	}
}

// protoCycle reports whether making proto the prototype of obj would close a
// cycle of prototype, proxy target and proxy handler links. The objects whose
// prototype may change are obj and, when obj is a proxy without a
// setPrototypeOf trap, the targets it forwards to; a trap only changes
// prototypes through the guarded builtins again.
func protoCycle(objV, protoV goja.Value) (cycle, tooLong bool) {
	obj, ok := objV.(*goja.Object)
	if !ok {
		return false, false
	}
	proto, ok := protoV.(*goja.Object)
	if !ok {
		return false, false
	}
	changing := map[*goja.Object]struct{}{}
	for o := obj; o != nil && len(changing) <= maxProtoWalk; {
		if _, seen := changing[o]; seen {
			break
		}
		changing[o] = struct{}{}
		s := objectImpl(o)
		if !s.IsValid() || s.Type().Name() != "proxyObject" {
			break
		}
		o = objectField(s, "target")
	}
	seen := map[*goja.Object]struct{}{proto: {}}
	queue := []*goja.Object{proto}
	for len(queue) > 0 {
		o := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if _, hit := changing[o]; hit {
			return true, false
		}
		for _, next := range protoEdges(o) {
			if _, done := seen[next]; done {
				continue
			}
			if len(seen) >= maxProtoWalk {
				return false, true
			}
			seen[next] = struct{}{}
			queue = append(queue, next)
		}
	}
	return false, false
}

// protoEdges returns the objects a property lookup on o recurses into
// natively: an ordinary object's prototype; a proxy's target and handler.
func protoEdges(o *goja.Object) []*goja.Object {
	s := objectImpl(o)
	if !s.IsValid() {
		return nil
	}
	var out []*goja.Object
	add := func(e *goja.Object) {
		if e != nil {
			out = append(out, e)
		}
	}
	if s.Type().Name() != "proxyObject" {
		add(objectField(s, "prototype"))
		return out
	}
	add(objectField(s, "target"))
	if h, ok := fieldByName(s, "handler"); ok && h.Kind() == reflect.Interface && !h.IsNil() {
		if hi := h.Elem(); hi.Kind() == reflect.Pointer && !hi.IsNil() && hi.Elem().Kind() == reflect.Struct {
			add(objectField(hi.Elem(), "handler")) // a jsProxyHandler's handler object
		}
	}
	return out
}

func isProxy(o *goja.Object) bool {
	s := objectImpl(o)
	return s.IsValid() && s.Type().Name() == "proxyObject"
}

var gojaObjectType = reflect.TypeOf((*goja.Object)(nil))

// objectImpl returns the struct behind a goja object (its objectImpl), or
// the zero Value.
func objectImpl(obj *goja.Object) reflect.Value {
	if obj == nil {
		return reflect.Value{}
	}
	self := reflect.ValueOf(obj).Elem().FieldByName("self")
	if !self.IsValid() || self.Kind() != reflect.Interface || self.IsNil() {
		return reflect.Value{}
	}
	impl := self.Elem()
	if impl.Kind() != reflect.Pointer || impl.IsNil() || impl.Elem().Kind() != reflect.Struct {
		return reflect.Value{}
	}
	return impl.Elem()
}

// fieldByName is reflect's FieldByName without its panic on a nil embedded
// pointer.
func fieldByName(s reflect.Value, name string) (reflect.Value, bool) {
	sf, ok := s.Type().FieldByName(name)
	if !ok {
		return reflect.Value{}, false
	}
	f, err := s.FieldByIndexErr(sf.Index)
	if err != nil {
		return reflect.Value{}, false
	}
	return f, true
}

// objectField reads an unexported *goja.Object field of s, or nil.
func objectField(s reflect.Value, name string) *goja.Object {
	f, ok := fieldByName(s, name)
	if !ok || f.Type() != gojaObjectType || !f.CanAddr() {
		return nil
	}
	// #nosec G103 -- the field's type was checked above; goja exposes no
	// other way to read a prototype, target or handler without running
	// script code (traps).
	return *(**goja.Object)(unsafe.Pointer(f.UnsafeAddr()))
}

// wrapProxyConstruct and wrapProxyRevocable hand goja every proxy handler
// through guardedHandler. (The constructor is already counted by the
// generic instrumentation underneath.)
func (g *nativeGuard) wrapProxyConstruct(orig nativeConstructFunc) nativeConstructFunc {
	return func(args []goja.Value, newTarget *goja.Object) *goja.Object {
		return orig(g.guardProxyArgs(args), newTarget)
	}
}

func (g *nativeGuard) wrapProxyRevocable(orig nativeCallFunc) nativeCallFunc {
	return func(call goja.FunctionCall) goja.Value {
		defer g.restore(g.enter())
		return orig(goja.FunctionCall{This: call.This, Arguments: g.guardProxyArgs(call.Arguments)})
	}
}

func (g *nativeGuard) guardProxyArgs(args []goja.Value) []goja.Value {
	if len(args) < 2 {
		return args
	}
	if _, ok := args[0].(*goja.Object); !ok {
		return args // goja throws
	}
	h, ok := args[1].(*goja.Object)
	if !ok {
		return args
	}
	out := make([]goja.Value, len(args))
	copy(out, args)
	out[1] = g.guardedHandler(h)
	return out
}

// guardedHandler is what goja gets as the handler of a script's proxy: a
// proxy that looks each trap up on the script's handler h and hands goja a
// counted wrapper that calls it with h as this.
//
// goja calls a trap from Go, and when the trap is itself a callable proxy
// that call never passes through a builtin or a JS frame: a proxy used as
// its own trap (h.apply = F for F = new Proxy(fn, h)), directly or through a
// getter on h, recursed natively forever without allocating. Through the
// wrapper every trap call is counted. The lookup is counted too, so a
// lookup that recurses through h's prototype chain is bounded even where
// wrapSetPrototypeOf is not involved.
//
// The guard's own target is an empty object whose prototype is h: having no
// own properties, it satisfies goja's proxy invariant checks whatever the
// trap lookup returns (h itself may be frozen), and the prototype link keeps
// h an edge of the proxy for protoCycle. The script never sees the guard:
// traps get h as this, and a getter on h runs with h as its receiver.
func (g *nativeGuard) guardedHandler(h *goja.Object) goja.Value {
	vm := g.vm
	shell := vm.NewObject()
	if err := shell.SetPrototype(h); err != nil {
		panic(err)
	}
	wrappers := map[*goja.Object]goja.Value{}
	return vm.ToValue(vm.NewProxy(shell, &goja.ProxyTrapConfig{
		Get: func(_ *goja.Object, name string, _ goja.Value) goja.Value {
			defer g.restore(g.enter())
			v := h.Get(name)
			if v == nil {
				return goja.Undefined()
			}
			trap, ok := v.(*goja.Object)
			if !ok {
				return v
			}
			if _, callable := goja.AssertFunction(trap); !callable {
				return v // goja reports it as not a function
			}
			if w, ok := wrappers[trap]; ok {
				return w
			}
			w := vm.ToValue(func(call goja.FunctionCall) goja.Value {
				defer g.restore(g.enter())
				args := make([]goja.Value, 0, len(call.Arguments)+1)
				args = append(args, h)
				args = append(args, call.Arguments...)
				return g.funcCall(goja.FunctionCall{This: trap, Arguments: args})
			})
			wrappers[trap] = w
			return w
		},
	}))
}

// wrapParse bounds the nesting of the text JSON.parse decodes recursively.
// The text is coerced once, here, and handed on as a string, so a toString
// with side effects still runs exactly once.
func (g *nativeGuard) wrapParse(orig nativeCallFunc) nativeCallFunc {
	return func(call goja.FunctionCall) goja.Value {
		saved := g.enter()
		defer g.restore(saved)
		text := call.Argument(0).ToString()
		if saved+1+jsonNestingDepth(text.String()) > g.max {
			panic(g.newRangeError(stackOverflowMessage))
		}
		args := make([]goja.Value, len(call.Arguments))
		copy(args, call.Arguments)
		if len(args) == 0 {
			args = append(args, text)
		} else {
			args[0] = text
		}
		return orig(goja.FunctionCall{This: call.This, Arguments: args})
	}
}

// jsonNestingDepth is the deepest bracket nesting in s outside string
// literals. Malformed input is left to JSON.parse to reject.
func jsonNestingDepth(s string) int {
	depth, deepest := 0, 0
	inString, escaped := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '[', '{':
			depth++
			if depth > deepest {
				deepest = depth
			}
		case ']', '}':
			depth--
		}
	}
	return deepest
}

// wrapStringify bounds the nesting JSON.stringify serializes recursively,
// including nesting produced lazily by toJSON or a replacer that hands back
// a fresh object at every level and so needs no heap to go arbitrarily deep.
//
// It drives goja's own JSON.stringify with a replacer function of its own
// that learns each value's depth from its holder (the `this` of the
// replacer call), so the output is goja's, byte for byte:
//   - a script replacer function is called from it, with the same this and
//     arguments, at the same point;
//   - a script replacer array (property list) is emulated: every plain
//     object goja would serialize through the list is handed back as a proxy
//     exposing exactly the listed keys, in list order, each read from the
//     object when goja asks for it, which is what goja does with the list.
//     Cycles through such objects are detected here with goja's error,
//     since the proxies hide them from goja's own check.
func (g *nativeGuard) wrapStringify(orig nativeCallFunc) nativeCallFunc {
	vm := g.vm
	return func(call goja.FunctionCall) goja.Value {
		saved := g.enter()
		defer g.restore(saved)
		base := saved + 1

		var userReplacer goja.Callable
		var propertyList []goja.Value
		listMode := false
		if obj, ok := call.Argument(1).(*goja.Object); ok {
			if g.isArray(goja.FunctionCall{Arguments: []goja.Value{obj}}).ToBoolean() {
				listMode = true
				propertyList = jsonPropertyList(vm, obj)
			} else if fn, ok := goja.AssertFunction(obj); ok {
				userReplacer = fn
			}
		}

		depths := make(map[*goja.Object]int)
		var path []*goja.Object // list mode: the objects on the current path, by depth
		replacer := func(rc goja.FunctionCall) goja.Value {
			holder, _ := rc.This.(*goja.Object)
			d := depths[holder] + 1 // goja's own root holder is not in the map: depth 1
			key := rc.Argument(0)
			value := rc.Argument(1)
			g.depth = base + d - 1
			if userReplacer != nil {
				v, err := userReplacer(rc.This, key, value)
				if err != nil {
					panic(err)
				}
				value = v
			}
			obj, ok := value.(*goja.Object)
			if !ok {
				return value
			}
			if _, callable := goja.AssertFunction(obj); callable {
				return value // serialized as undefined; never a holder
			}
			if base+d > g.max {
				panic(g.newRangeError(stackOverflowMessage))
			}
			if !listMode {
				depths[obj] = d
				return value
			}
			if len(path) >= d {
				path = path[:d-1]
			}
			for _, ancestor := range path {
				if ancestor.SameAs(obj) {
					panic(vm.NewTypeError("Converting circular structure to JSON"))
				}
			}
			path = append(path, obj)
			if g.isArray(goja.FunctionCall{Arguments: []goja.Value{obj}}).ToBoolean() || jsonPrimitiveWrapper(obj) {
				depths[obj] = d
				return value
			}
			sub := vm.ToValue(vm.NewProxy(vm.NewObject(), propertyListTraps(vm, obj, propertyList))).(*goja.Object)
			depths[sub] = d
			return sub
		}

		args := []goja.Value{call.Argument(0), vm.ToValue(replacer)}
		if len(call.Arguments) > 2 {
			args = append(args, call.Arguments[2])
		}
		return orig(goja.FunctionCall{This: call.This, Arguments: args})
	}
}

// jsonPropertyList builds JSON.stringify's property list from a replacer
// array exactly as goja does: numbers, strings and Number/String objects,
// stringified, first occurrence kept.
func jsonPropertyList(vm *goja.Runtime, arr *goja.Object) []goja.Value {
	length := jsonToLength(arr.Get("length"))
	seen := map[string]bool{}
	list := make([]goja.Value, 0)
	for i := int64(0); i < length; i++ {
		v := arr.Get(strconv.FormatInt(i, 10))
		var name string
		switch {
		case v == nil:
			continue
		case goja.IsNumber(v), goja.IsString(v):
			name = v.String()
		default:
			o, ok := v.(*goja.Object)
			if !ok {
				continue
			}
			switch o.ClassName() {
			case "Number", "String":
				name = v.String()
			default:
				continue
			}
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		list = append(list, vm.ToValue(name))
	}
	return list
}

func jsonToLength(v goja.Value) int64 {
	if v == nil {
		return 0
	}
	i := v.ToInteger()
	if i < 0 {
		return 0
	}
	return i
}

// jsonPrimitiveWrapper reports the objects JSON.stringify unwraps to a
// primitive instead of serializing their properties.
func jsonPrimitiveWrapper(o *goja.Object) bool {
	switch o.ClassName() {
	case "Number", "String", "Boolean", "BigInt", "Symbol":
		return true
	}
	return false
}

func propertyListTraps(vm *goja.Runtime, obj *goja.Object, list []goja.Value) *goja.ProxyTrapConfig {
	return &goja.ProxyTrapConfig{
		OwnKeys: func(*goja.Object) *goja.Object {
			return vm.NewArray(toInterfaces(list)...)
		},
		GetOwnPropertyDescriptor: func(*goja.Object, string) goja.PropertyDescriptor {
			return goja.PropertyDescriptor{
				Value:        goja.Undefined(),
				Writable:     goja.FLAG_TRUE,
				Enumerable:   goja.FLAG_TRUE,
				Configurable: goja.FLAG_TRUE,
			}
		},
		Get: func(_ *goja.Object, property string, _ goja.Value) goja.Value {
			if v := obj.Get(property); v != nil {
				return v
			}
			return goja.Undefined()
		},
	}
}

func toInterfaces(vs []goja.Value) []interface{} {
	out := make([]interface{}, len(vs))
	for i, v := range vs {
		out[i] = v
	}
	return out
}
