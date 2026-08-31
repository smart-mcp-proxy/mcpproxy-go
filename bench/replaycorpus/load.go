package replaycorpus

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smart-mcp-proxy/mcpproxy-go/internal/contracts"
)

// Options configures a load. Its ZERO VALUE is the safe, documented default:
// bodies off, no fleet resolution claimed, warnings to stderr, the repository
// root discovered from the working directory.
type Options struct {
	// Bodies selects whether recorded request and response bodies are read.
	// The zero value is BodiesOff — see BodyPolicy for why that direction.
	Bodies BodyPolicy

	// Encoding is the tiktoken encoding used to count bodies on a bodies-on
	// load. Empty means DefaultEncoding. Ignored with bodies off, where there
	// is nothing to count.
	Encoding string

	// Warnf receives operator-facing warnings. Nil means stderr. Every warning
	// is also appended to Corpus.Warnings, so a caller that renders a report
	// does not have to intercept the printing to know a warning fired.
	Warnf func(format string, args ...any)

	// FleetResolver answers whether a recorded (server, tool) still exists in
	// the fleet a replay will score against. Nil means NO FLEET WAS SUPPLIED,
	// and the loader then refuses to assert either replayability or its
	// absence — Corpus.FleetChecked says which happened. A replay run needs a
	// fleet input regardless (a recording-only invocation computes nothing);
	// this field is how that fleet reaches the usability classification.
	FleetResolver func(serverName, toolName string) bool

	// repoRoot overrides the discovered repository working tree. Unexported
	// because it exists for tests, which must exercise the refusal without
	// writing a file into the real checkout to be refused.
	repoRoot string

	// counter overrides the tiktoken counter. Unexported for the reason given
	// in tokenize.go: an exported hook would hand body text to a caller outside
	// this package, which is the invariant tokenizing here protects.
	counter tokenCounter
}

// Corpus is the loader's whole output: grouped sessions, the accounting for
// everything that did not contribute, and the posture the load ran under. It
// carries counts and identities — never content.
type Corpus struct {
	Sessions   []*ReplaySession `json:"sessions"`
	Exclusions ExclusionReport  `json:"exclusions"`

	// Bodies and Encoding record the posture this corpus was produced under, so
	// a report can state it rather than assume it. Encoding is empty with
	// bodies off, where nothing was tokenized.
	Bodies   BodyPolicy `json:"bodies"`
	Encoding string     `json:"encoding,omitempty"`

	// RecordsRead is every line decoded, including those later dropped. It is
	// the denominator an exclusion report is read against.
	RecordsRead int `json:"records_read"`

	// FleetChecked reports whether a fleet resolver was supplied. False means
	// the `unreplayable` flag was never evaluated — not that nothing was
	// unreplayable.
	FleetChecked bool `json:"fleet_checked"`

	// Warnings mirrors everything sent to Options.Warnf.
	Warnings []string `json:"warnings,omitempty"`
}

// TotalCalls counts every call in every session, sub-calls included, each once.
func (c *Corpus) TotalCalls() int {
	total := 0
	for _, session := range c.Sessions {
		total += session.CallCount + session.SubCallCount
	}
	return total
}

// LoadFile loads a replay input from disk.
//
// The path is refused BEFORE it is opened if it lies inside the repository
// working tree, and refused if it names a CSV export. Both checks precede I/O
// on purpose: the first is a privacy rule that must not depend on the file
// existing, and the second saves an operator from a confusing parse error when
// the real problem is the export format.
func LoadFile(path string, opts Options) (*Corpus, error) {
	repoRoot := opts.repoRoot
	if repoRoot == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve working directory to locate the repository root: %w", err)
		}
		repoRoot = detectRepositoryRoot(wd)
	}
	if err := assertOutsideRepository(path, repoRoot); err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.Ext(path), ".csv") {
		return nil, csvRejection("input path names a .csv file")
	}

	f, err := os.Open(path) //nolint:gosec // operator-supplied replay input, refused above if inside the checkout
	if err != nil {
		return nil, fmt.Errorf("open replay input %q: %w", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only

	return Load(f, opts)
}

// Load decodes activity JSONL from r.
//
// It is the boundary the package doc's second invariant is stated about: bodies
// read here exist only as locals inside this call and the helpers it hands them
// to, and what it returns holds counts alone.
func Load(r io.Reader, opts Options) (*Corpus, error) {
	corpus := &Corpus{Bodies: opts.Bodies, FleetChecked: opts.FleetResolver != nil}
	warn := func(format string, args ...any) {
		corpus.Warnings = append(corpus.Warnings, fmt.Sprintf(format, args...))
		if opts.Warnf != nil {
			opts.Warnf(format, args...)
			return
		}
		fmt.Fprintf(os.Stderr, format+"\n", args...)
	}

	counter, err := resolveCounter(&opts)
	if err != nil {
		return nil, err
	}
	if opts.Bodies == BodiesOnUnmasked {
		corpus.Encoding = encodingName(&opts)
		warn("%s", bodiesOnWarning)
	}

	buffered := bufio.NewReader(r)
	if err := rejectCSVContent(buffered); err != nil {
		return nil, err
	}

	// A json.Decoder rather than a line scanner: a bodies-on record can be
	// megabytes, and a scanner would fail on its buffer limit rather than on
	// anything wrong with the input.
	dec := json.NewDecoder(buffered)
	var calls []*ReplayCall
	sessionOf := make(map[string]string)

	for {
		var rec contracts.ActivityRecord
		if err := dec.Decode(&rec); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode activity JSONL at record %d: %w", corpus.RecordsRead+1, err)
		}
		corpus.RecordsRead++

		decoded, ok := admit(&rec, &corpus.Exclusions)
		if !ok {
			continue
		}
		call := newCall(decoded, &opts, counter, &corpus.Exclusions)
		calls = append(calls, call)
		sessionOf[call.ID] = rec.WorkSessionID
	}

	if corpus.RecordsRead == 0 {
		warn("replay: input contained no activity records — a replay over an empty recording computes nothing")
	}
	corpus.Sessions = group(calls, sessionOf, &corpus.Exclusions)
	return corpus, nil
}

// resolveCounter picks the tokenizer for this load. Bodies off gets a counter
// that cannot be used, rather than a real one: constructing the tiktoken
// encoding would reach for a vocabulary the default posture never needs, and an
// offline bodies-off run must not fail on a download it has no use for.
func resolveCounter(opts *Options) (tokenCounter, error) {
	if opts.counter != nil {
		return opts.counter, nil
	}
	if opts.Bodies != BodiesOnUnmasked {
		return noBodyCounter{}, nil
	}
	return newTokenCounter(encodingName(opts))
}

func encodingName(opts *Options) string {
	if opts.Encoding != "" {
		return opts.Encoding
	}
	return DefaultEncoding
}

// rejectCSVContent sniffs the first non-blank byte. A JSONL export always
// starts an object; anything else is the CSV projection, whose header line
// would otherwise surface as an opaque JSON syntax error.
func rejectCSVContent(r *bufio.Reader) error {
	for {
		b, err := r.Peek(1)
		if err != nil {
			return nil // empty or unreadable input: let the decoder report it
		}
		if b[0] == ' ' || b[0] == '\t' || b[0] == '\r' || b[0] == '\n' {
			if _, err := r.Discard(1); err != nil {
				return nil
			}
			continue
		}
		if b[0] != '{' {
			return csvRejection("input does not begin with a JSON object")
		}
		return nil
	}
}

// decodedRecord is the transient carrier for one record's content and derived
// booleans. It exists so bodies stay confined to the load call: it is never
// stored on a ReplayCall, never returned, and goes out of scope as soon as the
// call's costs are computed.
type decodedRecord struct {
	id            string
	requestID     string
	parentID      string
	serverName    string
	toolName      string
	status        string
	timestamp     time.Time
	internal      bool
	truncated     bool
	sensitive     bool
	requestBytes  int
	responseBytes int

	// arguments and response are the only fields holding recorded content.
	// Nothing copies them onward.
	arguments string
	response  string
}

// admit decides whether a record is a call this loader can account for, and
// records the drop when it is not. The activity log is wider than tool calls —
// quarantine changes, policy decisions and server changes all arrive in an
// export — and folding those into a session as zero-cost calls would inflate
// the call count of every unit of work.
func admit(rec *contracts.ActivityRecord, report *ExclusionReport) (*decodedRecord, bool) {
	switch string(rec.Type) {
	case "tool_call", "internal_tool_call":
	default:
		report.drop(ReasonNotACall)
		return nil, false
	}
	if rec.ToolName == "" {
		report.drop(ReasonMissingTool)
		return nil, false
	}

	decoded := &decodedRecord{
		id:            rec.ID,
		requestID:     rec.RequestID,
		parentID:      rec.ParentID,
		serverName:    rec.ServerName,
		toolName:      rec.ToolName,
		status:        rec.Status,
		timestamp:     rec.Timestamp,
		internal:      string(rec.Type) == "internal_tool_call",
		truncated:     rec.ResponseTruncated,
		sensitive:     rec.HasSensitiveData,
		requestBytes:  rec.RequestBytes,
		responseBytes: rec.ResponseBytes,
		response:      rec.Response,
	}
	if len(rec.Arguments) > 0 {
		// Marshalled here, not carried as a map: a map would keep the recorded
		// values alive on a struct, and Go's map marshalling sorts keys, so the
		// counted text is also deterministic across runs (SC-002).
		if encoded, err := json.Marshal(rec.Arguments); err == nil {
			decoded.arguments = string(encoded)
		}
	}
	return decoded, true
}

// newCall converts an admitted record into a ReplayCall, classifying it and
// pricing both cost components once. This is where content stops: everything
// after this point sees counts.
func newCall(rec *decodedRecord, opts *Options, counter tokenCounter, report *ExclusionReport) *ReplayCall {
	call := &ReplayCall{
		ID:            rec.id,
		RequestID:     rec.requestID,
		ParentID:      rec.parentID,
		ServerName:    rec.serverName,
		ToolName:      rec.toolName,
		Status:        rec.status,
		Timestamp:     rec.timestamp,
		Internal:      rec.internal,
		RequestBytes:  rec.requestBytes,
		ResponseBytes: rec.responseBytes,
	}
	call.classify(rec, opts, report)

	// A record with a parent_id is a sandbox sub-call, which is the one case
	// where both byte counts being zero has a known cause worth naming.
	isSubCall := rec.parentID != ""
	call.RequestCost = requestCost(rec, isSubCall, opts, counter.Count)
	call.ResponseCost = responseCost(rec, isSubCall, opts, counter.Count, report)
	return call
}
