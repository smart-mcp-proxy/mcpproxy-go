package jsruntime

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestExecuteTimeoutStopsCatastrophicRegex: vm.Interrupt is only checked by
// goja's interpreter loop, never inside a native call, so a backtracking regex
// (the backreference forces goja onto regexp2) ignored the timeout. Execute
// reported TIMEOUT while the script goroutine kept a core busy practically
// forever, and the released pool slot let every new call leak another one.
func TestExecuteTimeoutStopsCatastrophicRegex(t *testing.T) {
	result, ec := execute(context.Background(), newMockToolCaller(),
		`/^(a+)+\1$/.test("a".repeat(40) + "b")`, ExecutionOptions{TimeoutMs: 50})
	if result.Ok {
		t.Fatal("expected the catastrophic regex to time out")
	}
	if result.Error == nil || result.Error.Code != ErrorCodeTimeout {
		t.Fatalf("expected %s, got %+v", ErrorCodeTimeout, result.Error)
	}

	select {
	case <-ec.scriptDone:
	case <-time.After(regexMatchTimeout + 3*time.Second):
		t.Fatal("script goroutine still running inside the regex match long after the timeout")
	}
}

// TestExecuteMemoryBudgetInterrupts: goja has no heap limit, so a script could
// grow the process heap without bound until the timeout (120s by default) and
// get the proxy killed by the OS. The memory budget interrupts it instead.
func TestExecuteMemoryBudgetInterrupts(t *testing.T) {
	start := time.Now()
	result, ec := execute(context.Background(), newMockToolCaller(),
		`var a = []; for (;;) { a.push("x".repeat(1 << 20)) }`,
		ExecutionOptions{TimeoutMs: 30000, MaxMemoryBytes: 64 << 20})
	if result.Ok {
		t.Fatal("expected the allocation loop to hit the memory budget")
	}
	if result.Error == nil || result.Error.Code != ErrorCodeMemoryLimit {
		t.Fatalf("expected %s, got %+v", ErrorCodeMemoryLimit, result.Error)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("memory budget took %v to stop the script; it should not wait for the timeout", elapsed)
	}
	select {
	case <-ec.scriptDone:
	case <-time.After(2 * time.Second):
		t.Fatal("script goroutine still running after the memory interrupt")
	}
}

// TestExecuteMemoryBudgetIgnoresGarbage: the budget is on memory the script
// keeps alive. Allocating far more than the budget in total, while holding
// little at any one time, must not trip it.
func TestExecuteMemoryBudgetIgnoresGarbage(t *testing.T) {
	result, _ := execute(context.Background(), newMockToolCaller(),
		`var n = 0; for (var i = 0; i < 256; i++) { var s = "x".repeat(1 << 20); n += s.length; s = null } n`,
		ExecutionOptions{TimeoutMs: 30000, MaxMemoryBytes: 64 << 20})
	if !result.Ok {
		t.Fatalf("garbage-only allocation tripped the memory budget: %+v", result.Error)
	}
}

// TestExecuteMemoryBudgetDisabled: a negative budget turns the watchdog off.
func TestExecuteMemoryBudgetDisabled(t *testing.T) {
	result, _ := execute(context.Background(), newMockToolCaller(),
		`var a = []; for (var i = 0; i < 96; i++) { a.push("x".repeat(1 << 20)) } a.length`,
		ExecutionOptions{TimeoutMs: 30000, MaxMemoryBytes: -1})
	if !result.Ok {
		t.Fatalf("disabled budget still interrupted the script: %+v", result.Error)
	}
}

// TestExecuteTimeoutStopsGlobalRegexLoop: a global regex runs every match from
// one native loop, and regexp2 restarts its per-match deadline on each one. A
// pattern whose every match stays under regexMatchTimeout but whose matches
// add up to minutes (a failing backtracking lookahead per position) outlived
// the timeout unless each match goes back through the interpreter.
func TestExecuteTimeoutStopsGlobalRegexLoop(t *testing.T) {
	result, ec := execute(context.Background(), newMockToolCaller(),
		`("a".repeat(22) + "c").repeat(400).replace(/(?=(a+)+b)|./g, "")`, ExecutionOptions{TimeoutMs: 100})
	if result.Ok {
		t.Fatal("expected the global regex loop to time out")
	}
	if result.Error == nil || result.Error.Code != ErrorCodeTimeout {
		t.Fatalf("expected %s, got %+v", ErrorCodeTimeout, result.Error)
	}

	select {
	case <-ec.scriptDone:
	case <-time.After(regexMatchTimeout + 3*time.Second):
		t.Fatal("script goroutine still running the global regex loop long after the timeout")
	}
}

// TestRegexBuiltinsStillWork: routing matches through the interpreter must not
// change what the regex built-ins return.
func TestRegexBuiltinsStillWork(t *testing.T) {
	result, _ := execute(context.Background(), newMockToolCaller(), `[
		"a1b22c333".replace(/\d+/g, "#"),
		"x-y_z".split(/[-_]/).join(","),
		("k=v; k2=v2".match(/(\w+)=(\w+)/g) || []).join("|"),
		Array.from("a1b2".matchAll(/[a-z](\d)/g), m => m[1]).join(""),
		"hello".search(/l+/),
		/^(a)\1$/.test("aa"),
		/x/.exec("yxz").index,
		"a,b".split(/,/, -1).join(","),
		"a,b".split(/,/, 4294967296).length,
		"a,b,c".split(/,/, 2).join(","),
		RegExp.prototype[Symbol.split].name,
		"a,b,c".split(/,/, { valueOf() { return 2 } }).join(","),
		(function () { try { RegExp.prototype[Symbol.split].call(null, "a,b"); return "no throw" } catch (e) { return e.name } })(),
		(function () { try { RegExp.prototype.exec.call(undefined, "a"); return "no throw" } catch (e) { return e.name } })(),
		(function () {
			var log = [];
			class R extends RegExp { constructor(...a) { super(...a); log.push("species") } }
			var rx = new R(",");
			log.length = 0;
			"a,b".split(rx, { valueOf() { log.push("limit"); return 5 } });
			return log.join(">");
		})()
	]`, ExecutionOptions{TimeoutMs: 5000})
	if !result.Ok {
		t.Fatalf("regex built-ins failed: %+v", result.Error)
	}
	got := fmt.Sprint(result.Value)
	want := "[a#b#c# x,y,z k=v|k2=v2 12 2 true 1 a,b 0 a,b [Symbol.split] a,b TypeError TypeError species>limit]"
	if got != want {
		t.Fatalf("regex built-ins changed behaviour:\n got %s\nwant %s", got, want)
	}
}
