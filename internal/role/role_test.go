package role

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseReadsTheGrantGrammar(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		want    Line
		wantErr string
	}{
		{raw: "kv.get", want: Line{Target: "kv.get"}},
		{raw: "kv.get db-password --ttl 1h", want: Line{Target: "kv.get", Scope: "db-password", TTL: "1h"}},
		{raw: "pg.query --profile staging --max-uses 3 --rate 10/1h --note deploys",
			want: Line{Target: "pg.query", Profile: "staging", MaxUses: 3, Rate: "10/1h", Note: "deploys"}},
		{raw: "--ttl 1h kv.get", want: Line{Target: "kv.get", TTL: "1h"}},
		{raw: "", wantErr: "names nothing"},
		{raw: "--ttl 1h", wantErr: "names no target"},
		{raw: "kv.get a b", wantErr: "more than a target and a record"},
		{raw: "kv.get --agent claude", wantErr: "the agent is given at issue"},
		{raw: "kv.get --server lab", wantErr: "unknown flag"},
	} {
		got, err := Parse(tc.raw)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("%q: err = %v, want %q", tc.raw, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", tc.raw, err)
			continue
		}
		got.Raw = ""
		if got != tc.want {
			t.Errorf("%q = %+v, want %+v", tc.raw, got, tc.want)
		}
	}
}

func isolate(t *testing.T) (dataDir, configDir, repo string) {
	t.Helper()
	dataDir, configDir, repo = t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("RTA_DATA_DIR", dataDir)
	t.Setenv("RTA_CONFIG", filepath.Join(configDir, "config.yaml"))
	t.Setenv("RTA_POLICY", "")
	t.Chdir(repo)
	return dataDir, configDir, repo
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The team's roles come from the policy file beside the ceiling; the
// operator's own from their config. Both are listed, and each says which.
func TestRolesComeFromBothFilesAndSayWhich(t *testing.T) {
	_, configDir, repo := isolate(t)
	write(t, filepath.Join(repo, ".rta-policy.yaml"), "maxTTL: 8h\nroles:\n  dev:\n    ttl: 4h\n    grants:\n      - kv.get\n      - note.add\n")
	write(t, filepath.Join(configDir, "config.yaml"), "roles:\n  mine:\n    grants: [sys.load]\n")
	all, verr := Available()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(all) != 2 || all[0].Name != "dev" || !all[0].Team || all[0].Role.TTL != "4h" ||
		all[1].Name != "mine" || all[1].Team || len(all[1].Role.Grants) != 1 {
		t.Fatalf("roles = %+v", all)
	}
	dev, verr := Find("dev")
	if verr != nil || len(dev.Role.Grants) != 2 {
		t.Fatalf("Find(dev) = %+v, %v", dev, verr)
	}
	if _, verr := Find("nope"); verr == nil || verr.Code != "role.unknown" {
		t.Fatalf("Find(nope) = %v", verr)
	}
}

func TestTheSameNameInTwoFilesIsRefusedRatherThanPicked(t *testing.T) {
	_, configDir, repo := isolate(t)
	write(t, filepath.Join(repo, ".rta-policy.yaml"), "roles:\n  dev:\n    grants: [kv.get]\n")
	write(t, filepath.Join(configDir, "config.yaml"), "roles:\n  dev:\n    grants: [kv]\n")
	_, verr := Find("dev")
	if verr == nil || verr.Code != "role.ambiguous" || !strings.Contains(verr.Message, ".rta-policy.yaml") ||
		!strings.Contains(verr.Message, "config.yaml") {
		t.Fatalf("Find(dev) = %v, want both files named", verr)
	}
}

func TestARoleNameIsALowercaseWord(t *testing.T) {
	_, configDir, _ := isolate(t)
	write(t, filepath.Join(configDir, "config.yaml"), "roles:\n  Dev Ops:\n    grants: [kv.get]\n")
	if _, verr := Available(); verr == nil || verr.Code != "role.badname" {
		t.Fatalf("Available = %v", verr)
	}
}

func TestAnEmptyRoleAndABadLineAreNamed(t *testing.T) {
	s := Source{Name: "dev", From: "/team/.rta-policy.yaml"}
	if _, err := s.Lines(); err == nil || !strings.Contains(err.Error(), "grants nothing") {
		t.Fatalf("empty role: %v", err)
	}
	s.Role.Grants = []string{"kv.get", "kv.get a b"}
	if _, err := s.Lines(); err == nil || !strings.Contains(err.Error(), `role "dev" in /team/.rta-policy.yaml`) {
		t.Fatalf("bad line: %v", err)
	}
}

// A file the operator named with RTA_POLICY is theirs, whatever it holds;
// only a file the walk found is the team's.
func TestAFileTheOperatorNamedIsNotTheTeams(t *testing.T) {
	_, _, repo := isolate(t)
	own := filepath.Join(repo, "mine.yaml")
	write(t, own, "roles:\n  dev:\n    grants: [kv.get]\n")
	t.Setenv("RTA_POLICY", own)
	all, verr := Available()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(all) != 1 || all[0].Team {
		t.Fatalf("roles = %+v, want one of the operator's own", all)
	}
}
