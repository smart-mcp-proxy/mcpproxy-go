package shellwords

import (
	"reflect"
	"testing"
)

func TestSplit(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{"simple", "npx -y @modelcontextprotocol/server-filesystem /tmp",
			[]string{"npx", "-y", "@modelcontextprotocol/server-filesystem", "/tmp"}},
		{"double_quotes", `docker run -e API_KEY="abc def" -i --rm mcp/sqlite`,
			[]string{"docker", "run", "-e", "API_KEY=abc def", "-i", "--rm", "mcp/sqlite"}},
		{"single_quotes", `sh -c 'echo hello world'`,
			[]string{"sh", "-c", "echo hello world"}},
		{"extra_whitespace", "  uvx   mcp-server-fetch  ",
			[]string{"uvx", "mcp-server-fetch"}},
		{"empty", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Split(tc.input)
			if err != nil {
				t.Fatalf("Split(%q) error: %v", tc.input, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Split(%q) = %#v, want %#v", tc.input, got, tc.want)
			}
		})
	}
}

func TestSplitUnbalancedQuote(t *testing.T) {
	if _, err := Split(`sh -c "unterminated`); err == nil {
		t.Fatal("expected an error for an unbalanced quote")
	}
}
