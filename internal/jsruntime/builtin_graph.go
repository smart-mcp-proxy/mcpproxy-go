package jsruntime

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/dop251/goja"
)

// Finding the builtins to instrument (installStackGuard) means walking the
// whole object graph of a fresh VM: every object reachable from the roots
// through prototypes and own properties. Interpreted in the VM that walk is
// most of what installing the guard costs (about 1.6 ms of 1.9 ms per
// execution, far more under the race detector).
//
// A fresh VM's graph is the same every time: goja builds it the same way and
// no script has run. So the walk runs once, on a template VM, and records how
// each object was first reached (a spanning tree of the graph: from which
// object, through which step). Every later VM follows the same steps from its
// own roots with plain Go lookups. If anything does not match on the way (a
// step that leads nowhere or to an object already reached, a function that
// is not one, an object of another implementation kind, a global object with
// other keys), the VM is walked in full instead. installStackGuard runs on a
// fresh VM, which always matches; the checks catch a VM changed before it.

// collectGraphScript walks the object graph from the roots breadth first and
// returns, for every object, the object it was first reached from, the step
// ("r" root, "p" prototype, "v" property value, "g" getter, "s" setter), the
// root index or property key, and whether it is a function. It runs before
// any script code, so nothing it calls can have been tampered with.
const collectGraphScript = `(function (roots) {
	var seen = new Set(), objs = [], parent = [], step = [], key = [], fn = [];
	function add(v, p, s, k) {
		if (v === null || (typeof v !== "object" && typeof v !== "function") || seen.has(v)) return;
		seen.add(v);
		objs.push(v); parent.push(p); step.push(s); key.push(k); fn.push(typeof v === "function");
	}
	for (var r = 0; r < roots.length; r++) add(roots[r], -1, "r", r);
	for (var i = 0; i < objs.length; i++) {
		var o = objs[i];
		add(Object.getPrototypeOf(o), i, "p", null);
		var keys = Reflect.ownKeys(o);
		for (var j = 0; j < keys.length; j++) {
			var d = Object.getOwnPropertyDescriptor(o, keys[j]);
			if (!d) continue;
			if ("value" in d) { add(d.value, i, "v", keys[j]); } else { add(d.get, i, "g", keys[j]); add(d.set, i, "s", keys[j]); }
		}
	}
	return [objs, parent, step, key, fn];
})`

var collectGraphProgram = goja.MustCompile("collect-builtins", collectGraphScript, true)

// graphStep is how one object of the template graph was first reached.
type graphStep struct {
	parent int
	step   byte // 'r', 'p', 'v', 'g', 's'
	root   int
	name   string       // a string key
	sym    *goja.Symbol // or a well-known symbol key
	fn     bool
	kind   string // objectKind
}

type builtinGraph struct {
	steps   []graphStep
	globals []string // the global object's own string keys, in order
}

// wellKnownSymbols are shared by every goja runtime, so a step keyed by one
// can be followed in another VM.
var wellKnownSymbols = []*goja.Symbol{
	goja.SymHasInstance, goja.SymIsConcatSpreadable, goja.SymIterator, goja.SymMatch, goja.SymMatchAll,
	goja.SymReplace, goja.SymSearch, goja.SymSpecies, goja.SymSplit, goja.SymToPrimitive,
	goja.SymToStringTag, goja.SymUnscopables,
}

// builtinTemplate is the graph of a fresh sandboxed VM, or nil when it cannot
// be recorded in a form other VMs can follow (every VM is then walked).
var builtinTemplate = sync.OnceValue(func() *builtinGraph {
	vm := goja.New()
	setupSandbox(vm)
	walk, err := walkBuiltins(vm, builtinRootValues(vm))
	if err != nil {
		return nil
	}
	t := &builtinGraph{steps: make([]graphStep, len(walk.objs)), globals: vm.GlobalObject().GetOwnPropertyNames()}
	for i := range walk.objs {
		st := graphStep{parent: walk.parent[i], fn: walk.fn[i], kind: objectKind(walk.objs[i])}
		if len(walk.step[i]) != 1 {
			return nil
		}
		st.step = walk.step[i][0]
		key := walk.key[i]
		switch st.step {
		case 'r':
			st.root = int(key.ToInteger())
		case 'p':
		case 'v', 'g', 's':
			switch k := key.(type) {
			case *goja.Symbol:
				for _, w := range wellKnownSymbols {
					if k == w {
						st.sym = w
					}
				}
				if st.sym == nil {
					return nil
				}
			case goja.String:
				st.name = k.String()
				if strings.ContainsRune(st.name, utf8.RuneError) {
					return nil // not a key a Go string can name
				}
			default:
				return nil
			}
		default:
			return nil
		}
		if st.step != 'r' && (st.parent < 0 || st.parent >= i) {
			return nil
		}
		t.steps[i] = st
	}
	return t
})

// builtinWalk is collectGraphScript's answer.
type builtinWalk struct {
	objs   []*goja.Object
	parent []int
	step   []string
	key    []goja.Value
	fn     []bool
}

func builtinRootValues(vm *goja.Runtime, extraRoots ...goja.Value) []goja.Value {
	roots := make([]goja.Value, 0, len(builtinRootPrograms)+len(extraRoots))
	for _, p := range builtinRootPrograms {
		if v, err := vm.RunProgram(p); err == nil {
			roots = append(roots, v)
		}
	}
	return append(roots, extraRoots...)
}

func walkBuiltins(vm *goja.Runtime, roots []goja.Value) (*builtinWalk, error) {
	collectValue, err := vm.RunProgram(collectGraphProgram)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errStackGuardSetup, err)
	}
	collect, ok := goja.AssertFunction(collectValue)
	if !ok {
		return nil, errStackGuardSetup
	}
	rootList := make([]interface{}, len(roots))
	for i, r := range roots {
		rootList[i] = r
	}
	res, err := collect(goja.Undefined(), vm.ToValue(rootList))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errStackGuardSetup, err)
	}
	parts, ok := res.(*goja.Object)
	if !ok {
		return nil, errStackGuardSetup
	}
	w := &builtinWalk{}
	if err := vm.ExportTo(parts.Get("0"), &w.objs); err != nil {
		return nil, fmt.Errorf("%w: %v", errStackGuardSetup, err)
	}
	if err := vm.ExportTo(parts.Get("1"), &w.parent); err != nil {
		return nil, fmt.Errorf("%w: %v", errStackGuardSetup, err)
	}
	if err := vm.ExportTo(parts.Get("2"), &w.step); err != nil {
		return nil, fmt.Errorf("%w: %v", errStackGuardSetup, err)
	}
	if err := vm.ExportTo(parts.Get("4"), &w.fn); err != nil {
		return nil, fmt.Errorf("%w: %v", errStackGuardSetup, err)
	}
	keys, ok := parts.Get("3").(*goja.Object)
	if !ok {
		return nil, errStackGuardSetup
	}
	w.key = make([]goja.Value, len(w.objs))
	for i := range w.key {
		w.key[i] = keys.Get(strconv.Itoa(i))
	}
	if len(w.parent) != len(w.objs) || len(w.step) != len(w.objs) || len(w.fn) != len(w.objs) {
		return nil, errStackGuardSetup
	}
	return w, nil
}

// collectBuiltins returns every function object reachable from builtinRoots
// (and extraRoots) by walking the graph.
func collectBuiltins(vm *goja.Runtime, extraRoots ...goja.Value) ([]*goja.Object, error) {
	w, err := walkBuiltins(vm, builtinRootValues(vm, extraRoots...))
	if err != nil {
		return nil, err
	}
	var funcs []*goja.Object
	for i, o := range w.objs {
		if w.fn[i] {
			funcs = append(funcs, o)
		}
	}
	return funcs, nil
}

// builtinFunctions returns every function object reachable from builtinRoots
// in vm, which must be fresh: through the template when vm's graph follows
// it, by walking vm's graph otherwise.
func builtinFunctions(vm *goja.Runtime) ([]*goja.Object, error) {
	if t := builtinTemplate(); t != nil {
		if funcs, ok := t.follow(vm); ok {
			return funcs, nil
		}
	}
	return collectBuiltins(vm)
}

// follow resolves the template's steps in vm and returns its functions, or
// ok=false when vm's graph does not match the template.
func (t *builtinGraph) follow(vm *goja.Runtime) (funcs []*goja.Object, ok bool) {
	defer func() {
		if r := recover(); r != nil { // a lookup that threw: not the template's graph
			funcs, ok = nil, false
		}
	}()
	globals := vm.GlobalObject().GetOwnPropertyNames()
	if len(globals) != len(t.globals) {
		return nil, false
	}
	for i, k := range globals {
		if k != t.globals[i] {
			return nil, false
		}
	}
	gopdFn, _, found := nativeFuncFields(vm.Get("Object").ToObject(vm).Get("getOwnPropertyDescriptor").ToObject(vm))
	if !found || *gopdFn == nil {
		return nil, false
	}
	gopd := *gopdFn
	roots := builtinRootValues(vm)
	objs := make([]*goja.Object, len(t.steps))
	seen := make(map[*goja.Object]struct{}, len(t.steps))
	for i, st := range t.steps {
		var v goja.Value
		switch st.step {
		case 'r':
			if st.root < 0 || st.root >= len(roots) {
				return nil, false
			}
			v = roots[st.root]
		case 'p':
			if p := objs[st.parent].Prototype(); p != nil {
				v = p
			}
		case 'v':
			if st.sym != nil {
				v = objs[st.parent].GetSymbol(st.sym)
			} else {
				v = objs[st.parent].Get(st.name)
			}
		case 'g', 's':
			var key goja.Value = st.sym
			if st.sym == nil {
				key = vm.ToValue(st.name)
			}
			d, isObj := gopd(goja.FunctionCall{Arguments: []goja.Value{objs[st.parent], key}}).(*goja.Object)
			if !isObj {
				return nil, false
			}
			if st.step == 'g' {
				v = d.Get("get")
			} else {
				v = d.Get("set")
			}
		}
		o, isObj := v.(*goja.Object)
		if !isObj {
			return nil, false
		}
		if _, dup := seen[o]; dup {
			return nil, false
		}
		seen[o] = struct{}{}
		// Same kind of object, so a builtin replaced before the guard was
		// installed (by a function of another kind) is noticed.
		if objectKind(o) != st.kind {
			return nil, false
		}
		objs[i] = o
		if st.fn {
			funcs = append(funcs, o)
		}
	}
	return funcs, true
}
