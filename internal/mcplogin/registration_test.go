package mcplogin

import (
	"strings"
	"testing"
	"time"
)

func TestValidateRegistrationOK(t *testing.T) {
	reg, rerr := ValidateRegistration(RegistrationRequest{
		RedirectURIs: []string{"http://127.0.0.1:1/cb"},
		ClientName:   "  My\x07 Client  ",
		SoftwareID:   "sw", SoftwareVersion: "1.0",
		TokenEndpointAuthMethod: "none",
		GrantTypes:              []string{"refresh_token", "authorization_code"},
		ResponseTypes:           []string{"code"},
	})
	if rerr != nil {
		t.Fatal(rerr)
	}
	if reg.ClientName != "my client" || reg.TokenEndpointAuthMethod != "none" ||
		len(reg.GrantTypes) != 2 || reg.GrantTypes[0] != "authorization_code" ||
		len(reg.ResponseTypes) != 1 || reg.ResponseTypes[0] != "code" {
		t.Fatalf("%+v", reg)
	}
	if _, rerr := ValidateRegistration(RegistrationRequest{RedirectURIs: []string{"https://a/cb"}}); rerr != nil {
		t.Fatal(rerr)
	}
}

func TestValidateRegistrationRefusals(t *testing.T) {
	base := func() RegistrationRequest { return RegistrationRequest{RedirectURIs: []string{"https://a/cb"}} }
	type tc struct {
		mod  func(*RegistrationRequest)
		code string
	}
	cases := map[string]tc{
		"no redirects":   {func(r *RegistrationRequest) { r.RedirectURIs = nil }, ErrCodeInvalidRedirectURI},
		"fragment":       {func(r *RegistrationRequest) { r.RedirectURIs = []string{"https://a/cb#x"} }, ErrCodeInvalidRedirectURI},
		"http non-loop":  {func(r *RegistrationRequest) { r.RedirectURIs = []string{"http://a/cb"} }, ErrCodeInvalidRedirectURI},
		"custom scheme":  {func(r *RegistrationRequest) { r.RedirectURIs = []string{"x://cb"} }, ErrCodeInvalidRedirectURI},
		"secret basic":   {func(r *RegistrationRequest) { r.TokenEndpointAuthMethod = "client_secret_basic" }, ErrCodeInvalidClientMetadata},
		"secret post":    {func(r *RegistrationRequest) { r.TokenEndpointAuthMethod = "client_secret_post" }, ErrCodeInvalidClientMetadata},
		"implicit grant": {func(r *RegistrationRequest) { r.GrantTypes = []string{"implicit"} }, ErrCodeInvalidClientMetadata},
		"client creds": {func(r *RegistrationRequest) {
			r.GrantTypes = []string{"authorization_code", "client_credentials"}
		}, ErrCodeInvalidClientMetadata},
		"token response": {func(r *RegistrationRequest) { r.ResponseTypes = []string{"token"} }, ErrCodeInvalidClientMetadata},
		"code+token":     {func(r *RegistrationRequest) { r.ResponseTypes = []string{"code", "token"} }, ErrCodeInvalidClientMetadata},
	}
	for name, c := range cases {
		r := base()
		c.mod(&r)
		_, rerr := ValidateRegistration(r)
		if rerr == nil || rerr.Code != c.code || rerr.Description == "" {
			t.Errorf("%s: %+v", name, rerr)
		}
	}
}

func TestValidateRegistrationCaps(t *testing.T) {
	r := RegistrationRequest{
		RedirectURIs: []string{"https://a/cb"}, ClientName: strings.Repeat("é", 200),
		SoftwareID: strings.Repeat("s", 200), SoftwareVersion: strings.Repeat("v", 200),
	}
	reg, rerr := ValidateRegistration(r)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if len(reg.ClientName) > 128 || len(reg.SoftwareID) > 64 || len(reg.SoftwareVersion) > 64 {
		t.Fatalf("caps: %d %d %d", len(reg.ClientName), len(reg.SoftwareID), len(reg.SoftwareVersion))
	}
	if !strings.HasPrefix(reg.ClientName, "é") || strings.ContainsRune(reg.ClientName, '\uFFFD') {
		t.Fatal("truncation must keep valid utf-8")
	}
}

func TestNewClientID(t *testing.T) {
	a, err := NewClientID()
	if err != nil || len(a) != 22 {
		t.Fatalf("%q %v", a, err)
	}
	b, _ := NewClientID()
	if a == b {
		t.Fatal("same id")
	}
}

func TestRegistrationResponse(t *testing.T) {
	reg, _ := ValidateRegistration(RegistrationRequest{RedirectURIs: []string{"https://a/cb"}, ClientName: "X", SoftwareID: " SW\x07", SoftwareVersion: "1.0"})
	reg.ClientID = "abc"
	m := reg.Response(time.Unix(1700000000, 0))
	if m["client_id"] != "abc" || m["client_id_issued_at"] != int64(1700000000) ||
		m["token_endpoint_auth_method"] != "none" || m["client_name"] != "x" {
		t.Fatalf("%v", m)
	}
	if m["software_id"] != "sw" || m["software_version"] != "1.0" {
		t.Fatalf("software fields: %v", m)
	}
	bare, _ := ValidateRegistration(RegistrationRequest{RedirectURIs: []string{"https://a/cb"}})
	if _, has := bare.Response(time.Unix(1, 0))["software_id"]; has {
		t.Fatal("empty software_id must be omitted")
	}
	if _, has := m["client_secret"]; has {
		t.Fatal("client_secret present")
	}
	if uris, _ := m["redirect_uris"].([]string); len(uris) != 1 {
		t.Fatalf("%v", m["redirect_uris"])
	}
}
