package replaycorpus

import (
	"fmt"

	"github.com/pkoukk/tiktoken-go"
)

// DefaultEncoding is the tiktoken encoding used to count recorded bodies. It
// matches the rest of the benchmark (bench.DefaultEncoding) so a replay figure
// and a corpus figure are commensurable — but it is DUPLICATED here rather than
// imported, because this package must not import `bench` (package doc,
// invariant 1). The duplication is one string; the cycle would be structural.
const DefaultEncoding = "cl100k_base"

// tokenCounter is the seam through which bodies are turned into counts. It is
// UNEXPORTED, and Options holds it in an unexported field, on purpose: an
// exported hook would let a caller outside this package receive body text,
// which is precisely the invariant tokenization-inside-the-loader exists to
// protect (package doc, invariant 2). Tests substitute a deterministic stub so
// the suite never reaches the network for a vocabulary.
type tokenCounter interface {
	Count(text string) int
}

// tiktokenCounter is the real counter: the same cl100k_base encoding the rest
// of the harness uses, reached directly rather than through bench.Tokenizer.
type tiktokenCounter struct {
	enc *tiktoken.Tiktoken
}

func newTokenCounter(encoding string) (tokenCounter, error) {
	enc, err := tiktoken.GetEncoding(encoding)
	if err != nil {
		// tiktoken fetches its vocabulary over the network unless
		// TIKTOKEN_CACHE_DIR names a POPULATED cache, so this is the error an
		// offline first run hits. Say so, rather than leaving the operator to
		// guess why a local file needed the internet.
		return nil, fmt.Errorf("load tiktoken encoding %q (bodies-on replay needs a populated TIKTOKEN_CACHE_DIR to run offline): %w", encoding, err)
	}
	return &tiktokenCounter{enc: enc}, nil
}

// Count returns the number of tokens in text. The text is a parameter and never
// a field: nothing in this type retains what it counted.
func (c *tiktokenCounter) Count(text string) int {
	return len(c.enc.Encode(text, nil, nil))
}

// noBodyCounter is the counter installed for a bodies-off load. Reaching it is
// a bug — bodies-off has no text to count — so it returns zero rather than
// pulling a vocabulary down for a call that should never happen.
type noBodyCounter struct{}

func (noBodyCounter) Count(string) int { return 0 }
