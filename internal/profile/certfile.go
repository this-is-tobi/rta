package profile

import (
	"crypto/x509"
	"encoding/pem"
	"regexp"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// referenced is what a `secrets:` reference read for input f is handed on as.
// For a setting declared to name a file of certificates (plugin.FileCertificates),
// the certificates as that file's content, which the plugin's server writes for
// the call (plugin.FileContent); for any other input, the text read.
//
// **A CA read from where it is kept, for each call.** A CA a cluster keeps
// renews itself in place, and a copy exported to a file goes stale on the day
// the cluster renews it — CloudNativePG's own lasts ninety days. Read from the
// cluster's Secret for each call, the CA in use is the one checked against.
// Only certificates are handed on, and checked first: a block of anything else
// is refused, a private key above all, since a file is a place a key would be
// read from by more than the call that needed it; and a bundle whose every
// certificate has expired is refused by its date, since no chain anchored at
// it verifies and the refusal of the handshake would blame the server.
//
// **And PEM is refused on any other setting that is no credential.** Such a
// setting is read as text or as a path, and before this a CA mapped onto one
// reached the plugin as the PEM where a path belongs: the open failed on it,
// with the whole text in the refusal. A key mapped there was worse, the same
// sentence carrying the key.
func referenced(name string, c plugin.Capability, f plugin.Field, ref config.SecretRef, text string) (any, *view.Error) {
	from := ref.Scheme + ":" + ref.Ref
	if f.File == plugin.FileCertificates {
		return certificates(name, f, from, text)
	}
	if f.Type.Sensitive() || !strings.Contains(text, "-----BEGIN ") {
		return text, nil
	}
	if keyArmour.MatchString(text) {
		return nil, view.Errorf("core.profile.secret.key", "profile %q maps %s onto %s, which it reads as a PEM private key",
			name, from, f.Name).
			WithHint("rta writes no private key to a file and hands none over as text: keep " + f.Name +
				" the path of a file on this machine")
	}
	ns := plugin.Namespace(c.ID)
	return nil, view.Errorf("core.profile.secret.pem", "profile %q maps %s onto %s, which it reads as PEM, and %s "+
		"does not say %s names a file of it", name, from, f.Name, ns, f.Name).
		WithHint("a " + ns + " that declares " + f.Name + " a file of certificates is handed them in a file for each " +
			"call; until it does, write them to a file and name that file in set:")
}

// certificates is the certificates text holds, as the content of the file the
// setting f names, or why there are none to hand on.
func certificates(name string, f plugin.Field, from, text string) (any, *view.Error) {
	refuse := func(code, what, hint string) *view.Error {
		return view.Errorf(code, "profile %q maps %s onto %s, which %s", name, from, f.Name, what).WithHint(hint)
	}
	// By its armour, wherever it stands, before anything is decoded: a key
	// whose body does not decode is a key all the same, and one after a
	// certificate would otherwise end the reading there, unseen.
	if keyArmour.MatchString(text) {
		return nil, refuse("core.profile.secret.key", "it reads as a PEM private key where certificates belong",
			f.Name+" takes the certificates alone, the CA's .crt beside its key; rta writes no private key to a file")
	}
	var last time.Time
	var bundle []byte
	for rest := []byte(text); ; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return nil, refuse("core.profile.secret.certificate", "it reads as a PEM "+strings.ToLower(block.Type)+
				" where certificates belong", f.Name+" takes PEM certificates and nothing else")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, refuse("core.profile.secret.certificate", "it reads as a certificate that does not parse",
				"the key holds a certificate that is damaged, or is not a certificate")
		}
		// The certificate as it parsed, and nothing else the text held: what
		// lay between the blocks, a header, a block that did not decode, is
		// not written to the plugin's file.
		bundle = append(bundle, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes})...)
		if cert.NotAfter.After(last) {
			last = cert.NotAfter
		}
	}
	if len(bundle) == 0 {
		return nil, refuse("core.profile.secret.certificate", "it reads as no PEM certificate",
			f.Name+" takes PEM certificates — the CA's .crt, not a DER file or a value that only names one")
	}
	if time.Now().After(last) {
		return nil, refuse("core.profile.secret.expired", "it reads as certificates that have every one expired, the last on "+
			last.UTC().Format(time.DateOnly),
			"a CA that renews itself in place has not renewed this one; the operator that manages it is where to look")
	}
	return plugin.NewFileContent(bundle, from), nil
}

// keyArmour is the line a PEM private key opens with, of every kind —
// PRIVATE KEY, RSA, EC, ENCRYPTED, OPENSSH — matched on that line alone, so a
// key whose body does not decode is still one.
var keyArmour = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)
