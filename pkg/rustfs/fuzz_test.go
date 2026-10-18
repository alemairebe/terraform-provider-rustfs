package rustfs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

// FuzzPolicyDocument fuzzes the decoder for the "policy" field of an
// info-canned-policy reply. The field is rendered by the server, which may
// answer with either a JSON string wrapping the document or an inline JSON
// object, and the document itself is untrusted: malformed input must produce an
// error, never a panic. A document that does decode must survive the round trip
// through the writer used by CreatePolicy, because the provider reads a policy
// into state and writes it back on the next apply.
func FuzzPolicyDocument(f *testing.F) {
	seeds := []string{
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::bucket/*"]}]}`,
		`{"Statement":[{"Effect":"Allow","Action":"s3:GetObject","Resource":["arn:aws:s3:::bucket/*"]}]}`,
		`{"Statement":[{"Effect":"Deny","Action":["s3:*"],"Resource":[]}]}`,
		`{"Statement":[{"Action":[""]}]}`,
		`{"Statement":null}`,
		`"{\"Statement\":[{\"Effect\":\"Allow\",\"Action\":[\"s3:GetObject\"]}]}"`,
		`"{}"`,
		`"not json at all"`,
		`{}`,
		`null`,
		`""`,
		`[]`,
		`{"Statement":`,
	}
	for _, seed := range seeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		statements, err := parsePolicyField(json.RawMessage(raw))
		if err != nil {
			return
		}

		// The decoded statements are written back to the admin API on the next
		// apply, so a document that decoded must survive that round trip
		// unchanged — including every field added to PolicyStatement later.
		encoded, err := json.Marshal(policyPost{Version: "2012-10-17", Statement: statements})
		if err != nil {
			t.Fatalf("decoded document is not encodable: %v", err)
		}
		reread, err := parsePolicyField(encoded)
		if err != nil {
			t.Fatalf("document written back from decoded statements does not decode: %v (encoded %s)", err, encoded)
		}
		rereadEncoded, err := json.Marshal(policyPost{Version: "2012-10-17", Statement: reread})
		if err != nil {
			t.Fatalf("re-decoded document is not encodable: %v", err)
		}
		if !bytes.Equal(encoded, rereadEncoded) {
			t.Fatalf("round trip changed the document: %s became %s", encoded, rereadEncoded)
		}
	})
}

// FuzzAdminErrorBody fuzzes parseAPIError, which turns a non-2xx admin or S3
// response body into an *APIError. The body may be S3-style XML, admin-style
// JSON, plain text or an HTML page emitted by an intermediary, and callers
// match on Error() — which must stay the raw body — for "not found" detection.
func FuzzAdminErrorBody(f *testing.F) {
	seeds := []struct {
		status int
		body   string
	}{
		{http.StatusNotFound, `<Error><Code>NoSuchBucket</Code><Message>The specified bucket does not exist</Message></Error>`},
		{http.StatusForbidden, `{"code":"AccessDenied","message":"not allowed"}`},
		{http.StatusInternalServerError, `<html><body><h1>502 Bad Gateway</h1></body></html>`},
		{http.StatusBadRequest, "config target 'missing' not found for subsystem 'notify_webhook'"},
		{http.StatusServiceUnavailable, ""},
		{http.StatusOK, `<Error><Code></Code></Error>`},
		{http.StatusNotFound, `<Error><Code>NoSuchKey</Code><Message>Café ünïcode</Message></Error>`},
		{http.StatusForbidden, `{"code":"NoSuchBucket","message":"{}"}`},
	}
	for _, seed := range seeds {
		f.Add(seed.status, seed.body)
	}

	f.Fuzz(func(t *testing.T, status int, body string) {
		resp := &http.Response{StatusCode: status}
		apiErr := parseAPIError(resp, []byte(body))

		if apiErr == nil {
			t.Fatal("parseAPIError returned nil")
		}
		if apiErr.StatusCode != status {
			t.Fatalf("StatusCode = %d, want %d", apiErr.StatusCode, status)
		}
		if apiErr.Raw != body {
			t.Fatalf("Raw = %q, want the verbatim body %q", apiErr.Raw, body)
		}
		if apiErr.Error() == "" {
			t.Fatal("Error() returned an empty message")
		}
		if body != "" && apiErr.Error() != body {
			t.Fatalf("Error() = %q, want the raw body %q so body matching keeps working", apiErr.Error(), body)
		}
	})
}

// FuzzConfigKV fuzzes the config-kv text parser. The body of get-config-kv is
// rendered by the server and treated as untrusted, so any byte sequence must
// parse or fail with an error, never a panic. The second half checks the
// escaping used by set-config-kv: whatever a value contains, it must survive
// being rendered into a directive and parsed back.
func FuzzConfigKV(f *testing.F) {
	f.Add(`notify_webhook:primary endpoint="http://example.com/hooks" enable="off"`, `http://example.com/hooks`)
	f.Add("# RUSTFS_NOTIFY_WEBHOOK_ENDPOINT=http://example.com/rustfs/events\nnotify_webhook:primary enable=\"on\"", `a"b\c`)
	f.Add("scope key=\"v\" other='w'", "")
	f.Add(`scope unterminated="value`, "line\nbreak")
	f.Add(`scope trailing\`, `\`)
	f.Add("scope key=\"\" key2=unquoted", "quote\" inside \"value\"")

	f.Fuzz(func(t *testing.T, text, value string) {
		// The server body is untrusted; a panic here would take the provider down.
		_, _ = parseConfigKVs(text)

		want := []ConfigKV{{Key: "key", Value: value}}
		directive := buildConfigDirective("scope", want)
		got, err := parseConfigLine(directive)
		if err != nil {
			t.Fatalf("rendered directive %q did not parse: %v", directive, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round trip of %q changed the pairs: got %#v, want %#v", directive, got, want)
		}
	})
}
