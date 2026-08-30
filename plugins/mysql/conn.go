package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	stdnet "net"
	"net/url"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// connFields are the inputs every capability here shares.
//
// Every one is Local, and that is the security property rather than a detail.
// Together they name which server this call reaches and as whom, and an MCP
// caller may not choose that: an input a plugin declares is published in the
// tool schema and accepted from a caller, and caller values resolve last —
// above config, above the host's own environment — so an agent that could set
// `host` would point rta at a database of its own and have the host supply
// $RTA_MYSQL_PASSWORD beside it. Local closes that without changing anything
// for the two callers who should choose: config still fills these, and a
// person at a terminal still passes them as ordinary flags.
//
// password differs only in also declaring EnvFallback, and the distinction is
// the point: EnvFallback is for values that genuinely are credentials. A field
// that merely chooses a destination must come from an explicit caller or from
// config, never from an ambient variable the MCP server happened to inherit.
func connFields() []plugin.Field {
	return []plugin.Field{
		// host and port carry the endpoint roles, so a profile naming a
		// cluster reaches this database through a port-forward the host opens
		// and closes. This plugin never learns a forward was there, which is
		// why none of the code below changes to gain it.
		{Name: "host", Type: plugin.String, Default: "localhost", Config: "host",
			Local: true, Endpoint: plugin.EndpointHost, Help: "database host"},
		{Name: "port", Type: plugin.Int, Default: 3306, Config: "port",
			Local: true, Endpoint: plugin.EndpointPort, Min: 1, Max: 65535, Help: "database port"},
		{Name: "user", Type: plugin.String, Default: "root", Config: "user",
			Local: true, Help: "user to connect as"},
		// Empty by default rather than a guessed name. MySQL will connect with
		// no default database selected, and every capability here that needs
		// one qualifies its own tables — so the zero-config case reaches a
		// server and can still describe it, instead of failing on a database
		// name this plugin invented.
		{Name: "database", Type: plugin.String, Default: "", Config: "database",
			Local: true, Help: "database to select (optional — the server is reachable without one)"},
		// Local for a different reason than the four above: it does not change
		// where the call goes, it changes whether the transport is protected.
		// An agent that could set it could ask for `false` and downgrade a
		// connection the operator configured as verified.
		//
		// `preferred` is the default for the reason pg defaults to `prefer`:
		// it uses TLS when the server offers it and does not fail against the
		// local container somebody is trying this against first.
		{Name: "tls", Type: plugin.String, Default: "preferred", Config: "tls",
			Local:    true,
			Endpoint: plugin.EndpointTLS,
			Options:  []string{"false", "preferred", "true", "skip-verify"},
			Help:     "TLS negotiation mode"},
		{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true,
			Help: "password for the user"},
	}
}

// dsn builds go-sql-driver's connection string from the resolved inputs.
//
// Built through mysql.Config rather than by concatenating a string, because
// the driver's own FormatDSN escapes what needs escaping. A password
// containing '@' or '/' silently produces a different DSN under hand
// assembly, and the failure it causes is an authentication error that names
// nothing.
func dsn(req plugin.Request) string {
	c := mysql.NewConfig()
	c.Net = "tcp"
	c.Addr = fmt.Sprintf("%s:%d", req.String("host"), req.Int("port"))
	c.User = req.String("user")
	c.Passwd = req.String("password")
	c.DBName = req.String("database")
	c.TLSConfig = req.String("tls")
	// Timestamps come back as time.Time rather than []byte, so a column of
	// them formats the same way everywhere instead of once per call site.
	c.ParseTime = true
	// Without this the driver reports a lost connection as a bare
	// "invalid connection" with nothing to classify. Named errors are what
	// classify below turns into something an operator can act on.
	c.CheckConnLiveness = true
	return c.FormatDSN()
}

// connect opens a pool and proves it works before handing it back.
//
// sql.Open never dials — it validates the DSN and returns a lazy pool — so
// without the ping here, every capability would discover an unreachable
// server at its own first query and each would have to classify the same
// failure separately.
func connect(ctx context.Context, req plugin.Request) (*sql.DB, *view.Error) {
	db, err := sql.Open("mysql", dsn(req))
	if err != nil {
		return nil, view.Errorf("mysql.conn.invalid", "%v", err).
			WithHint("`rta explain mysql.overview` lists every input and where each one can come from")
	}
	// One connection, because a capability here runs one query and exits. A
	// pool that outlives the call would hold a socket open against somebody
	// else's server for nothing.
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, classify(err, req)
	}
	return db, nil
}

// classify turns a driver error into something an operator can act on.
//
// Every branch here is a sentence somebody has stared at without knowing what
// to do next. The MySQL error numbers are the stable part of the protocol —
// the text beside them is localized and version-dependent, so switching on the
// number is the only version of this that keeps working.
func classify(err error, req plugin.Request) *view.Error {
	// An error that is already a view.Error has been classified by whatever
	// raised it, and re-wrapping would bury a specific answer under a generic
	// one.
	var already *view.Error
	if errors.As(err, &already) {
		return already
	}

	where := fmt.Sprintf("%s:%d", req.String("host"), req.Int("port"))

	var myErr *mysql.MySQLError
	if errors.As(err, &myErr) {
		switch myErr.Number {
		case 1045: // ER_ACCESS_DENIED_ERROR
			return view.Errorf("mysql.auth.failed", "%s rejected user %q", where, req.String("user")).
				WithHint("set $" + plugin.LocalEnvVar("mysql.overview", "password") + ", or check --user")
		case 1044: // ER_DBACCESS_DENIED_ERROR
			return view.Errorf("mysql.database.denied", "%q may not use database %q",
				req.String("user"), req.String("database")).
				WithHint("the credentials are valid but not granted on this database — check SHOW GRANTS")
		case 1049: // ER_BAD_DB_ERROR
			return view.Errorf("mysql.database.notfound", "%s has no database %q", where, req.String("database")).
				WithHint("`rta mysql database list` shows what is there")
		case 1146: // ER_NO_SUCH_TABLE
			return view.Errorf("mysql.table.notfound", "%s", myErr.Message).
				WithHint("`rta mysql table list` shows what is there")
		case 1142, 1143: // ER_TABLEACCESS_DENIED_ERROR, ER_COLUMNACCESS_DENIED_ERROR
			return view.Errorf("mysql.denied", "%s", myErr.Message).
				WithHint("the credentials are valid but not authorized for this — check SHOW GRANTS")
		case 1130: // ER_HOST_NOT_PRIVILEGED
			return view.Errorf("mysql.host.denied", "%s will not accept connections from this machine", where).
				WithHint("MySQL authorizes on user@host — the grant has to name where you are connecting from")
		case 1290: // ER_OPTION_PREVENTS_STATEMENT
			return view.Errorf("mysql.readonly", "%s", myErr.Message).
				WithHint("the server is running with --read-only; this is a replica or was set that way deliberately")
		}
		return view.Errorf("mysql.query.failed", "%d: %s", myErr.Number, myErr.Message).
			WithHint("`rta explain mysql.overview` lists every input and where each one can come from")
	}

	var netErr *stdnet.OpError
	if errors.As(err, &netErr) || strings.Contains(err.Error(), "connection refused") {
		return view.Errorf("mysql.conn.refused", "nothing is listening on %s", where).
			WithHint("is the server up, and is --host/--port right?")
	}
	var dnsErr *stdnet.DNSError
	if errors.As(err, &dnsErr) {
		return view.Errorf("mysql.host.unknown", "no address for %q", req.String("host")).
			WithHint("`rta net dns " + req.String("host") + "` shows what DNS returns")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return view.Errorf("mysql.conn.timeout", "%s did not answer in time", where).
			WithHint("a firewall that drops rather than refuses looks exactly like this")
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return view.Errorf("mysql.conn.timeout", "%s did not answer in time", where).
			WithHint("a firewall that drops rather than refuses looks exactly like this")
	}
	return view.Errorf("mysql.conn.failed", "could not reach %s: %v", where, err).
		WithHint("`rta explain mysql.overview` lists every input and where each one can come from")
}
