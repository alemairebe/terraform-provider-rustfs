// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestTLSCertificateStateDataSourceSchema(t *testing.T) {
	d := NewTLSCertificateStateDataSource()
	resp := &datasource.SchemaResponse{}
	d.Schema(context.Background(), datasource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{
		"schema_version", "resource_id", "target_revision", "certificate_fingerprint",
		"not_after_unix", "last_issuance_id", "observed_certificate_fingerprint",
	} {
		attribute, ok := attrs[name]
		if !ok {
			t.Errorf("missing schema attribute %q", name)
			continue
		}
		if !attribute.IsComputed() {
			t.Errorf("%s must be computed", name)
		}
		if attribute.GetDescription() == "" {
			t.Errorf("%s must carry a description", name)
		}
	}
}

func TestTLSCertificateStateDataSourceMetadata(t *testing.T) {
	d := NewTLSCertificateStateDataSource()
	resp := &datasource.MetadataResponse{}
	d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_tls_certificate_state" {
		t.Errorf("expected rustfs_tls_certificate_state, got %s", resp.TypeName)
	}
}

// readTLSCertificateState drives the data source once against client.
func readTLSCertificateState(t *testing.T, client *AllClient) *datasource.ReadResponse {
	t.Helper()

	d := NewTLSCertificateStateDataSource().(*TLSCertificateStateDataSource)
	configureResp := &datasource.ConfigureResponse{}
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: client}, configureResp)
	if configureResp.Diagnostics.HasError() {
		t.Fatalf("data source configure: %v", configureResp.Diagnostics)
	}

	sch := dataSourceSchemaOf(t, d)
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, TLSCertificateStateDataSourceModel{}),
	}, resp)
	return resp
}

func TestTLSCertificateStateDataSourceRead(t *testing.T) {
	t.Parallel()

	client, recorder := routedClient(t, requestRoute{
		method:       http.MethodGet,
		pathContains: "tls/certificate-state",
		status:       http.StatusOK,
		body: `{"schema_version":"rustfs.tls-publish/v1alpha1","resource_id":"server-default",` +
			`"target_revision":"1e04976a-316c-43ed-86cd-e7779b8ccd0a",` +
			`"certificate_fingerprint":"67fd508faf9701264f6385249f4cd3a269334625c20c475118ed0f8926f77e32",` +
			`"not_after_unix":1797684279,"last_issuance_id":"65f4ec47-53a7-415b-be51-1381f51adb9c",` +
			`"observed_certificate_fingerprint":"67fd508faf9701264f6385249f4cd3a269334625c20c475118ed0f8926f77e32"}`,
	})

	resp := readTLSCertificateState(t, client)

	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateString(t, resp.State, path.Root("schema_version"), "rustfs.tls-publish/v1alpha1")
	assertStateString(t, resp.State, path.Root("resource_id"), "server-default")
	assertStateString(t, resp.State, path.Root("target_revision"), "1e04976a-316c-43ed-86cd-e7779b8ccd0a")
	assertStateString(t, resp.State, path.Root("certificate_fingerprint"), "67fd508faf9701264f6385249f4cd3a269334625c20c475118ed0f8926f77e32")
	assertStateString(t, resp.State, path.Root("last_issuance_id"), "65f4ec47-53a7-415b-be51-1381f51adb9c")
	assertStateString(t, resp.State, path.Root("observed_certificate_fingerprint"), "67fd508faf9701264f6385249f4cd3a269334625c20c475118ed0f8926f77e32")
	assertStateInt64(t, resp.State, path.Root("not_after_unix"), 1797684279)
	recorder.assertRequest(t, "GET /rustfs/admin/v3/tls/certificate-state")
}

func TestTLSCertificateStateDataSourceBootstrap(t *testing.T) {
	t.Parallel()

	client, _ := routedClient(t, requestRoute{
		method:       http.MethodGet,
		pathContains: "tls/certificate-state",
		status:       http.StatusOK,
		body: `{"schema_version":"rustfs.tls-publish/v1alpha1","resource_id":"server-default",` +
			`"target_revision":"bootstrap-2bdfefa0843cd9b13c7cac9a1e446dde1b943706c200a11982202712f4161ba3",` +
			`"certificate_fingerprint":"2bdfefa0843cd9b13c7cac9a1e446dde1b943706c200a11982202712f4161ba3",` +
			`"not_after_unix":1795092189,"last_issuance_id":null,"observed_certificate_fingerprint":null}`,
	})

	resp := readTLSCertificateState(t, client)

	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateString(t, resp.State, path.Root("target_revision"), "bootstrap-2bdfefa0843cd9b13c7cac9a1e446dde1b943706c200a11982202712f4161ba3")
	var issuance types.String
	if diags := resp.State.GetAttribute(context.Background(), path.Root("last_issuance_id"), &issuance); diags.HasError() {
		t.Fatalf("last_issuance_id: %v", diags)
	}
	if !issuance.IsNull() {
		t.Errorf("expected a null last_issuance_id, got %s", issuance)
	}
}

func TestTLSCertificateStateDataSourceErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			name: "1.0.1 unimplemented route reports the capability gap",
			routes: []requestRoute{
				{method: http.MethodGet, pathContains: "tls/certificate-state", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "A header you provided implies functionality that is not implemented.")},
				{pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.1"}`},
			},
			wantSummary: "Error reading TLS certificate state",
			wantDetail:  "requires RustFS 1.0.2 or later",
		},
		{
			name: "plain HTTP listener is explained instead of the raw payload",
			routes: []requestRoute{{
				method:       http.MethodGet,
				pathContains: "tls/certificate-state",
				status:       http.StatusForbidden,
				body:         s3ErrorXML("AccessDenied", "certificate management requires TLS on the RustFS listener"),
			}},
			wantSummary: "TLS certificate management needs the native TLS listener",
			wantDetail:  "ssl = false",
		},
		{
			name: "publication disabled is explained",
			routes: []requestRoute{{
				method:       http.MethodGet,
				pathContains: "tls/certificate-state",
				status:       http.StatusServiceUnavailable,
				body:         s3ErrorXML("InvalidRequest", "certificate publication is disabled or not configured safely"),
			}},
			wantSummary: "TLS certificate publication is not enabled",
			wantDetail:  "RUSTFS_TLS_RELOAD_ENABLE",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)

			resp := readTLSCertificateState(t, client)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}
}

func TestTLSCertificateStateDataSourceConfigure(t *testing.T) {
	t.Parallel()

	d := NewTLSCertificateStateDataSource().(*TLSCertificateStateDataSource)
	resp := &datasource.ConfigureResponse{}
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)

	assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Data Source Configure Type", "Expected *AllClient, got: string.")
}
