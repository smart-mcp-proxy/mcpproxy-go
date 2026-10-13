package jsruntime

import (
	"context"
	"testing"
	"time"
)

// TestExecuteTimeoutInterruptsTheVM: a script that never yields must not
// outlive its timeout. Before the fix Execute returned TIMEOUT to the caller
// and left the goroutine spinning on the busy loop for the life of the
// process — one core per runaway script, with nothing to bound it once the
// pool slot was released.
func TestExecuteTimeoutInterruptsTheVM(t *testing.T) {
	result, ec := execute(context.Background(), newMockToolCaller(), `for (;;) {}`, ExecutionOptions{TimeoutMs: 50})
	if result.Ok {
		t.Fatal("expected the busy loop to time out")
	}
	if result.Error == nil || result.Error.Code != ErrorCodeTimeout {
		t.Fatalf("expected %s, got %+v", ErrorCodeTimeout, result.Error)
	}

	select {
	case <-ec.scriptDone:
		// the VM was interrupted and the script goroutine returned
	case <-time.After(2 * time.Second):
		t.Fatal("script goroutine still running 2s after the timeout: the VM was not interrupted")
	}
}

// TestSingletonCallToolDispatchesUnderTheExecutionContext: a lone call_tool()
// must dispatch under the execution's timeout context, like call_tools()
// batches already do, so an upstream call still in flight when the script is
// cut off is cancelled instead of orphaned.
func TestSingletonCallToolDispatchesUnderTheExecutionContext(t *testing.T) {
	caller := &ctxCapturingCaller{}
	result, ec := execute(context.Background(), caller, `call_tool("s", "t", {})`, ExecutionOptions{TimeoutMs: 5000})
	if !result.Ok {
		t.Fatalf("script failed: %v", result.Error)
	}
	if caller.ctx == nil {
		t.Fatal("call_tool dispatched with a nil context")
	}
	if caller.ctx.Done() == nil {
		t.Fatal("call_tool dispatched under a context that can never be cancelled (context.Background)")
	}
	if caller.ctx != ec.ctx {
		t.Fatal("call_tool must dispatch under the execution's own timeout context")
	}
}

// TestExecuteTimeoutDuringExportDoesNotPanic: RunString can return before the
// script's work is done — a getter on the returned object runs during
// value.Export(). An interrupt landing there is raised by goja as a PANIC
// carrying *goja.InterruptedError, not as a RunString error, and an
// unrecovered panic in the script goroutine takes the whole process down.
func TestExecuteTimeoutDuringExportDoesNotPanic(t *testing.T) {
	result, ec := execute(context.Background(), newMockToolCaller(),
		`({ get value() { for (;;) {} } })`, ExecutionOptions{TimeoutMs: 50})
	if result.Ok {
		t.Fatal("expected the busy getter to time out")
	}
	if result.Error == nil || result.Error.Code != ErrorCodeTimeout {
		t.Fatalf("expected %s, got %+v", ErrorCodeTimeout, result.Error)
	}
	select {
	case <-ec.scriptDone:
	case <-time.After(2 * time.Second):
		t.Fatal("script goroutine did not return after the interrupt during export")
	}
}
