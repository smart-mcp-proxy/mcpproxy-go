package oauth

import "testing"

func TestExtractResourceMetadataURL_RFC7235Params(t *testing.T) {
	const want = "https://api.example.com/.well-known/oauth-protected-resource"
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"case-insensitive name", `Bearer Resource_Metadata="` + want + `"`, want},
		{"whitespace around equals", `Bearer error="invalid_token", resource_metadata = "` + want + `"`, want},
		{"other param with similar suffix", `Bearer x_resource_metadata="https://wrong.example", resource_metadata="` + want + `"`, want},
		{"mention inside another quoted value", `Bearer error_description="see resource_metadata=\"https://wrong.example\"", resource_metadata="` + want + `"`, want},
		{"only a lookalike param", `Bearer x_resource_metadata="https://wrong.example"`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractResourceMetadataURL(tt.header); got != tt.want {
				t.Errorf("ExtractResourceMetadataURL(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}
