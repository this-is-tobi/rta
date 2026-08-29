package git

import (
	"strings"
	"testing"

	gitconfig "github.com/go-git/go-git/v5/config"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// git's package doc says every capability is Read because "a repository's
// history and diffs are not credentials". That was true of seven of them and
// never of git.config, which reads the machine-wide scopes — where git keeps
// credentials by convention. The shapes below are the documented ones, not
// invented: GitLab publishes the `url.<base>.insteadOf` PAT rewrite, Azure
// DevOps publishes `http.<url>.extraHeader`, and `github.token` is what the
// GitHub CLI and hub have both written for years.

func TestCredentialsInConfigAreMasked(t *testing.T) {
	for _, tc := range []struct {
		name, key, value string
		wantGone         string
	}{
		{
			// GitLab's own documented pattern. The secret is in the KEY.
			name:     "PAT in a url.insteadOf subsection",
			key:      "url.https://oauth2:glpat-SECRET-TOKEN-abc123@gitlab.com/.insteadOf",
			value:    "https://gitlab.com/",
			wantGone: "glpat-SECRET-TOKEN-abc123",
		},
		{
			name:     "a legacy forge token",
			key:      "github.token",
			value:    "ghp_REALLYSECRETVALUE",
			wantGone: "ghp_REALLYSECRETVALUE",
		},
		{
			name:     "an Authorization header",
			key:      "http.https://dev.azure.com/.extraHeader",
			value:    "Authorization: Basic BASE64SECRETVALUE",
			wantGone: "BASE64SECRETVALUE",
		},
		{
			name:     "a password in a remote URL",
			key:      "remote.origin.url",
			value:    "https://bob:hunter2@git.internal/team/repo.git",
			wantGone: "hunter2",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := maskConfigValue(tc.key, tc.value)
			shown := maskURLCredentials(tc.key) + " = " + got
			if strings.Contains(shown, tc.wantGone) {
				t.Fatalf("the secret survived: %s", shown)
			}
			if !strings.Contains(shown, view.Mask) {
				t.Fatalf("nothing was masked, so nothing tells the reader something is hidden: %s", shown)
			}
		})
	}
}

func TestMaskingLeavesOrdinaryConfigAlone(t *testing.T) {
	// The other half. Masking that fires on everything is masking nobody
	// reads around, and this capability's whole job is answering "what is my
	// config".
	for _, tc := range []struct{ key, value string }{
		{"user.name", "Tobi"},
		{"user.email", "someone@example.com"},
		{"core.editor", "vim"},
		{"remote.origin.url", "git@github.com:owner/repo.git"},
		// A user with no password is identity, not a secret — masking it
		// would hide something that is not one.
		{"remote.origin.url", "ssh://git@git.internal/team/repo.git"},
		{"credential.helper", "osxkeychain"},
		{"init.defaultbranch", "main"},
	} {
		if got := maskConfigValue(tc.key, tc.value); got != tc.value {
			t.Errorf("%s: %q became %q", tc.key, tc.value, got)
		}
	}
}

func TestTheUsernameSurvivesMaskingButThePasswordDoesNot(t *testing.T) {
	// Naming who the credential belongs to is what makes the row worth
	// reading — and GitLab's pattern puts the constant "oauth2" there, so a
	// row that masked it would say nothing at all.
	got := maskURLCredentials("https://oauth2:glpat-abc@gitlab.com/")
	if !strings.Contains(got, "oauth2") {
		t.Errorf("the username was masked too: %s", got)
	}
	if strings.Contains(got, "glpat-abc") {
		t.Errorf("the token survived: %s", got)
	}
}

func TestMCPGetsTheRepositoryConfigAndNotTheMachinesOwn(t *testing.T) {
	// The scopes an agent may see. Machine-wide config is not the repository
	// — it is the operator's identity and, by git's own conventions, their
	// forge credentials — so it is withheld from MCP entirely rather than
	// masked and handed over. A person at their own terminal still gets it.
	dir, _ := testRepo(t)

	mcp := plugin.NewRequest(map[string]any{"path": dir}, false, false).
		WithSurface(plugin.SurfaceMCP)
	tbl := table(t, runConfig, mcp)
	for _, r := range tbl.Rows {
		if r[0] != "local" {
			t.Fatalf("an MCP caller was shown the %s scope: %v", r[0], r)
		}
	}

	// The control: the same call at a terminal is unchanged. Asserted as
	// "does not refuse and does not restrict itself to local by construction"
	// rather than by requiring a global config to exist, since the machine
	// running this may legitimately have none.
	cli := table(t, runConfig, req(t, dir, nil))
	if len(cli.Rows) < len(tbl.Rows) {
		t.Fatalf("the CLI saw fewer rows (%d) than MCP (%d)", len(cli.Rows), len(tbl.Rows))
	}
}

// addConfigRows is where the two halves meet, so the masking has to hold
// through the real row builder rather than only in the helpers.
func TestRowsBuiltFromAConfigCarryNoSecret(t *testing.T) {
	raw := gitconfig.NewConfig()
	raw.Raw.Section("github").SetOption("token", "ghp_LEAKME")
	raw.Raw.Section("url").Subsection("https://oauth2:glpat-LEAKME@gitlab.com/").
		SetOption("insteadOf", "https://gitlab.com/")

	var tbl view.Table
	addConfigRows(&tbl, "global", raw)
	all := ""
	for _, r := range tbl.Rows {
		all += strings.Join(r, " ") + "\n"
	}
	if len(tbl.Rows) == 0 {
		t.Fatal("no rows were produced, so this test proves nothing")
	}
	for _, secret := range []string{"ghp_LEAKME", "glpat-LEAKME"} {
		if strings.Contains(all, secret) {
			t.Errorf("%s reached the table:\n%s", secret, all)
		}
	}
}
