package jsruntime

import (
	"context"
	"encoding/json"
	"fmt"
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
	stackChildMarker = "STACK_GUARD_RESULT "
)

// TestStackGuardChild is the child half of TestStackGuardVectors.
func TestStackGuardChild(t *testing.T) {
	code := os.Getenv(stackChildEnv)
	if code == "" {
		t.Skip("child process only")
	}
	budget, _ := strconv.ParseInt(os.Getenv(stackChildBudgetEnv), 10, 64)
	if os.Getenv(stackChildGenEnv) != "" {
		v, err := goja.New().RunString(code)
		if err != nil {
			t.Fatalf("generate source: %v", err)
		}
		code = v.String()
	}
	result := Execute(context.Background(), marshalingToolCaller{newMockToolCaller()}, code, ExecutionOptions{
		TimeoutMs: 60000, MaxMemoryBytes: budget, Language: os.Getenv(stackChildLangEnv),
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
	{name: "JSON.parse re-entered from the reviver", code: `var t="[".repeat(1500)+"]".repeat(1500); function r(k,v){ return JSON.parse(t, r) } JSON.parse(t, r)`},
	{name: "proxy cycle setPrototypeOf(p, proxy of p)", code: `var p=new Proxy({},{}); var q=new Proxy(p,{}); Object.setPrototypeOf(p,q); p.x`, want: cyclicProtoMessage},
	{name: "proxy cycle handler prototype", code: `var h={}; var p=new Proxy({},h); Object.setPrototypeOf(h,p); p.x`, want: cyclicProtoMessage},
	{name: "proxy cycle handler __proto__", code: `var h={}; var p=new Proxy({},h); h.__proto__=p; p.x`, want: cyclicProtoMessage},
	{name: "proxy cycle target is handler", code: `var o={}; var p=new Proxy(o,o); Object.setPrototypeOf(o,p); p.x`, want: cyclicProtoMessage},
	{name: "proxy cycle through Object.create", code: `var p=new Proxy({},{}); var o=Object.create(p); Object.setPrototypeOf(p,o); o.x`, want: cyclicProtoMessage},
	{name: "proxy cycle Reflect.setPrototypeOf", code: `var p=new Proxy({},{}); var o=Object.create(p); Reflect.setPrototypeOf(p,o); 'x' in o`, want: "ok"},
	{name: "proxy cycle via revocable", code: `var r=Proxy.revocable({},{}); var o=Object.create(r.proxy); Object.setPrototypeOf(r.proxy,o); o.x`, want: cyclicProtoMessage},
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
	{name: "deep Map chain as result", code: `var m=new Map(); for(var i=0;i<1000000;i++) m=new Map([[1,m]]); m`},
	{name: "deep arrays as call_tool args", code: `var a=[]; for(var i=0;i<1500000;i++) a=[a]; call_tool("s","t",{a:a})`},
	// An acyclic graph that reuses exported objects: each one is exported
	// once, but the longest path through the Go graph is the chain's length.
	// "b" is exported first (JS order) and builds the chain one level at a
	// time; json.Marshal then walks "a" (sorted first), the whole chain.
	{name: "shared chain as result", code: `var a=[{}]; for(var i=1;i<2000000;i++) a.push({n:a[i-1]}); ({b:a, a:a[a.length-1]})`},
	{name: "shared chain as call_tool args", code: `var a=[{}]; for(var i=1;i<2000000;i++) a.push({n:a[i-1]}); call_tool("s","t",{b:a, a:a[a.length-1]})`},
	{name: "shared chain in call_tools args", code: `var a=[{}]; for(var i=1;i<2000000;i++) a.push({n:a[i-1]}); call_tools([{server:"s",tool:"t",args:{b:a, a:a[a.length-1]}}])`},
	{name: "shared Map chain as result", code: `var a=[new Map()]; for(var i=1;i<1000000;i++) a.push(new Map([[1,a[i-1]]])); ({b:a, a:a[a.length-1]})`},
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
		result *Result
		output string
		err    error
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
			if v.heapBound {
				budget = 0
			}
			r, out, err := runStackChild(v, budget)
			outcomes[i] = outcome{r, out, err}
		}(i, v)
	}
	wg.Wait()

	for i, v := range stackVectors {
		o := outcomes[i]
		t.Run(v.name, func(t *testing.T) {
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
	// A builtin replaced before the guard is installed is found by the walk.
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
