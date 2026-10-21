// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestSiteReplicationResourceSchema(t *testing.T) {
	r := NewSiteReplicationResource()
	resp := &frameworkresource.SchemaResponse{}
	r.Schema(context.Background(), frameworkresource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %s", resp.Diagnostics)
	}
	for _, attr := range []string{"name", "endpoint", "access_key", "secret_key", "skip_tls_verify", "ca_cert_pem"} {
		if _, ok := resp.Schema.Attributes[attr]; !ok {
			t.Errorf("missing schema attribute %q", attr)
		}
	}

	// Only the peer identity forces a replacement. Endpoint, credentials and
	// TLS settings edit the peer in place, so adding a peer to the group never
	// means recreating the peers already managed.
	name := resp.Schema.Attributes["name"].(schema.StringAttribute)
	if len(name.PlanModifiers) == 0 {
		t.Error("name must carry RequiresReplace: renaming a peer is a different peer")
	}
	for _, attr := range []string{"endpoint", "access_key", "secret_key", "ca_cert_pem"} {
		field := resp.Schema.Attributes[attr].(schema.StringAttribute)
		if len(field.PlanModifiers) != 0 {
			t.Errorf("%s must not force a replacement: the server edits it in place", attr)
		}
	}
	skipTLS := resp.Schema.Attributes["skip_tls_verify"].(schema.BoolAttribute)
	if len(skipTLS.PlanModifiers) != 0 {
		t.Error("skip_tls_verify must not force a replacement: the server edits it in place")
	}
}

// The acceptance test skips itself when RUSTFS_SITE_REPLICATION_PEER_ENDPOINT is
// unset, and the add route rejects a loopback endpoint, so a run from the host
// against a published port can never form the group: the live path is covered by
// the compose test profile. The flows below drive Create, Read (including
// not-found and error), Update, Delete and ImportState against the mocked admin
// API harness in provider_test.go, and pin the two endpoints' different wire
// contracts: add takes an array of sites with a plural `endpoints` field, edit
// takes a single PeerInfo with a singular `endpoint`.

const (
	siteReplicationAddPath    = "/rustfs/admin/v3/site-replication/add"
	siteReplicationEditPath   = "/rustfs/admin/v3/site-replication/edit"
	siteReplicationRemovePath = "/rustfs/admin/v3/site-replication/remove"
	siteReplicationInfoPath   = "/rustfs/admin/v3/site-replication/info"

	mockSiteReplicationPeerEndpoint = "http://rustfs-peer:9000"
)

var (
	siteReplicationAccessKey     = unitTestCredential("RUSTFS_TEST_PEER_ACCESS_KEY", 'a')
	siteReplicationSecret        = unitTestCredential("RUSTFS_TEST_PEER_SECRET_KEY", 's')
	siteReplicationSecretRotated = unitTestCredential("RUSTFS_TEST_PEER_SECRET_KEY_ROTATED", 'r')
)

// siteReplicationInfoReply is the mocked site-replication/info payload: a peer
// named like the plan's, plus the local site.
const siteReplicationInfoReply = `{"enabled":true,"name":"local","sites":[` +
	`{"name":"local","endpoint":"http://rustfs:9000","deploymentID":"dep-local"},` +
	`{"name":"tf-acc-peer","endpoint":"http://rustfs-peer:9000","deploymentID":"dep-peer","skipTlsVerify":true}]}`

// siteReplicationInfoNoPeerReply is the same payload without the peer.
const siteReplicationInfoNoPeerReply = `{"enabled":true,"name":"local","sites":[` +
	`{"name":"local","endpoint":"http://rustfs:9000","deploymentID":"dep-local"}]}`

func siteReplicationPlan(endpoint, secretKey string, skipTLSVerify types.Bool) SiteReplicationResourceModel {
	return SiteReplicationResourceModel{
		Name:          types.StringValue("tf-acc-peer"),
		Endpoint:      types.StringValue(endpoint),
		AccessKey:     types.StringValue(siteReplicationAccessKey),
		SecretKey:     types.StringValue(secretKey),
		SkipTLSVerify: skipTLSVerify,
		CACertPEM:     types.StringNull(),
	}
}

// assertSiteReplicationModelEqual reports the fields that differ, without
// printing the peer credentials.
func assertSiteReplicationModelEqual(t *testing.T, what string, got, want SiteReplicationResourceModel) {
	t.Helper()
	if got.Name != want.Name {
		t.Errorf("%s: name = %q, want %q", what, got.Name.ValueString(), want.Name.ValueString())
	}
	if got.Endpoint != want.Endpoint {
		t.Errorf("%s: endpoint = %q, want %q", what, got.Endpoint.ValueString(), want.Endpoint.ValueString())
	}
	if got.SkipTLSVerify != want.SkipTLSVerify {
		t.Errorf("%s: skip_tls_verify = %v, want %v", what, got.SkipTLSVerify, want.SkipTLSVerify)
	}
	if got.CACertPEM != want.CACertPEM {
		t.Errorf("%s: ca_cert_pem differs (value redacted)", what)
	}
	if got.AccessKey.ValueString() != want.AccessKey.ValueString() || got.SecretKey.ValueString() != want.SecretKey.ValueString() {
		t.Errorf("%s: peer credentials differ from the plan (values redacted)", what)
	}
}

func TestSiteReplicationResourceCreate(t *testing.T) {
	ctx := context.Background()
	r := NewSiteReplicationResource().(*SiteReplicationResource)
	sch := resourceSchemaOf(t, r)
	srv, recorder := newRecorderServer(t, http.StatusOK, "")
	r.client = newUnitAllClient(srv)

	// A trailing slash in the configuration must not reach the wire.
	plan := siteReplicationPlan(mockSiteReplicationPeerEndpoint+"/", siteReplicationSecret, types.BoolValue(false))
	resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, SiteReplicationResourceModel{})}
	r.Create(ctx, frameworkresource.CreateRequest{Plan: resourcePlan(t, sch, plan)}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	var created SiteReplicationResourceModel
	requireNoDiagnostics(t, resp.State.Get(ctx, &created))
	assertSiteReplicationModelEqual(t, "create state", created, plan)

	request := recorder.only(t)
	if request.Method != http.MethodPut || request.Path != siteReplicationAddPath {
		t.Fatalf("create sent %s %s, want PUT %s", request.Method, request.Path, siteReplicationAddPath)
	}
	var addedSites []rustfs.SiteReplicationSite
	if err := json.Unmarshal(request.Body, &addedSites); err != nil {
		t.Fatalf("create body is not an array of sites: %v", err)
	}
	if len(addedSites) != 1 {
		t.Fatalf("create body carries %d sites, want one", len(addedSites))
	}
	if addedSites[0].Endpoint != mockSiteReplicationPeerEndpoint || addedSites[0].Name != "tf-acc-peer" {
		t.Errorf("create body endpoint/name = %q/%q, want %q/tf-acc-peer",
			addedSites[0].Endpoint, addedSites[0].Name, mockSiteReplicationPeerEndpoint)
	}
	if addedSites[0].AccessKey != plan.AccessKey.ValueString() || addedSites[0].SecretKey != plan.SecretKey.ValueString() {
		t.Error("create body lost the peer credentials (values redacted)")
	}
}

func TestSiteReplicationResourceCreateError(t *testing.T) {
	r := NewSiteReplicationResource().(*SiteReplicationResource)
	sch := resourceSchemaOf(t, r)
	srv, _ := newRecorderServer(t, http.StatusInternalServerError, internalErrorBody)
	r.client = newUnitAllClient(srv)

	plan := siteReplicationPlan(mockSiteReplicationPeerEndpoint, siteReplicationSecret, types.BoolValue(false))
	resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, SiteReplicationResourceModel{})}
	r.Create(context.Background(), frameworkresource.CreateRequest{Plan: resourcePlan(t, sch, plan)}, resp)
	assertDiagnosticSummary(t, resp.Diagnostics, "Error creating site replication")
}

func TestSiteReplicationResourceRead(t *testing.T) {
	ctx := context.Background()
	r := NewSiteReplicationResource().(*SiteReplicationResource)
	sch := resourceSchemaOf(t, r)
	srv, recorder := newRecorderServer(t, http.StatusOK, siteReplicationInfoReply)
	r.client = newUnitAllClient(srv)

	state := siteReplicationPlan(mockSiteReplicationPeerEndpoint, siteReplicationSecret, types.BoolValue(false))
	resp := &frameworkresource.ReadResponse{State: resourceState(t, sch, state)}
	r.Read(ctx, frameworkresource.ReadRequest{State: resourceState(t, sch, state)}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	var read SiteReplicationResourceModel
	requireNoDiagnostics(t, resp.State.Get(ctx, &read))
	if read.Endpoint.ValueString() != mockSiteReplicationPeerEndpoint {
		t.Errorf("read endpoint = %q, want %q", read.Endpoint.ValueString(), mockSiteReplicationPeerEndpoint)
	}
	// The server reports the peer's TLS setting, not the plan's.
	if !read.SkipTLSVerify.ValueBool() {
		t.Error("read skip_tls_verify = false, want the server's true")
	}
	if read.Name.ValueString() != "tf-acc-peer" {
		t.Errorf("read name = %q, want tf-acc-peer", read.Name.ValueString())
	}
	if request := recorder.only(t); request.Path != siteReplicationInfoPath {
		t.Errorf("read requested %s, want %s", request.Path, siteReplicationInfoPath)
	}
}

func TestSiteReplicationResourceReadDropsMissingPeer(t *testing.T) {
	ctx := context.Background()
	r := NewSiteReplicationResource().(*SiteReplicationResource)
	sch := resourceSchemaOf(t, r)
	srv, _ := newRecorderServer(t, http.StatusOK, siteReplicationInfoNoPeerReply)
	r.client = newUnitAllClient(srv)

	state := siteReplicationPlan(mockSiteReplicationPeerEndpoint, siteReplicationSecret, types.BoolValue(false))
	resp := &frameworkresource.ReadResponse{State: resourceState(t, sch, state)}
	r.Read(ctx, frameworkresource.ReadRequest{State: resourceState(t, sch, state)}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)
	// The state is not printed: it holds the peer credentials.
	if !resp.State.Raw.IsNull() {
		t.Fatal("read of a removed peer must drop the resource, but the state is not null")
	}
}

func TestSiteReplicationResourceReadError(t *testing.T) {
	r := NewSiteReplicationResource().(*SiteReplicationResource)
	sch := resourceSchemaOf(t, r)
	srv, _ := newRecorderServer(t, http.StatusInternalServerError, internalErrorBody)
	r.client = newUnitAllClient(srv)

	state := siteReplicationPlan(mockSiteReplicationPeerEndpoint, siteReplicationSecret, types.BoolValue(false))
	resp := &frameworkresource.ReadResponse{State: resourceState(t, sch, state)}
	r.Read(context.Background(), frameworkresource.ReadRequest{State: resourceState(t, sch, state)}, resp)
	assertDiagnosticSummary(t, resp.Diagnostics, "Error reading site replication")
}

func TestSiteReplicationResourceUpdate(t *testing.T) {
	ctx := context.Background()
	r := NewSiteReplicationResource().(*SiteReplicationResource)
	sch := resourceSchemaOf(t, r)
	srv, recorder := newRecorderServer(t, http.StatusOK, "")
	r.client = newUnitAllClient(srv)

	state := siteReplicationPlan(mockSiteReplicationPeerEndpoint, siteReplicationSecret, types.BoolValue(false))
	plan := state
	plan.SecretKey = types.StringValue(siteReplicationSecretRotated)
	resp := &frameworkresource.UpdateResponse{State: resourceState(t, sch, state)}
	r.Update(ctx, frameworkresource.UpdateRequest{
		Plan:  resourcePlan(t, sch, plan),
		State: resourceState(t, sch, state),
	}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	var afterUpdate SiteReplicationResourceModel
	requireNoDiagnostics(t, resp.State.Get(ctx, &afterUpdate))
	assertSiteReplicationModelEqual(t, "update state", afterUpdate, plan)

	request := recorder.only(t)
	if request.Method != http.MethodPut || request.Path != siteReplicationEditPath {
		t.Fatalf("update sent %s %s, want PUT %s", request.Method, request.Path, siteReplicationEditPath)
	}
	// The body is not printed on failure: it carries the peer credentials.
	body := string(request.Body)
	if strings.Contains(body, `"endpoints"`) {
		t.Error("edit body must use the singular endpoint field, not the add-only \"endpoints\"")
	}
	if !strings.HasPrefix(body, "{") {
		t.Error("edit body must be a single PeerInfo object, not the add-only array")
	}
	var edited map[string]any
	if err := json.Unmarshal(request.Body, &edited); err != nil {
		t.Fatalf("edit body is not JSON: %v", err)
	}
	if edited["name"] != "tf-acc-peer" || edited["endpoint"] != mockSiteReplicationPeerEndpoint {
		t.Errorf("edit body name/endpoint = %v/%v, want tf-acc-peer/%s",
			edited["name"], edited["endpoint"], mockSiteReplicationPeerEndpoint)
	}
	if edited["secretKey"] != siteReplicationSecretRotated {
		t.Error("edit body does not carry the rotated secret key (value redacted)")
	}
}

func TestSiteReplicationResourceUpdateError(t *testing.T) {
	r := NewSiteReplicationResource().(*SiteReplicationResource)
	sch := resourceSchemaOf(t, r)
	srv, _ := newRecorderServer(t, http.StatusInternalServerError, internalErrorBody)
	r.client = newUnitAllClient(srv)

	state := siteReplicationPlan(mockSiteReplicationPeerEndpoint, siteReplicationSecret, types.BoolValue(false))
	resp := &frameworkresource.UpdateResponse{State: resourceState(t, sch, state)}
	r.Update(context.Background(), frameworkresource.UpdateRequest{
		Plan:  resourcePlan(t, sch, state),
		State: resourceState(t, sch, state),
	}, resp)
	assertDiagnosticSummary(t, resp.Diagnostics, "Error updating site replication")
}

func TestSiteReplicationResourceDelete(t *testing.T) {
	ctx := context.Background()
	r := NewSiteReplicationResource().(*SiteReplicationResource)
	sch := resourceSchemaOf(t, r)
	srv, recorder := newRecorderServer(t, http.StatusOK, "")
	r.client = newUnitAllClient(srv)

	state := siteReplicationPlan(mockSiteReplicationPeerEndpoint, siteReplicationSecret, types.BoolValue(false))
	resp := &frameworkresource.DeleteResponse{State: resourceState(t, sch, state)}
	r.Delete(ctx, frameworkresource.DeleteRequest{State: resourceState(t, sch, state)}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	request := recorder.only(t)
	if request.Method != http.MethodPut || request.Path != siteReplicationRemovePath {
		t.Fatalf("delete sent %s %s, want PUT %s", request.Method, request.Path, siteReplicationRemovePath)
	}
	var removed struct {
		Sites []string `json:"sites"`
	}
	if err := json.Unmarshal(request.Body, &removed); err != nil {
		t.Fatalf("delete body is not JSON: %v", err)
	}
	if len(removed.Sites) != 1 || removed.Sites[0] != "tf-acc-peer" {
		t.Errorf("delete body sites = %v, want [tf-acc-peer]", removed.Sites)
	}
}

func TestSiteReplicationResourceDeleteError(t *testing.T) {
	r := NewSiteReplicationResource().(*SiteReplicationResource)
	sch := resourceSchemaOf(t, r)
	srv, _ := newRecorderServer(t, http.StatusInternalServerError, internalErrorBody)
	r.client = newUnitAllClient(srv)

	state := siteReplicationPlan(mockSiteReplicationPeerEndpoint, siteReplicationSecret, types.BoolValue(false))
	resp := &frameworkresource.DeleteResponse{State: resourceState(t, sch, state)}
	r.Delete(context.Background(), frameworkresource.DeleteRequest{State: resourceState(t, sch, state)}, resp)
	assertDiagnosticSummary(t, resp.Diagnostics, "Error deleting site replication")
}

func TestSiteReplicationResourceImportState(t *testing.T) {
	ctx := context.Background()
	r := NewSiteReplicationResource().(*SiteReplicationResource)
	sch := resourceSchemaOf(t, r)

	resp := &frameworkresource.ImportStateResponse{State: resourceState(t, sch, SiteReplicationResourceModel{})}
	r.ImportState(ctx, frameworkresource.ImportStateRequest{ID: "tf-acc-peer"}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	var name types.String
	requireNoDiagnostics(t, resp.State.GetAttribute(ctx, path.Root("name"), &name))
	if name.ValueString() != "tf-acc-peer" {
		t.Errorf("imported name = %q, want tf-acc-peer", name.ValueString())
	}
}

func siteReplicationLifecycleModel() *SiteReplicationResourceModel {
	return &SiteReplicationResourceModel{
		Name:          types.StringValue("peer"),
		Endpoint:      types.StringValue("https://peer.invalid:9000/"),
		AccessKey:     types.StringValue(siteReplicationAccessKey),
		SecretKey:     types.StringValue(siteReplicationSecret),
		SkipTLSVerify: types.BoolValue(false),
		CACertPEM:     types.StringNull(),
	}
}

// TestSiteReplicationResourceLifecyclePaths covers the paths the acceptance
// suite cannot reach with a live server: a peer that disappeared behind
// Terraform's back, a delete that arrives after the peer is already removed,
// and every failing API call in Create, Read and Delete.
//
// site-replication/info answers 200 on an empty cluster (probed live:
// {"enabled":false,"name":"127.0.0.1","sites":[]}), so Read decides a peer is
// gone from the site list it gets back, not from an error.
func TestSiteReplicationResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			// The live server answers the empty-cluster payload below with 200;
			// a peer that is not in Sites has been removed server-side.
			name:        "peer missing from the site list is removed from state",
			routes:      []requestRoute{{status: http.StatusOK, body: `{"enabled":false,"name":"local","sites":[]}`}},
			wantRemoved: true,
		},
		{
			name:   "configured peer keeps state and is refreshed",
			routes: []requestRoute{{status: http.StatusOK, body: `{"enabled":true,"name":"local","sites":[{"name":"peer","endpoint":"https://peer.invalid:9000","deploymentID":"d1","skipTlsVerify":true}]}`}},
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading site replication",
			wantDetail:  "Could not read site replication: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewSiteReplicationResource(), client)

			resp := h.read(siteReplicationLifecycleModel())

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
				assertStateString(t, resp.State, path.Root("endpoint"), "https://peer.invalid:9000")
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertDiagnosticDetail(t, resp.Diagnostics, "AccessDenied")
			assertStatePresent(t, resp.State)
		})
	}

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			// Probed live: site-replication/remove for an unknown site answers
			// 400 InvalidRequest "none of the requested sites are configured",
			// so destroying a peer that is already gone is a no-op.
			name:   "already removed peer is not an error",
			routes: []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "none of the requested sites are configured")}},
		},
		{
			// Same code, different message: the matcher must not swallow an
			// unrelated InvalidRequest.
			name:        "unrelated InvalidRequest is still reported",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "failed to reach peer site")}},
			wantSummary: "Error deleting site replication",
			wantDetail:  "Could not delete site replication: ",
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "remove exploded")}},
			wantSummary: "Error deleting site replication",
			wantDetail:  "Could not delete site replication: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewSiteReplicationResource(), client)

			resp := h.delete(siteReplicationLifecycleModel())

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	createCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "rejected peer reports the server error and writes no state",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "site peer unreachable")}},
			wantSummary: "Error creating site replication",
			wantDetail:  "Could not create site replication: ",
		},
		{
			name:        "peer already configured is refused",
			routes:      []requestRoute{{status: http.StatusConflict, body: s3ErrorXML("InvalidRequest", "site replication already configured")}},
			wantSummary: "Error creating site replication",
			wantDetail:  "site replication already configured",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewSiteReplicationResource(), client)
			plan := siteReplicationLifecycleModel()

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, plan)
		})
	}

	t.Run("create: accepted peer writes state", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{status: http.StatusOK, body: `{"status":"success"}`})
		h := newCRUDHarness(t, NewSiteReplicationResource(), client)
		plan := siteReplicationLifecycleModel()

		resp := h.create(plan)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("name"), "peer")
	})

	t.Run("update: rejected edit reports a diagnostic and keeps the prior state", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "edit exploded")})
		h := newCRUDHarness(t, NewSiteReplicationResource(), client)

		plan := siteReplicationLifecycleModel()
		plan.Endpoint = types.StringValue("https://new.invalid:9000/")
		state := siteReplicationLifecycleModel()
		state.Endpoint = types.StringValue("https://old.invalid:9000/")

		resp := h.update(plan, state)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error updating site replication", "Could not update site replication: ")
		assertStateString(t, resp.State, path.Root("endpoint"), "https://old.invalid:9000/")
	})

	t.Run("update: capability rejection is surfaced and keeps the prior state", func(t *testing.T) {
		// An in-place edit can be refused when the peer cannot negotiate the
		// change (for example TLS settings on an http endpoint). The diagnostic
		// must name the server's refusal and the state must stay on the values
		// Terraform already knows.
		client, _ := routedClient(t, requestRoute{
			status: http.StatusBadRequest,
			body:   s3ErrorXML("InvalidRequest", "site replication TLS settings require an HTTPS endpoint"),
		})
		h := newCRUDHarness(t, NewSiteReplicationResource(), client)

		plan := siteReplicationLifecycleModel()
		plan.SkipTLSVerify = types.BoolValue(true)
		state := siteReplicationLifecycleModel()

		resp := h.update(plan, state)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error updating site replication", "Could not update site replication: ")
		assertDiagnosticDetail(t, resp.Diagnostics, "require an HTTPS endpoint")
		assertStatePresent(t, resp.State)
	})

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewSiteReplicationResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
