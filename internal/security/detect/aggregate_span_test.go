package detect

import (
	"reflect"
	"testing"
)

// sigWithSpans returns a signal carrying the given spans, so the union tests
// read as data rather than struct literals.
func sigWithSpans(id string, tier Tier, spans ...Span) Signal {
	return Signal{CheckID: id, Tier: tier, ThreatType: ThreatToolPoisoning, Confidence: 0.5, Detail: id, Spans: spans}
}

func span(checkID string, start, end int, tier Tier) Span {
	return Span{Field: SpanFieldDescription, Start: start, End: end, CheckID: checkID, Tier: tier.String()}
}

// TestAggregateUnionsSpansAcrossEverySignal is the reason the union exists:
// aggregate emits exactly ONE Finding per tool and copies Evidence/Description
// from the primary signal alone. Primary-wins on spans would silently swallow
// every mark a non-primary check found, so a description tripping two rules
// would highlight one of them and quietly lose the other.
func TestAggregateUnionsSpansAcrossEverySignal(t *testing.T) {
	f, ok := aggregate(ToolView{Server: "srv", Name: "create_user"}, []Signal{
		sigWithSpans("tpa.bundle.x", TierHard, span("tpa.bundle.x", 10, 20, TierHard)),
		sigWithSpans("shadowing.cross_server", TierSoft, span("shadowing.cross_server", 1893, 1899, TierSoft)),
	}, "tpa-descriptions")
	if !ok {
		t.Fatal("expected a finding")
	}
	if len(f.Spans) != 2 {
		t.Fatalf("Spans = %+v, want both signals' spans unioned", f.Spans)
	}
	// The finding's own RuleID names the primary signal only; per-mark labelling
	// has to come from the spans, so each must keep its own CheckID and Tier.
	if f.Spans[0].CheckID != "tpa.bundle.x" || f.Spans[0].Tier != "hard" {
		t.Errorf("span[0] = %+v, want the hard bundle span first (lowest offset)", f.Spans[0])
	}
	if f.Spans[1].CheckID != "shadowing.cross_server" || f.Spans[1].Tier != "soft" {
		t.Errorf("span[1] = %+v, want the soft shadowing span", f.Spans[1])
	}
}

func TestAggregateDedupesIdenticalSpans(t *testing.T) {
	// The same check can match the same offsets twice (a rule present in two
	// bundle entries, a tool scanned against two peers). One mark, not two.
	dup := span("shadowing.cross_server", 5, 11, TierSoft)
	f, _ := aggregate(ToolView{Server: "srv", Name: "t"}, []Signal{
		sigWithSpans("shadowing.cross_server", TierSoft, dup),
		sigWithSpans("shadowing.cross_server", TierSoft, dup),
	}, "s")
	if len(f.Spans) != 1 {
		t.Fatalf("Spans = %+v, want the exact repeat deduped to one", f.Spans)
	}
	// Same range, DIFFERENT check id is a genuinely different mark and stays.
	f2, _ := aggregate(ToolView{Server: "srv", Name: "t"}, []Signal{
		sigWithSpans("a.check", TierSoft, span("a.check", 5, 11, TierSoft)),
		sigWithSpans("b.check", TierSoft, span("b.check", 5, 11, TierSoft)),
	}, "s")
	if len(f2.Spans) != 2 {
		t.Errorf("Spans = %+v, want two spans (same range, different check ids)", f2.Spans)
	}
}

// TestAggregateFirstSeenDuplicateWinsMetadata pins which duplicate survives:
// dedupe is on (Field,Start,End,CheckID), so Tier/Snippet come from the FIRST
// occurrence. Without a rule here the surviving tier would depend on signal
// ordering, which is exactly the nondeterminism the baseline tests forbid.
func TestAggregateFirstSeenDuplicateWinsMetadata(t *testing.T) {
	first := Span{Field: SpanFieldDescription, Start: 5, End: 11, CheckID: "c", Tier: "hard", Snippet: "first"}
	second := Span{Field: SpanFieldDescription, Start: 5, End: 11, CheckID: "c", Tier: "soft", Snippet: "second"}
	f, _ := aggregate(ToolView{Server: "s", Name: "t"}, []Signal{
		sigWithSpans("c", TierHard, first),
		sigWithSpans("c", TierSoft, second),
	}, "s")
	if len(f.Spans) != 1 || f.Spans[0].Snippet != "first" || f.Spans[0].Tier != "hard" {
		t.Errorf("Spans = %+v, want the first-seen duplicate's metadata", f.Spans)
	}
}

func TestAggregateCapsSpansAtMax(t *testing.T) {
	var sigs []Signal
	for i := 0; i < MaxSpansPerFinding+17; i++ {
		sigs = append(sigs, sigWithSpans("c", TierSoft, span("c", i*10, i*10+4, TierSoft)))
	}
	f, _ := aggregate(ToolView{Server: "s", Name: "t"}, sigs, "s")
	if len(f.Spans) != MaxSpansPerFinding {
		t.Fatalf("len(Spans) = %d, want the cap %d", len(f.Spans), MaxSpansPerFinding)
	}
	// The cap is applied AFTER the sort, so it keeps the earliest matches in the
	// text rather than an arbitrary slice of the signal order.
	if f.Spans[0].Start != 0 || f.Spans[MaxSpansPerFinding-1].Start != (MaxSpansPerFinding-1)*10 {
		t.Errorf("cap kept %+v … %+v, want the lowest offsets", f.Spans[0], f.Spans[MaxSpansPerFinding-1])
	}
}

// TestAggregateSpanOrderIsInputOrderIndependent guards the repo's
// baseline-determinism contract: two scans that emit the same signals in a
// different order must produce byte-identical findings.
func TestAggregateSpanOrderIsInputOrderIndependent(t *testing.T) {
	dup := span("dup.check", 5, 11, TierSoft)
	forward := []Signal{
		sigWithSpans("a.check", TierHard, span("a.check", 40, 44, TierHard), span("a.check", 12, 18, TierHard)),
		sigWithSpans("dup.check", TierSoft, dup),
		sigWithSpans("b.check", TierSoft, span("b.check", 12, 18, TierSoft)),
		sigWithSpans("dup.check", TierSoft, dup),
	}
	shuffled := []Signal{forward[3], forward[2], forward[0], forward[1]}

	f1, _ := aggregate(ToolView{Server: "s", Name: "t"}, forward, "s")
	f2, _ := aggregate(ToolView{Server: "s", Name: "t"}, shuffled, "s")
	if !reflect.DeepEqual(f1.Spans, f2.Spans) {
		t.Fatalf("span order depends on signal order:\n forward  = %+v\n shuffled = %+v", f1.Spans, f2.Spans)
	}
	// And the sort key is (Field, Start, End, CheckID) — offsets first, check id
	// only as the tie-break.
	want := []Span{
		span("dup.check", 5, 11, TierSoft),
		span("a.check", 12, 18, TierHard),
		span("b.check", 12, 18, TierSoft),
		span("a.check", 40, 44, TierHard),
	}
	if !reflect.DeepEqual(f1.Spans, want) {
		t.Errorf("Spans = %+v,\nwant %+v", f1.Spans, want)
	}
}

func TestAggregateNoSpansStaysNil(t *testing.T) {
	// A finding from checks that match normalized text carries no spans at all,
	// and must serialize as an absent key rather than an empty array.
	f, _ := aggregate(ToolView{Server: "s", Name: "t"}, []Signal{soft("phrase.injection", 0.4)}, "s")
	if f.Spans != nil {
		t.Errorf("Spans = %+v, want nil for a span-less finding", f.Spans)
	}
}
