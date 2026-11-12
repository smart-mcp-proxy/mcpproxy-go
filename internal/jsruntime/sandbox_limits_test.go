package jsruntime

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dlclark/regexp2"
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
		`var n = 0; for (var i = 0; i < 512; i++) { var s = "x".repeat(1024).repeat(1024); n += s.length; s = null } n`,
		ExecutionOptions{TimeoutMs: 30000, MaxMemoryBytes: 128 << 20})
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

// blockingCaller pushes the heap over budget only once a call is in flight,
// by holding a large allocation, then blocks until its context is cancelled.
// The budget is measured on the process heap, so the script is over budget
// exactly while it waits here, never before it dispatches.
type blockingCaller struct{ cancelled chan struct{} }

func (b *blockingCaller) CallTool(ctx context.Context, _, _ string, _ map[string]interface{}) (interface{}, error) {
	hold := make([]byte, 96<<20)
	<-ctx.Done()
	runtime.KeepAlive(hold)
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
		`call_tool("s", "t", {}); true`,
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
		`call_tools([{server: "s", tool: "t", args: {}}]); true`,
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
// The churn is paced (~128 MiB/s) so the garbage one collection can float stays
// far under the budget even on a slow, instrumented runner; the heuristic does
// not promise this for unpaced churn.
func TestExecuteMemoryBudgetSustainedChurn(t *testing.T) {
	result, _ := execute(context.Background(), newMockToolCaller(),
		`var end = Date.now() + 3000, n = 0;
		 while (Date.now() < end) {
			var s = "x".repeat(1024).repeat(256); n += s.length; s = null;
			var pause = Date.now() + 2; while (Date.now() < pause) {}
		 }
		 n > 0`,
		ExecutionOptions{TimeoutMs: 30000, MaxMemoryBytes: 128 << 20})
	if !result.Ok {
		t.Fatalf("sustained churn tripped the memory budget: %+v", result.Error)
	}
}

// TestRegexMatchTimeoutAfterIdleClock: regexMatchTimeout is process-wide, so
// it also bounds the tokenizer's patterns. regexp2 ≤ v1.11.4 computed a new
// deadline from a clock that had stopped ticking after ~1s idle, so the first
// slow-ish match after a pause timed out almost at once, reading as "no match"
// to goja and truncating tiktoken's token counts (bench/arms flaked on CI).
func TestRegexMatchTimeoutAfterIdleClock(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for regexp2's timeout clock to go idle")
	}
	// The first alternative backtracks before the second one matches. The
	// input is grown until a match takes a few hundred milliseconds here
	// (much slower under -race): well under the timeout, well over the
	// clock's 100ms tick.
	re := regexp2.MustCompile(`^(?:(a+)+x|a+y)$`, regexp2.ECMAScript)
	var input string
	for n := 10; ; n++ {
		if n > 40 {
			t.Skip("could not calibrate a match between the clock tick and the timeout")
		}
		input = strings.Repeat("a", n) + "y"
		start := time.Now()
		ok, err := re.MatchString(input)
		if err != nil || !ok {
			t.Fatalf("calibration match (n=%d): ok=%v err=%v", n, ok, err)
		}
		if time.Since(start) >= 150*time.Millisecond {
			break
		}
	}
	time.Sleep(regexMatchTimeout + 2*time.Second) // let the clock stop
	start := time.Now()
	ok, err := re.MatchString(input)
	if err != nil || !ok {
		t.Fatalf("match after an idle clock spuriously failed after %v: ok=%v err=%v", time.Since(start), ok, err)
	}
}
