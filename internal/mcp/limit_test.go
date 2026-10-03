package mcp

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// slow hands a stream over a few bytes at a time, so a message arrives in
// pieces that end anywhere, inside a string and an escape included.
type slow struct {
	io.Reader
	step int
}

func (s slow) Read(p []byte) (int, error) { return s.Reader.Read(p[:min(len(p), s.step)]) }

func readAll(t *testing.T, r io.Reader) ([]byte, error) {
	t.Helper()
	return io.ReadAll(r)
}

func limitedOf(s string, max, step int) io.ReadCloser {
	return limitTo(io.NopCloser(slow{strings.NewReader(s), step}), max)
}

// The size of a message is what the decoder holds of it, from where it opens to
// where it closes: braces and quotes inside a string are not structure, an
// escaped quote does not end one, and white space between messages is free.
func TestAMessageWithinTheLimitPassesWhateverItHolds(t *testing.T) {
	msg := `{"jsonrpc":"2.0","id":1,"params":{"v":"a } ] \" { [ \\","w":[1,2,{"x":"y"}]}}`
	stream := msg + "\n  \n" + msg + "\r\n" + msg
	for _, step := range []int{1, 2, 3, 7, 64, 4096} {
		got, err := readAll(t, limitedOf(stream, len(msg), step))
		if err != nil || string(got) != stream {
			t.Fatalf("step %d: a stream of messages of exactly the limit was cut: %v (%d of %d bytes)",
				step, err, len(got), len(stream))
		}
	}
}

func TestAMessageOverTheLimitEndsTheStreamWithTheReason(t *testing.T) {
	msg := `{"v":"` + strings.Repeat("x", 100) + `"}`
	for _, step := range []int{1, 5, 4096} {
		got, err := readAll(t, limitedOf(msg, len(msg)-1, step))
		if err == nil || !strings.Contains(err.Error(), "a request over") {
			t.Fatalf("step %d: an oversized message ended with %v", step, err)
		}
		if len(got) >= len(msg) {
			t.Errorf("step %d: the whole oversized message (%d bytes) was handed on", step, len(got))
		}
	}
}

// Newlines inside a message are white space to the decoder, which does not read
// by lines, so a message cannot be made small by writing it across many.
func TestNewlinesInsideAMessageDoNotMakeItSmall(t *testing.T) {
	msg := "[\n" + strings.Repeat("\"xxxxxxxx\",\n", 200) + "1]"
	if _, err := readAll(t, limitedOf(msg, 1000, 4096)); err == nil {
		t.Fatal("a message of 2,000 bytes across 200 lines passed a limit of 1,000")
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
func (discard) Close() error                { return nil }

// The server over stdio ends the session on a request over the size the
// listener holds one to, and says why, instead of holding it.
func TestTheStdioServerEndsOnARequestOverTheLimit(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	server := NewServer(testRegistry(t), "test", Options{})
	in, out := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), server, &sdk.IOTransport{Reader: LimitRequests(in), Writer: discard{}}, 0, nil)
	}()
	go func() {
		_, _ = io.WriteString(out, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"x","arguments":{"v":"`)
		chunk := strings.Repeat("x", 1<<20)
		for range maxRequestBody/len(chunk) + 1 {
			if _, err := io.WriteString(out, chunk); err != nil {
				return
			}
		}
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "a request over") {
			t.Fatalf("the session ended with %v, want the limit named", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the server went on reading a request over the limit")
	}
}

func TestAnEndedStreamStaysEnded(t *testing.T) {
	r := limitedOf(`{"v":"`+strings.Repeat("x", 50)+`"}`, 10, 4096)
	buf := make([]byte, 64)
	if _, err := r.Read(buf); err == nil {
		t.Fatal("no error for an oversized message")
	}
	if n, err := r.Read(buf); err == nil || n != 0 {
		t.Fatalf("a stream that was ended gave %d bytes and %v again", n, err)
	}
}
