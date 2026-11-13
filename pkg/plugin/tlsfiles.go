package plugin

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"
	"time"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/view"
)

// maxTLSFileBytes is more than any file a TLS setting names holds: a CA
// bundle, the system's own running to a few hundred kilobytes, a certificate
// or a key. A file past it is not the one the setting meant, and read whole it
// would be memory spent on a mistyped path.
const maxTLSFileBytes = 4 << 20

// readTLSFile reads the file a TLS setting names: a regular file, of at most
// maxTLSFileBytes.
//
// Opened without blocking, and refused unless it is a regular file. A TLS
// setting is read before anything is dialled, where no timeout runs yet, and
// a named pipe in the file's place, or /dev/zero or a terminal named by
// mistake, held the call until it was cancelled. A directory is refused as
// one, so PathInput sends the reader back to the setting.
func readTLSFile(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	switch {
	case info.IsDir():
		return nil, &fs.PathError{Op: "read", Path: path, Err: syscall.EISDIR}
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("%s is not a regular file, and a pipe or a device is never read as one", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxTLSFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxTLSFileBytes {
		return nil, fmt.Errorf("%s is larger than %s, more than any certificate file holds", path, format.Bytes(maxTLSFileBytes))
	}
	return data, nil
}

// CAPool reads the PEM bundle the setting names into the pool a connection
// checks its server's certificate against, in place of the system's roots,
// or answers nil when the setting is empty: the system's roots. The setting is
// a path on the machine running the call, ~ expanded. A file that does not
// read is refused as <namespace>.tls.ca.unreadable, one that holds no PEM
// certificate as <namespace>.tls.ca.invalid, and one whose every certificate
// has expired as <namespace>.tls.ca.expired, each naming the setting the way
// the reader of req changes it.
//
// In place of the system's roots, never beside them: Go checks a certificate
// against RootCAs alone once it is set. That is what an operator naming a
// private CA means, and what Surface.CAHint tells one about to name it.
//
// Exported because every connection plugin read its CA file for itself, and
// the copies drifted on what a refusal said and on whether a pipe held the
// call.
func CAPool(req Request, namespace, setting string) (*x509.CertPool, *view.Error) {
	path := ExpandHome(req.String(setting))
	if path == "" {
		return nil, nil
	}
	sf := req.Surface()
	pem, err := readTLSFile(path)
	if err != nil {
		return nil, view.Errorf(namespace+".tls.ca.unreadable", "%v", err).
			WithHint(sf.SettingName(setting) + " is a path on this machine, read here rather than by the server").
			WithInput(PathInput(setting, err))
	}
	pool := x509.NewCertPool()
	// What the file has to hold, rather than a guess at what it held instead:
	// only a file with no PEM certificate in it reaches this line, a private
	// key or a DER-encoded certificate among them. A self-signed server's own
	// certificate is exactly what belongs here.
	if !pool.AppendCertsFromPEM(pem) {
		return nil, view.Errorf(namespace+".tls.ca.invalid", "%s holds no PEM certificate", path).
			WithHint(sf.SettingName(setting) + " wants a PEM certificate, the CA's or a self-signed server's own; " +
				"a private key or a DER-encoded certificate is not one").
			WithInput(setting)
	}
	// **A copy outlives the CA it was taken from.** A CA a cluster keeps renews
	// itself in place, and the copy on this machine keeps the dates it was
	// copied with. Once every certificate in it has expired no chain anchored
	// at it verifies — Go and OpenSSL both refuse an expired root — and the
	// handshake said "a certificate nothing here trusts", with a hint to name
	// the CA the operator had named. Refused before anything is dialled, by its
	// date and not its subject: a CA's subject can name a cluster and a
	// namespace, which a refusal handed to an agent does not.
	if last := lastExpiry(pem); !last.IsZero() && time.Now().After(last) {
		// The setting rather than the path, for a file written for this call
		// alone: the path names nothing once the call returns.
		where := path
		if req.Temporary(setting) {
			where = sf.SettingName(setting)
		}
		return nil, view.Errorf(namespace+".tls.ca.expired", "every certificate in %s has expired, the last on %s",
			where, last.UTC().Format(time.DateOnly)).
			WithHint(staleCopy(req, setting)).WithInput(setting)
	}
	return pool, nil
}

// staleCopy is the way out of a CA copy that has gone stale: the copy is
// exported again from where the CA is kept — or, when what expired is a
// server's own self-signed certificate, which the setting holds as its own
// CA, the certificate is renewed on the server, since exporting it again
// would bring back the same expired one.
//
// A CA the operator's profile reads from where it is kept, for each call
// (Request.Temporary), is no copy, and exporting it again changes nothing:
// what is kept there has expired, so whoever renews it is the one to ask.
func staleCopy(req Request, setting string) string {
	sf := req.Surface()
	if req.Temporary(setting) {
		return sf.SettingName(setting) + " is read for each call from where the CA is kept, and what is kept " +
			"there has expired: whoever renews that CA is the one to ask"
	}
	return sf.SettingName(setting) + " names a copy, and a CA that renews in place leaves its copies behind: " +
		"export the one in use again — or, when a server's own self-signed certificate is what expired, renew it there"
}

// lastExpiry is the latest end date among the certificates in a PEM bundle,
// zero when it holds none that parse: the bundle is good for as long as one of
// them is.
func lastExpiry(bundle []byte) time.Time {
	var last time.Time
	for _, c := range certificatesIn(bundle) {
		if c.NotAfter.After(last) {
			last = c.NotAfter
		}
	}
	return last
}

// certificatesIn is every certificate a PEM bundle holds that parses, in
// order, skipping a block of any other type.
func certificatesIn(bundle []byte) []*x509.Certificate {
	var out []*x509.Certificate
	for {
		var block *pem.Block
		block, bundle = pem.Decode(bundle)
		if block == nil {
			return out
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		if c, err := x509.ParseCertificate(block.Bytes); err == nil {
			out = append(out, c)
		}
	}
}
