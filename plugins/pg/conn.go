package main

import (
	"context"
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
// Each one declares a Config key, which is the whole reason ADR 0016 exists:
// an operator states the connection once, in their own file, and never types
// it again. The handler reads req.String("host") and cannot tell whether that
// came from a flag, the config file or the declared default — which is the
// point, not an accident of the API.
//
// **Every one of them is also Local, and that is a security property rather
// than a detail** (PROJECT.md D94). Together they name *which server this
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
// distinction is deliberate (D74): EnvFallback is for values that genuinely
// are credentials, so the host resolves it from $RTA_PG_PASSWORD and
// `rta explain` prints that variable name. A field that merely chooses a
// destination must come from an explicit caller or from config, never from
// an ambient variable the MCP server happened to inherit.
func connFields() []plugin.Field {
	return []plugin.Field{
		// host and port carry the endpoint roles, so a profile naming a
		// cluster reaches this database through a port-forward the host opens
		// and closes: pg never learns a forward was there, which is ADR 0004's
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
		// the machine is already inside the API server's TLS (ADR 0018 §7).
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
	}
	if pw := req.String("password"); pw != "" {
		parts = append(parts, "password="+quote(pw))
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
	where := fmt.Sprintf("%s:%d", req.String("host"), req.Int("port"))

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "28P01", "28000": // invalid_password, invalid_authorization
			return view.Errorf("pg.auth.failed", "%s rejected the credentials for %q",
				where, req.String("user")).
				WithHint("set $" + plugin.LocalEnvVar("pg.status", "password") +
					", or check the role name — rta never reads ~/.pgpass")
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
func rowsToTable(rows pgx.Rows) (view.Table, error) {
	var t view.Table
	for _, fd := range rows.FieldDescriptions() {
		t.Columns = append(t.Columns, view.Column{Name: string(fd.Name)})
	}
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return t, err
		}
		row := make([]string, len(vals))
		for i, v := range vals {
			row[i] = cell(v)
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
