package jsruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dop251/goja"
)

// A Go stack overflow is fatal to the whole process, so every vector below
// runs in a child process (this test binary, re-executed): the parent asserts
// that the child survived and that Execute reported an error.

const (
	stackChildEnv       = "JSRUNTIME_STACK_GUARD_CHILD_SCRIPT"
	stackChildBudgetEnv = "JSRUNTIME_STACK_GUARD_CHILD_BUDGET"
	stackChildLangEnv   = "JSRUNTIME_STACK_GUARD_CHILD_LANGUAGE"
	// stackChildGenEnv marks the script as a JS expression that, evaluated in
	// a plain VM, builds the source to execute: source too large for an
	// environment variable, such as a deeply nested script text.
	stackChildGenEnv = "JSRUNTIME_STACK_GUARD_CHILD_GENERATE"
	// stackChildNoShapeEnv turns the Function-constructor shape check off,
	// so a vector exercises the prototype post-check alone.
	stackChildNoShapeEnv = "JSRUNTIME_STACK_GUARD_CHILD_NO_SHAPE"
	stackChildMarker     = "STACK_GUARD_RESULT "
)

// TestStackGuardChild is the child half of TestStackGuardVectors.
func TestStackGuardChild(t *testing.T) {
	code := os.Getenv(stackChildEnv)
	if code == "" {
		t.Skip("child process only")
	}
	budget, _ := strconv.ParseInt(os.Getenv(stackChildBudgetEnv), 10, 64)
	if os.Getenv(stackChildNoShapeEnv) != "" {
		functionShapeCheck = false
	}
	if os.Getenv(stackChildGenEnv) != "" {
		v, err := goja.New().RunString(code)
		if err != nil {
			t.Fatalf("generate source: %v", err)
		}
		code = v.String()
	}
	result := Execute(context.Background(), marshalingToolCaller{newMockToolCaller()}, code, ExecutionOptions{
		// Generous: the heaviest builders take tens of seconds on a slow
		// -race runner, and a timeout would hide what the vector proves.
		TimeoutMs: 120000, MaxMemoryBytes: budget, Language: os.Getenv(stackChildLangEnv),
	})
	out, err := json.Marshal(result)
	if err != nil {
		out, _ = json.Marshal(NewErrorResult(NewJsError(ErrorCodeSerializationError, err.Error())))
	}
	fmt.Println(stackChildMarker + string(out))
}

// marshalingToolCaller JSON-encodes the arguments of every call, as the
// proxy does downstream of call_tool, so a vector that hands a tool call a
// value too deep for Go's recursive encoders crashes the child.
type marshalingToolCaller struct{ *mockToolCaller }

func (m marshalingToolCaller) CallTool(ctx context.Context, server, tool string, args map[string]interface{}) (interface{}, error) {
	if _, err := json.Marshal(args); err != nil {
		return nil, err
	}
	return m.mockToolCaller.CallTool(ctx, server, tool, args)
}

type stackVector struct {
	name string
	code string
	// heapBound vectors build their depth out of retained heap: the default
	// memory budget may stop them before the depth limit does. The others
	// run with a 1 GiB budget, so that building their (heavier under -race)
	// structures cannot hide a missing depth limit behind MEMORY_LIMIT.
	heapBound bool
	// defaultBudget runs the vector with the default memory budget, with the
	// same strict assertions as the others (a heapBound vector may also end
	// in MEMORY_LIMIT or ok).
	defaultBudget bool
	// noShapeCheck runs the child with the Function-constructor shape check
	// off (stackChildNoShapeEnv).
	noShapeCheck bool
	// want, when set, is the error the vector must fail with instead of the
	// stack overflow RangeError.
	want string
	// generate: code is a JS expression building the script source (see
	// stackChildGenEnv); language is ExecutionOptions.Language.
	generate bool
	language string
}

var stackVectors = []stackVector{
	// 1. Deep array nesting through the natively recursive Array methods.
	{name: "array String", code: `var a=[]; for(var i=0;i<600000;i++) a=[a]; String(a).length`},
	{name: "array join", code: `var a=[]; for(var i=0;i<600000;i++) a=[a]; a.join().length`},
	{name: "array toString", code: `var a=[]; for(var i=0;i<600000;i++) a=[a]; a.toString().length`},
	{name: "array toLocaleString", code: `var a=[]; for(var i=0;i<600000;i++) a=[a]; a.toLocaleString().length`},
	{name: "array JSON.stringify", code: `var a=[]; for(var i=0;i<600000;i++) a=[a]; JSON.stringify(a).length`},
	{name: "array flat(Infinity)", code: `var a=[]; for(var i=0;i<3000000;i++) a=[a]; a.flat(Infinity).length`, heapBound: true},
	// 2. Deep object chains, serialized and returned.
	{name: "object JSON.stringify", code: `var o={}; for(var i=0;i<600000;i++) o={n:o}; JSON.stringify(o).length`},
	{name: "object JSON.stringify replacer array", code: `var o={}; for(var i=0;i<600000;i++) o={n:o}; JSON.stringify(o, ["n"]).length`},
	{name: "object as result", code: `var o={}; for(var i=0;i<2500000;i++) o={n:o}; o`, heapBound: true},
	// 3. Lazy depth that needs almost no heap.
	{name: "lazy toJSON", code: `var o={toJSON:function(){return {x:o}}}; JSON.stringify(o)`},
	{name: "lazy replacer", code: `JSON.stringify({}, function(k,v){ return {x:1} })`},
	{name: "lazy index getter", code: `function mk(){var a=[]; Object.defineProperty(a,0,{get:function(){return mk()}}); a.length=1; return a}; String(mk())`},
	{name: "lazy getter as result", code: `function mk(){return {get n(){return mk()}}}; mk()`, heapBound: true},
	// 4. JS recursion that re-enters native code, and plain JS recursion.
	{name: "recursion through forEach", code: `function f(){[1].forEach(f)} f()`},
	{name: "recursion through apply", code: `function g(){return g.apply(this)} g()`},
	{name: "recursion through sort", code: `function c(a,b){ [2,1].sort(c); return 0 } [2,1].sort(c)`},
	{name: "plain recursion", code: `function h(){h()} h()`},
	// 5. Long chains built cheaply, and builtins that recurse into each other.
	{name: "bind chain", code: `var f=function(){return 1}; for(var i=0;i<1000000;i++) f=f.bind(); f()`, heapBound: true},
	{name: "proxy chain", code: `var p={}; for(var i=0;i<4000000;i++) p=new Proxy(p,{}); p.x`, heapBound: true},
	{name: "prototype chain", code: `var p={}; for(var i=0;i<2000000;i++) p=Object.create(p); p.x`, heapBound: true},
	{name: "JSON.parse deep text", code: `JSON.parse("[".repeat(3000000)+"]".repeat(3000000)).length`},
	{name: "Reflect.apply chain", code: `var x=[function(){return 1},null,[]]; for(var i=0;i<600000;i++) x=[Reflect.apply,null,x]; Reflect.apply.apply(null,x)`},
	{name: "toString = String.prototype.trim", code: `var o={}; o.toString=String.prototype.trim; String(o)`},
	{name: "toString = Object.prototype.toLocaleString", code: `var o={}; o.toString=Object.prototype.toLocaleString; String(o)`},
	{name: "Symbol.toPrimitive = String.prototype.concat", code: `var o={}; o[Symbol.toPrimitive]=String.prototype.concat; String(o)`},
	{name: "getter = Error.prototype.toString", code: `var o={}; Object.defineProperty(o,'name',{get:Error.prototype.toString}); o.name`},
	{name: "getter = RegExp flags", code: `var g=Object.getOwnPropertyDescriptor(RegExp.prototype,'flags').get; var o={}; Object.defineProperty(o,'global',{get:g}); o.global`},
	{name: "toJSON = Object.prototype.toLocaleString", code: `var o={}; o.toJSON=Object.prototype.toLocaleString; o.toString=Object.prototype.toLocaleString; JSON.stringify(o)`},
	{name: "RegExp exec through lastIndex", code: `var re=/a/g; re.lastIndex=re; re.valueOf=re.exec; re.exec("a")`},
	{name: "bound constructor", code: `var o={}; o.toString=String.bind(null,o); String(o)`},
	{name: "proxy trap = bound constructor", code: `var h={}; var p=new Proxy({},h); h.get=String.bind(null,p); String(p)`},
	{name: "TypedArray toString shares Array toString", code: `var ts=Object.getPrototypeOf(Int8Array.prototype).toString; var o={}; o.join=ts; o.toString=ts; String(o)`},
	// Recursion inside one builtin call over a structure that needs no heap:
	// a cyclic array, cyclic prototype chains through proxies (which goja's
	// own cycle check stops at), and a callable proxy used as its own trap.
	{name: "cyclic array flat(Infinity)", code: `var a=[]; a[0]=a; a.flat(Infinity).length`},
	{name: "cyclic array flat(1e9)", code: `var a=[]; a[0]=a; a.flat(1e9).length`},
	{name: "flat re-entered from a getter", code: `var g=[]; Object.defineProperty(g,0,{get:function(){ return d.flat(Infinity) }}); g.length=1; var d=g; for(var i=0;i<1000;i++) d=[d]; d.flat(Infinity)`},
	// The reviver walk recurses natively over what the reviver builds: a
	// cycle, or a getter that hands back a fresh object at every level.
	{name: "JSON.parse reviver builds a cycle", code: `JSON.parse('{"a":0,"b":0}', function(k,v){ if(k==="a"){ var c={}; c.x=c; this.b=c } return v })`},
	{name: "JSON.parse reviver builds a cyclic array", code: `JSON.parse('[0,0]', function(k,v){ if(k==="0"){ var c=[]; c[0]=c; this[1]=c } return v })`},
	{name: "JSON.parse reviver lazy getters", code: `function mk(){ var o={}; Object.defineProperty(o,"x",{get:mk, enumerable:true}); return o } JSON.parse('{"a":0,"b":0}', function(k,v){ if(k==="a"){ this.b=mk() } return v })`},
	{name: "JSON.parse reviver through a proxy", code: `var p; p=new Proxy({}, {ownKeys:function(){ return ["x"] }, getOwnPropertyDescriptor:function(){ return {value:p, enumerable:true, configurable:true} }, get:function(){ return p }}); JSON.parse('{"a":0,"b":0}', function(k,v){ if(k==="a"){ this.b=p } return v })`},
	{name: "JSON.parse re-entered from the reviver", code: `var t="[".repeat(1500)+"]".repeat(1500); function r(k,v){ return JSON.parse(t, r) } JSON.parse(t, r)`},
	{name: "proxy cycle setPrototypeOf(p, proxy of p)", code: `var p=new Proxy({},{}); var q=new Proxy(p,{}); Object.setPrototypeOf(p,q); p.x`, want: cyclicProtoMessage},
	{name: "proxy cycle handler prototype", code: `var h={}; var p=new Proxy({},h); Object.setPrototypeOf(h,p); p.x`, want: cyclicProtoMessage},
	{name: "proxy cycle handler __proto__", code: `var h={}; var p=new Proxy({},h); h.__proto__=p; p.x`, want: cyclicProtoMessage},
	{name: "proxy cycle target is handler", code: `var o={}; var p=new Proxy(o,o); Object.setPrototypeOf(o,p); p.x`, want: cyclicProtoMessage},
	{name: "proxy cycle through Object.create", code: `var p=new Proxy({},{}); var o=Object.create(p); Object.setPrototypeOf(p,o); o.x`, want: cyclicProtoMessage},
	{name: "proxy cycle Reflect.setPrototypeOf", code: `var p=new Proxy({},{}); var o=Object.create(p); Reflect.setPrototypeOf(p,o); 'x' in o`, want: "ok"},
	{name: "proxy cycle via revocable", code: `var r=Proxy.revocable({},{}); var o=Object.create(r.proxy); Object.setPrototypeOf(r.proxy,o); o.x`, want: cyclicProtoMessage},
	// A forwarding chain longer than the walk bound must fail closed: a
	// truncated set of changing objects hides the target actually mutated.
	{name: "proxy cycle through a forwarding chain past the walk bound", code: `var base={}; var inner=new Proxy(base,{}); var outer=inner; for(var i=1;i<65538;i++) outer=new Proxy(outer,{}); Object.setPrototypeOf(outer,inner); base.missing`, want: protoChainTooLongMessage},
	// A setPrototypeOf trap runs instead of the structural check, but what it
	// forwards to the target is checked again.
	{name: "proxy cycle through a forwarding setPrototypeOf trap", code: `var t={}; var p=new Proxy(t,{setPrototypeOf:function(t2,v){ return Reflect.setPrototypeOf(t2,v) }}); Object.setPrototypeOf(p,p); p.x`, want: "trap returned falsish"},
	{name: "proxy cycle through a trap calling Object.setPrototypeOf", code: `var t={}; var p=new Proxy(t,{setPrototypeOf:function(t2,v){ Object.setPrototypeOf(t2,v); return true }}); Object.setPrototypeOf(p,p); p.x`, want: cyclicProtoMessage},
	{name: "proxy cycle through a trap-less proxy in front of a trap", code: `var t={}; var q=new Proxy(t,{setPrototypeOf:function(t2,v){ return Reflect.setPrototypeOf(t2,v) }}); var p=new Proxy(q,{}); Reflect.setPrototypeOf(p,p); p.x`, want: "ok"},
	// The trap decision for an inner proxy only holds while nothing can run
	// script before goja looks it up: an outer hop whose trap lookup runs a
	// getter (on the handler, on its prototype, through a handler proxy)
	// can delete the inner trap, so the change is checked as forwarded.
	{name: "outer trap getter deletes the inner trap", code: `var target={}; var ih={setPrototypeOf:function(){ return true }}; var inner=new Proxy(target,ih); var outer=new Proxy(inner,{get setPrototypeOf(){ delete ih.setPrototypeOf; return undefined }}); Reflect.setPrototypeOf(outer,inner); target.missing`, want: "ok"},
	{name: "outer trap getter on the handler's prototype deletes the inner trap", code: `var target={}; var ih={setPrototypeOf:function(){ return true }}; var inner=new Proxy(target,ih); var outer=new Proxy(inner,Object.create({get setPrototypeOf(){ delete ih.setPrototypeOf; return undefined }})); Object.setPrototypeOf(outer,inner); target.missing`, want: cyclicProtoMessage},
	{name: "middle trap getter deletes the innermost trap", code: `var target={}; var ih={setPrototypeOf:function(){ return true }}; var p3=new Proxy(target,ih); var p2=new Proxy(p3,{get setPrototypeOf(){ delete ih.setPrototypeOf; return undefined }}); var p1=new Proxy(p2,{}); Reflect.setPrototypeOf(p1,p3); target.missing`, want: "ok"},
	{name: "handler proxy deletes the inner trap", code: `var target={}; var ih={setPrototypeOf:function(){ return true }}; var inner=new Proxy(target,ih); var outer=new Proxy(inner,new Proxy({},{get:function(t,k){ if(k==="setPrototypeOf") delete ih.setPrototypeOf; return undefined }})); Reflect.setPrototypeOf(outer,inner); target.missing`, want: "ok"},
	{name: "Object.prototype trap getter deletes the inner trap", code: `var target={}; var ih=Object.create(null); ih.setPrototypeOf=function(){ return true }; var inner=new Proxy(target,ih); Object.defineProperty(Object.prototype,"setPrototypeOf",{get:function(){ delete ih.setPrototypeOf; return undefined }, configurable:true}); var outer=new Proxy(inner,{}); Reflect.setPrototypeOf(outer,inner); target.missing`, want: "ok"},
	// Script that runs during the change (a trap getter) can build part of
	// a cycle the forwarded change then completes: checked after the change
	// too, and rolled back.
	{name: "trap getter completes a cycle, Reflect", code: `var t={}, proto={}; var bridge=new Proxy(proto,{}); var p=new Proxy(t,{get setPrototypeOf(){ Object.setPrototypeOf(proto,t); return undefined }}); var r=Reflect.setPrototypeOf(p,bridge); t.missing; [r, Object.getPrototypeOf(t)===Object.prototype].join()`, want: "ok"},
	{name: "trap getter completes a cycle, Object.setPrototypeOf", code: `var t={}, proto={}; var bridge=new Proxy(proto,{}); var p=new Proxy(t,{get setPrototypeOf(){ Object.setPrototypeOf(proto,t); return undefined }}); try { Object.setPrototypeOf(p,bridge) } catch (e) {} t.missing; Object.setPrototypeOf(p,bridge)`, want: cyclicProtoMessage},
	{name: "trap getter completes a cycle, __proto__", code: `var t={}, proto={}; var bridge=new Proxy(proto,{}); var p=new Proxy(t,{get setPrototypeOf(){ Object.setPrototypeOf(proto,t); return undefined }}); try { p.__proto__=bridge } catch (e) {} t.missing; p.__proto__=bridge`, want: cyclicProtoMessage},
	{name: "trap getter completes a cycle through a handler link", code: `var t={}, hb={}; var bridge=new Proxy({},hb); var p=new Proxy(t,{get setPrototypeOf(){ Object.setPrototypeOf(hb,t); return undefined }}); Reflect.setPrototypeOf(p,bridge); t.missing; bridge.missing`, want: "ok"},
	{name: "trap getter completes a deeper cycle", code: `var t={}, a={}, b={}; Object.setPrototypeOf(a,b); var bridge=new Proxy(a,{}); var p=new Proxy(t,{get setPrototypeOf(){ Object.setPrototypeOf(b,t); return undefined }}); Reflect.setPrototypeOf(p,bridge); t.missing; a.missing; b.missing`, want: "ok"},
	{name: "three-object cycle across two nested trap getters", code: `var x={}, y={}, z={}; var py=new Proxy(y,{get setPrototypeOf(){ Object.setPrototypeOf(z,x); return undefined }}); var px=new Proxy(x,{get setPrototypeOf(){ Reflect.setPrototypeOf(py,new Proxy(z,{})); return undefined }}); Reflect.setPrototypeOf(px,new Proxy(y,{})); x.missing; y.missing; z.missing`, want: "ok"},
	{name: "builtin handler whose prototype is a trap getter", code: `var target={}; var ih={setPrototypeOf:function(){ return true }}; var inner=new Proxy(target,ih); Object.defineProperty(Object.prototype,"setPrototypeOf",{get:function(){ delete ih.setPrototypeOf; return undefined }, configurable:true}); var outer=new Proxy(inner,Math); Reflect.setPrototypeOf(outer,inner); target.missing`, want: "ok"},
	{name: "trap getter revokes the proxy it is looked up on", code: `var t={}, proto={}; var bridge=new Proxy(proto,{}); var r=Proxy.revocable(t,{get setPrototypeOf(){ r.revoke(); Object.setPrototypeOf(proto,t); return undefined }}); Reflect.setPrototypeOf(r.proxy,bridge); t.missing`, want: "ok"},
	// A Function-constructor body that escapes the function expression runs
	// script while the function is made and can expose it before its
	// prototype is set: refused as not a single function, and the prototype
	// assignment is checked like any other change.
	{name: "Function body escape, Reflect.construct with newTarget", code: `var target={}; var proto=new Proxy(target,{}); Object.defineProperty(globalThis,"sink",{configurable:true, set:function(v){ Object.setPrototypeOf(target,v) }}); function NT(){} NT.prototype=proto; var f; try { f=Reflect.construct(Function,["}); sink = (function(){"],NT) } catch (e) { target.missing; throw e } f.missing; target.missing`, want: functionShapeMessage},
	{name: "Function body escape, Reflect.construct with newTarget (post-check alone)", code: `var target={}; var proto=new Proxy(target,{}); Object.defineProperty(globalThis,"sink",{configurable:true, set:function(v){ Object.setPrototypeOf(target,v) }}); function NT(){} NT.prototype=proto; var f; try { f=Reflect.construct(Function,["}); sink = (function(){"],NT) } catch (e) { target.missing; throw e } f.missing; target.missing`, want: cyclicProtoMessage, noShapeCheck: true},
	{name: "GeneratorFunction body escape with newTarget", code: `var GF=Object.getPrototypeOf(function*(){}).constructor; var target={}; var proto=new Proxy(target,{}); Object.defineProperty(globalThis,"sink",{configurable:true, set:function(v){ Object.setPrototypeOf(target,v) }}); function NT(){} NT.prototype=proto; var f; try { f=Reflect.construct(GF,["}); sink = (function*(){"],NT) } catch (e) { target.missing; throw e } f.missing; target.missing`, want: functionShapeMessage},
	{name: "GeneratorFunction body escape with newTarget (post-check alone)", code: `var GF=Object.getPrototypeOf(function*(){}).constructor; var target={}; var proto=new Proxy(target,{}); Object.defineProperty(globalThis,"sink",{configurable:true, set:function(v){ Object.setPrototypeOf(target,v) }}); function NT(){} NT.prototype=proto; var f; try { f=Reflect.construct(GF,["}); sink = (function*(){"],NT) } catch (e) { target.missing; throw e } f.missing; target.missing`, want: cyclicProtoMessage, noShapeCheck: true},
	{name: "AsyncFunction body escape with newTarget", code: `var AF=Object.getPrototypeOf(async function(){}).constructor; var target={}; var proto=new Proxy(target,{}); Object.defineProperty(globalThis,"sink",{configurable:true, set:function(v){ Object.setPrototypeOf(target,v) }}); function NT(){} NT.prototype=proto; var f; try { f=Reflect.construct(AF,["}); sink = (async function(){"],NT) } catch (e) { target.missing; throw e } f.missing; target.missing`, want: functionShapeMessage},
	{name: "AsyncFunction body escape with newTarget (post-check alone)", code: `var AF=Object.getPrototypeOf(async function(){}).constructor; var target={}; var proto=new Proxy(target,{}); Object.defineProperty(globalThis,"sink",{configurable:true, set:function(v){ Object.setPrototypeOf(target,v) }}); function NT(){} NT.prototype=proto; var f; try { f=Reflect.construct(AF,["}); sink = (async function(){"],NT) } catch (e) { target.missing; throw e } f.missing; target.missing`, want: cyclicProtoMessage, noShapeCheck: true},
	{name: "class extends Function body escape", code: `var target={}; class X extends Function { constructor(b){ super(b) } } Object.setPrototypeOf(X.prototype,new Proxy(target,{})); Object.defineProperty(globalThis,"sink",{configurable:true, set:function(v){ Object.setPrototypeOf(target,v) }}); var f; try { f=new X("}); sink = (function(){") } catch (e) { target.missing; throw e } f.missing; target.missing`, want: functionShapeMessage},
	{name: "class extends Function body escape (post-check alone)", code: `var target={}; class X extends Function { constructor(b){ super(b) } } Object.setPrototypeOf(X.prototype,new Proxy(target,{})); Object.defineProperty(globalThis,"sink",{configurable:true, set:function(v){ Object.setPrototypeOf(target,v) }}); var f; try { f=new X("}); sink = (function(){") } catch (e) { target.missing; throw e } f.missing; target.missing`, want: cyclicProtoMessage, noShapeCheck: true},
	{name: "Function body escape returns an existing object", code: `var target={}; Object.setPrototypeOf(Function.prototype,new Proxy(target,{})); try { Function("}); (target || function(){") } catch (e) {} target.missing; Function("}); (target || function(){")`, want: functionShapeMessage},
	{name: "Function body escape returns an existing object (post-check alone)", code: `var target={}; Object.setPrototypeOf(Function.prototype,new Proxy(target,{})); try { Function("}); (target || function(){") } catch (e) {} target.missing; Function("}); (target || function(){")`, want: cyclicProtoMessage, noShapeCheck: true},
	{name: "Function body escape, plain call re-links the escaped function", code: `Object.defineProperty(globalThis,"sink",{configurable:true, set:function(v){ Object.setPrototypeOf(v,null); Object.setPrototypeOf(Function.prototype,new Proxy(v,{})) }}); var f; try { f=Function("}); sink = (function(){") } catch (e) { (function(){}).missing; throw e } (function(){}).missing`, want: functionShapeMessage},
	{name: "Function body escape, plain call re-links the escaped function (post-check alone)", code: `Object.defineProperty(globalThis,"sink",{configurable:true, set:function(v){ Object.setPrototypeOf(v,null); Object.setPrototypeOf(Function.prototype,new Proxy(v,{})) }}); var f; try { f=Function("}); sink = (function(){") } catch (e) { (function(){}).missing; throw e } (function(){}).missing`, want: cyclicProtoMessage, noShapeCheck: true},
	{name: "callable proxy as its own apply trap", code: `var h={}; var F=new Proxy(function(){},h); h.apply=F; F()`},
	{name: "callable proxy as its own apply trap, frozen handler", code: `var h={}; var F=new Proxy(function(){},h); h.apply=F; Object.freeze(h); F()`},
	{name: "callable proxy as the getter of its own trap", code: `var h={}; var F=new Proxy(function(){},h); Object.defineProperty(h,'apply',{get:F}); F()`},
	// 6. Host functions: arguments are exported natively.
	{name: "cyclic Map as result", code: `var m=new Map(); m.set(1,m); m`, want: "JSON-serializable"},
	{name: "cyclic Set as result", code: `var s=new Set(); s.add(s); s`, want: "JSON-serializable"},
	{name: "cyclic object as call_tool args", code: `var o={}; o.o=o; call_tool("s","t",o)`, want: "circular"},
	{name: "cyclic Map as call_tool args", code: `var m=new Map(); m.set(1,m); call_tool("s","t",{m:m})`, want: "circular"},
	{name: "cyclic Map in call_tools args", code: `var m=new Map(); m.set(1,m); call_tools([{server:"s",tool:"t",args:{m:m}}])`, want: "circular"},
	{name: "deep arrays as result", code: `var a=[]; for(var i=0;i<1500000;i++) a=[a]; a`},
	{name: "deep Map chain as result", code: `var m=new Map(); for(var i=0;i<20000;i++) m=new Map([[1,m]]); m`},
	{name: "deep arrays as call_tool args", code: `var a=[]; for(var i=0;i<1500000;i++) a=[a]; call_tool("s","t",{a:a})`},
	// An acyclic graph that reuses exported objects: each one is exported
	// once, but the longest path through the Go graph is the chain's length.
	// 20000 links: ten times the native depth limit, so the guard's error is
	// what is asserted, small enough to build quickly under -race.
	// "b" is exported first (JS order) and builds the chain one level at a
	// time; json.Marshal then walks "a" (sorted first), the whole chain.
	{name: "shared chain as result", code: `var a=[{}]; for(var i=1;i<20000;i++) a.push({n:a[i-1]}); ({b:a, a:a[a.length-1]})`},
	{name: "shared chain as call_tool args", code: `var a=[{}]; for(var i=1;i<20000;i++) a.push({n:a[i-1]}); call_tool("s","t",{b:a, a:a[a.length-1]})`},
	{name: "shared chain in call_tools args", code: `var a=[{}]; for(var i=1;i<20000;i++) a.push({n:a[i-1]}); call_tools([{server:"s",tool:"t",args:{b:a, a:a[a.length-1]}}])`},
	{name: "shared Map chain as result", code: `var a=[new Map()]; for(var i=1;i<20000;i++) a.push(new Map([[1,a[i-1]]])); ({b:a, a:a[a.length-1]})`},
	// An acyclic graph that shares objects expands exponentially when the Go
	// value is encoded: bounded by the expanded size, not by its height.
	{name: "shared DAG as result", code: `var x={}; for(var i=0;i<60;i++) x={a:x,b:x}; x`, want: "too large"},
	{name: "shared array DAG as result", code: `var x=[]; for(var i=0;i<60;i++) x=[x,x]; x`, want: "too large"},
	{name: "shared Map DAG as result", code: `var x=new Map(); for(var i=0;i<60;i++) x=new Map([[1,x],[2,x]]); x`, want: "too large"},
	{name: "shared string as result", code: `var s="x".repeat(1<<20); var a=[]; for(var i=0;i<100000;i++) a.push(s); a`, want: "too large"},
	{name: "shared typed array as result", code: `var t=new Float64Array(1<<20); var a=[]; for(var i=0;i<100000;i++) a.push(t); a`, want: "too large"},
	{name: "huge sparse array as result", code: `var a=[]; a.length=4294967295; a`, want: "too large"},
	// A BigInt is encoded as its full decimal text: charged by its length.
	{name: "shared BigInt DAG as result", code: `var x={n:1n<<32768n}; for(var i=0;i<20;i++) x={a:x,b:x}; x`, want: "too large"},
	{name: "shared BigInt object DAG as result", code: `var x={n:Object(1n<<32768n)}; for(var i=0;i<20;i++) x={a:x,b:x}; x`, want: "too large"},
	{name: "shared BigInt DAG as call_tool args", code: `var x=[-(1n<<32768n)]; for(var i=0;i<20;i++) x=[x,x]; call_tool("s","t",{x:x})`, want: "too large"},
	// call_tools options: an ignored option is never exported.
	{name: "huge array in ignored call_tools option", code: `call_tools([], {ignored: new Array(4294967295)}); "done"`, want: "ok"},
	{name: "huge array as call_tools max_parallel", code: `var r=call_tools([], {max_parallel: new Array(4294967295)}); if(!r.ok) throw new Error(r.error.message); r`, want: "max_parallel must be an integer"},
	// Array holes: a length is free to set, but each exported slot is not.
	// 67108861 elements pass the JSON-size charge; their slots would be 1 GiB.
	{name: "long sparse array as result", code: `new Array(67108861)`, want: "too large", defaultBudget: true},
	{name: "long array with one element as result", code: `var a=[]; a[67108860]=1; a`, want: "too large", defaultBudget: true},
	{name: "long array shared under the JSON bound as result", code: `var a=[]; a.length=1048577; [a]`, want: "too large", defaultBudget: true},
	{name: "long sparse array as call_tool args", code: `call_tool("s","t",{a:new Array(67108861)})`, want: "too large", defaultBudget: true},
	{name: "long sparse array in call_tools args", code: `call_tools([{server:"s",tool:"t",args:{a:new Array(67108861)}}])`, want: "too large", defaultBudget: true},
	{name: "long sparse array as call_tools max_parallel", code: `var r=call_tools([], {max_parallel: new Array(67108861)}); if(!r.ok) throw new Error(r.error.message); r`, want: "max_parallel must be an integer", defaultBudget: true},
	{name: "long sparse array as call_tools requests", code: `call_tools(new Array(67108861))`, want: "too large", defaultBudget: true},
	{name: "holes under the bound as result", code: `var a=new Array(500000); a[5]=1; a`, want: "ok", defaultBudget: true},
	{name: "shared DAG as call_tool args", code: `var x={}; for(var i=0;i<60;i++) x={a:x,b:x}; call_tool("s","t",{x:x})`, want: "too large"},
	{name: "shared DAG in call_tools args", code: `var x={}; for(var i=0;i<60;i++) x={a:x,b:x}; call_tools([{server:"s",tool:"t",args:{x:x}}])`, want: "too large"},
	{name: "lazy getters as result, re-entering export", code: `function mk(){return {get n(){ return call_tool("s","t",mk()) }}}; mk()`},
	{name: "cyclic Map written into input", code: `var m=new Map(); m.set(1,m); input.x=m; "done"`, want: "ok"},
	{name: "cyclic Map written into a call_tool result", code: `var m=new Map(); m.set(1,m); var r=call_tool("s","t",{}); r.x=m; r.result=m; "done"`, want: "ok"},
	{name: "cyclic Map written into call_tools slots", code: `var m=new Map(); m.set(1,m); var r=call_tools([{server:"s",tool:"t"}]); r[0]=m; r[0].z=m; "done"`, want: "ok"},
	{name: "call_tool lazy args", code: `function mk(){return {get n(){return mk()}}}; call_tool("s","t",mk())`, heapBound: true},
	{name: "call_tool deep args", code: `var o={}; for(var i=0;i<2500000;i++) o={n:o}; call_tool("s","t",o)`, heapBound: true},
	{name: "call_tools deep args", code: `var o={}; for(var i=0;i<2500000;i++) o={n:o}; call_tools([{server:"s",tool:"t",args:o}])`, heapBound: true},
	// 7. Nested source text: goja's parser and compiler (and esbuild for
	// TypeScript) recurse natively on it, before any guard counts anything.
	{name: "eval nested brackets", code: `eval("[".repeat(300000)+"]".repeat(300000))`, want: sourceTooDeepMessage},
	{name: "eval nested brackets 3M", code: `eval("[".repeat(3000000)+"]".repeat(3000000))`, want: sourceTooDeepMessage},
	{name: "eval binary chain", code: `eval("1"+"+1".repeat(5000000))`, want: sourceTooDeepMessage},
	{name: "eval nested blocks", code: `eval("{".repeat(3000000)+"}".repeat(3000000))`, want: sourceTooDeepMessage},
	{name: "eval ternary chain", code: `eval("1"+"?1:1".repeat(3000000))`, want: sourceTooDeepMessage},
	{name: "eval unary chain", code: `eval("!".repeat(5000000)+"1")`, want: sourceTooDeepMessage},
	{name: "eval member chain", code: `var a={}; eval("a"+".b".repeat(5000000))`, want: sourceTooDeepMessage},
	{name: "eval call chain", code: `function f(){return f} eval("f"+"()".repeat(3000000))`, want: sourceTooDeepMessage},
	{name: "indirect eval", code: `(0,eval)("[".repeat(1000000)+"]".repeat(1000000))`, want: sourceTooDeepMessage},
	{name: "eval through call", code: `eval.call(null, "[".repeat(1000000)+"]".repeat(1000000))`, want: sourceTooDeepMessage},
	{name: "eval of a String object is not code", code: `typeof eval(new String("[".repeat(1000000)+"]".repeat(1000000)))`, want: "ok"},
	{name: "new Function nested parens", code: `new Function("return "+"(".repeat(3000000)+"1"+")".repeat(3000000))()`, want: sourceTooDeepMessage},
	{name: "Function call nested parens", code: `Function("return "+"(".repeat(3000000)+"1"+")".repeat(3000000))()`, want: sourceTooDeepMessage},
	{name: "Function nesting split across params and body", code: `Function("a=" + "[".repeat(1000000), "]".repeat(1000000) + ")\n{")`, want: sourceTooDeepMessage},
	{name: "Function param toString runs once", code: `var n=0; var p={toString:function(){ n++; return n>1 ? "[".repeat(1000000)+"]".repeat(1000000) : "a" }}; Function(p, "return 1")(); n`, want: "ok"},
	{name: "AsyncFunction nested parens", code: `var AF=Object.getPrototypeOf(async function(){}).constructor; AF("return "+"(".repeat(3000000)+"1"+")".repeat(3000000))`, want: sourceTooDeepMessage},
	{name: "GeneratorFunction nested parens", code: `var GF=Object.getPrototypeOf(function*(){}).constructor; new GF("return "+"(".repeat(3000000)+"1"+")".repeat(3000000))`, want: sourceTooDeepMessage},
	{name: "Function reached through a constructor property", code: `(function(){}).constructor.constructor("return "+"(".repeat(3000000)+"1"+")".repeat(3000000))`, want: sourceTooDeepMessage},
	{name: "Reflect.construct Function", code: `Reflect.construct(Function, ["return "+"(".repeat(3000000)+"1"+")".repeat(3000000)])`, want: sourceTooDeepMessage},
	{name: "eval from a deep native stack", code: `var t="[".repeat(1000000)+"]".repeat(1000000); function f(n){ return n ? [n-1].map(f)[0] : eval(t) } f(1000)`, want: sourceTooDeepMessage},
	{name: "script text nested brackets", code: `"[".repeat(1000000)+"]".repeat(1000000)`, generate: true, want: sourceTooDeepMessage},
	{name: "script text binary chain", code: `"1"+"+1".repeat(5000000)`, generate: true, want: sourceTooDeepMessage},
	{name: "script text nested regex groups", code: `"/"+"(".repeat(1000000)+")".repeat(1000000)+"/"`, generate: true, want: sourceTooDeepMessage},
	{name: "TypeScript nested brackets", code: `"[".repeat(1000000)+"]".repeat(1000000)`, generate: true, language: "typescript", want: sourceTooDeepMessage},
	{name: "TypeScript nested parens", code: `"let x: number = "+"(".repeat(1000000)+"1"+")".repeat(1000000)`, generate: true, language: "typescript", want: sourceTooDeepMessage},
	// TypeScript nesting that consumes no punctuation: esbuild recurses once
	// per keyword operator ("keyof keyof T", "readonly readonly T[]", a type
	// predicate "a is a is ...", "infer U extends infer U extends ...").
	{name: "TypeScript keyof chain", code: `"type T = " + "keyof ".repeat(5000000) + "any; 7"`, generate: true, language: "typescript", want: sourceTooDeepMessage},
	{name: "TypeScript keyof chain separated by U+00A0", code: `"type T = " + "keyof\u00a0".repeat(3000000) + "any; 7"`, generate: true, language: "typescript", want: sourceTooDeepMessage},
	{name: "TypeScript keyof chain separated by U+1680", code: `"type T = " + "keyof\u1680".repeat(3000000) + "any; 7"`, generate: true, language: "typescript", want: sourceTooDeepMessage},
	{name: "TypeScript keyof chain separated by U+2000", code: `"type T = " + "keyof\u2000".repeat(3000000) + "any; 7"`, generate: true, language: "typescript", want: sourceTooDeepMessage},
	{name: "TypeScript keyof chain separated by U+2028", code: `"type T = " + "keyof\u2028".repeat(3000000) + "any; 7"`, generate: true, language: "typescript", want: sourceTooDeepMessage},
	{name: "TypeScript keyof chain separated by U+2029", code: `"type T = " + "keyof\u2029".repeat(3000000) + "any; 7"`, generate: true, language: "typescript", want: sourceTooDeepMessage},
	{name: "TypeScript keyof chain separated by U+202F", code: `"type T = " + "keyof\u202f".repeat(3000000) + "any; 7"`, generate: true, language: "typescript", want: sourceTooDeepMessage},
	{name: "TypeScript keyof chain separated by U+205F", code: `"type T = " + "keyof\u205f".repeat(3000000) + "any; 7"`, generate: true, language: "typescript", want: sourceTooDeepMessage},
	{name: "TypeScript keyof chain separated by U+3000", code: `"type T = " + "keyof\u3000".repeat(3000000) + "any; 7"`, generate: true, language: "typescript", want: sourceTooDeepMessage},
	{name: "TypeScript keyof chain separated by U+FEFF", code: `"type T = " + "keyof\ufeff".repeat(3000000) + "any; 7"`, generate: true, language: "typescript", want: sourceTooDeepMessage},
	{name: "TypeScript readonly chain separated by NBSP", code: `"type T = " + "readonly\u00a0".repeat(3000000) + "any[]; 7"`, generate: true, language: "typescript", want: sourceTooDeepMessage},
	{name: "TypeScript readonly chain", code: `"type T = " + "readonly ".repeat(5000000) + "any[]; 7"`, generate: true, language: "typescript", want: sourceTooDeepMessage},
	{name: "TypeScript type predicate chain", code: `"type T = " + "a is ".repeat(5000000) + "a; 7"`, generate: true, language: "typescript", want: sourceTooDeepMessage},
	{name: "TypeScript asserts predicate chain", code: `"function f(x): asserts x is " + "x is ".repeat(5000000) + "x {}; 7"`, generate: true, language: "typescript", want: sourceTooDeepMessage},
	{name: "TypeScript infer extends chain", code: `"type T<X> = X extends " + "infer U extends ".repeat(5000000) + "any ? 1 : 0; 7"`, generate: true, language: "typescript", want: sourceTooDeepMessage},
	{name: "RegExp constructor nested groups", code: `new RegExp("(".repeat(1000000)+")".repeat(1000000)).source.length`, want: "Invalid regular expression"},
	// Nesting just under the limit compiles, without a crash.
	{name: "script text nested parens under the limit", code: `"0&&"+"(".repeat(49000)+"1"+")".repeat(49000)`, generate: true, want: "ok"},
	{name: "script text arrow chain under the limit", code: `"0&&("+"a=>".repeat(20000)+"1)"`, generate: true, want: "ok"},
	{name: "eval nested brackets under the limit", code: `eval("0&&"+"[".repeat(49000)+"]".repeat(49000))`, want: "ok"},
	{name: "Function spread nesting under the limit", code: `Function("a", "return "+"[...".repeat(19000)+"a"+"]".repeat(19000)); 1`, want: "ok"},
	{name: "TypeScript keyof chain under the limit", code: `"type T = " + "keyof ".repeat(90000) + "any; 7"`, generate: true, language: "typescript", want: "ok"},
	{name: "TypeScript nested parens under the limit", code: `"let x: number = 0&&"+"(".repeat(49000)+"1"+")".repeat(49000)+"; x"`, generate: true, language: "typescript", want: "ok"},
}

// TestStackGuardVectors: none of these may kill the process; each must come
// back as an error result.
func TestStackGuardVectors(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a child process per vector")
	}
	type outcome struct {
		result  *Result
		output  string
		err     error
		elapsed time.Duration
	}
	outcomes := make([]outcome, len(stackVectors))
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	for i, v := range stackVectors {
		wg.Add(1)
		go func(i int, v stackVector) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			budget := int64(1 << 30)
			if v.heapBound || v.defaultBudget {
				budget = 0
			}
			start := time.Now()
			r, out, err := runStackChild(v, budget)
			outcomes[i] = outcome{r, out, err, time.Since(start)}
		}(i, v)
	}
	wg.Wait()

	for i, v := range stackVectors {
		o := outcomes[i]
		t.Run(v.name, func(t *testing.T) {
			t.Logf("child took %v", o.elapsed.Round(time.Millisecond))
			if o.err != nil || o.result == nil {
				tail := o.output
				if len(tail) > 2000 {
					tail = tail[:2000]
				}
				t.Fatalf("child process died (%v): the script crashed the process\n%s", o.err, tail)
			}
			if v.want == "ok" {
				if !o.result.Ok {
					t.Fatalf("expected success, got %s: %.300s", o.result.Error.Code, o.result.Error.Message)
				}
				return
			}
			if o.result.Ok {
				if v.heapBound {
					// The chain fitted in both the memory budget and the Go
					// stack on this platform (seen on macOS amd64 for the
					// prototype chain). The child survived, which is what the
					// vector proves; whether the watchdog trips first depends
					// on the allocator.
					return
				}
				t.Fatalf("expected an error result, got ok")
			}
			msg := o.result.Error.Message
			switch {
			case v.want != "":
				if !strings.Contains(msg, v.want) {
					t.Fatalf("unexpected error %s: %.300s (want %q)", o.result.Error.Code, msg, v.want)
				}
			case strings.Contains(msg, stackOverflowMessage):
			case v.heapBound && o.result.Error.Code == ErrorCodeMemoryLimit:
			default:
				t.Fatalf("unexpected error %s: %.300s", o.result.Error.Code, msg)
			}
		})
	}
}

func runStackChild(v stackVector, budget int64) (*Result, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStackGuardChild$", "-test.v")
	cmd.Env = append(os.Environ(), stackChildEnv+"="+v.code, stackChildBudgetEnv+"="+strconv.FormatInt(budget, 10),
		stackChildLangEnv+"="+v.language)
	if v.generate {
		cmd.Env = append(cmd.Env, stackChildGenEnv+"=1")
	}
	if v.noShapeCheck {
		cmd.Env = append(cmd.Env, stackChildNoShapeEnv+"=1")
	}
	out, err := cmd.CombinedOutput()
	output := string(out)
	if err != nil {
		return nil, output, err
	}
	i := strings.Index(output, stackChildMarker)
	if i < 0 {
		return nil, output, fmt.Errorf("no result line")
	}
	line := output[i+len(stackChildMarker):]
	if j := strings.IndexByte(line, '\n'); j >= 0 {
		line = line[:j]
	}
	var result Result
	if err := json.Unmarshal([]byte(line), &result); err != nil {
		return nil, output, err
	}
	return &result, output, nil
}

// TestStackGuardLegitimateDepth: the limits leave ordinary nesting alone.
func TestStackGuardLegitimateDepth(t *testing.T) {
	cases := []struct {
		name string
		code string
		want interface{}
	}{
		{"nested arrays String", `var a=[1]; for(var i=0;i<500;i++) a=[a]; String(a)`, "1"},
		{"nested arrays join and flat", `var a=[1]; for(var i=0;i<500;i++) a=[a,2]; a.flat(Infinity).length`, int64(501)},
		{"JSON round trip depth 1000", `var o={v:1}; for(var i=0;i<1000;i++) o={n:o}; var s=JSON.stringify(o); var p=JSON.parse(s); var d=0; while(p.n){p=p.n; d++} d`, int64(1000)},
		{"JSON replacer array depth 1000", `var o={v:1}; for(var i=0;i<1000;i++) o={n:o, x:i}; JSON.stringify(o, ["n","v"]).length`, int64(len(strings.Repeat(`{"n":`, 1000)) + len(`{"v":1}`) + 1000)},
		{"JS recursion depth 3000", `function r(n){ return n ? r(n-1)+1 : 0 } r(3000)`, int64(3000)},
		{"recursion through forEach depth 1000", `var d=0; function f(n){ if(n){ d++; [n-1].forEach(f) } } f(1000); d`, int64(1000)},
		{"deep result depth 1000", `var o={v:1}; for(var i=0;i<1000;i++) o={n:o}; var r=o; r.n.n.v=2; r.n.n.v`, int64(2)},
		{"RangeError is catchable", `var a=[]; for(var i=0;i<100000;i++) a=[a]; var m; try { String(a) } catch (e) { m = (e instanceof RangeError) + ":" + e.message } m`, "true:" + stackOverflowMessage},
		{"guard depth restored after catch", `var a=[]; for(var i=0;i<100000;i++) a=[a]; for (var k=0;k<5;k++) { try { String(a) } catch (e) {} } var b=[1]; for(var i=0;i<500;i++) b=[b]; String(b)`, "1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := Execute(context.Background(), newMockToolCaller(), tc.code, ExecutionOptions{TimeoutMs: 30000})
			if !result.Ok {
				t.Fatalf("expected success, got %+v", result.Error)
			}
			if result.Value != tc.want {
				t.Fatalf("got %#v, want %#v", result.Value, tc.want)
			}
		})
	}
}

// TestSourceGuard: eval and the Function constructors keep working on
// ordinary source, and refuse oversized source with a catchable RangeError.
func TestSourceGuard(t *testing.T) {
	big := `"[".repeat(60000)+"]".repeat(60000)`
	cases := []struct {
		name string
		code string
		want interface{}
	}{
		{"eval", `eval("1+2")`, int64(3)},
		{"eval of a non-string", `eval(5)`, int64(5)},
		{"eval of nothing", `typeof eval()`, "undefined"},
		{"eval name and length", `eval.name + eval.length`, "eval1"},
		{"eval runs in the global scope", `var x = 1; (function(){ var x = 2; return eval("x") })()`, int64(1)},
		{"eval declares globals", `eval("var y = 7"); y`, int64(7)},
		{"eval syntax error", `try { eval("(") } catch (e) { e instanceof SyntaxError }`, true},
		{"eval refusal is a RangeError", `try { eval(` + big + `) } catch (e) { (e instanceof RangeError) + ":" + e.message.indexOf("` + sourceTooDeepMessage + `") }`, "true:0"},
		{"Function", `new Function("a", "b", "return a+b")(1, 2)`, int64(3)},
		{"Function call form", `Function("return 4")()`, int64(4)},
		{"Function with object arguments", `Function({toString:function(){return "a"}}, {toString:function(){return "return a*2"}})(4)`, int64(8)},
		{"Function with a symbol argument", `try { Function(Symbol()) } catch (e) { e instanceof TypeError }`, true},
		{"Function source", `String(Function("a", "return a")).indexOf("anonymous")`, int64(9)},
		{"Function prototype", `Object.getPrototypeOf(Function("return 1")) === Function.prototype`, true},
		{"AsyncFunction prototype", `var AF = Object.getPrototypeOf(async function(){}).constructor; Object.getPrototypeOf(AF("return 1")) === AF.prototype`, true},
		{"GeneratorFunction", `var GF = Object.getPrototypeOf(function*(){}).constructor; GF("yield 1; yield 2")().next().value`, int64(1)},
		{"Function refusal is a RangeError", `try { Function(` + big + `) } catch (e) { e instanceof RangeError }`, true},
		{"Function refusal counts every parameter", `try { Function("a" + "=1".repeat(60000), "b" + "=1".repeat(60000), "") } catch (e) { e instanceof RangeError }`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := Execute(context.Background(), newMockToolCaller(), tc.code, ExecutionOptions{TimeoutMs: 30000})
			if !result.Ok {
				t.Fatalf("expected success, got %+v", result.Error)
			}
			if result.Value != tc.want {
				t.Fatalf("got %#v, want %#v", result.Value, tc.want)
			}
		})
	}
}

// TestSourceGuardScriptText: a script over the limit is refused before it is
// compiled, in either language; one just under it runs.
func TestSourceGuardScriptText(t *testing.T) {
	over := "1" + strings.Repeat("+1", maxSourceTokens+1)
	for _, lang := range []string{"javascript", "typescript"} {
		result := Execute(context.Background(), newMockToolCaller(), over, ExecutionOptions{TimeoutMs: 30000, Language: lang})
		if result.Ok || result.Error.Code != ErrorCodeSyntaxError || !strings.Contains(result.Error.Message, sourceTooDeepMessage) {
			t.Fatalf("%s: expected a SYNTAX_ERROR naming the limit, got %+v", lang, result)
		}
	}
	// Long flat source: identifiers, strings and whitespace are not counted.
	flat := strings.Repeat("var abcdefghij = 'lorem ipsum dolor sit amet'\n", 20000) + "abcdefghij"
	if result := Execute(context.Background(), newMockToolCaller(), flat, ExecutionOptions{TimeoutMs: 30000}); !result.Ok {
		t.Fatalf("flat script: expected success, got %+v", result.Error)
	}
}

func TestSourceOperatorTokens(t *testing.T) {
	cases := []struct {
		src  string
		want int
	}{
		{"", 0},
		{"abc def 123 $_x", 0},
		{"a+b", 1},
		{"[[1]]", 4},
		{"typeof typeof x", 2},
		{"newer instanceofx in", 1},
		{"'([{'", 5},
		{"// (\n/* [ */", 8},
		{"caf\u00e9 \u00e9t\u00e9", 0},
		{"\t\n\r ", 0},
		{"do x; while(0) else", 5},
	}
	for _, tc := range cases {
		if got := sourceOperatorTokens(tc.src, maxSourceTokens); got != tc.want {
			t.Errorf("sourceOperatorTokens(%q) = %d, want %d", tc.src, got, tc.want)
		}
	}
	// The count stops just past the limit.
	if got := sourceOperatorTokens(strings.Repeat("(", 1000), 10); got != 11 {
		t.Errorf("limited count = %d, want 11", got)
	}

	// TypeScript: every word counts as well.
	tsCases := []struct {
		src  string
		want int
	}{
		{"", 0},
		{"abc def 123 $_x", 4},
		{"keyof keyof T", 3},
		{"a is a is a", 5},
		{"a+b", 3},
		{"caf\u00e9 \u00e9t\u00e9", 2},
		{"\t\n\r ", 0},
		{"\u4e2d\u6587 x\u0301", 2},
		// Non-ASCII separators never merge words.
		{"keyof\u00a0keyof\u00a0T", 5}, {"keyof\u2028keyof", 3}, {"keyof\u2029keyof", 3},
		{"keyof\ufeffkeyof", 3}, {"keyof\u3000keyof", 3}, {"keyof\u200bkeyof", 3},
		{"a\u00a0\u00a0b", 4}, {"a\xffb", 3}, {"\u2013\u2014", 2},
	}
	for _, tc := range tsCases {
		if got := typeScriptSourceTokens(tc.src, maxSourceTokens); got != tc.want {
			t.Errorf("typeScriptSourceTokens(%q) = %d, want %d", tc.src, got, tc.want)
		}
	}
}

// TestTranspileTypeScriptBoundsOutput: esbuild indents every nested line, so
// deeply nested source gets whitespace-free output instead of output that
// grows with the square of the depth; ordinary source keeps the readable form.
func TestTranspileTypeScriptBoundsOutput(t *testing.T) {
	deep := strings.Repeat("{", 49000) + strings.Repeat("}", 49000)
	out, jsErr := TranspileTypeScript(deep)
	if jsErr != nil {
		t.Fatalf("transpile: %v", jsErr)
	}
	if len(out) > 1<<20 {
		t.Fatalf("output of %d bytes for %d bytes of source", len(out), len(deep))
	}
	out, jsErr = TranspileTypeScript("function f(x: number): number {\n  if (x) {\n    return x;\n  }\n  return 0;\n}\n")
	if jsErr != nil {
		t.Fatalf("transpile: %v", jsErr)
	}
	if !strings.Contains(out, "\n    return x;\n") {
		t.Fatalf("ordinary source lost its readable output:\n%s", out)
	}
	if _, jsErr := TranspileTypeScript("type T = " + strings.Repeat("keyof ", maxSourceTokens) + "any"); jsErr == nil || jsErr.Code != ErrorCodeSyntaxError || !strings.Contains(jsErr.Message, sourceTooDeepMessage) {
		t.Fatalf("expected a SYNTAX_ERROR naming the limit, got %+v", jsErr)
	}
}

// TestStackGuardCallDepthLimitMessage: goja's own stack overflow error has an
// empty message; Execute names it.
func TestStackGuardCallDepthLimitMessage(t *testing.T) {
	result := Execute(context.Background(), newMockToolCaller(), `function h(){ h() } h()`, ExecutionOptions{TimeoutMs: 30000})
	if result.Ok || result.Error.Code != ErrorCodeRuntimeError || !strings.Contains(result.Error.Message, stackOverflowMessage) {
		t.Fatalf("expected a RUNTIME_ERROR naming the stack overflow, got %+v", result)
	}
}

// TestStackGuardInstrumentsEveryBuiltin: every builtin a script can reach is
// counted, including intrinsics goja only creates on first use and bound
// functions. An uncounted builtin is a way back to an unbounded recursion.
func TestStackGuardInstrumentsEveryBuiltin(t *testing.T) {
	vm := goja.New()
	setupSandbox(vm)
	g, err := installStackGuard(vm)
	if err != nil {
		t.Fatal(err)
	}
	// Touch lazily created intrinsics, then walk from what that produced.
	extra, err := vm.RunString(`[
		new Int8Array(2), new Uint8ClampedArray(1), new Float64Array(1), new DataView(new ArrayBuffer(4)),
		new Map([[1,2]]).keys(), new Set([1]).entries(), new WeakMap(), new WeakSet(),
		[1].entries(), "ab"[Symbol.iterator](), "aXbX".matchAll(/X/g), /a/y, new Date(0), Symbol("s"), Object(Symbol("o")),
		Object(1n), new Error("e"), new TypeError("t"), new RangeError("r"), new SyntaxError("s"),
		(function*(){ yield 1 })(), (async function(){})(), Promise.resolve(1), new Proxy({}, {}),
		(function(){ return arguments })(1), (function(){ "use strict"; return arguments })(1),
		Function("return 1"), new Function("a", "return a"), class A {}, () => 1, { m() {} },
		Object.getOwnPropertyDescriptor(Function.prototype, "caller"),
	]`)
	if err != nil {
		t.Fatal(err)
	}
	funcs, err := collectBuiltins(vm, extra)
	if err != nil {
		t.Fatal(err)
	}
	toSource, _ := goja.AssertFunction(vm.Get("Function").ToObject(vm).Get("prototype").ToObject(vm).Get("toString"))
	for _, fn := range funcs {
		_, _, native := nativeFuncFields(fn)
		src, err := toSource(fn)
		if err != nil {
			t.Fatal(err)
		}
		// A builtin of a kind nativeFuncFields does not know (a goja upgrade
		// adding one) would be silently left out: catch it by its source.
		if !native && strings.Contains(src.String(), "[native code]") {
			t.Errorf("builtin %v is of a kind the guard cannot instrument", fn.Get("name"))
			continue
		}
		if !native {
			continue
		}
		if _, ok := g.patched[fn]; !ok {
			t.Errorf("builtin %v is not instrumented", fn.Get("name"))
		}
	}
	if len(g.patched) < 200 {
		t.Fatalf("only %d builtins instrumented; the walk is missing intrinsics", len(g.patched))
	}

	// Bound functions are created at run time and instrumented by bind.
	if _, err := vm.RunString(`var __o = {}; __o.toString = String.bind(null, __o);`); err != nil {
		t.Fatal(err)
	}
	if _, err := vm.RunString(`try { String(__o); "no error" } catch (e) { if (!(e instanceof RangeError)) throw e }`); err != nil {
		t.Fatalf("bound function not counted: %v", err)
	}
}

// TestBuiltinTemplateMatchesWalk: following the recorded template finds
// exactly the functions a full walk of a fresh VM finds, and a VM whose graph
// differs from the template is walked instead.
func TestBuiltinTemplateMatchesWalk(t *testing.T) {
	tmpl := builtinTemplate()
	if tmpl == nil {
		t.Fatal("no template: every execution walks the builtins in full")
	}
	vm := goja.New()
	setupSandbox(vm)
	followed, ok := tmpl.follow(vm)
	if !ok {
		t.Fatal("a fresh sandboxed VM does not follow the template")
	}
	walked, err := collectBuiltins(vm)
	if err != nil {
		t.Fatal(err)
	}
	// The builtins: the roots' own JS functions ((function*(){}), ...) are
	// created anew each time the roots are evaluated, and are not patched.
	set := func(fs []*goja.Object) map[*goja.Object]struct{} {
		m := make(map[*goja.Object]struct{}, len(fs))
		for _, f := range fs {
			if _, _, native := nativeFuncFields(f); native {
				m[f] = struct{}{}
			}
		}
		return m
	}
	fset, wset := set(followed), set(walked)
	if len(fset) != len(wset) || len(fset) < 200 {
		t.Fatalf("template found %d builtins, the walk %d", len(fset), len(wset))
	}
	for f := range wset {
		if _, ok := fset[f]; !ok {
			t.Fatalf("the template misses %v", f.Get("name"))
		}
	}

	// A global added before the guard is installed: the template no longer
	// describes the VM.
	other := goja.New()
	setupSandbox(other)
	if err := other.Set("extra", func(goja.FunctionCall) goja.Value { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, ok := tmpl.follow(other); ok {
		t.Fatal("followed the template on a VM with another global")
	}
	if funcs, err := builtinFunctions(other); err != nil || len(funcs) <= len(walked) {
		t.Fatalf("fallback walk: %d functions, %v", len(funcs), err)
	}
	// A builtin replaced by one of another kind: the template no longer
	// describes the VM. (A consistency check, not tamper detection: the guard
	// is only installed on pristine VMs, see newGuardedVM.)
	changed := goja.New()
	setupSandbox(changed)
	if _, err := changed.RunString(`Array.prototype.join = function(){}; Object.defineProperty(Map.prototype, "size", {get: String.prototype.trim.bind("")})`); err != nil {
		t.Fatal(err)
	}
	if _, ok := tmpl.follow(changed); ok {
		t.Fatal("followed the template on a VM whose builtins were replaced")
	}
}

// TestNativeFuncFieldsLayout pins the goja internals installStackGuard
// relies on: if a goja upgrade moves them, this fails before code_execution
// starts refusing every script.
func TestNativeFuncFieldsLayout(t *testing.T) {
	vm := goja.New()
	native := vm.Get("Array").ToObject(vm).Get("prototype").ToObject(vm).Get("join").ToObject(vm)
	if call, _, ok := nativeFuncFields(native); !ok || *call == nil {
		t.Fatal("cannot reach the call function of a builtin")
	}
	for _, name := range []string{"String", "Object", "Number"} { // String: nativeFuncObject; the others: templatedFuncObject
		if _, construct, ok := nativeFuncFields(vm.Get(name).ToObject(vm)); !ok || *construct == nil {
			t.Fatalf("cannot reach the construct function of %s", name)
		}
	}
	bound, err := vm.RunString(`String.bind(null)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := nativeFuncFields(bound.ToObject(vm)); !ok {
		t.Fatal("cannot reach the functions of a bound function")
	}
	jsFn, err := vm.RunString(`(function(){})`)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := nativeFuncFields(jsFn.ToObject(vm)); ok {
		t.Fatal("a JS function must not be treated as a builtin")
	}
}

// deep1500 builds a chain of 1500 objects in o: deep, but well within the
// native depth budget when serialized alone.
const deep1500 = `var o={}; for(var i=0;i<1500;i++) o={x:o}; `

// TestStackGuardStringifyMatchesGoja: the depth-tracking JSON.stringify
// produces goja's output, byte for byte, and goja's errors.
func TestStackGuardStringifyMatchesGoja(t *testing.T) {
	exprs := []string{
		`JSON.stringify({a:1,b:"x",c:[1,2,{d:null}],e:undefined,f:function(){},g:Symbol("s")})`,
		`JSON.stringify([undefined, function(){}, Symbol("s"), NaN, Infinity, -0, 1e21, 0.1])`,
		`JSON.stringify(undefined)`, `JSON.stringify(function(){})`, `JSON.stringify(null)`, `JSON.stringify("\u2028\ud800\"\\\n")`,
		`JSON.stringify({a:[1,{b:2}]}, null, 2)`, `JSON.stringify({a:[1,{b:2}]}, null, "--")`,
		`JSON.stringify({a:[1,{b:2}]}, null, 20)`, `JSON.stringify({a:[1,{b:2}]}, null, "0123456789abc")`,
		`JSON.stringify({a:[1,{b:2}]}, null, new Number(3))`, `JSON.stringify({a:[1,{b:2}]}, null, new String("~"))`,
		`JSON.stringify({a:[], b:{}}, null, 2)`,
		`JSON.stringify({a:1,b:{c:2,d:[3]}}, function(k,v){ return typeof v === "number" ? v*10 : v })`,
		`JSON.stringify({a:1,b:{c:2}}, function(k,v){ if (k === "b") return undefined; return v })`,
		`JSON.stringify({a:1}, function(k,v){ return k === "" ? [this === undefined, typeof this, v.a] : v })`,
		`JSON.stringify({a:{b:1}}, function(k,v){ if (k === "a") { this.z = 5 } return v })`,
		`JSON.stringify({a:1}, function(k,v){ return k === "" ? {x:{y:[1,2]}} : v })`,
		`JSON.stringify({b:1, a:2, 1:3, c:{b:4, 1:5}}, ["b", "1", "a", "c"])`,
		`JSON.stringify({b:1, a:2, 1:3}, ["b", 1, new String("a"), new Number(1), {}, true, "b"])`,
		`JSON.stringify(Object.create({inh:1}, {own:{value:2, enumerable:false}}), ["inh", "own", "none"])`,
		`JSON.stringify([{a:1,b:2},[{a:3,c:4}]], ["a"])`,
		`JSON.stringify({a:{toJSON:function(k){ return "T" + k }}, b:new Date(0)}, ["a","b"])`,
		`JSON.stringify({a:{b:{c:1}}}, ["a","b","c"], 1)`,
		`var log=[]; var o={get a(){log.push("a"); return {get c(){log.push("c"); return 1}}}, get b(){log.push("b"); return 2}}; JSON.stringify(o, ["a","b","c"]) + log.join()`,
		`var log=[]; var o={get a(){log.push("a"); return {get c(){log.push("c"); return 1}}}, get b(){log.push("b"); return 2}}; JSON.stringify(o) + log.join()`,
		`var log=[]; var p=new Proxy({x:1,y:{z:2}},{get:function(t,k){ log.push(String(k)); return t[k] }}); JSON.stringify(p, ["y","x","z"]) + log.join()`,
		`JSON.stringify({d:new Date(0), n:new Number(5), s:new String("s"), b:new Boolean(false), r:/x/g, m:new Map([[1,2]])})`,
		`JSON.stringify({n:new Number(5), s:new String("s"), b:new Boolean(true)}, ["n","s","b"])`,
		`JSON.stringify({toJSON:function(){ return {a:1} }})`,
		`JSON.stringify({a:{toJSON:function(k){ return k + "!" }}})`,
		`JSON.stringify(new Proxy([1,2,{a:3}], {}))`,
		`JSON.stringify({a:1n})`, `JSON.stringify(1n)`, `JSON.stringify({a:Object(1n)})`, `JSON.stringify({a:1n}, ["a"])`,
		`var o={}; o.self=o; JSON.stringify(o)`, `var o={}; o.self=o; JSON.stringify(o, ["self"])`,
		`var a=[]; a.push(a); JSON.stringify(a)`, `var a=[]; a.push({x:a}); JSON.stringify(a, ["x"])`,
		`var s={a:1}; JSON.stringify({x:s, y:s, z:[s,s]}, ["x","y","z","a"])`,
		`JSON.stringify({a:1}, function(){ throw new Error("boom") })`,
		`JSON.stringify({get a(){ throw new TypeError("getter") }})`,
		`JSON.stringify({a:[1]}, {not:"a replacer"})`,
		`JSON.stringify(input)`, `JSON.stringify(input, ["k", "nested", "v"])`,
		// Boxed primitives are unwrapped by goja, also through a property list.
		`var o=Object(Symbol("s")); o.a=1; JSON.stringify([o], ["a"])`, `var o=Object(Symbol("s")); o.a=o; JSON.stringify(o, ["a"])`,
		`var o=Object(Symbol("s")); o.a=1; JSON.stringify([o])`, `var o=new Number(1); o.a=o; JSON.stringify([o], ["a"])`,
		// A sibling serialized after a deep subtree runs at its own depth.
		deep1500 + `JSON.stringify({a:o, get b(){ return JSON.stringify(o).length }}).length`,
		deep1500 + `JSON.stringify({a:o, b:{toJSON:function(){ return JSON.stringify(o).length }}}).length`,
		deep1500 + `JSON.stringify([o, {toJSON:function(){ return JSON.stringify(o).length }}]).length`,
		deep1500 + `var arr=[o]; Object.defineProperty(arr, 1, {get:function(){ return JSON.stringify(o).length }, enumerable:true}); JSON.stringify(arr).length`,
		deep1500 + `JSON.stringify({a:o, get b(){ return JSON.stringify(o).length }}, ["a","b","x"]).length`,
		deep1500 + `JSON.stringify([o, {get b(){ return JSON.stringify(o).length }}], ["x","b"]).length`,
		deep1500 + `JSON.stringify({a:o, get b(){ return JSON.stringify(o).length }}, function(k,v){ return v }).length`,
		// What the script sees while goja serializes is unchanged.
		`var inner={b:1}; JSON.stringify({a:inner}, function(k,v){ return k === "b" ? (this === inner) : v })`,
		`var arr=[1]; JSON.stringify({a:arr}, function(k,v){ return k === "0" ? (this === arr) : v })`,
		`var o={get g(){ return this === o }}; JSON.stringify(o)`, `var o={toJSON:function(){ return this === o }}; JSON.stringify({k:o})`,
		`var o={a:1, get b(){ o.c=3; delete o.a; return 2 }}; JSON.stringify(o) + JSON.stringify(o)`,
		`JSON.stringify(Object.freeze({a:Object.freeze([1,{b:2}])}))`, `JSON.stringify({t:new Uint8Array([1,2]), d:new Date(0), r:/x/, e:new Error("m")})`,
		`(function(){ return JSON.stringify(arguments) })(1, {a:2})`, `class C { constructor(){ this.p = 1 } get g(){ return 1 } }; JSON.stringify(new C())`,
		`var s={x:1}; JSON.stringify({a:s, b:[s, s]})`, `var o={}; var a=[o]; o.a=a; JSON.stringify(a)`,
		`var o={a:{}}; JSON.stringify(o, function(k,v){ return k === "a" ? o : v })`,
		`var log=[]; var p=new Proxy({x:{y:1}},{get:function(t,k){ log.push(String(k)); return t[k] }, ownKeys:function(t){ log.push("keys"); return Reflect.ownKeys(t) }}); JSON.stringify({p:p}) + log.join()`,
		`JSON.stringify([new Proxy([1,[2]], {})])`,
		// A getter may freeze its own property: the guard's bookkeeping adds no
		// proxy invariants of its own.
		`var o={get x(){ Object.defineProperty(this,"x",{value:2,writable:false,configurable:false}); return 1 }}; JSON.stringify(o) + JSON.stringify(o)`,
		`var a=[0]; Object.defineProperty(a,0,{get:function(){ Object.defineProperty(a,0,{value:2,writable:false,configurable:false}); return 1 }, configurable:true, enumerable:true}); JSON.stringify(a) + JSON.stringify(a)`,
		`var o={get x(){ Object.defineProperty(this,"x",{get:undefined, configurable:false}); return 1 }}; JSON.stringify(o)`,
		`var o={a:1, get b(){ Object.defineProperty(o,"c",{value:3, enumerable:true, configurable:false}); Object.freeze(o); return 2 }}; JSON.stringify(o) + JSON.stringify(o)`,
		`JSON.stringify(Object.freeze({a:{b:Object.freeze([1])}}), null, 1)`,
		`var o={get x(){ Object.defineProperty(this,"x",{value:2,writable:false,configurable:false}); return 1 }, "\ud800":5}; JSON.stringify(o)`,
		`var o={b:1}; Object.defineProperty(o,"a",{value:0, enumerable:false}); o[Symbol("s")]=1; o[2]=3; JSON.stringify(o)`,
		`JSON.stringify(new Uint8Array([1,2])) + JSON.stringify((function(){ return arguments })(1,[2]))`,
		// Revisits see the object as it is then; objects with the same keys
		// share a shell but not their values; cycles through every kind.
		`var s={x:1}; JSON.stringify({a:s, get b(){ s.y=2; delete s.x; return s }, c:[s]})`,
		`JSON.stringify([{a:1,b:2},{a:3,b:4},{b:5,a:6},{}, {}])`, `JSON.stringify([{1:1,a:2},{a:3,1:4}])`,
		// Shells are keyed by their key list, unambiguously.
		`JSON.stringify([{"a\u0000b":1}, {a:2, b:3}])`, `JSON.stringify([{a:2, b:3}, {"a\u0000b":1}])`,
		`JSON.stringify([{"1:a":1, b:2}, {"1":3, "a:b":4}, {"a\u0000":5}, {"a":6, "":7}])`,
		// A polluted Object.prototype reaches no descriptor or handler the
		// guard makes.
		`Object.prototype.get=function(){ return 9 }; JSON.stringify({a:1,b:[1,{c:2}]}, ["a","b","c"])`,
		`Object.prototype.set=function(){}; JSON.stringify({a:1}, ["a"])`,
		`Object.prototype.get=function(){ return 9 }; Object.prototype.set=function(){}; JSON.stringify({a:1,b:[1,{c:2}],"\ud800":3}) + JSON.stringify({a:1,b:{c:2}}, function(k,v){ return v })`,
		`Object.prototype.value=5; Object.prototype.writable=false; Object.prototype.enumerable=false; Object.prototype.configurable=false; JSON.stringify({a:1,b:{a:2}}, ["a","b"]) + JSON.stringify({a:1,b:{a:2}}) + JSON.stringify({a:1}, function(k,v){ return v })`,
		`Object.prototype.ownKeys=function(){ return ["zz"] }; Object.prototype.has=function(){ return true }; Object.prototype.getOwnPropertyDescriptor=function(){ return {value:7, enumerable:true, configurable:true} }; JSON.stringify({a:1,b:[2,{c:3}]}) + JSON.stringify({a:1,b:{c:3}}, ["a","b","c"]) + JSON.stringify({a:1,b:{c:3}}, function(k,v){ return v }) + JSON.stringify({"\ud800":1, q:[1]})`,
		`Object.prototype.get=function(t,k){ return k === "x" ? 2 : undefined }; JSON.stringify({p:new Proxy({x:1},{})}) + JSON.stringify({p:new Proxy({x:1},{})}, ["p","x"])`,
		`var o={}; o.p=new Proxy(o, {}); JSON.stringify(o)`, `var a=[]; a[0]=new Proxy(a, {}); JSON.stringify(a)`,
		`var o={}; var p=new Proxy({}, {get:function(t,k){ return k === "x" ? o : undefined }, ownKeys:function(){ return ["x"] }, getOwnPropertyDescriptor:function(){ return {value:1, enumerable:true, configurable:true} }}); o.p=p; JSON.stringify(o)`,
		`var o={"\ud800":1}; o.self=o; JSON.stringify(o)`, `var o={}; o.n=new Number(1); o.n.o=o; JSON.stringify(o)`,
		`var o={a:{}}; JSON.stringify(o, function(k,v){ return k === "b" ? o : (k === "a" ? {b:1} : v) })`,
		`var r=Proxy.revocable({}, {}); r.revoke(); JSON.stringify({a:r.proxy})`, `var r=Proxy.revocable([], {}); r.revoke(); JSON.stringify([r.proxy])`,
		`var log=[]; var h={}; ["get","ownKeys","getOwnPropertyDescriptor","has"].forEach(function(t){ h[t]=function(){ log.push(t + (typeof arguments[1] === "string" ? " " + arguments[1] : "")); return Reflect[t].apply(null, arguments) } }); var p=new Proxy({a:1, b:[1,{c:2}], toJSON:undefined}, h); var q=new Proxy([1,p], h); JSON.stringify({q:q, p:p}, null, 1) + log.join("|")`,
		`var p=new Proxy({}, {ownKeys:function(){ return ["x","y"] }, getOwnPropertyDescriptor:function(t,k){ return k === "x" ? {value:1, enumerable:true, configurable:true} : undefined }, get:function(t,k){ return k + "!" }}); JSON.stringify(p)`,
		`var p=new Proxy({}, {ownKeys:function(){ return [1] }}); JSON.stringify(p)`,
		`var o={}; var p=new Proxy(o, {}); o.self=p; JSON.stringify(p)`,
		deep1500 + `JSON.stringify(new Proxy({a:o, get b(){ return JSON.stringify(o).length }}, {})).length`,
		deep1500 + `JSON.stringify(new Proxy([o, 1], {get:function(t,k){ return k === "1" ? JSON.stringify(o).length : t[k] }})).length`,
	}
	// One key per level: goja serializes a Go map in Go's random order.
	input := map[string]interface{}{"nested": map[string]interface{}{"v": []interface{}{1, "two"}}}
	run := func(vm *goja.Runtime, expr string) string {
		_ = vm.Set("input", input)
		v, err := vm.RunString(`(function(){ try { var r = (function(){ return eval(` + strconv.Quote(expr) + `) })(); return typeof r + ":" + String(r) } catch (e) { return "throw:" + e.name + ":" + e.message } })()`)
		if err != nil {
			return "go error: " + err.Error()
		}
		return v.String()
	}
	for _, expr := range exprs {
		plain := goja.New()
		guarded := goja.New()
		if _, err := installStackGuard(guarded); err != nil {
			t.Fatal(err)
		}
		want, got := run(plain, expr), run(guarded, expr)
		if got != want {
			t.Errorf("%s\n got: %s\nwant: %s", expr, got, want)
		}
	}
}

// TestStackGuardParseMatchesGoja: JSON.parse keeps goja's results and errors,
// and coerces its argument once.
func TestStackGuardParseMatchesGoja(t *testing.T) {
	exprs := []string{
		`JSON.stringify(JSON.parse('{"a":[1,{"b":"[[[{"}],"c":"\\"]"}'))`,
		`JSON.stringify(JSON.parse('[1,2]', function(k,v){ return typeof v === "number" ? v+1 : v }))`,
		`JSON.parse('[1,')`, `JSON.parse('')`, `JSON.parse()`, `JSON.parse('1', null)`, `JSON.parse('{"a":1}', 5).a`,
		`var n=0; JSON.parse({toString:function(){ n++; return "[1]" }}); n`,
		`JSON.stringify(JSON.parse("[".repeat(1000) + "]".repeat(1000))).length`,
		// The reviver walk: order, holders, deletion, replacement, mutation.
		`var log=[]; var r=JSON.parse('{"a":[1,{"b":2}],"c":{"d":null},"e":"s"}', function(k,v){ log.push(k + "=" + JSON.stringify(v) + "@" + Array.isArray(this)); return v }); log.join("|") + JSON.stringify(r)`,
		`JSON.stringify(JSON.parse('{"a":1,"b":2,"c":[1,2,3]}', function(k,v){ return k === "b" || k === "1" ? undefined : v }))`,
		`JSON.stringify(JSON.parse('{"a":1,"b":{"c":2}}', function(k,v){ if (k === "a") { this.b = {z:9}; this.x = 1 } return v }))`,
		`JSON.stringify(JSON.parse('[1,2,3]', function(k,v){ if (k === "0") { this.length = 1 } return v }))`,
		`JSON.stringify(JSON.parse('[1,2]', function(k,v){ if (k === "0") { this.push(7) } return v }))`,
		`JSON.parse('{"a":1}', function(k,v){ return k === "" ? this : v })[""].a`,
		`JSON.stringify(JSON.parse('{"a":1,"b":2}', function(k,v){ if (k === "a") Object.defineProperty(this,"b",{value:5, configurable:false}); return k === "b" ? 7 : v }))`,
		`JSON.stringify(JSON.parse('{"a":1,"b":2}', function(k,v){ if (k === "a") Object.freeze(this); return k === "b" ? undefined : v }))`,
		`JSON.stringify(JSON.parse('{"__proto__":1,"\\ud800":2}', function(k,v){ return typeof v === "number" ? v * 10 : v }))`,
		`JSON.parse('{"a":{"x":1}}', function(k,v){ if (k === "a") { return new Proxy(v, {}) } return v }); "ok"`,
		`var log=[]; var p=new Proxy({q:1}, {ownKeys:function(t){ log.push("keys"); return Reflect.ownKeys(t) }, get:function(t,k){ log.push("get " + String(k)); return t[k] }, defineProperty:function(t,k,d){ log.push("def " + k + " " + JSON.stringify(d)); return Reflect.defineProperty(t,k,d) }, deleteProperty:function(t,k){ log.push("del " + k); return delete t[k] }}); JSON.parse('{"a":0}', function(k,v){ if (k === "a") return p; if (k === "q") return undefined; return v }); JSON.parse('{"a":{"b":1}}', function(k,v){ if (k === "a") { this.a = p } return v }); log.join("|")`,
		`JSON.parse('{"a":1}', function(){ throw new RangeError("rev") })`,
		`JSON.parse('[1', function(){ return 1 })`, `JSON.parse('{"a":1}', {}).a`, `JSON.parse('7', function(k,v){ return [k, v, typeof this] }).join()`,
		`var n=0; JSON.parse({toString:function(){ n++; return "[1]" }}, function(k,v){ return v }); n`,
		`JSON.stringify(JSON.parse('[[[1]]]', new Proxy(function(k,v){ return v }, {})))`,
		`Object.prototype.get=function(){}; Object.prototype.set=function(){}; JSON.stringify(JSON.parse('{"a":[1,{"b":2}]}', function(k,v){ return typeof v === "number" ? v+1 : v }))`,
		`Object.prototype.value=1; Object.prototype.writable=false; Object.prototype.enumerable=false; Object.prototype.configurable=false; JSON.stringify(JSON.parse('{"a":[1,{"b":2}]}', function(k,v){ return typeof v === "number" ? v+1 : v }))`,
		`Object.prototype.defineProperty=function(){ return false }; Object.prototype.deleteProperty=function(){ return false }; JSON.stringify(JSON.parse('{"a":1,"b":2}', function(k,v){ return k === "b" ? undefined : v }))`,
		`var o={v:1}; for(var i=0;i<1000;i++) o={n:o}; var p=JSON.parse(JSON.stringify(o), function(k,v){ return v }); var d=0; while(p.n){p=p.n; d++} d`,
	}
	run := func(vm *goja.Runtime, expr string) string {
		v, err := vm.RunString(`(function(){ try { var r = (function(){ return eval(` + strconv.Quote(expr) + `) })(); return typeof r + ":" + String(r) } catch (e) { return "throw:" + e.name + ":" + e.message } })()`)
		if err != nil {
			return "go error: " + err.Error()
		}
		return v.String()
	}
	for _, expr := range exprs {
		plain := goja.New()
		guarded := goja.New()
		if _, err := installStackGuard(guarded); err != nil {
			t.Fatal(err)
		}
		want, got := run(plain, expr), run(guarded, expr)
		if got != want {
			t.Errorf("%s\n got: %s\nwant: %s", expr, got, want)
		}
	}
}

func TestJSONNestingDepth(t *testing.T) {
	cases := map[string]int{
		``: 0, `1`: 0, `[]`: 1, `[[],{}]`: 2, `{"a":[{"b":[]}]}`: 4,
		`"[[[["`: 0, `["\"[[["]`: 1, `["\\",[[]]]`: 3,
	}
	for in, want := range cases {
		if got := jsonNestingDepth(in); got != want {
			t.Errorf("jsonNestingDepth(%q) = %d, want %d", in, got, want)
		}
	}
}

// TestStackGuardBuiltinsMatchGoja: flat, the prototype setters and proxies
// keep goja's results and errors for ordinary use.
func TestStackGuardBuiltinsMatchGoja(t *testing.T) {
	exprs := []string{
		// Array.prototype.flat
		`JSON.stringify([1,[2,[3,[4,[5]]]]].flat())`, `JSON.stringify([1,[2,[3,[4,[5]]]]].flat(2))`,
		`JSON.stringify([1,[2,[3,[4,[5]]]]].flat(Infinity))`, `JSON.stringify([1,[2,[3]]].flat(0))`,
		`JSON.stringify([1,[2,[3]]].flat(-1))`, `JSON.stringify([1,[2,[3]]].flat("2"))`, `JSON.stringify([1,[2,[3]]].flat(NaN))`,
		`JSON.stringify([1,[2,[3]]].flat(undefined))`, `JSON.stringify([1,[2,[3]]].flat({valueOf:function(){ return 5 }}))`,
		`JSON.stringify([1,,[2,,3]].flat(Infinity))`, `JSON.stringify(Array.prototype.flat.call({length:2, 0:[1,[2]], 1:3}, Infinity))`,
		`Array.prototype.flat.call(null)`, `Array.prototype.flat.call(undefined, 1)`,
		`class A extends Array {}; var r = A.from([1,[2,[3]]]).flat(Infinity); (r instanceof A) + JSON.stringify(r)`,
		`JSON.stringify(new Proxy([1,[2,[3]]], {}).flat(Infinity))`,
		`var a=[1]; for(var i=0;i<900;i++) a=[a]; a.flat(Infinity).length`,
		`JSON.stringify([[1],[2,[3]]].flatMap(function(x){ return x }))`,
		// goja reads the receiver's length before it coerces the depth.
		`var o={0:7,length:1}; var d={valueOf:function(){ o.length=0; return 1 }}; JSON.stringify(Array.prototype.flat.call(o,d))`,
		`var log=[]; var o={get length(){ log.push("length"); return 1 }, 0:[1]}; Array.prototype.flat.call(o, {valueOf:function(){ log.push("depth"); return Infinity }}); log.join()`,
		`[1].flat(Symbol())`, `[1,[2]].flat({valueOf:function(){ throw new Error("boom") }})`, `JSON.stringify([1,[2]].flat(1n))`,
		`var calls=0; JSON.stringify([1,[2,[3]]].flat({valueOf:function(){ calls++; return 2 }})) + calls`,
		// A species result that is not an array, with depth Infinity.
		`var a=[1,2]; a.constructor={}; a.constructor[Symbol.species]=function(){ return {} }; JSON.stringify(a.flat(Infinity))`,
		`var a=[1,[2,[3]]]; a.constructor={}; a.constructor[Symbol.species]=function(){ return {} }; JSON.stringify(a.flat(Infinity))`,
		`var a=[1,[2]]; a.constructor={}; a.constructor[Symbol.species]=function(){ return {pre:[0]} }; JSON.stringify(a.flat(Infinity))`,
		`var a=[1,[2]]; a.constructor={}; a.constructor[Symbol.species]=function(){ return new Array(1e6) }; var r=a.flat(Infinity); r.length + ":" + r[0] + r[1]`,
		`var a=[1,[2]]; a.constructor={}; a.constructor[Symbol.species]=function(){ var r=[]; r.length=4294967295; return r }; var r=a.flat(Infinity); r.length + ":" + r[0] + r[1]`,
		`var a=[1,[2]]; a.constructor={}; a.constructor[Symbol.species]=function(){ return new Uint8Array(4) }; var r=a.flat(Infinity); Array.prototype.join.call(r)`,
		`var a=[1,[2]]; a.constructor={}; a.constructor[Symbol.species]=function(){ return new Uint8Array(1e7) }; var r=a.flat(Infinity); r.length + ":" + r[0] + r[1]`,
		// The species result is only written to, never read.
		`var log=[]; var a=[1,[2,[3]]]; a.constructor={}; a.constructor[Symbol.species]=function(){ var r={}; Object.defineProperty(r,"7",{get:function(){ log.push("read"); return [] }, enumerable:true}); return r }; var r=a.flat(1e9); log.join() + ":" + r[0] + r[1] + r[2]`,
		`var a=[1,[2]]; a.constructor={}; a.constructor[Symbol.species]=function(){ return Object.freeze([]) }; a.flat()`,
		`var a=[1]; a.constructor={}; a.constructor[Symbol.species]=5; a.flat()`, `var a=[1]; a.constructor={}; a.constructor[Symbol.species]=null; JSON.stringify(a.flat())`,
		`var a=[1]; a.constructor=undefined; JSON.stringify(a.flat())`, `var a=[1]; a.constructor=5; JSON.stringify(a.flat())`,
		`var log=[]; var src=new Proxy([1,[2,,[3]]], {has:function(t,k){ log.push("has " + String(k)); return k in t }, get:function(t,k){ log.push("get " + String(k)); return t[k] }}); JSON.stringify(src.flat(Infinity)) + log.join("|")`,
		`var a=[1,[2,[3,[4]]]]; var r=a.flat(2); JSON.stringify(r) + Array.isArray(r[2])`,
		`var a=[1]; for(var i=0;i<900;i++) a=[a,[i]]; a.flat(Infinity).length`,
		`JSON.stringify([[1,[2]],[3]].flat(Infinity)) + JSON.stringify([].flat()) + JSON.stringify(Array.prototype.flat.call("ab"))`,
		`Object.prototype.get=function(){}; Object.prototype.set=function(){}; Object.prototype.value=3; Object.prototype.writable=false; JSON.stringify([1,[2,[3]],,4].flat(Infinity))`,
		`Object.prototype.has=function(){ return false }; Object.prototype.defineProperty=function(){ return false }; var a=[1,[2]]; a.constructor={}; a.constructor[Symbol.species]=function(){ return new Proxy([], {}) }; JSON.stringify(a.flat(Infinity))`,
		`var r=Proxy.revocable([], {}); r.revoke(); try { [r.proxy].flat() } catch (e) { e.name }`,
		// prototype setters
		`var o={}; var p={x:1}; Object.setPrototypeOf(o,p) === o && o.x`, `var o={}; o.__proto__={y:2}; o.y`,
		`var o={}; Reflect.setPrototypeOf(o,{z:3}) + ":" + o.z`, `var o={}; Object.setPrototypeOf(o,null); Object.getPrototypeOf(o)`,
		`var a={}; var b=Object.create(a); Object.setPrototypeOf(a,b)`, `var a={}; var b=Object.create(a); Reflect.setPrototypeOf(a,b)`,
		`var a={}; a.__proto__=a`, `Object.setPrototypeOf(1, {})`, `Object.setPrototypeOf(undefined, {})`, `Object.setPrototypeOf({}, 1)`,
		`Reflect.setPrototypeOf(1, {})`, `Object.setPrototypeOf(Object.preventExtensions({}), {})`,
		`var o={}; o.__proto__=5; Object.getPrototypeOf(o) === Object.prototype`,
		`var p=new Proxy({}, {}); Object.setPrototypeOf(p, {w:4}); p.w`,
		`var log=[]; var p=new Proxy({}, {setPrototypeOf:function(t,v){ log.push("trap"); return Reflect.setPrototypeOf(t,v) }}); Object.setPrototypeOf(p, {}); log.join()`,
		`function F(){}; F.prototype = new Proxy({}, {}); var o = new F(); o instanceof F`,
		// A setPrototypeOf trap handles the change: it runs, and goja's own
		// invariant checks apply to its answer.
		`var target={}; var calls=0; var proxy=new Proxy(target, {setPrototypeOf(){ calls++; return true }}); [Reflect.setPrototypeOf(proxy, target), calls].join()`,
		`var target={}; var calls=0; var proxy=new Proxy(target, {setPrototypeOf(){ calls++; return true }}); [Object.setPrototypeOf(proxy, target) === proxy, calls].join()`,
		`var target={}; var calls=0; var proxy=new Proxy(target, {setPrototypeOf(){ calls++; return true }}); proxy.__proto__=target; calls`,
		`var target={}; var proxy=new Proxy(target, {setPrototypeOf(){ return true }}); Object.setPrototypeOf(proxy, proxy) === proxy`,
		`var target={}; var proxy=new Proxy(target, {setPrototypeOf(){ return false }}); [Reflect.setPrototypeOf(proxy, target)].join()`,
		`var target={}; var proxy=new Proxy(target, {setPrototypeOf(){ return false }}); Object.setPrototypeOf(proxy, target)`,
		`var target=Object.preventExtensions({}); var proxy=new Proxy(target, {setPrototypeOf(){ return true }}); Object.setPrototypeOf(proxy, {})`,
		`var target={}; var proxy=new Proxy(target, {setPrototypeOf:5}); Object.setPrototypeOf(proxy, target)`,
		`var target={}; var proxy=new Proxy(target, {setPrototypeOf:null}); Reflect.setPrototypeOf(proxy, {}) + ":" + (Object.getPrototypeOf(target) !== Object.prototype)`,
		`var target={}; var proxy=new Proxy(target, Object.create({setPrototypeOf(){ return true }})); Reflect.setPrototypeOf(proxy, target)`,
		`var target={}; var calls=0; var inner=new Proxy(target, {setPrototypeOf(){ calls++; return true }}); var outer=new Proxy(inner, {}); [Reflect.setPrototypeOf(outer, target), calls].join()`,
		`var a={}; var b=Object.create(a); var p=new Proxy(a, {setPrototypeOf(t,v){ return Reflect.setPrototypeOf(t,v) }}); [Reflect.setPrototypeOf(p, b), Reflect.setPrototypeOf(p, {q:1}), p.q].join()`,
		`var r=Proxy.revocable({}, {}); r.revoke(); Object.setPrototypeOf(r.proxy, {})`, `var r=Proxy.revocable({}, {}); r.revoke(); Reflect.setPrototypeOf(r.proxy, {})`,
		`var t={}; var p=new Proxy(t, new Proxy({}, {})); [Reflect.setPrototypeOf(p, {q:1}), p.q].join()`,
		`var t={}; var p=new Proxy(t, {get setPrototypeOf(){ return undefined }}); [Reflect.setPrototypeOf(p, {q:2}), p.q].join()`,
		// proxies
		`var h={get:function(t,k,r){ return [this === h, k, r === p, t.v].join() }}; var p=new Proxy({v:7},h); p.q`,
		`var h=Object.freeze({get:function(t,k){ return k + "!" }}); new Proxy({}, h).abc`,
		`var p=new Proxy({a:1}, {}); p.a + ":" + ("a" in p) + ":" + Object.keys(p).join()`,
		`var p=new Proxy(function(a,b){ return a+b }, {apply:function(t,th,args){ return t.apply(th,args)*10 }}); p(1,2)`,
		`var p=new Proxy(function(){ this.x=1 }, {construct:function(t,args,nt){ return {made:args.length} }}); new p(1,2).made`,
		`var p=new Proxy({}, {get:5}); p.x`, `var p=new Proxy({}, {get:null}); p.x`,
		`var p=new Proxy({}, {get:function(){ throw new TypeError("trap boom") }}); p.x`,
		`var t={}; Object.defineProperty(t,"c",{value:1}); var p=new Proxy(t,{get:function(){ return 2 }}); p.c`,
		`var h={}; Object.defineProperty(h,"get",{get:function(){ return function(t,k){ return "via getter " + k } }}); new Proxy({},h).z`,
		`var base={get:function(t,k){ return "inherited " + k }}; new Proxy({}, Object.create(base)).m`,
		`var r=Proxy.revocable({a:1},{}); var v=r.proxy.a; r.revoke(); try { r.proxy.a } catch (e) { v + ":" + e.name }`,
		`new Proxy({}, new Proxy({}, {get:function(t,k){ return k === "get" ? function(){ return "meta" } : undefined }})).anything`,
		`Proxy({}, {})`, `new Proxy(1, {})`, `new Proxy({}, 1)`, `new Proxy({})`,
		`var calls=0; var h={has:function(t,k){ calls++; return k === "y" }}; var p=new Proxy({},h); ("y" in p) + ":" + ("x" in p) + ":" + calls`,
		`var p=new Proxy([1,2,3], {}); Array.isArray(p) + ":" + p.length + ":" + JSON.stringify(p)`,
		`var p=new Proxy({}, {ownKeys:function(){ return ["a","b"] }, getOwnPropertyDescriptor:function(t,k){ return {value:k, enumerable:true, configurable:true} }}); JSON.stringify(Object.keys(p))`,
	}
	run := func(vm *goja.Runtime, expr string) string {
		v, err := vm.RunString(`(function(){ try { var r = (function(){ return eval(` + strconv.Quote(expr) + `) })(); return typeof r + ":" + String(r) } catch (e) { return "throw:" + e.name + ":" + e.message } })()`)
		if err != nil {
			return "go error: " + err.Error()
		}
		return v.String()
	}
	for _, expr := range exprs {
		plain := goja.New()
		guarded := goja.New()
		if _, err := installStackGuard(guarded); err != nil {
			t.Fatal(err)
		}
		want, got := run(plain, expr), run(guarded, expr)
		if got != want {
			t.Errorf("%s\n got: %s\nwant: %s", expr, got, want)
		}
	}
}

// TestStackGuardExportMatchesGoja: the bounded export gives goja's Export
// value for every kind of value a script can hand back.
func TestStackGuardExportMatchesGoja(t *testing.T) {
	exprs := []string{
		`1`, `1.5`, `"s"`, `true`, `null`, `undefined`, `10n`, `Symbol("x")`, `NaN`,
		`({a:1, b:"x", c:[1,2,{d:null}], e:undefined, f:{g:{h:[]}}})`,
		`[1,,3]`, `var a=[1,2]; a[10]=5; a`, `var a=[]; a.length=3; a`, `var a=[1]; Object.defineProperty(a,1,{get:function(){ return {x:2} }, enumerable:true}); a`,
		`var o={}; Object.defineProperty(o,"hidden",{value:1, enumerable:false}); o.shown=2; o[Symbol("s")]=3; o`,
		`({get g(){ return [1,{y:2}] }})`, `Object.create({inherited:1}, {own:{value:2, enumerable:true}})`,
		`Object.create(null)`, `(function(){ return arguments })(1, {a:2}, [3])`,
		`new Map([[1,"a"],["k",{v:[1]}],[{o:1},2]])`, `new Set([1,"a",{b:2},[3]])`, `new Map()`, `new Set()`,
		`new Date(0)`, `new Number(3)`, `new String("ab")`, `var s=new String("ab"); s.extra={x:1}; s`, `new Boolean(false)`, `Object(1n)`,
		`/re/g`, `var r=/re/; r.extra=[1]; r`, `new Error("m")`, `var e=new TypeError("m"); e.code={c:1}; e`,
		`new Uint8Array([1,2])`, `new Float64Array([1.5])`,
		`Math`, `JSON`, `var shared={s:1}; ({a:shared, b:[shared, shared]})`,
		`class K { constructor(){ this.p = {q:1} } }; new K()`,
		`[[1,[2,[3,[4]]]], {deep:{deeper:{deepest:[{}]}}}]`,
		`var o={v:1}; for(var i=0;i<1000;i++) o={n:o}; o`, `var a=[1]; for(var i=0;i<1000;i++) a=[a]; a`,
		// Keys are looked up as the JS strings they are, lone surrogates included.
		`({["\ud800"]:7})`, `({"\ud800":1, "\ufffd":2})`, `({a:{["x\udc00"]:[1]}})`,
		`var a=[{}]; for(var i=1;i<1000;i++) a.push({n:a[i-1]}); a`,
	}
	for _, expr := range exprs {
		plain := goja.New()
		guarded := goja.New()
		g, err := installStackGuard(guarded)
		if err != nil {
			t.Fatal(err)
		}
		pv, err1 := plain.RunString(expr)
		gv, err2 := guarded.RunString(expr)
		if err1 != nil || err2 != nil {
			t.Fatalf("%s: %v / %v", expr, err1, err2)
		}
		want, got := pv.Export(), g.export(gv)
		if !reflect.DeepEqual(normalizeExport(got), normalizeExport(want)) {
			t.Errorf("%s\n got: %#v\nwant: %#v", expr, got, want)
		}
	}
	// Shared references stay shared, as goja's export context keeps them.
	vm := goja.New()
	g, err := installStackGuard(vm)
	if err != nil {
		t.Fatal(err)
	}
	v, err := vm.RunString(`var s={x:1}; ({a:s, b:s})`)
	if err != nil {
		t.Fatal(err)
	}
	m := g.export(v).(map[string]interface{})
	if reflect.ValueOf(m["a"]).Pointer() != reflect.ValueOf(m["b"]).Pointer() {
		t.Error("a shared object exported to two Go values")
	}

	// Reusing an exported object counts its whole height: the longest path
	// through the exported Go graph stays within the budget.
	for _, expr := range []string{
		`var a=[{}]; for(var i=1;i<3000;i++) a.push({n:a[i-1]}); a`,
		`var a=[{}]; for(var i=1;i<3000;i++) a.push({n:a[i-1]}); ({b:a, a:a[a.length-1]})`,
		`var a=[new Map()]; for(var i=1;i<3000;i++) a.push(new Map([[1,a[i-1]]])); a`,
		`var a=[new Set()]; for(var i=1;i<3000;i++) a.push(new Set([a[i-1]])); a`,
		`var a=[[]]; for(var i=1;i<3000;i++) a.push([a[i-1], a[i-1]]); a`,
	} {
		v, err := vm.RunString(expr)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := g.tryExport(v, false); ok {
			t.Errorf("%s: exported a graph deeper than the budget", expr)
		}
		if _, ok := g.tryExport(v, true); ok {
			t.Errorf("%s: exported tool arguments deeper than the budget", expr)
		}
	}
}

// normalizeExport replaces what DeepEqual cannot compare (a Symbol is a
// pointer per VM, NaN is not equal to itself) with comparable stand-ins.
func normalizeExport(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, x := range t {
			out[k] = normalizeExport(x)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, x := range t {
			out[i] = normalizeExport(x)
		}
		return out
	case [][2]interface{}:
		out := make([][2]interface{}, len(t))
		for i, x := range t {
			out[i] = [2]interface{}{normalizeExport(x[0]), normalizeExport(x[1])}
		}
		return out
	case *goja.Symbol:
		return "symbol:" + t.String()
	case float64:
		if t != t {
			return "NaN"
		}
	}
	return v
}

// TestStackGuardToJS: Go data reaches the script as plain JS objects and
// arrays with the same content.
func TestStackGuardToJS(t *testing.T) {
	caller := newMockToolCaller()
	caller.results["s:t"] = map[string]interface{}{"items": []interface{}{1.0, "two", map[string]interface{}{"three": 3.5}}, "__proto__": "data"}
	input := map[string]interface{}{"a": map[string]interface{}{"b": []interface{}{true, nil}}, "n": 2.0}
	code := `var r = call_tool("s","t",{});
		var b = call_tools([{server:"s",tool:"t"}]);
		input.added = {x:1};
		[typeof input.a, input.a.b.length, input.a.b[0], input.a.b[1] === null, input.n, Object.keys(input).join(),
		 r.ok, r.result.items[2].three, Object.getPrototypeOf(r.result) === Object.prototype, r.result.__proto__,
		 Array.isArray(b), b[0].ok, b[0].result.items[1], JSON.stringify(input)].join("|")`
	result := Execute(context.Background(), caller, code, ExecutionOptions{TimeoutMs: 30000, Input: input})
	if !result.Ok {
		t.Fatalf("expected success, got %+v", result.Error)
	}
	want := `object|2|true|true|2|a,n,added|true|3.5|true|data|true|true|two|{"a":{"b":[true,null]},"n":2,"added":{"x":1}}`
	if result.Value != want {
		t.Fatalf("got  %v\nwant %v", result.Value, want)
	}
}

// BenchmarkExecuteTrivial is the fixed cost of one execution (VM, sandbox,
// guard installation).
func BenchmarkExecuteTrivial(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if r := Execute(context.Background(), newMockToolCaller(), "1+1", ExecutionOptions{TimeoutMs: 30000}); !r.Ok {
			b.Fatal(r.Error)
		}
	}
}

// BenchmarkExecuteStringify600KB serializes about 600 KB of nested objects
// and arrays through the depth-tracking JSON.stringify.
func BenchmarkExecuteStringify600KB(b *testing.B) {
	code := `var rows=[]; for(var i=0;i<7500;i++) rows.push({id:i, name:"row"+i, tags:["a","b"], meta:{x:i, y:[i,i+1]}});
		JSON.stringify({rows:rows}).length`
	for i := 0; i < b.N; i++ {
		if r := Execute(context.Background(), newMockToolCaller(), code, ExecutionOptions{TimeoutMs: 60000}); !r.Ok {
			b.Fatal(r.Error)
		}
	}
}

// BenchmarkGuardedStringify600KB times JSON.stringify alone, about 600 KB of
// nested objects and arrays, on a guarded VM.
func BenchmarkGuardedStringify600KB(b *testing.B) {
	vm := goja.New()
	if _, err := installStackGuard(vm); err != nil {
		b.Fatal(err)
	}
	if _, err := vm.RunString(`var rows=[]; for(var i=0;i<7500;i++) rows.push({id:i, name:"row"+i, tags:["a","b"], meta:{x:i, y:[i,i+1]}}); var data={rows:rows}`); err != nil {
		b.Fatal(err)
	}
	prog := goja.MustCompile("bench", `JSON.stringify(data).length`, false)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v, err := vm.RunProgram(prog)
		if err != nil || v.ToInteger() < 500_000 {
			b.Fatal(v, err)
		}
	}
}

// TestBigIntDigitsLowerBound: the charge for a BigInt never exceeds the
// length json.Marshal writes, and stays within a digit of it.
func TestBigIntDigitsLowerBound(t *testing.T) {
	for _, bits := range []uint{0, 1, 2, 3, 4, 9, 10, 63, 64, 100, 1000, 3321, 3322, 32768, 100000} {
		for _, neg := range []bool{false, true} {
			for _, delta := range []int64{-1, 0, 1} {
				x := new(big.Int).Lsh(big.NewInt(1), bits)
				x.Add(x, big.NewInt(delta))
				if neg {
					x.Neg(x)
				}
				b, err := json.Marshal(x)
				if err != nil {
					t.Fatal(err)
				}
				got, want := bigIntDigits(x), int64(len(b))
				if got > want || got < want-1 {
					t.Errorf("bigIntDigits(%d bits, neg=%v, %+d) = %d, marshaled %d bytes", bits, neg, delta, got, want)
				}
			}
		}
	}
}

// TestGoDataReachesScriptsAsPlainObjects: whatever Go types the input and a
// tool result hold, the script gets plain JS objects and arrays (no
// Go-backed object), so a JSON.stringify property list applies to them as
// to any other object.
func TestGoDataReachesScriptsAsPlainObjects(t *testing.T) {
	type pair struct{ A, B int }
	caller := newMockToolCaller()
	caller.results["s:t"] = map[string]interface{}{"a": 1.0, "b": 2.0, "m": map[string]string{"a": "1", "b": "2"}, "st": pair{1, 2}}
	input := map[string]interface{}{
		"a": 1.0, "b": 2.0,
		"m":   map[string]string{"a": "1", "b": "2"},
		"mm":  map[string]map[string]int{"x": {"a": 1, "b": 2}},
		"s":   []string{"x", "y"},
		"st":  pair{1, 2},
		"pst": &pair{3, 4},
		"n":   nil,
	}
	code := `var r = call_tool("s","t",{}).result; var b = call_tools([{server:"s",tool:"t"}])[0].result;
		[JSON.stringify(input, ["a"]), JSON.stringify(input.m, ["a"]), JSON.stringify(input.mm, ["x","a"]),
		 JSON.stringify(input.st, ["A"]), JSON.stringify(input.pst, ["B"]), JSON.stringify(input.s), Array.isArray(input.s),
		 JSON.stringify(r, ["a"]), JSON.stringify(r.m, ["b"]), JSON.stringify(r.st, ["A"]), JSON.stringify(b.m, ["a"]),
		 (input.m.c = 1, JSON.stringify(input.m)), input.n === null].join(" | ")`
	result := Execute(context.Background(), caller, code, ExecutionOptions{TimeoutMs: 10000, Input: input})
	if !result.Ok {
		t.Fatalf("unexpected error: %+v", result.Error)
	}
	want := `{"a":1} | {"a":"1"} | {"x":{"a":1}} | {"A":1} | {"B":4} | ["x","y"] | true | {"a":1} | {"b":"2"} | {"A":1} | {"a":"1"} | {"a":"1","b":"2","c":1} | true`
	if result.Value != want {
		t.Fatalf("got  %v\nwant %v", result.Value, want)
	}
}

// TestSourceGuardMatchesGoja: the Function-like constructors keep goja's
// order of observable steps (newTarget.prototype before the arguments are
// converted; conversion stops at a Symbol), results and errors.
func TestSourceGuardMatchesGoja(t *testing.T) {
	exprs := []string{
		`Function(Symbol(), {toString(){ throw new Error("unexpected conversion") }})`,
		`var log=[]; try { Function({toString(){ log.push("a"); return "a" }}, Symbol(), {toString(){ log.push("body"); return "" }}) } catch (e) { log.push(e.name) } log.join()`,
		`var log=[]; try { new Function({toString(){ log.push("a"); return "a" }}, Symbol()) } catch (e) { log.push(e.name) } log.join()`,
		`Function("a", Symbol())`, `Function(Symbol())`, `typeof Function()`, `Function("a", "b", "return a+b")(2, 3)`,
		`var log=[]; Function({toString(){ log.push("a"); return "a" }}, {toString(){ log.push("b"); return "b" }}, {toString(){ log.push("body"); return "" }}); log.join()`,
		`var log=[]; var nt=new Proxy(function(){}, {get(t,k){ log.push("get " + String(k)); return t[k] }}); Reflect.construct(Function, [{toString(){ log.push("body"); return "return 1" }}], nt); log.join()`,
		`var custom={c:1}; var nt=new Proxy(function(){}, {get(t,k){ return k === "prototype" ? custom : t[k] }}); var f=Reflect.construct(Function, ["return 2"], nt); [Object.getPrototypeOf(f) === custom, f(), f.c].join()`,
		`var nt=new Proxy(function(){}, {get(t,k){ return k === "prototype" ? 5 : t[k] }}); Object.getPrototypeOf(Reflect.construct(Function, ["return 2"], nt)) === Function.prototype`,
		`var nt=new Proxy(function(){}, {get(t,k){ if (k === "prototype") throw new RangeError("proto"); return t[k] }}); var n=0; try { Reflect.construct(Function, [{toString(){ n++; return "" }}], nt) } catch (e) { e.message + n }`,
		`class F extends Function { constructor(){ super("return 7") } }; var f=new F(); [f instanceof F, f()].join()`,
		`var GF=Object.getPrototypeOf(function*(){}).constructor; var custom=Object.create(GF.prototype); var nt=new Proxy(function(){}, {get(t,k){ return k === "prototype" ? custom : t[k] }}); Object.getPrototypeOf(Reflect.construct(GF, ["yield 1"], nt)) === custom`,
		`var AF=Object.getPrototypeOf(async function(){}).constructor; Object.getPrototypeOf(new AF("return 1")) === AF.prototype`,
		`Object.getPrototypeOf(Reflect.construct(Function, [""], Function)) === Function.prototype`,
		`Object.getPrototypeOf(new Function("")) === Function.prototype`,
		// Legitimate parameters and bodies pass the shape check unchanged.
		`Function("a = 1", "{b, c} = {}", "...rest", "return [a, b, c, rest.length].join()")(undefined, {b:2, c:3}, 4, 5)`,
		`Function("'use strict'; return typeof this")()`, `Function("return 1 // trailing comment")()`, `Function("return 2 /* open")`,
		`Function("a /* x */", "b // y", "return a + b")(1, 2)`, `Function("return '})' + \"})\" + /}\\)/.source + ` + "`${'})'}`" + `")()`,
		`Function("function inner(){ return {a:1} } return inner().a")()`, `Function("return {a:1}")().a`, `Function("")()`, `Function()()`,
		`Function("a", "b")(1)`, `Function("return arguments.length")(1, 2, 3)`, `Function("<!-- html comment\nreturn 3")()`,
		`var GF=Object.getPrototypeOf(function*(){}).constructor; GF("a", "yield a; yield* [2, 3]")(1).next().value`,
		`var AF=Object.getPrototypeOf(async function(){}).constructor; AF("return await 1")() instanceof Promise`,
		`Function("return 1;;")()`, `Function("return 1\n;")()`, `Function("}")`, `Function("(")`, `Function("a)", "")`, `Function("a) {", "")`,
		`Function("\ud800")`, `Function("return '\ud800'.length")()`, `Function("a", "/*", "*/ return 1")`,
		`Function("a = function(){ return 1 }", "return a()")()`,
	}
	run := func(vm *goja.Runtime, expr string) string {
		v, err := vm.RunString(`(function(){ try { var r = (function(){ return eval(` + strconv.Quote(expr) + `) })(); return typeof r + ":" + String(r) } catch (e) { return "throw:" + e.name + ":" + e.message } })()`)
		if err != nil {
			return "go error: " + err.Error()
		}
		return v.String()
	}
	for _, expr := range exprs {
		plain := goja.New()
		guarded := goja.New()
		if _, err := installStackGuard(guarded); err != nil {
			t.Fatal(err)
		}
		want, got := run(plain, expr), run(guarded, expr)
		if got != want {
			t.Errorf("%s\n got: %s\nwant: %s", expr, got, want)
		}
	}
	// Arguments that close the function expression are a SyntaxError (as
	// in the spec), where goja would run what follows.
	for _, expr := range []string{
		`Function("}); (function(){")`, `Function("});(function(){")`, `Function("x", "}).call(0, function(){")`,
		`Function("a) { return 1 }, function(b", "")`, `new Function("}); 1; (function(){")`,
		`var GF=Object.getPrototypeOf(function*(){}).constructor; GF("}); (function*(){")`,
		`var AF=Object.getPrototypeOf(async function(){}).constructor; AF("}); (async function(){")`,
		`class X extends Function { constructor(b){ super(b) } } new X("}); (function(){")`,
	} {
		guarded := goja.New()
		if _, err := installStackGuard(guarded); err != nil {
			t.Fatal(err)
		}
		if got, want := run(guarded, expr), "throw:SyntaxError:"+functionShapeMessage; got != want {
			t.Errorf("%s\n got: %s\nwant: %s", expr, got, want)
		}
	}
}
