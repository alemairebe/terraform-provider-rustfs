// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestSiteReplicationRessourceSchema(t *testing.T) {
	r := NewSiteReplicationRessource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %s", resp.Diagnostics)
	}
	for _, attr := range []string{"name", "endpoint", "access_key", "secret_key", "skip_tls_verify", "ca_cert_pem"} {
		if _, ok := resp.Schema.Attributes[attr]; !ok {
			t.Errorf("missing schema attribute %q", attr)
		}
	}
}

// TestSiteReplicationRessourceImportState covers ImportState, which is a plain
// passthrough: the import ID is the peer name and is copied verbatim into
// "name" by resource.ImportStatePassthroughID. The resource therefore has no
// composite "<a>/<b>" import grammar and no malformed-ID diagnostic to assert -
// an ID that would be malformed for the resources that split on "/" is accepted
// here unchanged, and the acceptance test consequently has no import-error
// step. What this test pins down is the passthrough itself: the ID lands in
// "name" unchanged, no other attribute is invented (only Read fills them in,
// and the write-only credentials never come back), and an empty ID - which the
// framework helper skips - leaves the state untouched rather than half-built.
func TestSiteReplicationRessourceImportState(t *testing.T) {
	ctx := context.Background()
	r, ok := NewSiteReplicationRessource().(resource.ResourceWithImportState)
	if !ok {
		t.Fatal("rustfs_site_replication must implement resource.ResourceWithImportState")
	}
	schemaResp := &resource.SchemaResponse{}
	r.Schema(ctx, resource.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %s", schemaResp.Diagnostics)
	}

	for _, tc := range []struct {
		name string
		id   string
		// The framework helper passes nothing through when the ID is empty,
		// so no state is built at all.
		emptyID bool
	}{
		{name: "peer name", id: "peer"},
		{name: "composite-looking id", id: "group/policy"},
		{name: "empty id", id: "", emptyID: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := &resource.ImportStateResponse{State: tfsdk.State{
				Schema: schemaResp.Schema,
				Raw:    tftypes.NewValue(schemaResp.Schema.Type().TerraformType(ctx), nil),
			}}

			r.ImportState(ctx, resource.ImportStateRequest{ID: tc.id}, resp)

			if resp.Diagnostics.HasError() {
				t.Fatalf("import diagnostics: %s", resp.Diagnostics)
			}
			if tc.emptyID {
				// resource.ImportStatePassthroughID ignores an empty ID
				// silently, so nothing - not even a null attribute - is
				// written and the ID is neither rejected nor kept.
				if !resp.State.Raw.IsNull() {
					t.Errorf("empty import ID populated state: %s", resp.State.Raw)
				}
				return
			}
			var got SiteReplicationRessourceModel
			if diags := resp.State.Get(ctx, &got); diags.HasError() {
				t.Fatalf("state diagnostics: %s", diags)
			}
			if got.Name.ValueString() != tc.id {
				t.Errorf("name = %q, want the import ID %q passed through verbatim", got.Name.ValueString(), tc.id)
			}
			if !got.Endpoint.IsNull() || !got.AccessKey.IsNull() || !got.SecretKey.IsNull() ||
				!got.SkipTLSVerify.IsNull() || !got.CACertPEM.IsNull() {
				t.Errorf("import must populate the name only, got %+v", got)
			}
		})
	}
}

// TestSiteReplicationRessourceUpdate covers Update against a stand-in admin API
// without needing a cluster that can form a site-replication group (see
// TestAccSiteReplicationResource, which skips on a single-node server). It
// asserts the edit request the resource sends, that the plan is written back to
// state, and that a rejected edit surfaces as a diagnostic instead of a
// half-written state.
func TestSiteReplicationRessourceUpdate(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name       string
		status     int
		response   string
		wantUpdate bool
		wantErr    string
	}{
		{
			name:       "edit is accepted",
			status:     http.StatusOK,
			response:   `{"status":"success"}`,
			wantUpdate: true,
		},
		{
			name:     "edit is rejected",
			status:   http.StatusForbidden,
			response: `<?xml version="1.0" encoding="UTF-8"?><Error><Code>InvalidRequest</Code><Message>invalid site endpoint</Message></Error>`,
			wantErr:  "Error updating site replication",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotMethod, gotPath, gotBody string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				body, _ := io.ReadAll(r.Body)
				gotBody = string(body)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.response))
			}))
			t.Cleanup(server.Close)

			client := rustfs.New(&rustfs.RustfsAdminConfig{
				AccessKey:    "admin",
				AccessSecret: "secret",
				Endpoint:     strings.TrimPrefix(server.URL, "http://"),
			})
			r := &SiteReplicationRessource{client: &AllClient{RustClient: client}}

			schemaResp := &resource.SchemaResponse{}
			r.Schema(ctx, resource.SchemaRequest{}, schemaResp)
			if schemaResp.Diagnostics.HasError() {
				t.Fatalf("schema diagnostics: %s", schemaResp.Diagnostics)
			}
			schemaType := schemaResp.Schema.Type().TerraformType(ctx)

			// The trailing slash is deliberate: modelToSite trims it before
			// the endpoint reaches the wire.
			plan := tfsdk.Plan{Schema: schemaResp.Schema, Raw: tftypes.NewValue(schemaType, nil)}
			diags := plan.Set(ctx, SiteReplicationRessourceModel{
				Name:          types.StringValue("peer"),
				Endpoint:      types.StringValue("http://127.0.0.1:9002/"),
				AccessKey:     types.StringValue("peeradmin"),
				SecretKey:     types.StringValue("peersecret"),
				SkipTLSVerify: types.BoolValue(true),
				CACertPEM:     types.StringValue(""),
			})
			if diags.HasError() {
				t.Fatalf("plan diagnostics: %s", diags)
			}

			resp := &resource.UpdateResponse{State: tfsdk.State{
				Schema: schemaResp.Schema,
				Raw:    tftypes.NewValue(schemaType, nil),
			}}
			r.Update(ctx, resource.UpdateRequest{Plan: plan}, resp)

			if gotMethod != http.MethodPut {
				t.Errorf("edit request method = %q, want %q", gotMethod, http.MethodPut)
			}
			if !strings.HasSuffix(gotPath, "/site-replication/edit") {
				t.Errorf("edit request path = %q, want it to end in /site-replication/edit", gotPath)
			}

			if tc.wantErr != "" {
				if !resp.Diagnostics.HasError() {
					t.Fatalf("expected a diagnostic for a rejected edit, got none")
				}
				var messages []string
				for _, d := range resp.Diagnostics.Errors() {
					messages = append(messages, fmt.Sprintf("%s: %s", d.Summary(), d.Detail()))
				}
				if joined := strings.Join(messages, "\n"); !strings.Contains(joined, tc.wantErr) {
					t.Errorf("diagnostics = %q, want them to mention %q", joined, tc.wantErr)
				}
				return
			}

			if resp.Diagnostics.HasError() {
				t.Fatalf("update diagnostics: %s", resp.Diagnostics)
			}

			var payload []rustfs.SiteReplicationSite
			if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
				t.Fatalf("edit body must be a JSON array: %v (body: %s)", err, gotBody)
			}
			if len(payload) != 1 {
				t.Fatalf("edit body must carry exactly one site, got %d (body: %s)", len(payload), gotBody)
			}
			site := payload[0]
			if site.Name != "peer" {
				t.Errorf("edit body name = %q, want %q", site.Name, "peer")
			}
			if site.Endpoint != "http://127.0.0.1:9002" {
				t.Errorf("edit body endpoint = %q, want the trailing slash trimmed", site.Endpoint)
			}
			if site.AccessKey != "peeradmin" || site.SecretKey != "peersecret" {
				// The values are credentials: report the mismatch, not them.
				t.Error("edit body credentials do not match the configured ones")
			}
			if !site.SkipTLSVerify {
				t.Error("edit body skipTlsVerify = false, want true")
			}

			var got SiteReplicationRessourceModel
			if diags := resp.State.Get(ctx, &got); diags.HasError() {
				t.Fatalf("state diagnostics: %s", diags)
			}
			if got.Endpoint.ValueString() != "http://127.0.0.1:9002/" || !got.SkipTLSVerify.ValueBool() {
				t.Errorf("state after update = %+v, want the plan written back unchanged", got)
			}
		})
	}
}
