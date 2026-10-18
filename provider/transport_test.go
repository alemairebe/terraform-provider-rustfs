package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// `insecure` is documented as "skip TLS verification for self-signed servers",
// so it has to reach the transport that both API clients use.
func TestNewHTTPTransportInsecure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	for _, tc := range []struct {
		name     string
		insecure bool
		wantErr  bool
	}{
		{name: "verifies certificates by default", insecure: false, wantErr: true},
		{name: "insecure skips verification", insecure: true, wantErr: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: newHTTPTransport(true, tc.insecure)}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatalf("building request: %v", err)
			}
			resp, err := client.Do(req)
			if err != nil {
				if !tc.wantErr {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			defer resp.Body.Close()
			if tc.wantErr {
				t.Fatalf("expected a certificate error, got status %d", resp.StatusCode)
			}
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected 200, got %d", resp.StatusCode)
			}
		})
	}
}
