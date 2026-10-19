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
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestSiteReplicationRessourceSchema(t *testing.T) {
	r := NewSiteReplicationRessource()
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

func siteReplicationPlan(endpoint, secretKey string, skipTLSVerify types.Bool) SiteReplicationRessourceModel {
	return SiteReplicationRessourceModel{
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
func assertSiteReplicationModelEqual(t *testing.T, what string, got, want SiteReplicationRessourceModel) {
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
	r := NewSiteReplicationRessource().(*SiteReplicationRessource)
	sch := resourceSchemaOf(t, r)
	srv, recorder := newRecorderServer(t, http.StatusOK, "")
	r.client = newUnitAllClient(srv)

	// A trailing slash in the configuration must not reach the wire.
	plan := siteReplicationPlan(mockSiteReplicationPeerEndpoint+"/", siteReplicationSecret, types.BoolValue(false))
	resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, SiteReplicationRessourceModel{})}
	r.Create(ctx, frameworkresource.CreateRequest{Plan: resourcePlan(t, sch, plan)}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	var created SiteReplicationRessourceModel
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
	r := NewSiteReplicationRessource().(*SiteReplicationRessource)
	sch := resourceSchemaOf(t, r)
	srv, _ := newRecorderServer(t, http.StatusInternalServerError, internalErrorBody)
	r.client = newUnitAllClient(srv)

	plan := siteReplicationPlan(mockSiteReplicationPeerEndpoint, siteReplicationSecret, types.BoolValue(false))
	resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, SiteReplicationRessourceModel{})}
	r.Create(context.Background(), frameworkresource.CreateRequest{Plan: resourcePlan(t, sch, plan)}, resp)
	assertDiagnosticSummary(t, resp.Diagnostics, "Error creating site replication")
}

func TestSiteReplicationResourceRead(t *testing.T) {
	ctx := context.Background()
	r := NewSiteReplicationRessource().(*SiteReplicationRessource)
	sch := resourceSchemaOf(t, r)
	srv, recorder := newRecorderServer(t, http.StatusOK, siteReplicationInfoReply)
	r.client = newUnitAllClient(srv)

	state := siteReplicationPlan(mockSiteReplicationPeerEndpoint, siteReplicationSecret, types.BoolValue(false))
	resp := &frameworkresource.ReadResponse{State: resourceState(t, sch, state)}
	r.Read(ctx, frameworkresource.ReadRequest{State: resourceState(t, sch, state)}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	var read SiteReplicationRessourceModel
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
	r := NewSiteReplicationRessource().(*SiteReplicationRessource)
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
	r := NewSiteReplicationRessource().(*SiteReplicationRessource)
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
	r := NewSiteReplicationRessource().(*SiteReplicationRessource)
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

	var afterUpdate SiteReplicationRessourceModel
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
	r := NewSiteReplicationRessource().(*SiteReplicationRessource)
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
	r := NewSiteReplicationRessource().(*SiteReplicationRessource)
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
	r := NewSiteReplicationRessource().(*SiteReplicationRessource)
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
	r := NewSiteReplicationRessource().(*SiteReplicationRessource)
	sch := resourceSchemaOf(t, r)

	resp := &frameworkresource.ImportStateResponse{State: resourceState(t, sch, SiteReplicationRessourceModel{})}
	r.ImportState(ctx, frameworkresource.ImportStateRequest{ID: "tf-acc-peer"}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	var name types.String
	requireNoDiagnostics(t, resp.State.GetAttribute(ctx, path.Root("name"), &name))
	if name.ValueString() != "tf-acc-peer" {
		t.Errorf("imported name = %q, want tf-acc-peer", name.ValueString())
	}
}
