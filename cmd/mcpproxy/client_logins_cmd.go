package main

// Spec 132 PR-2b: the credentials of a client in `client show`, and
// `client revoke-login`. Both are thin clients of the /clients routes; the
// authorization (administrator only) lives server-side.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// clientCredentialRow is one entry of a client's `credentials` list.
type clientCredentialRow struct {
	Kind         string     `json:"kind"`
	Prefix       string     `json:"prefix"`
	GrantID      string     `json:"grant_id"`
	State        string     `json:"state"`
	ExpiresAt    *time.Time `json:"expires_at"`
	ApprovedAt   *time.Time `json:"approved_at"`
	LastUsedAt   *time.Time `json:"last_used_at"`
	ReportedName string     `json:"reported_name"`
	RedirectHost string     `json:"redirect_host"`
	ObservedName string     `json:"observed_name"`
	NameMismatch bool       `json:"name_mismatch"`
}

func timeCell(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return t.Format(time.RFC3339)
}

// printClientCredentials renders the credentials block of `client show`. It
// prints nothing while the payload has no credentials list, so an install that
// never used browser login shows exactly what it showed before. The reported
// and observed names come from the client, so they are escaped for the
// terminal.
func printClientCredentials(creds []clientCredentialRow, loginDisabled *bool) error {
	if creds == nil && loginDisabled == nil {
		return nil
	}
	fmt.Println("Credentials:")
	if len(creds) == 0 {
		fmt.Println("  none")
	} else {
		rows := make([][]string, 0, len(creds))
		for _, c := range creds {
			id := c.GrantID
			if id == "" {
				id = c.Prefix
			}
			detail := "-"
			if c.Kind == "browser_login" {
				detail = fmt.Sprintf("%s via %s", dashCell(sanitizeCell(c.ReportedName, 40)), dashCell(sanitizeCell(c.RedirectHost, 40)))
			}
			rows = append(rows, []string{c.Kind, sanitizeCell(id, 40), c.State, timeCell(c.ApprovedAt), timeCell(c.LastUsedAt), detail})
		}
		if err := printTable([]string{"KIND", "ID", "STATE", "APPROVED", "LAST USED", "NAME VIA HOST"}, rows); err != nil {
			return err
		}
	}
	for _, c := range creds {
		if c.Kind == "browser_login" && c.NameMismatch {
			fmt.Printf("Warning: login %s reported the name %q but its client introduced itself as %q\n",
				sanitizeCell(c.GrantID, 40), sanitizeCell(c.ReportedName, 60), sanitizeCell(c.ObservedName, 60))
		}
	}
	if loginDisabled != nil && *loginDisabled {
		fmt.Println("Browser login: disabled (existing logins are listed; none can sign in)")
	}
	return nil
}

func dashCell(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func newClientRevokeLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke-login <id> <grant-id>",
		Short: "Revoke one browser login of a client",
		Long: `Revoke one browser login (a sign-in a person approved on the consent page).
It stops working on its next request. The client's API key, its other logins and
its profile binding stay. Find the grant id with: mcpproxy client show <id>

Examples:
  mcpproxy client revoke-login cursor 3f9a1c2e7b44`,
		Args: argsOrHelpJSON(cobra.ExactArgs(2)),
		RunE: func(_ *cobra.Command, args []string) error {
			// A blank id would build a path ending in a slash: refuse it here.
			if strings.TrimSpace(args[0]) == "" || strings.TrimSpace(args[1]) == "" {
				return flagValidationError{fmt.Errorf("both the client id and the grant id are required")}
			}
			sess, err := newRESTSession("client")
			if err != nil {
				return err
			}
			data, ref, err := sess.call(http.MethodDelete, clientPath(args[0])+"/logins/"+url.PathEscape(args[1]), nil)
			if e := clientRefusal(ref, err, args[0], ""); e != nil {
				return e
			}
			if structuredOutput() {
				return printData(data)
			}
			var res struct {
				Changed bool `json:"changed"`
			}
			if err := json.Unmarshal(data, &res); err != nil {
				return err
			}
			if res.Changed {
				fmt.Printf("Revoked login %s of %s\n", args[1], args[0])
			} else {
				fmt.Printf("Login %s of %s was already revoked\n", args[1], args[0])
			}
			return nil
		},
	}
}
