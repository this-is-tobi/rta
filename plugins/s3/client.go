package main

import (
	"context"
	"crypto/x509"
	"errors"
	stdnet "net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// connFields are the inputs every capability here shares.
//
// endpoint/region/tls default to a local MinIO's own defaults (127.0.0.1:9000,
// plain HTTP) rather than a real AWS endpoint, the same reason plugins/vault
// defaults address to a local `vault server -dev` — zero config should reach
// something a person can actually stand up and try this against, not a SaaS
// endpoint that needs an account first. Real S3 (or R2, or Ceph) is an
// operator's own config: endpoint: s3.amazonaws.com, tls: true.
//
// access-key mirrors pg's `user`: identifying, not secret, Config-settable.
// secret-key mirrors pg's `password` and vault's `token`: Secret, Local, and
// EnvFallback, so an MCP server resolves it from RTA_S3_SECRET_KEY rather
// than an agent ever supplying or inventing one.
// Every field here is Local, and that is the security property rather than a
// detail — the same reasoning plugins/pg's own connFields documents at
// length. Together they name which endpoint this call
// reaches and as whom, and an MCP caller may not choose that: an agent that
// could set `endpoint` could point rta at a bucket it controls and have the
// host supply $RTA_S3_SECRET_KEY beside it. They remain Config-backed and
// remain ordinary flags for a person at a terminal.
func connFields() []plugin.Field {
	return []plugin.Field{
		// One input holding host[:port], which is the address role. This is
		// the shape that made a two-role Host/Port scheme unworkable and the
		// reason the roles are what they are.
		{Name: "endpoint", Type: plugin.String, Default: "127.0.0.1:9000", Config: "endpoint",
			Local: true, Endpoint: plugin.EndpointAddress, Help: "S3-compatible endpoint, host[:port]"},
		{Name: "region", Type: plugin.String, Default: "us-east-1", Config: "region",
			Local: true, Help: "bucket region"},
		// Local for the downgrade reason rather than the redirect one: an
		// agent that could set this could ask for plaintext against an
		// endpoint the operator configured as HTTPS.
		{Name: "tls", Type: plugin.Bool, Default: false, Config: "tls",
			Local: true, Endpoint: plugin.EndpointTLS,
			Help: "use HTTPS (a local MinIO ordinarily does not)"},
		{Name: "access-key", Type: plugin.String, Config: "access-key",
			Local: true, Help: "access key ID"},
		{Name: "secret-key", Type: plugin.Secret, Local: true, EnvFallback: true,
			Help: "secret access key"},
		// Local for the same reason plugins/etcd's own ca-file is: it is read
		// off this machine's disk, not held as a value rta's own store could
		// manage. Not a plugin.Secret, and deliberately: a CA certificate is
		// the public half of a key pair — it is what a CA hands out for
		// wide distribution so anyone can verify what it signed, the same
		// reason a browser or an OS trust store ships thousands of them in
		// the clear. Nothing about it needs secrecy; ca-file only needs Local
		// for the same file-read-primitive reason address does, not because
		// its contents are sensitive.
		{Name: "ca-file", Type: plugin.String, Default: "", Config: "ca-file",
			Local: true, Help: "PEM bundle to verify the server against, beyond the host's own trust store"},
	}
}

// connect opens a client. minio-go's New never itself dials the network — it
// only parses the endpoint and builds the signer — so a bad endpoint or a
// dead server surfaces on the first real call, classified the same way any
// other request failure is.
func connect(req plugin.Request) (*minio.Client, *view.Error) {
	endpoint := req.String("endpoint")
	access := req.String("access-key")
	secret := req.String("secret-key")
	opts := &minio.Options{
		Creds: credentials.NewStaticV4(access, secret, ""),
		// ca-file only means anything over TLS, so setting it turns TLS on
		// the same way etcd's own ca-file does — the alternative is a value
		// that silently does nothing until --tls is also typed.
		Secure: req.Bool("tls") || req.String("ca-file") != "",
		Region: req.String("region"),
	}
	if ca := req.String("ca-file"); ca != "" {
		transport, verr := caTransport(ca)
		if verr != nil {
			return nil, verr
		}
		opts.Transport = transport
	}
	client, err := minio.New(endpoint, opts)
	if err != nil {
		return nil, view.Errorf("s3.conn.invalid", "%s: %v", endpoint, err).
			WithHint("endpoint is host[:port] with no scheme — set --tls separately")
	}
	return client, nil
}

// caTransport is minio-go's own default HTTPS transport (proxying, idle
// connection pooling, timeouts — the same one Secure:true would have built
// anyway) with its trust replaced by ca-file's bundle rather than the host's
// system trust store, the same full-replacement etcd's and vault's own
// ca-file already give: an operator naming a private CA means exactly that
// CA, not that CA in addition to the public web PKI.
func caTransport(ca string) (*http.Transport, *view.Error) {
	pem, err := os.ReadFile(ca)
	if err != nil {
		return nil, view.Errorf("s3.tls.ca.unreadable", "%v", err).
			WithHint("ca-file is a path on this machine, read by rta rather than by the server")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, view.Errorf("s3.tls.ca.invalid", "%s holds no PEM certificate", ca).
			WithHint("this wants the CA bundle, not the server's own certificate")
	}
	transport, err := minio.DefaultTransport(true)
	if err != nil {
		return nil, view.Errorf("s3.tls.transport", "%v", err)
	}
	transport.TLSClientConfig.RootCAs = pool
	return transport, nil
}

// classify turns a client error into something an operator can act on.
//
// minio-go returns its own errors as a value type, minio.ErrorResponse (not
// a pointer — confirmed against the vendored source's own ToErrorResponse,
// which type-switches on the bare value rather than a pointer). errors.As
// still works against a value type target: it compares the target's type,
// not its kind, against each error in the chain. Anything that never got a
// response at all — the connection itself failing — comes back as whatever
// the standard http.Client produced, the same net.OpError/net.DNSError/
// *url.Error family plugins/pg and plugins/vault already classify.
func classify(err error, req plugin.Request) *view.Error {
	where := req.String("endpoint")

	var errResp minio.ErrorResponse
	if errors.As(err, &errResp) {
		switch errResp.Code {
		case minio.NoSuchBucket:
			return view.Errorf("s3.bucket.notfound", "%s has no bucket %q", where, errResp.BucketName).
				WithHint("`rta s3 bucket list` shows what is there")
		case minio.NoSuchKey:
			return view.Errorf("s3.object.notfound", "no object %q in %q", errResp.Key, errResp.BucketName).
				WithHint("`rta s3 object list " + errResp.BucketName + "` shows what is there")
		case minio.NoSuchBucketPolicy:
			return view.Errorf("s3.policy.notfound", "%q has no bucket policy set", errResp.BucketName).
				WithHint("an absent policy is not the same as a deny-all one — access still follows IAM/bucket ACLs")
		case minio.AccessDenied:
			return view.Errorf("s3.denied", "%s refused: %s", where, errResp.Message).
				WithHint("the credentials are valid but not authorized for this — check the bucket policy or IAM")
		case minio.InvalidAccessKeyID, minio.SignatureDoesNotMatch:
			return view.Errorf("s3.auth.failed", "%s rejected the credentials", where).
				WithHint("set $" + plugin.LocalEnvVar("s3.overview", "secret-key") + ", or check --access-key")
		case minio.BucketAlreadyExists, minio.BucketAlreadyOwnedByYou:
			return view.Errorf("s3.bucket.exists", "%q already exists", errResp.BucketName).
				WithHint("`rta s3 bucket list` shows who owns what this plugin can see")
		}
		return view.Errorf("s3.request.failed", "%s: %s", errResp.Code, errResp.Message).
			WithHint("`rta explain s3.overview` lists every input and where each one can come from")
	}

	var netErr *stdnet.OpError
	if errors.As(err, &netErr) || strings.Contains(err.Error(), "connection refused") {
		return view.Errorf("s3.conn.refused", "nothing is listening on %s", where).
			WithHint("is the server up, and is --endpoint right?")
	}
	var dnsErr *stdnet.DNSError
	if errors.As(err, &dnsErr) {
		return view.Errorf("s3.host.unknown", "no address for %q", where).
			WithHint("`rta net dns " + hostOnly(where) + "` shows what DNS returns")
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return view.Errorf("s3.conn.timeout", "%s did not answer in time", where).
			WithHint("a firewall that drops rather than refuses looks exactly like this")
	}
	var certErr x509.UnknownAuthorityError
	if errors.As(err, &certErr) {
		return view.Errorf("s3.tls.untrusted", "%s presented a certificate nothing here trusts", where).
			WithHint("a local MinIO's self-signed cert needs --tls=false for a real try, or its CA " +
				"trusted with ca-file for the real thing")
	}
	return view.Errorf("s3.conn.failed", "could not reach %s: %v", where, err).
		WithHint("`rta explain s3.overview` lists every input and where each one can come from")
}

func hostOnly(endpoint string) string {
	host, _, err := stdnet.SplitHostPort(endpoint)
	if err != nil {
		return endpoint
	}
	return host
}

// withClient is the shape every capability here has: connect, or return the
// classified error; run.
func withClient(req plugin.Request, fn func(context.Context, *minio.Client) (view.View, error)) (view.View, error) {
	client, verr := connect(req)
	if verr != nil {
		return nil, verr
	}
	return fn(context.Background(), client)
}

// cap builds a capability with the shared connection inputs appended, so no
// declaration here can forget one and no two can disagree about a default —
// the same helper plugins/pg and plugins/vault document at length. Every
// capability here reaches off the box for the same reason theirs do, so
// every one is NoPreview for the same reason: the automatic dashboard runs
// Read capabilities unasked, and a live bucket is not something this plugin
// gets to decide, on its own, is fine to poll every few seconds. An operator
// who has looked at their own deployment and decided otherwise still can —
// dashboard.tiles accepts any capability regardless of NoPreview, because
// naming one in a config file is the asking.
func cap(c plugin.Capability, own ...plugin.Field) plugin.Capability {
	c.Inputs = append(own, connFields()...)
	c.NoPreview = true
	return c
}
