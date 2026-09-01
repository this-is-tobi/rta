package main

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"sort"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// kube.cert.list: every TLS certificate this cluster stores, and how close
// to expiry it is.
//
// Reads only `type: kubernetes.io/tls` Secrets, selected server-side with
// `--field-selector` rather than fetched-and-filtered — an Opaque secret's
// `data` is API-token and password material with no reason to ever leave
// the API server for this process's memory, let alone transit a JSON
// unmarshal it has no use for. Within a TLS secret, only `tls.crt` is read.
// `tls.key` is the private key: this capability never requests it, never
// decodes it, and its absence from every struct below is the property under
// test, not an oversight.
//
// Expiry judgement would ideally be builtin/internal/x509check, the same
// package `cert expiry` and `audit.web` use so two implementations cannot
// disagree about the same certificate's window — but that package sits under
// builtin/internal, and this plugin is a separate Go module entirely (its own
// go.mod, per plugins/kube/kubectl.go's own reasoning for shelling out rather
// than linking a client): an internal package is not reachable across that
// boundary, full stop, regardless of directory nesting. warnDays below is
// x509check.DefaultWarnDays's value, restated rather than shared — if that
// constant ever changes, this should change with it, and there is no
// compiler to enforce that today.

type tlsSecretItem struct {
	Metadata meta `json:"metadata"`
	Data     struct {
		Cert string `json:"tls.crt"`
	} `json:"data"`
}

// fetchTLSSecrets is shared with kube.overview's own composition, so both
// read the same Secrets the same way.
func fetchTLSSecrets(ctx context.Context, s selection) (list[tlsSecretItem], *view.Error) {
	raw, runErr := run(ctx, s.args("get", "secrets", "-o", "json",
		"--field-selector=type=kubernetes.io/tls")...)
	if runErr != nil {
		return list[tlsSecretItem]{}, runErr
	}
	var secrets list[tlsSecretItem]
	if err := json.Unmarshal(raw, &secrets); err != nil {
		return list[tlsSecretItem]{}, view.Errorf("kube.unreadable",
			"kubectl's answer for secrets could not be read: %v", err)
	}
	return secrets, nil
}

func runCertList(ctx context.Context, req plugin.Request) (view.View, error) {
	s, verr := selectionOf(req)
	if verr != nil {
		return nil, verr
	}
	secrets, secErr := fetchTLSSecrets(ctx, s)
	if secErr != nil {
		return nil, secErr
	}
	sort.Slice(secrets.Items, func(i, j int) bool {
		a, b := secrets.Items[i].Metadata, secrets.Items[j].Metadata
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})

	cols := []view.Column{}
	if s.AllNS {
		cols = append(cols, view.Column{Name: "namespace"})
	}
	cols = append(cols,
		view.Column{Name: "secret"},
		view.Column{Name: "subject"},
		view.Column{Name: "not-after", Kind: view.KindTimestamp},
		view.Column{Name: "status", Kind: view.KindStatus},
	)
	rows := make([][]string, 0, len(secrets.Items))
	for _, sec := range secrets.Items {
		row := []string{}
		if s.AllNS {
			row = append(row, sec.Metadata.Namespace)
		}
		subject, notAfter, status := certRow(sec.Data.Cert)
		rows = append(rows, append(row, sec.Metadata.Name, subject, notAfter, status))
	}
	return view.Table{Columns: cols, Rows: rows, Total: len(rows)}, nil
}

// warnDays mirrors builtin/internal/x509check.DefaultWarnDays — see the
// package comment above for why this plugin cannot import that constant
// instead of restating it.
const warnDays = 30

// certRow decodes one Secret's tls.crt and judges its expiry. A Secret that
// fails to decode or parse reports that as its status rather than being
// dropped — silently skipping a certificate this capability exists to check
// is the one wrong way to fail here. Only the leaf certificate (the first PEM
// block, the order Kubernetes and every TLS peer both use) is judged; a
// bundle's intermediates typically outlive it by years and are not the
// question being asked.
func certRow(b64PEM string) (subject, notAfter, status string) {
	raw, err := base64.StdEncoding.DecodeString(b64PEM)
	if err != nil {
		return "", "", "tls.crt is not valid base64"
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", "", "tls.crt has no parseable certificate"
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", "", "tls.crt does not parse: " + err.Error()
	}
	subject = leaf.Subject.CommonName
	notAfter = leaf.NotAfter.Format("2006-01-02")
	switch {
	case leaf.NotAfter.Before(time.Now()):
		status = "expired"
	case time.Until(leaf.NotAfter) < warnDays*24*time.Hour:
		status = "expiring soon"
	default:
		status = "ok"
	}
	return subject, notAfter, status
}

// certPressure is kube.overview's own summary: which secrets are already
// past their certificate's expiry, and which are inside the warning window
// but not yet there. "secret/namespace" rather than the certificate's
// subject, because the subject is what the object is *for* and the secret
// name is what an operator runs `kubectl describe secret` against next.
func certPressure(secrets list[tlsSecretItem]) (expired, expiringSoon []string) {
	for _, sec := range secrets.Items {
		_, _, status := certRow(sec.Data.Cert)
		label := sec.Metadata.Namespace + "/" + sec.Metadata.Name
		switch status {
		case "expired":
			expired = append(expired, label)
		case "expiring soon":
			expiringSoon = append(expiringSoon, label)
		}
	}
	sort.Strings(expired)
	sort.Strings(expiringSoon)
	return expired, expiringSoon
}
