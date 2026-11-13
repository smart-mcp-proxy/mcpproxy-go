package jsruntime

import (
	"fmt"

	"github.com/dop251/goja"
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
// grammar grows. Runs of non-ASCII bytes count as words too.
func typeScriptSourceTokens(src string, limit int) int {
	return countSourceTokens(src, limit, true)
}

func countSourceTokens(src string, limit int, everyWord bool) int {
	n := 0
	for i := 0; i < len(src) && n <= limit; {
		c := src[i]
		switch {
		case isSourceWordByte(c) || everyWord && c >= 0x80:
			j := i + 1
			for j < len(src) && (isSourceWordByte(src[j]) || everyWord && src[j] >= 0x80) {
				j++
			}
			if everyWord {
				n++
			} else if _, op := sourceKeywordOperators[src[i:j]]; op {
				n++
			}
			i = j
		case c > ' ' && c < 0x7f: // ASCII punctuation (word bytes handled above)
			n++
			i++
		default: // whitespace, control and non-ASCII bytes (identifiers, text)
			i++
		}
	}
	return n
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
var functionCtorRoots = []string{
	`Function`,
	`Object.getPrototypeOf(async function(){}).constructor`,
	`Object.getPrototypeOf(function*(){}).constructor`,
	`Object.getPrototypeOf(async function*(){}).constructor`,
}

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

	seen := make(map[*goja.Object]struct{})
	for _, root := range functionCtorRoots {
		v, err := vm.RunString(root)
		if err != nil {
			if root == functionCtorRoots[0] {
				return fmt.Errorf("%w: %v", errStackGuardSetup, err)
			}
			continue
		}
		ctor := v.ToObject(vm)
		if _, dup := seen[ctor]; dup {
			continue
		}
		seen[ctor] = struct{}{}
		call, construct, ok := nativeFuncFields(ctor)
		if !ok || *call == nil || *construct == nil {
			return errStackGuardSetup
		}
		origCall, origConstruct := *call, *construct
		*call = func(c goja.FunctionCall) goja.Value {
			c.Arguments = g.checkFunctionArgs(c.Arguments, toStringOnce)
			return origCall(c)
		}
		*construct = func(args []goja.Value, newTarget *goja.Object) *goja.Object {
			return origConstruct(g.checkFunctionArgs(args, toStringOnce), newTarget)
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
// string primitives. A Symbol is passed through for the constructor to
// reject with its own TypeError.
func (g *nativeGuard) checkFunctionArgs(args []goja.Value, toString nativeCallFunc) []goja.Value {
	out := make([]goja.Value, len(args))
	total := sourceOperatorTokens(functionCtorHead, maxSourceTokens)
	for i, a := range args {
		if _, isSymbol := a.(*goja.Symbol); isSymbol {
			out[i] = a
			continue
		}
		s := toString(goja.FunctionCall{This: goja.Undefined(), Arguments: []goja.Value{a}})
		out[i] = s
		total += sourceOperatorTokens(s.String(), maxSourceTokens) + 1 // + the "," joining parameters
		if total > maxSourceTokens {
			panic(g.newRangeError(sourceTooDeepError().Error()))
		}
	}
	return out
}
