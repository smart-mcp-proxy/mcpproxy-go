package toonenc

// Classify is the deterministic tabular-uniform predicate (FR-003b, v1
// rules). It expects a value parsed from JSON with json.Number decoding
// (as EncodeBlock produces) and never mutates it.
//
// v1 rules:
//   - a JSON array of at least 4 objects;
//   - every row value scalar or null (no nested objects/arrays);
//   - every key appearing in any row must be present in at least 90% of rows
//     (the union key set = all keys, each >=90%-present; a key below the
//     tolerance makes the array too ragged);
//   - an object with exactly one key whose value is such an array
//     ("envelope") also qualifies;
//   - empty arrays and arrays of non-objects do not qualify;
//   - key order is irrelevant.
//
// The classifier is a pure predicate: it never encodes. The FR-004 size
// comparison in EncodeBlock is an independent backstop for "uniform enough"
// borderline cases.
func Classify(v interface{}) Classification {
	envelope := false
	if obj, ok := v.(map[string]interface{}); ok && len(obj) == 1 {
		for _, inner := range obj {
			if arr, ok := inner.([]interface{}); ok {
				v = arr
				envelope = true
			}
		}
	}

	arr, ok := v.([]interface{})
	if !ok {
		return Classification{Reason: ReasonNotArray}
	}

	rows := len(arr)
	if rows < 4 {
		return Classification{Envelope: envelope, Rows: rows, Reason: ReasonTooFewRows}
	}

	// Single ordered pass: count key presence and reject nested values.
	// Presence counting is order-independent, so map iteration inside a row
	// cannot affect the result (FR-011).
	keyCounts := make(map[string]int)
	for _, elem := range arr {
		row, ok := elem.(map[string]interface{})
		if !ok {
			return Classification{Envelope: envelope, Rows: rows, Reason: ReasonNonObjectElements}
		}
		for k, val := range row {
			switch val.(type) {
			case map[string]interface{}, []interface{}:
				return Classification{Envelope: envelope, Rows: rows, Reason: ReasonNestedValues}
			}
			keyCounts[k]++
		}
	}

	// Every key must be present in >=90% of rows (integer math: no floats).
	for _, count := range keyCounts {
		if count*10 < rows*9 {
			return Classification{Envelope: envelope, Rows: rows, Reason: ReasonTooRagged}
		}
	}
	cols := len(keyCounts)
	if cols == 0 {
		// All rows are empty objects — the union key set collapses.
		return Classification{Envelope: envelope, Rows: rows, Reason: ReasonTooRagged}
	}

	return Classification{Tabular: true, Envelope: envelope, Rows: rows, Cols: cols}
}
