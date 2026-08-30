package app

import (
	"strings"
	"testing"
)

// **A secret typed under `set:` must not be printed back.**
//
// `secrets:` holds a reference and `set:` holds a value, so a password in the
// wrong block is inert — nothing reads it, and `profile show` already says so
// in its problems row. What it is not is harmless. The config file is written
// 0644 because it is documented to hold no secrets, and this command exists to
// be read and pasted; printing the literal turns one wrong line in a file into
// a credential on a screen and in a ticket.
//
// It is a mistake worth expecting rather than an exotic one. There is no
// command that writes a profile, so the block is hand-written from the
// documentation, and `set:` is the obvious-looking place to put a value.
func TestALiteralSecretUnderSetIsNotPrintedBack(t *testing.T) {
	const cfg = `profiles:
  staging:
    plugins:
      db:
        set:
          host: db.internal
          password: hunter2-in-the-wrong-block
`
	out, _, err := runWith(t, connRegistry(t), cfg, "profile", "show", "staging")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(out, "hunter2-in-the-wrong-block") {
		t.Errorf("the literal secret was printed back:\n%s", out)
	}
	if !strings.Contains(out, "redacted") {
		t.Errorf("the value was dropped rather than marked — somebody has to know it is there:\n%s", out)
	}
	// Redaction must be by the plugin's own declaration, not by blanking the
	// whole block: a host is not a secret and hiding it would make this command
	// useless for the thing it is actually for.
	if !strings.Contains(out, "db.internal") {
		t.Errorf("a non-secret was redacted too:\n%s", out)
	}
}

// The same, through --output json. That is the form that gets pasted, and a
// redaction applied only to the pretty renderer would miss the path where it
// matters most.
func TestTheRedactionSurvivesEveryOutputFormat(t *testing.T) {
	const cfg = `profiles:
  staging:
    plugins:
      db:
        set:
          password: hunter2-in-the-wrong-block
`
	// csv is not in the list because it renders tables only and refuses a
	// key/value view outright — a refusal is not a disclosure.
	for _, format := range []string{"json", "yaml", "md"} {
		out, _, err := runWith(t, connRegistry(t), cfg, "profile", "show", "staging", "-o", format)
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if strings.Contains(out, "hunter2-in-the-wrong-block") {
			t.Errorf("--output %s printed the literal secret:\n%s", format, out)
		}
	}
}
