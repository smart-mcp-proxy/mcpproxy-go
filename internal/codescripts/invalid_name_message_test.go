package codescripts

import (
	"strings"
	"testing"
)

// The refusal for a non-ASCII script name named a character that is not in
// the name. The validation loop walks bytes, and `string(name[i])` converts a
// byte to the code point of the same value, so the first byte of "語" (0xE8)
// was rendered as "è". The message is the body of an HTTP 400
// INVALID_SCRIPT_NAME (internal/httpapi/code_exec.go), so the caller is told
// to remove a character it never sent.
func TestInvalidNameErrorNamesTheRealCharacter(t *testing.T) {
	err := ValidateName("語")
	if err == nil {
		t.Fatal("a non-ASCII name must be refused")
	}
	msg := err.Error()
	if !strings.Contains(msg, "語") {
		t.Errorf("refusal does not name the offending character:\n%s", msg)
	}
	if strings.Contains(msg, "è") {
		t.Errorf("refusal names a Latin-1 character that is not in the name:\n%s", msg)
	}
}

// A malformed byte is not a character and must not be reported as one.
func TestInvalidNameErrorReportsMalformedByte(t *testing.T) {
	err := ValidateName("a\x80b")
	if err == nil {
		t.Fatal("a stray continuation byte must be refused")
	}
	if !strings.Contains(err.Error(), "byte 0x80") {
		t.Errorf("a stray byte is not a character and must be reported as a byte:\n%s", err.Error())
	}
}

// The echoed name is bounded at MaxNameLen+16 bytes with a raw byte slice, so
// a long non-ASCII name was cut inside a rune. %q then renders the dangling
// bytes as \xNN escapes, so the refusal shown to the caller ends in hex noise
// instead of the name it is quoting (#1647, #1664, #1665).
//
// The 1-byte padding covers all three offsets inside a 3-byte rune.
func TestInvalidNameErrorEchoIsNotCutInsideARune(t *testing.T) {
	for pad := 0; pad < 3; pad++ {
		name := strings.Repeat("a", pad) + strings.Repeat("\u8a9e", 60)

		err := ValidateName(name)
		if err == nil {
			t.Fatalf("pad=%d: an oversized name must be refused", pad)
		}
		if strings.Contains(err.Error(), `\x`) {
			t.Errorf("pad=%d: the echoed name was cut inside a rune: %s", pad, err.Error())
		}
	}
}

// ASCII refusals must not move.
func TestValidateNameASCIIMessagesUnchanged(t *testing.T) {
	cases := map[string]string{
		"":                      `invalid script name "": name is empty (names are 1-64 characters of A-Z, a-z, 0-9, '-' or '_' — a name, never a path)`,
		"bad/name":              `invalid script name "bad/name": character "/" is not allowed (names are 1-64 characters of A-Z, a-z, 0-9, '-' or '_' — a name, never a path)`,
		strings.Repeat("a", 65): `invalid script name "` + strings.Repeat("a", 65) + `": name is 65 characters long (names are 1-64 characters of A-Z, a-z, 0-9, '-' or '_' — a name, never a path)`,
	}
	for name, want := range cases {
		err := ValidateName(name)
		if err == nil {
			t.Fatalf("%q: expected a refusal", name)
		}
		if err.Error() != want {
			t.Errorf("%q:\n got %s\nwant %s", name, err.Error(), want)
		}
	}
	if err := ValidateName("good-name_1"); err != nil {
		t.Errorf("a valid name was refused: %v", err)
	}
}
