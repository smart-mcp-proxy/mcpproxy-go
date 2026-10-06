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
		// F-M (#1398): backslash escapes, as a POSIX shell would.
		{"escaped_space", `cat my\ dir/file.txt`,
			[]string{"cat", "my dir/file.txt"}},
		{"escaped_quote_outside", `echo \"hi\" it\'s`,
			[]string{"echo", `"hi"`, "it's"}},
		{"escaped_double_quote_inside", `echo "say \"hi\""`,
			[]string{"echo", `say "hi"`}},
		{"escaped_backslash_inside_double", `echo "a\\b"`,
			[]string{"echo", `a\b`}},
		{"escaped_dollar_inside_double", `echo "\$HOME"`,
			[]string{"echo", "$HOME"}},
		{"backslash_literal_in_single_quotes", `echo 'a\ b\"'`,
			[]string{"echo", `a\ b\"`}},
		{"other_backslash_inside_double_kept", `echo "a\nb"`,
			[]string{"echo", `a\nb`}},
		// Windows paths must not be mangled: a backslash before an
		// ordinary character stays literal, and \\ outside quotes too.
		{"windows_path", `C:\Users\me\node.exe server.js`,
			[]string{`C:\Users\me\node.exe`, "server.js"}},
		{"unc_path", `node \\host\share\server.js`,
			[]string{"node", `\\host\share\server.js`}},
		{"trailing_lone_backslash_literal", `echo a\`,
			[]string{"echo", `a\`}},
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
