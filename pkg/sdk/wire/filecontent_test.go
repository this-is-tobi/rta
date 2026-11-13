package wire

import (
	"bytes"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The content of a file a setting names crosses as it is, and where the host
// read it from does not cross at all: which Secret holds a CA is not a
// plugin's to learn, nor to repeat to an agent in a refusal.
func TestAFilesContentCrossesAndWhereItWasReadDoesNot(t *testing.T) {
	content := []byte("-----BEGIN CERTIFICATE-----\nMIIBdemo\n-----END CERTIFICATE-----\n")
	encoded, err := proto.Marshal(ValueToProto(plugin.NewFileContent(content, "kube:pg-ca/ca.crt")))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("pg-ca")) {
		t.Error("the reference the content was read by crossed the wire")
	}
	got, ok := ValuesFromProto(ValuesToProto(map[string]any{"ca-file": plugin.NewFileContent(content, "kube:pg-ca/ca.crt")}))["ca-file"].(plugin.FileContent)
	if !ok || !bytes.Equal(got.Bytes(), content) || got.String() != "a file read for this call" {
		t.Errorf("came back as %#v", got)
	}
}
