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
		`var a = []; for (;;) { a.push("x".repeat(1024).repeat(1024)) }`,
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
		`var n = 0; for (var i = 0; i < 256; i++) { var s = "x".repeat(1024).repeat(1024); n += s.length; s = null } n`,
		ExecutionOptions{TimeoutMs: 30000, MaxMemoryBytes: 64 << 20})
	if !result.Ok {
		t.Fatalf("garbage-only allocation tripped the memory budget: %+v", result.Error)
	}
}

// TestExecuteMemoryBudgetDisabled: a negative budget turns the watchdog off.
func TestExecuteMemoryBudgetDisabled(t *testing.T) {
	result, _ := execute(context.Background(), newMockToolCaller(),
		`var a = []; for (var i = 0; i < 96; i++) { a.push("x".repeat(1024).repeat(1024)) } a.length`,
		ExecutionOptions{TimeoutMs: 30000, MaxMemoryBytes: -1})
	if !result.Ok {
		t.Fatalf("disabled budget still interrupted the script: %+v", result.Error)
	}
}

// TestRegexBuiltinsStillWork pins what the regex built-ins return, so the
// sandbox limits never move them off goja's native regex paths: the generic
// spec path mis-reports a capture that sits inside a lookahead before a
// consuming capture, and its split limit coercion differs.
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
		})(),
		"abc".replace(/(?=..(c))(a)(b)/, "$2")
	]`, ExecutionOptions{TimeoutMs: 5000})
	if !result.Ok {
		t.Fatalf("regex built-ins failed: %+v", result.Error)
	}
	got := fmt.Sprint(result.Value)
	want := "[a#b#c# x,y,z k=v|k2=v2 12 2 true 1 a,b 0 a,b [Symbol.split] a,b TypeError TypeError species>limit ac]"
	if got != want {
		t.Fatalf("regex built-ins changed behaviour:\n got %s\nwant %s", got, want)
	}
}

// TestRegexSplitStaysLinear: splitting a long input on an absent delimiter is
// a linear scan on goja's native path. The generic spec path tries a sticky
// match at every position and re-decodes the input each time, which turned
// this into seconds and tens of GiB of allocation.
func TestRegexSplitStaysLinear(t *testing.T) {
	start := time.Now()
	result, _ := execute(context.Background(), newMockToolCaller(),
		`"a".repeat(100000).split(/,/).length`, ExecutionOptions{TimeoutMs: 30000})
	if !result.Ok {
		t.Fatalf("split failed: %+v", result.Error)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("split of 100k chars took %v; the native linear path is ~1ms", elapsed)
	}
}

// blockingCaller blocks every call until its context is cancelled.
type blockingCaller struct{ cancelled chan struct{} }

func (b *blockingCaller) CallTool(ctx context.Context, _, _ string, _ map[string]interface{}) (interface{}, error) {
	<-ctx.Done()
	close(b.cancelled)
	return nil, ctx.Err()
}

// TestExecuteMemoryBudgetCancelsInFlightCall: a script that is over its budget
// while it waits on an upstream call must not keep its heap until the call or
// the timeout ends. The memory trip cancels the execution context, so the call
// returns and the script stops with MEMORY_LIMIT.
func TestExecuteMemoryBudgetCancelsInFlightCall(t *testing.T) {
	caller := &blockingCaller{cancelled: make(chan struct{})}
	start := time.Now()
	result, _ := execute(context.Background(), caller,
		`var keep = []; for (var i = 0; i < 96; i++) { keep.push("x".repeat(1024).repeat(1024)) } call_tool("s", "t", {}); keep.length`,
		ExecutionOptions{TimeoutMs: 30000, MaxMemoryBytes: 32 << 20})
	if result.Error == nil || result.Error.Code != ErrorCodeMemoryLimit {
		t.Fatalf("expected %s, got ok=%v %+v", ErrorCodeMemoryLimit, result.Ok, result.Error)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("memory trip took %v; it waited for the timeout instead of cancelling the call", elapsed)
	}
	select {
	case <-caller.cancelled:
	default:
		t.Fatal("the in-flight upstream call was never cancelled")
	}
}

// TestExecuteMemoryBudgetCancelsInFlightBatch: the same for a call_tools()
// batch, whose workers dispatch under the execution context.
func TestExecuteMemoryBudgetCancelsInFlightBatch(t *testing.T) {
	caller := &blockingCaller{cancelled: make(chan struct{})}
	result, _ := execute(context.Background(), caller,
		`var keep = []; for (var i = 0; i < 96; i++) { keep.push("x".repeat(1024).repeat(1024)) } call_tools([{server: "s", tool: "t", args: {}}]); keep.length`,
		ExecutionOptions{TimeoutMs: 30000, MaxMemoryBytes: 32 << 20})
	if result.Error == nil || result.Error.Code != ErrorCodeMemoryLimit {
		t.Fatalf("expected %s, got ok=%v %+v", ErrorCodeMemoryLimit, result.Ok, result.Error)
	}
	select {
	case <-caller.cancelled:
	default:
		t.Fatal("the in-flight batch call was never cancelled")
	}
}

// TestExecuteMemoryBudgetSustainedChurn: a script that allocates and drops
// values for several seconds, across many collections, keeps little alive and
// must not be stopped for the garbage that floats through each collection.
func TestExecuteMemoryBudgetSustainedChurn(t *testing.T) {
	result, _ := execute(context.Background(), newMockToolCaller(),
		`var end = Date.now() + 3000, n = 0; while (Date.now() < end) { var s = "x".repeat(1024).repeat(1024); n += s.length; s = null } n > 0`,
		ExecutionOptions{TimeoutMs: 30000, MaxMemoryBytes: 64 << 20})
	if !result.Ok {
		t.Fatalf("sustained churn tripped the memory budget: %+v", result.Error)
	}
}
