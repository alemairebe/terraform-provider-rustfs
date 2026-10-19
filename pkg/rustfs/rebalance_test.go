// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStartRebalance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// POST /rustfs/admin/v3/rebalance/start is what RustFS 1.0.x registers;
		// the route answers {"id":"<uuid>"} once a second pool exists.
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/rebalance/start" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"c2f4f7a6-4a2a-4f5c-9a3a-3f1c9f5e9a10"}`))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	if err := client.StartRebalance(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestStartRebalanceSinglePool pins the server's answer for a deployment with
// one pool: the route is registered but reports NotImplemented because there is
// nothing to rebalance.
func TestStartRebalanceSinglePool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>NotImplemented</Code>` +
			`<Message>A header you provided implies functionality that is not implemented.</Message></Error>`))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	err := client.StartRebalance()
	if err == nil {
		t.Fatal("expected the single-pool NotImplemented answer to surface as an error")
	}
	if !IsAPIErrorCode(err, "NotImplemented") {
		t.Errorf("expected NotImplemented, got %v", err)
	}
}
