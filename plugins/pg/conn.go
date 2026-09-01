package main

import (
	"context"
	"crypto/x509"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// connFields are the inputs every capability here shares.
//
// Each one declares a Config key, which is the whole reason config keys exist:
// an operator states the connection once, in their own file, and never types
// it again. The handler reads req.String("host") and cannot tell whether that
// came from a flag, the config file or the declared default — which is the
// point, not an accident of the API.
//
// **Every one of them is also Local, and that is a security property rather
// than a detail**. Together they name *which server this
// call reaches and as whom*, and an MCP caller may not choose that. They
// were ordinary inputs until a design review found what that meant: an
// input a plugin declares is published in the MCP tool schema and accepted
// from a caller, and plugin.Resolve applies caller values last — above
// config, above the host's own environment — so an agent could name any
// database it liked and have rta fill $RTA_PG_PASSWORD in beside it,
// pointing a real credential at a machine the agent chose. Local closes it
// with no contract change: config still fills these, a person at a terminal
// still passes them as ordinary flags, and the one surface that must not
// choose them no longer can.
//
// The password differs only in also declaring EnvFallback, and that
// distinction is deliberate: EnvFallback is for values that genuinely
// are credentials, so the host resolves it from $RTA_PG_PASSWORD and
// `rta explain` prints that variable name. A field that merely chooses a
// destination must come from an explicit caller or from config, never from
// an ambient variable the MCP server happened to inherit.
func connFields() []plugin.Field {
	return []plugin.Field{
		// host and port carry the endpoint roles, so a profile naming a
		// cluster reaches this database through a port-forward the host opens
		// and closes: pg never learns a forward was there, which is the tunnel
		// contract and the reason none of the code below changes.
		{Name: "host", Type: plugin.String, Default: "localhost", Config: "host",
			Local: true, Endpoint: plugin.EndpointHost, Help: "database host"},
		{Name: "port", Type: plugin.Int, Default: 5432, Config: "port",
			Local: true, Endpoint: plugin.EndpointPort, Min: 1, Max: 65535, Help: "database port"},
		{Name: "user", Type: plugin.String, Default: "postgres", Config: "user",
			Local: true, Help: "role to connect as"},
		{Name: "database", Type: plugin.String, Default: "postgres", Config: "database",
			Local: true, Help: "database to connect to"},
		// Local for a slightly different reason than the four above: it does
		// not change *where* the call goes, it changes whether the transport
		// is protected. An agent that could set it could ask for `disable`
		// and downgrade a connection the operator configured as verify-full.
		// The tls role, and it is not a downgrade to argue about — it is
		// measured. Through a port-forward, `prefer` kills the forward on the
		// *clean disconnect*: PostgreSQL closes, the TLS layer's trailing
		// close_notify arrives at a socket that is already gone, the pod-side
		// read resets, and kubectl exits. The next call gets "connection
		// refused" on a local port and nothing connects the two. It buys
		// nothing either, since the forward is loopback and the hop that leaves
		// the machine is already inside the API server's TLS.
		//
		// Only when a tunnel is actually open. Every other call keeps `prefer`,
		// and a caller who says otherwise still wins.
		{Name: "sslmode", Type: plugin.String, Default: "prefer", Config: "sslmode",
			Local:    true,
			Endpoint: plugin.EndpointTLS,
			Options:  []string{"disable", "prefer", "require", "verify-ca", "verify-full"},
			Help:     "TLS negotiation mode"},
		{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true,
			Help: "password for the role"},
		// Local for the same reason plugins/etcd's own ca-file is: it is read
		// off this machine's disk. Named sslrootcert rather than etcd's
		// ca-file on purpose — unlike vault, which invented no libpq-shaped
		// word to mirror, sslmode above already commits this plugin to
		// libpq's own vocabulary, and sslrootcert is libpq's own keyword for
		// exactly this (jackc/pgx's pgconn.configTLS reads it directly,
		// alongside sslcert/sslkey for a client pair this plugin does not
		// expose). Not a plugin.Secret: a CA certificate is the public half
		// of a key pair, the half a CA hands out for wide distribution so
		// anyone can verify what it signed.
		//
		// **Does not itself change sslmode.** sslmode's own default,
		// prefer, tells pgx to skip verification regardless of what this
		// names — see its Help. An operator may want the CA filled in
		// before deciding how strict to be about it, and elevating sslmode
		// as a side effect of a different field would be a second,
		// undocumented way its value changes — worth documenting loudly
		// instead of working around silently.
		//
		// TLSAdjacent for the harder half of the same fact: under a tunnel,
		// sslmode is not merely left at prefer, it is forced to disable —
		// EndpointTLS's own unconditional rule — and disable never attempts
		// TLS at all, so a CA named here goes unread whatever sslmode says.
		// Unlike plugins/etcd's ca-file, nothing in connect() below turns TLS
		// back on when this is set: sslmode is a tier, not a bool, and
		// picking one on the operator's behalf is exactly the elevation the
		// comment above already declines to do. checkSet reads this flag to
		// refuse the combination instead of leaving it silently inert.
		{Name: "sslrootcert", Type: plugin.String, Default: "", Config: "sslrootcert",
			Local: true, TLSAdjacent: true,
			Help: "CA bundle to verify the server against — has no effect " +
				"unless sslmode is require or stricter, and is overridden along with sslmode " +
				"under a kube:/ssh: tunnel"},
	}
}

// dsn builds a connection string from the resolved inputs.
//
// Assembled as key=value with each value quoted rather than as a URL, because
// a password containing '@' or '/' silently produces a different connection
// string under URL parsing — and the failure is an authentication error that
// names nothing.
func dsn(req plugin.Request) string {
	quote := func(s string) string {
		return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
	}
	parts := []string{
		"host=" + quote(req.String("host")),
		fmt.Sprintf("port=%d", req.Int("port")),
		"user=" + quote(req.String("user")),
		"dbname=" + quote(req.String("database")),
		"sslmode=" + quote(req.String("sslmode")),
		// Emitted always, and empty on purpose. Leaving the key out does not
		// mean "no passfile" — pgconn's defaultSettings fills it with
		// $HOME/.pgpass and ParseConfig then loads a password from it whenever
		// none was supplied, so an operator who configured a host and a user
		// and deliberately no password got authenticated with whatever
		// credential their own interactive psql keeps for that host. That is
		// the exact shape connFields' Local-everywhere rule exists to prevent,
		// arriving one layer below it: not a value a caller chose, but a value
		// nobody chose, read out of the ambient environment.
		//
		// An empty passfile fails the open and pgconn skips the lookup, so
		// this fails closed. Note it is not interchangeable with the fix
		// pg.dump needs: libpq treats an empty PGPASSFILE as "use the default"
		// and reads ~/.pgpass anyway, which is why backup.go names a path
		// instead.
		"passfile=''",
	}
	if pw := req.String("password"); pw != "" {
		parts = append(parts, "password="+quote(pw))
	}
	if ca := req.String("sslrootcert"); ca != "" {
		parts = append(parts, "sslrootcert="+quote(ca))
	}
	return strings.Join(parts, " ")
}

// classify turns a driver error into something an operator can act on.
//
// This is the capability the design brief singles out — "Error with hints for
// the classic connection failures" — and it is why pg is the plugin that
// proves the contract: every one of these is a sentence somebody has stared
// at without knowing what to do next.
func classify(err error, req plugin.Request) *view.Error {
	// **An error that is already a view.Error has already been classified,
	// and classifying it twice loses it.** Every capability here runs its
	// work inside a closure — withConn, and readOnly inside that — and a
	// closure can only report a refusal by returning an error, so a
	// handler's own view.Error arrives back at exactly the same place a
	// driver failure does. Falling through the switches below, it matched
	// nothing and came out as `pg.conn.failed: could not connect to
	// 127.0.0.1:55432: the query returned more than 1.0 MiB` — a connection
	// error naming a hint about an input, for a connection that was fine.
	//
	// Found by running pg.query against a real server rather than by a test:
	// the row bound had unit tests either side of this function and none
	// through it, so the refusal was correct, reached, and then thrown away
	// one frame later.
	var already *view.Error
	if errors.As(err, &already) {
		return already
	}

	where := fmt.Sprintf("%s:%d", req.String("host"), req.Int("port"))

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "28P01", "28000": // invalid_password, invalid_authorization
			return view.Errorf("pg.auth.failed", "%s rejected the credentials for %q",
				where, req.String("user")).
				WithHint("set $" + plugin.LocalEnvVar("pg.status", "password") +
					", or check the role name — rta only ever uses the password you give it, " +
					"never ~/.pgpass")
		case "3D000": // invalid_catalog_name
			return view.Errorf("pg.database.missing", "%s has no database named %q",
				where, req.String("database")).
				WithHint("`rta pg database list` shows what is there")
		case "42501": // insufficient_privilege
			return view.Errorf("pg.denied", "%q may not do that on %s",
				req.String("user"), req.String("database")).
				WithHint("this is the database refusing, not rta")
		}
		return view.Errorf("pg.query.failed", "%s", pgErr.Message).
			WithHint("SQLSTATE " + pgErr.Code)
	}

	var netErr *net.OpError
	if errors.As(err, &netErr) || strings.Contains(err.Error(), "connection refused") {
		refused := view.Errorf("pg.conn.refused", "nothing is listening on %s", where)
		if loopback(req.String("host")) {
			// "Is the server up?" is the wrong question about a port on this
			// machine, and it is the question the general hint asks. A
			// loopback port with nothing on it is almost always a forward
			// that exited — and the reason it exited is worth naming,
			// because it is not obvious and it is caused by the default.
			return refused.WithHint("a local port with nothing on it is usually a " +
				"port-forward that exited — check the terminal running it. PostgreSQL TLS " +
				"through `kubectl port-forward` kills the forward on the first clean " +
				"disconnect, so `sslmode: disable` is what survives; that hop is already " +
				"inside the API server's TLS")
		}
		return refused.WithHint("is the server up, and is the port right? `rta net port " +
			req.String("host") + " --ports " + fmt.Sprint(req.Int("port")) + "` answers the second")
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return view.Errorf("pg.host.unknown", "no address for %q", req.String("host")).
			WithHint("`rta net dns " + req.String("host") + "` shows what DNS returns")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return view.Errorf("pg.conn.timeout", "%s did not answer in time", where).
			WithHint("a firewall that drops rather than refuses looks exactly like this")
	}
	if strings.Contains(err.Error(), "SSL is not enabled") ||
		strings.Contains(err.Error(), "server does not support SSL") {
		return view.Errorf("pg.tls.unsupported", "%s does not offer TLS", where).
			WithHint("--sslmode disable if that is expected on this network")
	}
	var certErr x509.UnknownAuthorityError
	if errors.As(err, &certErr) {
		return view.Errorf("pg.tls.untrusted", "%s presented a certificate nothing here trusts", where).
			WithHint("a tunnelled PostgreSQL commonly has its own operator- or cluster-generated CA; " +
				"pass it with sslrootcert — and check --sslmode is require or stricter, since prefer " +
				"never verifies it")
	}
	return view.Errorf("pg.conn.failed", "could not connect to %s: %v", where, err).
		WithHint("`rta explain pg.status` lists every input and where each one can come from")
}

// loopback reports whether a host names this machine.
//
// By parse and by name: an operator writes "localhost" about as often as
// "127.0.0.1", and a resolved tunnel hands back whichever kubectl printed.
func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// connect opens a connection, mapping any failure through classify.
func connect(ctx context.Context, req plugin.Request) (*pgx.Conn, *view.Error) {
	conn, err := pgx.Connect(ctx, dsn(req))
	if err != nil {
		return nil, classify(err, req)
	}
	return conn, nil
}

// readOnly runs fn inside a READ ONLY transaction.
//
// This is what lets pg.query declare Safety: Read honestly. The alternative
// is parsing the SQL and refusing anything that looks like a write, which is
// a game nobody wins — `WITH x AS (DELETE ... RETURNING *) SELECT * FROM x`
// is a SELECT statement that deletes rows. PostgreSQL enforces this itself,
// server-side, against the parsed statement rather than against a string, so
// an agent handed pg.query cannot mutate anything whatever it sends.
func readOnly(ctx context.Context, conn *pgx.Conn, fn func(pgx.Tx) error) error {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	return fn(tx)
}

// rowsToTable renders a result set, whatever its shape.
// ErrTooManyRows is what a result set larger than the caller allowed comes
// back as, so the handler can say which flag fixes it.
var ErrTooManyRows = errors.New("the result set is larger than the row bound")

// ErrTooLarge is the other half: within the row bound, over the byte one.
var ErrTooLarge = errors.New("the result set is larger than the size bound")

// maxRows bounds a result set rta wrote the SQL for and therefore already
// knows the size of — a ceiling against a catalogue nobody expected to be
// this big, not a limit anybody tunes.
const maxRows = 10000

// maxBytes bounds the same result set by size, because **a row bound is not
// a size bound**: `select body from documents` at two hundred rows is two
// hundred rows of whatever a text column holds, and a bytea column makes
// that arbitrary. The row bound alone was the whole protection here, and it
// counts the wrong thing.
//
// What makes it a correctness bound and not only a courtesy: a plugin's view
// crosses go-plugin's gRPC channel, and nothing configures
// MaxCallRecvMsgSize on either side, so grpc-go's 4 MiB default applies.
// Past it the caller does not get a large answer or a truncated one — the
// transport fails with ResourceExhausted, an error naming gRPC rather than
// the query that caused it, from a layer the operator has no flag for.
// Refusing here costs one comparison and says which flag fixes it.
//
// One mebibyte because that is the ceiling this codebase already applies
// twice for the same reason — builtin/http's response body and plugins/s3's
// inline object — and because the room between it and the transport's limit
// is where the host's own re-encoding lives.
const maxBytes = 1 << 20

// rowsToTable reads a result set into a table, bounded.
//
// **The bound is a parameter because forgetting it is the bug.** Every
// listing capability in this plugin declares a limit and applies it in SQL,
// and pg.query — the one capability whose SQL the *caller* writes — did not:
// `select * from users` streamed every row into a slice in the plugin, then
// through the host, then at a model's context. That is an unbounded
// allocation driven by an argument, which is a denial of service, and a bulk
// read of a table nobody consented to row by row, which is the more
// interesting half.
//
// One past the bound, so a full page and an overflowing one are told apart,
// and the overflow is **refused rather than truncated** — the same rule
// ai.ask's context bound follows, and for the same reason: a silently
// shortened answer is a different answer wearing the right shape. The caller
// gets to decide, by raising the bound or by writing a LIMIT.
func rowsToTable(rows pgx.Rows, bound int) (view.Table, error) {
	var t view.Table
	for _, fd := range rows.FieldDescriptions() {
		t.Columns = append(t.Columns, view.Column{Name: string(fd.Name)})
	}
	if bound <= 0 {
		bound = maxRows
	}
	var size int
	for rows.Next() {
		if len(t.Rows) == bound {
			return t, ErrTooManyRows
		}
		vals, err := rows.Values()
		if err != nil {
			return t, err
		}
		row := make([]string, len(vals))
		for i, v := range vals {
			row[i] = cell(v)
			size += len(row[i])
		}
		// Checked after the row is built rather than before, so the refusal
		// happens on the row that crosses the line instead of one row early
		// on a guess about how big the next one will be.
		if size > maxBytes {
			return t, ErrTooLarge
		}
		t.Rows = append(t.Rows, row)
	}
	return t, rows.Err()
}

// cell renders one value as text.
//
// fmt.Sprint alone is wrong here and the failure is loud: pgx decodes
// `numeric` into a pgtype.Numeric struct, so `select total from orders`
// printed `{5 3 false finite true}` — the struct's fields — in the first
// version of this. Every pgtype scalar implements driver.Valuer and knows how
// to render itself, so ask it before falling back.
func cell(v any) string {
	if v == nil {
		// NULL is not the empty string, but a table cell has nowhere to say
		// so; empty at least reads as absent rather than as a value.
		return ""
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	if valuer, ok := v.(driver.Valuer); ok {
		if dv, err := valuer.Value(); err == nil && dv != nil {
			if b, ok := dv.([]byte); ok {
				return string(b)
			}
			return fmt.Sprint(dv)
		}
	}
	if t, ok := v.(time.Time); ok {
		return t.Format(time.RFC3339)
	}
	return fmt.Sprint(v)
}
