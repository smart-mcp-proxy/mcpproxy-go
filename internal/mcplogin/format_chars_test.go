package mcplogin

import "testing"

// The format characters are built from code points so the source stays readable.
var (
	rlo  = string(rune(0x202e)) // right-to-left override
	pdf  = string(rune(0x202c))
	lri  = string(rune(0x2066))
	pdi  = string(rune(0x2069))
	zwsp = string(rune(0x200b))
	zwj  = string(rune(0x200d))
	lrm  = string(rune(0x200e))
	rlm  = string(rune(0x200f))
	wj   = string(rune(0x2060))
	bom  = string(rune(0xfeff))
)

// Unicode format characters (category Cf: bidi overrides and isolates,
// zero-width characters, BOM) are dropped from every name the client reports,
// so a name cannot reorder or hide the text the consent page puts around it.
func TestValidateRegistrationDropsFormatCharacters(t *testing.T) {
	cases := []struct{ in, want string }{
		{"cursor (" + rlo + "x)", "cursor (x)"},
		{"cur" + zwsp + "sor", "cursor"},
		{"a" + lri + "b" + pdi + "c", "abc"},
		{bom + "name" + rlm, "name"},
		{"x" + wj + "y" + zwj + lrm + "z", "xyz"},
		{"plain éü ok", "plain éü ok"},
		{"cursor (" + rlo + pdf + "x)", "cursor (x)"},
	}
	for _, c := range cases {
		reg, rerr := ValidateRegistration(RegistrationRequest{RedirectURIs: []string{"https://a/cb"}, ClientName: c.in, SoftwareID: c.in})
		if rerr != nil {
			t.Fatal(rerr)
		}
		if reg.ClientName != c.want || reg.SoftwareID != c.want {
			t.Errorf("%q -> name %q software_id %q, want %q", c.in, reg.ClientName, reg.SoftwareID, c.want)
		}
	}
}

func TestStripFormat(t *testing.T) {
	if got := StripFormat("a" + rlo + "b" + zwsp + "c"); got != "abc" {
		t.Fatalf("%q", got)
	}
	if got := StripFormat("plain"); got != "plain" {
		t.Fatalf("%q", got)
	}
}
