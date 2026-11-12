package plugin

import (
	"crypto/x509"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"

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
// read is refused as <namespace>.tls.ca.unreadable, and one that holds no PEM
// certificate as <namespace>.tls.ca.invalid, each naming the setting the way
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
	return pool, nil
}
