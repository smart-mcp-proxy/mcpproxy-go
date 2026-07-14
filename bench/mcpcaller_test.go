package bench

import "testing"

// Spec 085 US5 T041 — the live compact arm parses real retrieve_tools MCP
// responses in both modes: full entries carry "name", compact entries carry
// "id" (Spec 085 FR-002). Ranked order must be preserved verbatim.

func TestParseRetrieveResponse_FullMode(t *testing.T) {
	body := `{"tools":[
		{"name":"fs:read_file","score":0.9,"inputSchema":{"type":"object"}},
		{"name":"fs:list_directory","score":0.5,"inputSchema":{"type":"object"}}
	],"total":2}`
	ids, err := parseRetrieveResponse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{"fs:read_file", "fs:list_directory"}
	if !equalIDs(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
}

func TestParseRetrieveResponse_CompactMode(t *testing.T) {
	body := `{"tools":[
		{"id":"fs:read_file","score":0.9,"sig":"(path*:str)","desc":"Read a file.","lossy":false},
		{"id":"gh:create_issue","score":0.4,"sig":"(title*:str, meta~:obj)","desc":"Create.","lossy":true}
	],"total":2,"hint":"sig legend: ..."}`
	ids, err := parseRetrieveResponse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{"fs:read_file", "gh:create_issue"}
	if !equalIDs(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
}

func TestParseRetrieveResponse_Malformed(t *testing.T) {
	if _, err := parseRetrieveResponse("not json"); err == nil {
		t.Fatal("malformed body must error")
	}
	ids, err := parseRetrieveResponse(`{"tools":[]}`)
	if err != nil || len(ids) != 0 {
		t.Fatalf("empty tools list is valid: ids=%v err=%v", ids, err)
	}
}
