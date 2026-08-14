package config

import "errors"

// CodeExecutionDisabledMessage is the single explanation every surface gives
// when EnableCodeExecution is false. The MCP disabled stub, the handler gate
// that covers REST and tray dispatch, and the HTTP 403 body all render this
// string, so a caller who switches transports never has to wonder whether two
// different refusals mean two different things.
const CodeExecutionDisabledMessage = `Code execution is disabled. Enable it by setting "enable_code_execution": true in your mcpproxy configuration file.`

// ErrCodeExecutionDisabled is the TYPED identity of that refusal, matched with
// errors.Is; what a caller READS is always CodeExecutionDisabledMessage. The
// MCP contract forces the tool handler to answer with an isError result rather
// than a transport error, which leaves the REST layer nothing but a string to
// classify by. Carrying this sentinel out through the dispatch error lets the
// HTTP surface answer 403 — a refusal retrying cannot fix — instead of the 500
// a flattened message would produce.
var ErrCodeExecutionDisabled = errors.New("code execution is disabled")
