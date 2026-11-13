package plugin

import (
	"bytes"
	"fmt"
	"testing"
)

// What the host read for a call is held as it was read, prints as where it
// was read from, and never as what it holds.
func TestAFilesContentPrintsAsWhereItWasRead(t *testing.T) {
	content := []byte("-----BEGIN CERTIFICATE-----\nMIIBdemo\n-----END CERTIFICATE-----\n")
	f := NewFileContent(content, "kube:pg-ca/ca.crt")
	content[0] = 'X'
	if got := f.Bytes(); got[0] != '-' {
		t.Error("the content changed with the slice it was made from")
	}
	f.Bytes()[0] = 'Y'
	if !bytes.HasPrefix(f.Bytes(), []byte("-----BEGIN")) {
		t.Error("the content changed with a slice Bytes returned")
	}
	if fmt.Sprint(f) != "kube:pg-ca/ca.crt" || fmt.Sprintf("%v", f) != "kube:pg-ca/ca.crt" {
		t.Errorf("printed as %q", fmt.Sprint(f))
	}
}

// The host's check holds a file's content to the setting declared to name
// one: anywhere else it is a value of the wrong shape, refused as one.
func TestAFilesContentIsTakenOnlyByASettingNamingOne(t *testing.T) {
	c := Capability{ID: "demo.status", Summary: "s", Safety: Read, Inputs: []Field{
		{Name: "ca-file", Type: String, Help: "CA", Local: true, Config: "ca-file", File: FileCertificates},
		{Name: "host", Type: String, Help: "host", Local: true, Config: "host"},
	}}
	content := NewFileContent([]byte("x"), "kube:pg-ca/ca.crt")
	if verr := CheckInputs(c, NewRequest(map[string]any{"ca-file": content}, false, false)); verr != nil {
		t.Errorf("on the CA setting: %+v", verr)
	}
	if verr := CheckInputs(c, NewRequest(map[string]any{"host": content}, false, false)); verr == nil || verr.Code != "core.input.type" {
		t.Errorf("on the host: %+v", verr)
	}
}

// A value a page lays over one written for the call no longer names that
// file, so it is no longer Temporary; the rest stay so.
func TestAValueLaidOverATemporaryFileIsNotTemporary(t *testing.T) {
	req := NewRequest(map[string]any{"ca-file": "/tmp/x/ca-file.pem", "cert-file": "/tmp/x/c.pem"}, false, false).
		WithTemporary("ca-file", "cert-file").
		With(map[string]any{"ca-file": "/srv/ca.crt"})
	if req.Temporary("ca-file") || !req.Temporary("cert-file") || req.Temporary("host") {
		t.Errorf("ca-file %v, cert-file %v, host %v", req.Temporary("ca-file"), req.Temporary("cert-file"), req.Temporary("host"))
	}
}
