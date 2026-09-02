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
