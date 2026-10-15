package config

import (
	"reflect"
	"testing"
)

// KVUsers joins the store to the file that depends on it: every profile whose
// connections read an entry, once per profile, kube references ignored.
func TestKVUsersMapsEntriesOntoTheProfilesReadingThem(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"staging": {Plugins: map[string]Connection{
			"pg@aaaa": {Secrets: map[string]string{"password": "kv:db-pass"}},
			// The same entry from a second plugin: staging is one user,
			// not two.
			"s3@bbbb": {Secrets: map[string]string{"secret-key": "kv:db-pass"}},
		}},
		"prod": {Plugins: map[string]Connection{
			"pg@aaaa": {Secrets: map[string]string{
				"password": "kv:prod-db-pass",
				// A cluster reference is not a store entry.
				"user": "kube:pg-creds/user",
			}},
		}},
		"dev": {Plugins: map[string]Connection{
			"pg@aaaa": {Secrets: map[string]string{"password": "kv:db-pass"}},
		}},
	}}
	got := cfg.KVUsers()
	want := map[string][]string{
		"db-pass":      {"dev", "staging"},
		"prod-db-pass": {"prod"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("KVUsers() = %v, want %v", got, want)
	}
}

// KVReaders is the same walk named by connection: an instance of a plugin is
// the reference a grant and --profile name it by, and two instances of one
// plugin in a profile are two readers where KVUsers counts the profile once.
func TestKVReadersNamesTheInstanceThatReadsAnEntry(t *testing.T) {
	cfg := Config{Profiles: map[string]Profile{
		"staging": {Plugins: map[string]Connection{
			"pg@aaaa":             {Secrets: map[string]string{"password": "kv:db-pass"}},
			"pg/analytics@aaaa":   {Secrets: map[string]string{"password": "kv:db-pass"}},
			"pg/reporting@aaaa":   {Secrets: map[string]string{"password": "kv:report-pass"}},
			"redis/sessions@bbbb": {Secrets: map[string]string{"user": "kube:redis-creds/user"}},
			"pg/analytics-copy":   {Secrets: map[string]string{"password": "kv:db-pass"}},
		}},
	}}
	want := map[string][]string{
		"db-pass":     {"staging", "staging/analytics", "staging/analytics-copy"},
		"report-pass": {"staging/reporting"},
	}
	if got := cfg.KVReaders(); !reflect.DeepEqual(got, want) {
		t.Errorf("KVReaders() = %v, want %v", got, want)
	}
	if got, want := cfg.KVUsers()["db-pass"], []string{"staging"}; !reflect.DeepEqual(got, want) {
		t.Errorf("KVUsers() counts %v, want the profile once: %v", got, want)
	}
}
