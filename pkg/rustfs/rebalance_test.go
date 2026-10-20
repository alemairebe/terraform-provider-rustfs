// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testRebalanceID = "c2f4f7a6-4a2a-4f5c-9a3a-3f1c9f5e9a10"

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
		_, _ = w.Write([]byte(`{"id":"` + testRebalanceID + `"}`))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	id, err := client.StartRebalance(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != testRebalanceID {
		t.Errorf("trigger id = %q, want %q", id, testRebalanceID)
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

	_, err := client.StartRebalance(context.Background())
	if err == nil {
		t.Fatal("expected the single-pool NotImplemented answer to surface as an error")
	}
	if !IsAPIErrorCode(err, "NotImplemented") {
		t.Errorf("expected NotImplemented, got %v", err)
	}
}

func TestRebalanceStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/rebalance/status" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id":"` + testRebalanceID + `",
			"pools":[
				{
					"id":0,
					"status":"Started",
					"stopping":false,
					"used":0.42,
					"lastError":"slow disk",
					"cleanupWarnings":{
						"count":1,
						"lastMsg":"could not clean source",
						"lastBucket":"bucket-a",
						"lastObject":"object-a",
						"lastAt":"2026-10-20T10:00:00Z",
						"entries":[{"bucket":"bucket-a","object":"object-a","message":"could not clean source","timestamp":"2026-10-20T10:00:00Z"}]
					},
					"progress":{"objects":10,"versions":12,"bytes":345,"remainingBuckets":2,"bucket":"bucket-a","object":"object-a","elapsed":60,"eta":30}
				},
				{"id":1,"status":"","stopping":false,"used":0.1,"lastError":null,"cleanupWarnings":{"count":0},"progress":null}
			],
			"stoppedAt":null,
			"stopPropagation":{"lastAttemptAt":null,"failedPeers":[],"terminalReloadAttemptAt":null,"terminalReloadFailedPeers":[],"pendingTerminalReload":false}
		}`))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	status, err := client.RebalanceStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.ID != testRebalanceID {
		t.Errorf("id = %q, want %q", status.ID, testRebalanceID)
	}
	if len(status.Pools) != 2 {
		t.Fatalf("pools = %d, want 2", len(status.Pools))
	}
	first := status.Pools[0]
	if first.ID != 0 || first.Status != "Started" || first.Stopping {
		t.Errorf("unexpected first pool: %+v", first)
	}
	if first.Used != 0.42 {
		t.Errorf("used = %v, want 0.42", first.Used)
	}
	if first.LastError == nil || *first.LastError != "slow disk" {
		t.Errorf("last error = %v, want slow disk", first.LastError)
	}
	if first.CleanupWarnings.Count != 1 || len(first.CleanupWarnings.Entries) != 1 {
		t.Errorf("unexpected cleanup warnings: %+v", first.CleanupWarnings)
	}
	if first.Progress == nil {
		t.Fatal("progress is nil")
	}
	if first.Progress.Objects != 10 || first.Progress.Versions != 12 || first.Progress.Bytes != 345 {
		t.Errorf("unexpected progress: %+v", *first.Progress)
	}
	if first.Progress.RemainingBuckets != 2 || first.Progress.ETA != 30 {
		t.Errorf("unexpected progress: %+v", *first.Progress)
	}
	if status.Pools[1].Progress != nil {
		t.Errorf("inactive pool progress = %+v, want nil", status.Pools[1].Progress)
	}
	if status.StoppedAt != nil {
		t.Errorf("stopped at = %v, want nil", status.StoppedAt)
	}
	if status.StopPropagation.PendingTerminalReload {
		t.Error("pending terminal reload must be false")
	}
}

// TestRebalanceStatusNotStarted pins the route's answer when no rebalance has
// ever run: 404 with code NoSuchResource, which is a missing record rather than
// a missing route.
func TestRebalanceStatusNotStarted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<Error><Code>NoSuchResource</Code><Message>pool rebalance is not started</Message></Error>`))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	_, err := client.RebalanceStatus(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsAPIErrorCode(err, "NoSuchResource") {
		t.Errorf("expected NoSuchResource, got %v", err)
	}
}

func TestStopRebalance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/rustfs/admin/v3/rebalance/stop" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.RawQuery != "" {
			t.Errorf("stop must not send query parameters, got %q", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	if err := client.StopRebalance(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestStopRebalanceNotStarted pins the answer for a deployment with no rebalance
// to stop, which is how stop behaves after a run already reached a terminal
// state.
func TestStopRebalanceNotStarted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`<Error><Code>NoSuchResource</Code><Message>pool rebalance is not started</Message></Error>`))
	}))
	defer server.Close()

	client := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	client.accessSecret = "secret"

	err := client.StopRebalance(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsAPIErrorCode(err, "NoSuchResource") {
		t.Errorf("expected NoSuchResource, got %v", err)
	}
}
