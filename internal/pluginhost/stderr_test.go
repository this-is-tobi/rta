package pluginhost

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// A plugin's stderr reaches the operator's terminal as JSON, which makes its
// control bytes data — and the JSON escapes every character a terminal acts
// on, not only the ones encoding/json does. hclog encodes with encoding/json,
// which escapes C0 and writes DEL, the C1 controls and the characters that
// reorder text as they came, so a plugin logging "[ERROR] " and an 8-bit OSC,
// or a server-supplied name holding an override, put it on the terminal on
// every command that loaded it. go-plugin logs a line beginning [ERROR] at
// Error, the level the logger lets through.
func TestPluginStderrEscapesWhatATerminalActsOn(t *testing.T) {
	acted := []rune{0x7f, 0x9d, 0x9c, 0x9b, 0x85, 0x202e, 0x2066}
	var msg strings.Builder
	msg.WriteString("[ERROR] ")
	for _, r := range acted {
		msg.WriteString("x" + string(r))
	}
	msg.WriteString(" \x1b]52;c;Y3VybA==\x07 caf\xc3\xa9")

	var out bytes.Buffer
	pluginLogger("plugin.test", escapeActedOn(&out)).Error(msg.String())

	line := bytes.TrimSpace(out.Bytes())
	for _, r := range acted {
		if bytes.ContainsRune(line, r) {
			t.Errorf("U+%04X went out raw: %q", r, line)
		}
	}
	if !json.Valid(line) {
		t.Fatalf("the line is no longer JSON: %q", line)
	}
	var entry struct {
		Message string `json:"@message"`
	}
	if err := json.Unmarshal(line, &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Message != msg.String() {
		t.Errorf("decoded message = %q, want what the plugin wrote, %q", entry.Message, msg.String())
	}
}
