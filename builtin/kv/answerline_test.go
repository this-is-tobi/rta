package kv

import (
	"io"
	"os"
	"strings"
	"testing"
)

// The first store's question reads its answer a byte at a time, so the
// passphrase prompt after it reads what was typed next and nothing before.
// It stopped at the 64th byte, though, and left the rest of a longer line —
// a passphrase typed at the question by mistake, say — for that prompt, which
// took the tail as the first answer. The whole line goes, its start is kept.
func TestTheFirstStoreQuestionReadsItsWholeLine(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	was := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = was; _ = r.Close() })
	go func() {
		_, _ = w.WriteString(strings.Repeat("y", 100) + "\nnext\n")
		_ = w.Close()
	}()

	answer, err := readAnswer("")
	if err != nil {
		t.Fatal(err)
	}
	if answer != strings.Repeat("y", 64) {
		t.Errorf("the answer kept is %d bytes, want the line's first 64", len(answer))
	}
	rest, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != "next\n" {
		t.Errorf("the prompt after the question would read %q, want only what was typed after the answer", rest)
	}
}
