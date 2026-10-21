// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package rustfs

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSiteReplicationAdd(t *testing.T) {
	var gotPath, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody = readRequestBody(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	site := SiteReplicationSite{
		Name:          "peer",
		Endpoint:      "http://localhost:9002",
		AccessKey:     "peeradmin",
		SecretKey:     "peersecret",
		SkipTLSVerify: false,
	}
	if err := c.SiteReplicationAdd(site); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "/site-replication/add") {
		t.Errorf("wrong path: %s", gotPath)
	}
	// The body is never printed on failure: it carries the peer credentials.
	if !strings.Contains(gotBody, `"endpoints":"http://localhost:9002"`) {
		t.Error("add body must carry the endpoint in its \"endpoints\" field")
	}
	if !strings.Contains(gotBody, `"accessKey":"peeradmin"`) {
		t.Error("add body must carry the peer access key (value redacted)")
	}
	var payload []SiteReplicationSite
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("add body must be a JSON array: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("add body carries %d sites, want one", len(payload))
	}
	if payload[0].Name != "peer" {
		t.Errorf("add body name = %q, want peer", payload[0].Name)
	}
}

func TestEditPeer(t *testing.T) {
	var gotPath, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody = readRequestBody(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	site := SiteReplicationPeerEdit{
		Name:          "peer",
		Endpoint:      "http://localhost:9003",
		SkipTLSVerify: true,
	}
	if err := c.EditPeer(site); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "/site-replication/edit") {
		t.Errorf("wrong path: %s", gotPath)
	}
	// site-replication/edit parses a single PeerInfo whose endpoint field is
	// "endpoint" (singular); the add-only "endpoints" field and the array body
	// are rejected by RustFS 1.0.3.
	// The body is never printed on failure: it carries the peer credentials.
	if strings.Contains(gotBody, `"endpoints"`) {
		t.Error("edit body must not use the add-only \"endpoints\" field")
	}
	if !strings.HasPrefix(gotBody, "{") {
		t.Error("edit body must be a single object, not the add-only array")
	}
	var payload SiteReplicationPeerEdit
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("edit body must be a PeerInfo object: %v", err)
	}
	if payload.Name != "peer" || payload.Endpoint != "http://localhost:9003" {
		t.Errorf("edit body name/endpoint = %q/%q, want peer/http://localhost:9003", payload.Name, payload.Endpoint)
	}
	if !payload.SkipTLSVerify {
		t.Error("edit body must set skipTlsVerify")
	}
}

func TestRemovePeer(t *testing.T) {
	var gotPath, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotBody = readRequestBody(r)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	if err := c.RemovePeer([]string{"peer"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "/site-replication/remove") {
		t.Errorf("wrong path: %s", gotPath)
	}
	var body struct {
		Sites []string `json:"sites"`
	}
	if err := json.Unmarshal([]byte(gotBody), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	if len(body.Sites) != 1 || body.Sites[0] != "peer" {
		t.Errorf("remove body carries %d sites, want one", len(body.Sites))
	}
}

func TestSiteReplicationInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/site-replication/info") {
			t.Errorf("wrong path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"enabled":true,"name":"local","sites":[{"name":"peer","endpoint":"http://localhost:9002","deploymentID":"dep1"}]}`))
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	info, err := c.SiteReplicationInfo()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.Enabled || len(info.Sites) != 1 || info.Sites[0].Name != "peer" || info.Sites[0].Endpoint != "http://localhost:9002" {
		t.Errorf("unexpected info: %+v", info)
	}
}

func TestSiteReplicationResyncOp(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"op":"start","id":"abc","status":"success","state":"pending"}`))
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	resync, err := c.SiteReplicationResyncOp("start", SiteReplicationPeer{Name: "peer", DeploymentID: "dep1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "/site-replication/resync/op") {
		t.Errorf("wrong path: %s", gotPath)
	}
	if !strings.Contains(gotPath, "operation=start") {
		t.Errorf("expected operation=start, got: %s", gotPath)
	}
	if resync.OpType != "start" || resync.Status != "success" {
		t.Errorf("unexpected resync: %+v", resync)
	}
}

// repairPreflightReply is the dry-run response shape: camelCase fields, a
// site map keyed by deployment ID, and the "replication" family.
const repairPreflightReply = `{"mode":"dry-run","status":"planned","preflightToken":"preflight-token",` +
	`"retryEvents":2,"sites":{"dep-peer":{"deploymentId":"dep-peer","name":"peer",` +
	`"families":{"replication":{"planned":1,"succeeded":0,"failed":0,"retryEvents":2,` +
	`"tasks":[{"taskId":"task-1","status":"planned"}],"errors":[]}}}}}`

func TestSiteReplicationRepairPreflight(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotBody = r.Method, r.URL.Path, readRequestBody(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(repairPreflightReply))
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	preflight, err := c.SiteReplicationRepairPreflight()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPut || !strings.Contains(gotPath, "/site-replication/repair") {
		t.Errorf("preflight sent %s %s, want PUT /site-replication/repair", gotMethod, gotPath)
	}
	if gotBody != `{"mode":"dry-run"}` {
		t.Errorf("preflight body = %s, want a dry-run request", gotBody)
	}
	if preflight.PreflightToken != "preflight-token" || preflight.RetryEvents != 2 {
		t.Errorf("unexpected preflight: %+v", preflight)
	}
	family := preflight.Sites["dep-peer"].Families["replication"]
	if family.Planned != 1 || family.Succeeded != 0 || family.RetryEvents != 2 {
		t.Errorf("unexpected family progress: %+v", family)
	}
	if len(family.Tasks) != 1 || family.Tasks[0].TaskID != "task-1" || family.Tasks[0].Status != "planned" {
		t.Errorf("unexpected tasks: %+v", family.Tasks)
	}
}

func TestSiteReplicationRepairExecute(t *testing.T) {
	var gotPath, gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotBody = r.URL.Path, readRequestBody(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mode":"execute","operationId":"op-1","status":"partial",` +
			`"sites":{"dep-peer":{"deploymentId":"dep-peer","name":"peer","families":{"replication":` +
			`{"planned":2,"succeeded":1,"failed":1,"retryEvents":0,` +
			`"tasks":[{"taskId":"task-1","status":"succeeded"},{"taskId":"task-2","status":"failed","error":"denied"}],` +
			`"errors":["denied"]}}}},"createdAt":"2026-10-20T10:00:00Z","updatedAt":"2026-10-20T10:00:05Z",` +
			`"completedAt":"2026-10-20T10:00:05Z"}`))
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	operation, err := c.SiteReplicationRepairExecute("preflight-token", "op-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotPath, "/site-replication/repair") {
		t.Errorf("wrong path: %s", gotPath)
	}
	wantBody := `{"mode":"execute","preflightToken":"preflight-token","operationId":"op-1"}`
	if gotBody != wantBody {
		t.Errorf("execute body = %s, want %s", gotBody, wantBody)
	}
	if operation.OperationID != "op-1" || operation.Status != "partial" {
		t.Errorf("unexpected operation: %+v", operation)
	}
	if operation.CompletedAt != "2026-10-20T10:00:05Z" {
		t.Errorf("completedAt = %q, want the RFC 3339 timestamp", operation.CompletedAt)
	}
	family := operation.Sites["dep-peer"].Families["replication"]
	if family.Failed != 1 || len(family.Errors) != 1 || family.Errors[0] != "denied" {
		t.Errorf("unexpected family progress: %+v", family)
	}
	if len(family.Tasks) != 2 || family.Tasks[1].Error != "denied" {
		t.Errorf("unexpected tasks: %+v", family.Tasks)
	}
}

func TestSiteReplicationRepairStatus(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"mode":"execute","operationId":"op-1","status":"success",` +
			`"sites":{"dep-peer":{"deploymentId":"dep-peer","name":"peer","families":{"replication":` +
			`{"planned":1,"succeeded":1,"failed":0,"retryEvents":0,"tasks":[],"errors":[]}}}}` +
			`,"createdAt":"2026-10-20T10:00:00Z"}`))
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	operation, err := c.SiteReplicationRepairStatus("op-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodGet || !strings.Contains(gotPath, "/site-replication/repair/status") {
		t.Errorf("status sent %s %s, want GET /site-replication/repair/status", gotMethod, gotPath)
	}
	if gotQuery != "operation-id=op-1" {
		t.Errorf("status query = %q, want operation-id=op-1", gotQuery)
	}
	if operation.Status != "success" {
		t.Errorf("status = %q, want success", operation.Status)
	}
	if operation.CompletedAt != "" {
		t.Errorf("completedAt = %q, want empty for an absent field", operation.CompletedAt)
	}
}

func TestSiteReplicationRepairErrors(t *testing.T) {
	cases := []struct {
		name   string
		code   string
		body   string
		detail string
	}{
		{
			name:   "site replication is not configured",
			code:   "InvalidRequest",
			body:   "site replication is not configured",
			detail: "site replication is not configured",
		},
		{
			name:   "stale preflight",
			code:   "PreconditionFailed",
			body:   "site replication repair preflight is stale",
			detail: "preflight is stale",
		},
		{
			name:   "operation id bound to another preflight",
			code:   "ClientTokenConflict",
			body:   "repair operation ID is already bound to a different preflight",
			detail: "already bound to a different preflight",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>` + tc.code +
					`</Code><Message>` + tc.body + `</Message></Error>`))
			}))
			defer server.Close()
			c := testSiteReplicationClient(server)

			_, err := c.SiteReplicationRepairExecute("token", "op-1")
			if err == nil {
				t.Fatal("expected an error")
			}
			if !IsAPIErrorCode(err, tc.code) {
				t.Errorf("error code = %v, want %s", err, tc.code)
			}
			if !strings.Contains(err.Error(), tc.detail) {
				t.Errorf("error = %q, want detail %q", err.Error(), tc.detail)
			}
		})
	}
}

// TestSiteReplicationDeprecatedPeerWrappers keeps the deprecated client
// methods working for callers that have not moved to EditPeer and RemovePeer.
func TestSiteReplicationDeprecatedPeerWrappers(t *testing.T) {
	var paths, bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		bodies = append(bodies, readRequestBody(r))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	err := c.SiteReplicationEdit(SiteReplicationSite{
		Name:      "peer",
		Endpoint:  "http://localhost:9002",
		AccessKey: "peeradmin",
		SecretKey: "peersecret",
	})
	if err != nil {
		t.Fatalf("deprecated edit: %v", err)
	}
	if err := c.SiteReplicationRemove([]string{"peer"}); err != nil {
		t.Fatalf("deprecated remove: %v", err)
	}

	if len(paths) != 2 ||
		!strings.Contains(paths[0], "/site-replication/edit") ||
		!strings.Contains(paths[1], "/site-replication/remove") {
		t.Errorf("paths = %v, want edit then remove", paths)
	}
	if !strings.Contains(bodies[0], `"endpoint":"http://localhost:9002"`) {
		t.Errorf("edit body = %s, want the singular endpoint field", bodies[0])
	}
	if !strings.Contains(bodies[1], `"sites":["peer"]`) {
		t.Errorf("remove body = %s, want the sites array", bodies[1])
	}
}

// TestSiteReplicationRepairRequestErrors covers the transport failures of the
// preflight and status calls, which the provider reports as read/create errors.
func TestSiteReplicationRepairRequestErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("repair is busy"))
	}))
	defer server.Close()
	c := testSiteReplicationClient(server)

	if _, err := c.SiteReplicationRepairPreflight(); err == nil || !strings.Contains(err.Error(), "repair is busy") {
		t.Errorf("preflight error = %v, want the server message", err)
	}
	if _, err := c.SiteReplicationRepairStatus("op-1"); err == nil || !strings.Contains(err.Error(), "repair is busy") {
		t.Errorf("status error = %v, want the server message", err)
	}
}

func testSiteReplicationClient(server *httptest.Server) RustfsAdmin {
	c := New(&RustfsAdminConfig{
		Endpoint:  server.Listener.Addr().String(),
		AccessKey: "admin",
	})
	c.accessSecret = "secret"
	return c
}

func readRequestBody(r *http.Request) string {
	b, _ := io.ReadAll(r.Body)
	return string(b)
}
