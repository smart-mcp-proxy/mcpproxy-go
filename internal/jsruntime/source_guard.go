package jsruntime

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dop251/goja"
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
)

// Source-size limit for everything code_execution compiles.
//
// goja's parser and compiler (and esbuild, for TypeScript) recurse on the Go
// stack once per level of syntactic nesting: "[[[...]]]", "((...))", a chain
// "1+1+...", "a?b:a?b:...", "!!!...", "a.b.b...", "f()()...". Compilation
// happens before any instruction runs, so neither vm.Interrupt nor the native
// depth guard (stack_guard.go) can stop it, and a few megabytes of source
// overflow the 1 GB Go stack, which is fatal to the whole process. That
// source can be the script itself, or a string the script builds and hands to
// eval, Function, AsyncFunction, GeneratorFunction or AsyncGeneratorFunction.
//
// The limit does not try to tokenize JavaScript (a lexer that disagreed with
// goja about where a regex literal or a string ends could be steered into
// undercounting). It relies on a property that holds however the source is
// read: every level of nesting consumes at least one operator token, that is
// an ASCII punctuation character or one of the keyword operators below. The
// count of those, anywhere in the source (strings and comments included), is
// therefore an upper bound on the nesting depth. Measured on darwin/arm64 the
// costliest construct takes about 1.7 KB of Go stack per such token
// (parenthesized nesting), so maxSourceTokens keeps the worst case under
// about 200 MB. An ordinary script has a few thousand; the limit is reached
// at roughly 250-400 KB of source.
const maxSourceTokens = 100_000

const sourceTooDeepMessage = "source is too large or nested too deeply to compile"

// sourceKeywordOperators are the words that open a nesting level without any
// punctuation of their own ("typeof typeof x", "new new F", "a in a in a",
// "if(1);else if(1);...", "do do ...").
var sourceKeywordOperators = map[string]struct{}{
	"typeof": {}, "void": {}, "delete": {}, "await": {}, "yield": {}, "new": {},
	"in": {}, "instanceof": {}, "else": {}, "do": {},
}

// sourceOperatorTokens counts the operator tokens in src, stopping once the
// count exceeds limit.
func sourceOperatorTokens(src string, limit int) int {
	return countSourceTokens(src, limit, false)
}

// typeScriptSourceTokens counts the tokens of TypeScript source: every ASCII
// punctuation character and every word. TypeScript adds operators that nest
// with no punctuation at all and that esbuild parses recursively ("keyof
// keyof T", "readonly readonly T[]", a type predicate "a is a is ...",
// "infer U extends infer U extends ..."); rather than keep a list of them,
// every word counts, so the count bounds the nesting whatever words the
// grammar grows.
//
// A word is a run of ASCII word bytes and non-ASCII letters, digits and
// marks; every other non-ASCII rune (a space or line separator such as
// U+00A0, U+2028 or U+3000, U+FEFF, punctuation, symbols, format characters,
// invalid UTF-8) is a token of its own and ends a word. Everything esbuild
// reads as whitespace between two keywords is such a rune, so separating
// "keyof keyof ..." with one cannot make the words merge; a run of letters
// is at most one identifier to esbuild (or a syntax error).
func typeScriptSourceTokens(src string, limit int) int {
	return countSourceTokens(src, limit, true)
}

func countSourceTokens(src string, limit int, everyWord bool) int {
	n := 0
	for i := 0; i < len(src) && n <= limit; {
		c := src[i]
		switch {
		case isSourceWordByte(c) || everyWord && c >= utf8.RuneSelf && isWordRune(src[i:]):
			j := i + 1
			if c >= utf8.RuneSelf {
				_, size := utf8.DecodeRuneInString(src[i:])
				j = i + size
			}
			for j < len(src) {
				if isSourceWordByte(src[j]) {
					j++
					continue
				}
				if everyWord && src[j] >= utf8.RuneSelf && isWordRune(src[j:]) {
					_, size := utf8.DecodeRuneInString(src[j:])
					j += size
					continue
				}
				break
			}
			if everyWord {
				n++
			} else if _, op := sourceKeywordOperators[src[i:j]]; op {
				n++
			}
			i = j
		case everyWord && c >= utf8.RuneSelf: // any other non-ASCII rune
			_, size := utf8.DecodeRuneInString(src[i:])
			n++
			i += size
		case c > ' ' && c < 0x7f: // ASCII punctuation (word bytes handled above)
			n++
			i++
		default: // whitespace, control and (JavaScript) non-ASCII bytes
			i++
		}
	}
	return n
}

// isWordRune reports whether the rune s starts with is a non-ASCII letter,
// digit or mark.
func isWordRune(s string) bool {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && size <= 1 {
		return false
	}
	return unicode.In(r, unicode.L, unicode.Nd, unicode.Nl, unicode.Mn, unicode.Mc)
}

func isSourceWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '$'
}

func sourceTooDeepError() error {
	return fmt.Errorf("%s (more than %d operators and brackets)", sourceTooDeepMessage, maxSourceTokens)
}

// checkSourceSize reports a source too large to hand to the parser safely.
func checkSourceSize(src string) error {
	if sourceOperatorTokens(src, maxSourceTokens) > maxSourceTokens {
		return sourceTooDeepError()
	}
	return nil
}

// sourceTooLargeResult is the Execute result for a script refused by
// checkSourceSize before it was compiled.
func sourceTooLargeResult(err error) *Result {
	return NewErrorResult(NewJsError(ErrorCodeSyntaxError, "RangeError: "+err.Error()))
}

// functionCtorRoots evaluate to the constructors that compile their
// arguments; a root goja cannot evaluate (unsupported syntax) is skipped.
// head is the text goja's functionCtor puts before the parameters.
var functionCtorRoots = []struct{ expr, head string }{
	{`Function`, "(function anonymous("},
	{`Object.getPrototypeOf(async function(){}).constructor`, "(async function anonymous("},
	{`Object.getPrototypeOf(function*(){}).constructor`, "(function* anonymous("},
	{`Object.getPrototypeOf(async function*(){}).constructor`, "(async function* anonymous("},
}

// functionShapeCheck enables the check that a Function-like constructor's
// arguments form one function expression (checkFunctionShape). Only tests
// turn it off, to exercise the prototype post-check on its own.
var functionShapeCheck = true

// functionShapeMessage is the SyntaxError for constructor arguments that do
// not form a single function.
const functionShapeMessage = "Function constructor arguments must form a single function: the parameters and the body may not close it"

// installSourceGuard puts checkSourceSize in front of every way a script can
// make goja compile a string: eval and the Function-like constructors. It
// runs from installStackGuard, after the builtins were instrumented.
func (g *nativeGuard) installSourceGuard() error {
	vm := g.vm

	// String's own call converts a constructor argument exactly the way the
	// constructor would (ToString, which may run script code); done once, here.
	stringCall, _, ok := nativeFuncFields(vm.Get("String").ToObject(vm))
	if !ok || *stringCall == nil {
		return errStackGuardSetup
	}
	toStringOnce := *stringCall

	syntaxErrorCtor, ok := goja.AssertConstructor(vm.Get("SyntaxError"))
	if !ok {
		return errStackGuardSetup
	}
	newSyntaxError := func(msg string) *goja.Object {
		e, err := syntaxErrorCtor(nil, vm.ToValue(msg))
		if err != nil {
			panic(err)
		}
		return e
	}

	seen := make(map[*goja.Object]struct{})
	for i, root := range functionCtorRoots {
		v, err := vm.RunString(root.expr)
		if err != nil {
			if i == 0 {
				return fmt.Errorf("%w: %v", errStackGuardSetup, err)
			}
			continue
		}
		head := root.head
		ctor := v.ToObject(vm)
		if _, dup := seen[ctor]; dup {
			continue
		}
		seen[ctor] = struct{}{}
		call, construct, ok := nativeFuncFields(ctor)
		if !ok || *call == nil || *construct == nil {
			return errStackGuardSetup
		}
		origCall := *call
		// The prototype the constructor gives the new function by default
		// (ctor.prototype: a non-writable, non-configurable property).
		defProto, ok := ctor.Get("prototype").(*goja.Object)
		if !ok {
			return errStackGuardSetup
		}
		// checked converts and checks the arguments: their size, and that
		// they form one function expression, so that compiling them runs no
		// script code and yields a function nothing else refers to yet.
		checked := func(args []goja.Value) []goja.Value {
			out, complete := g.checkFunctionArgs(args, toStringOnce)
			if complete && functionShapeCheck && !functionShapeOK(head, out) {
				panic(newSyntaxError(functionShapeMessage))
			}
			return out
		}
		// made returns what goja's functionCtor returned, after the same
		// post-check as the prototype setters (breakProtoCycle): functionCtor
		// sets the prototype of the value its program evaluated to, which
		// the shape check makes a fresh function, but which is otherwise
		// whatever the program left (an object it exposed to script first).
		made := func(v goja.Value) *goja.Object {
			fn, isObj := v.(*goja.Object)
			if !isObj {
				panic(g.vm.NewTypeError("Function constructor did not return a function"))
			}
			if isProxy(fn) {
				// Never a fresh function: only an escaped program returns
				// one, and its forwarding cannot be checked after the fact.
				g.vm.Interrupt(errProtoCycleUnbroken)
				panic(g.vm.NewTypeError(cyclicProtoMessage))
			}
			if g.breakProtoCycle(fn, defProto) {
				panic(g.vm.NewTypeError(cyclicProtoMessage))
			}
			return fn
		}
		*call = func(c goja.FunctionCall) goja.Value {
			c.Arguments = checked(c.Arguments)
			return made(origCall(c))
		}
		// goja's construct resolves newTarget.prototype (which may run script
		// code) before it converts the arguments. The checked conversion must
		// run before goja compiles, so the construct is done here in goja's
		// order: the prototype first (getPrototypeFromCtor), then the
		// arguments, then the plain call (the constructor with its default
		// prototype) and the prototype set on the new function, as goja's
		// functionCtor does.
		*construct = func(args []goja.Value, newTarget *goja.Object) *goja.Object {
			proto := defProto
			if newTarget != nil && newTarget != ctor {
				if p, isObj := newTarget.Get("prototype").(*goja.Object); isObj {
					proto = p
				}
			}
			fn := made(origCall(goja.FunctionCall{This: goja.Undefined(), Arguments: checked(args)}))
			if proto != defProto {
				// fn is not a proxy (made): no trap runs. It is checked
				// again after the change, like any prototype change.
				if err := fn.SetPrototype(proto); err != nil {
					panic(err)
				}
				if g.breakProtoCycle(fn, defProto) {
					panic(g.vm.NewTypeError(cyclicProtoMessage))
				}
			}
			return fn
		}
	}

	// eval: goja runs a DIRECT eval (a call to the identifier eval that
	// resolves to the original function) without calling the function
	// object, so wrapping it in place would not see those calls. The global
	// is replaced by a checking function instead; with the original out of
	// the script's reach every eval is an indirect (global-scope) eval.
	global := vm.GlobalObject()
	evalCall, _, ok := nativeFuncFields(global.Get("eval").ToObject(vm))
	if !ok || *evalCall == nil {
		return errStackGuardSetup
	}
	origEval := *evalCall
	wrapper := vm.ToValue(func(c goja.FunctionCall) goja.Value {
		if len(c.Arguments) > 0 {
			if s, isString := c.Arguments[0].(goja.String); isString {
				if err := checkSourceSize(s.String()); err != nil {
					panic(g.newRangeError(err.Error()))
				}
			}
		}
		return origEval(c)
	}).ToObject(vm)
	if err := wrapper.DefineDataProperty("name", vm.ToValue("eval"), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE); err != nil {
		return fmt.Errorf("%w: %v", errStackGuardSetup, err)
	}
	if err := wrapper.DefineDataProperty("length", vm.ToValue(1), goja.FLAG_FALSE, goja.FLAG_FALSE, goja.FLAG_TRUE); err != nil {
		return fmt.Errorf("%w: %v", errStackGuardSetup, err)
	}
	if !g.patch(wrapper) {
		return errStackGuardSetup
	}
	if err := global.DefineDataProperty("eval", wrapper, goja.FLAG_TRUE, goja.FLAG_FALSE, goja.FLAG_TRUE); err != nil {
		return fmt.Errorf("%w: %v", errStackGuardSetup, err)
	}
	return nil
}

// functionCtorHead is the longest fixed text goja puts around a
// constructor's arguments (the async generator form).
const functionCtorHead = "(async function* anonymous(\n) {\n\n})"

// checkFunctionArgs converts the arguments of a Function-like constructor to
// strings once (so a toString cannot answer differently when the constructor
// converts them again), checks the source they form and returns them as
// string primitives. goja converts them in the same order (the parameters,
// then the body: argument order) and stops with a TypeError at the first
// Symbol, so conversion stops there too: the Symbol and everything after it
// are passed through unconverted, and the constructor throws at the Symbol
// before it reads any of the rest (nothing is compiled).
//
// complete reports that every argument was converted (no Symbol).
func (g *nativeGuard) checkFunctionArgs(args []goja.Value, toString nativeCallFunc) (out []goja.Value, complete bool) {
	out = make([]goja.Value, len(args))
	total := sourceOperatorTokens(functionCtorHead, maxSourceTokens)
	for i, a := range args {
		if _, isSymbol := a.(*goja.Symbol); isSymbol {
			copy(out[i:], args[i:])
			return out, false
		}
		s := toString(goja.FunctionCall{This: goja.Undefined(), Arguments: []goja.Value{a}})
		out[i] = s
		total += sourceOperatorTokens(s.String(), maxSourceTokens) + 1 // + the "," joining parameters
		if total > maxSourceTokens {
			panic(g.newRangeError(sourceTooDeepError().Error()))
		}
	}
	return out, true
}

// functionShapeOK reports whether the program goja's functionCtor builds from
// head and the converted arguments is one function expression and nothing
// else. The spec parses the parameters and the body on their own (as
// FormalParameters and FunctionBody), so a body like "}); f(); (function(){"
// is a SyntaxError; goja compiles the concatenation, which runs that code
// while the function is made and returns whatever the program evaluated to.
// A lone function expression runs no code when evaluated (default values
// run when the function is called).
//
// The text is built exactly as functionCtor builds it and parsed with
// goja's own parser. A source that parser rejects is left to the
// constructor, which rejects it with its own SyntaxError (goja only differs
// in writing a lone surrogate as a \u escape instead of U+FFFD, which is
// valid in the same places).
func functionShapeOK(head string, args []goja.Value) bool {
	var sb strings.Builder
	sb.WriteString(head)
	if len(args) > 1 {
		for i, a := range args[:len(args)-1] {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(a.String())
		}
	}
	sb.WriteString("\n) {\n")
	if len(args) > 0 {
		sb.WriteString(args[len(args)-1].String())
	}
	sb.WriteString("\n})")
	prog, err := parser.ParseFile(nil, "<eval>", sb.String(), 0)
	if err != nil {
		return true // the constructor reports the SyntaxError itself
	}
	if len(prog.Body) != 1 {
		return false
	}
	stmt, ok := prog.Body[0].(*ast.ExpressionStatement)
	if !ok {
		return false
	}
	_, ok = stmt.Expression.(*ast.FunctionLiteral)
	return ok
}
