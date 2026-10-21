// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
)

// newTestAdminServer spins up an httptest server speaking the RustFS admin
// API and returns a RustfsAdmin client pointed at it.
func newTestAdminServer(t *testing.T, handler http.HandlerFunc) *rustfs.RustfsAdmin {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := rustfs.New(&rustfs.RustfsAdminConfig{
		AccessKey:    "admin",
		AccessSecret: "secret",
		Endpoint:     strings.TrimPrefix(srv.URL, "http://"),
	})
	return &c
}

// Endpoint reports the configured value before the scheme is filled in; the
// site-replication repair interlock compares its confirm attribute against it.
func TestRustfsAdminEndpoint(t *testing.T) {
	client := rustfs.New(&rustfs.RustfsAdminConfig{Endpoint: "rustfs.example.com:9000", Ssl: true})
	if got := client.Endpoint(); got != "rustfs.example.com:9000" {
		t.Fatalf("Endpoint() = %q, want the configured value", got)
	}
}
