package credname

import "testing"

// A header is a credential by what it is called, whatever separators its name
// is spelled with: the ones an API takes its key in as well as Authorization.
func TestAHeaderIsACredentialByWhatItIsCalled(t *testing.T) {
	for name, want := range map[string]bool{
		"Authorization": true, "Proxy-Authorization": true, "Cookie": true, "PRIVATE-TOKEN": true,
		"X-Api-Key": true, "x_api_key": true, "XApiKey": true, "X-Vault-Token": true, "X-Amz-Signature": true,
		"Accept": false, "Content-Type": false, "X-Request-Id": false, "User-Agent": false, "": false,
	} {
		if got := Header(name); got != want {
			t.Errorf("Header(%q) = %v, want %v", name, got, want)
		}
	}
}

// A query parameter is one as a header is, and `sig` is one on its own.
func TestAQueryParameterIsACredentialAsAHeaderIs(t *testing.T) {
	for name, want := range map[string]bool{
		"api_key": true, "access_token": true, "sig": true, "SIG": true,
		"signature": true, "page": false, "sort": false, "design": false,
	} {
		if got := Param(name); got != want {
			t.Errorf("Param(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestAFieldIsACredentialByItsName(t *testing.T) {
	for name, want := range map[string]bool{
		"password": true, "db-password": true, "bearer": true, "basic-auth": true, "apikey": true,
		"host": false, "database": false, "": false,
	} {
		if got := Field(name); got != want {
			t.Errorf("Field(%q) = %v, want %v", name, got, want)
		}
	}
}
