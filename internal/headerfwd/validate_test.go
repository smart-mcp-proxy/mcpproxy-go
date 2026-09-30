package headerfwd

import (
	"fmt"
	"reflect"
	"testing"
)

func TestValidateNames(t *testing.T) {
	many := make([]string, 33)
	for i := range many {
		many[i] = fmt.Sprintf("X-H%d", i)
	}
	cases := []struct {
		name   string
		in     []string
		static map[string]string
		bad    bool
	}{
		{"ok", []string{"X-User-Id", "x-tenant-id"}, nil, false},
		{"empty", nil, nil, false},
		{"invalid token", []string{"X User"}, nil, true},
		{"empty name", []string{""}, nil, true},
		{"wildcard", []string{"X-*"}, nil, true},
		{"too many", many, nil, true},
		{"exactly 32", many[:32], nil, false},
		{"duplicate", []string{"X-A", "x-a"}, nil, true},
		{"denied", []string{"Authorization"}, nil, true},
		{"denied prefix", []string{"X-Forwarded-For"}, nil, true},
		{"static collision", []string{"X-Key"}, map[string]string{"x-key": "s"}, true},
	}
	for _, c := range cases {
		errs := ValidateNames(c.in, c.static)
		if (len(errs) > 0) != c.bad {
			t.Errorf("%s: errs=%v, want bad=%v", c.name, errs, c.bad)
		}
	}
}

func TestNormalizeNames(t *testing.T) {
	kept, dropped := NormalizeNames([]string{"x-user-id", "X-USER-ID", "Authorization", "X-*", "bad name", "x-tenant-id"})
	if want := []string{"X-User-Id", "X-Tenant-Id"}; !reflect.DeepEqual(kept, want) {
		t.Errorf("kept = %v, want %v", kept, want)
	}
	if len(dropped) != 4 {
		t.Errorf("dropped = %v, want 4 entries", dropped)
	}
}
