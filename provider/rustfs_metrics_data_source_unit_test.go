// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The acceptance test skips when the running RustFS answers 501 for
// GET /rustfs/admin/v3/metrics (every released server does), so the data
// source's success path, capability-gap diagnostic and unexpected-error
// diagnostic are exercised here against the admin API.

func TestMetricsDataSourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		d := NewMetricsDataSource().(*MetricsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if d.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		d := NewMetricsDataSource().(*MetricsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
	})

	t.Run("all client", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, "")
		d := NewMetricsDataSource().(*MetricsDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: newUnitAllClient(srv)}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if d.client == nil {
			t.Error("client was not set")
		}
	})
}

func TestMetricsDataSourceRead(t *testing.T) {
	t.Run("writes the raw metrics stream to the state", func(t *testing.T) {
		const body = "rustfs_hosts_total 1\nrustfs_drive_read_throughput 456\n"
		srv, recorder := newRecorderServer(t, http.StatusOK, body)
		d := NewMetricsDataSource().(*MetricsDataSource)
		d.client = newUnitAllClient(srv)
		sch := dataSourceSchemaOf(t, d)

		resp := readMetrics(t, d, sch)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}

		request := recorder.only(t)
		if request.Method != http.MethodGet || request.Path != "/rustfs/admin/v3/metrics" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}

		var got MetricsDataSourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.Metrics.ValueString() != body {
			t.Errorf("metrics = %q, want the raw stream %q", got.Metrics.ValueString(), body)
		}
	})

	t.Run("explains a capability gap with the server version", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/rustfs/admin/v3/metrics":
				w.WriteHeader(http.StatusNotImplemented)
				_, _ = io.WriteString(w, `<Error><Code>NotImplemented</Code><Message>not implemented</Message></Error>`)
			case "/rustfs/admin/v3/healthinfo":
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"version":"refs/tags/1.0.3"}`)
			default:
				t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}))
		t.Cleanup(srv.Close)

		d := NewMetricsDataSource().(*MetricsDataSource)
		d.client = newUnitAllClient(srv)
		resp := readMetrics(t, d, dataSourceSchemaOf(t, d))

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Metrics are not available on this RustFS server")
		for _, want := range []string{"RustFS 1.0.3", "GET /rustfs/admin/v3/metrics", "Upgrade RustFS"} {
			if !strings.Contains(diagnostic.Detail(), want) {
				t.Errorf("detail %q does not contain %q", diagnostic.Detail(), want)
			}
		}
	})

	t.Run("reports an unexpected error without calling it a capability gap", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusInternalServerError, `<Error><Code>InternalError</Code><Message>boom</Message></Error>`)
		d := NewMetricsDataSource().(*MetricsDataSource)
		d.client = newUnitAllClient(srv)
		resp := readMetrics(t, d, dataSourceSchemaOf(t, d))

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error reading metrics")
		if !strings.Contains(diagnostic.Detail(), "boom") {
			t.Errorf("detail %q does not carry the server payload", diagnostic.Detail())
		}
		if strings.Contains(diagnostic.Detail(), "not available") {
			t.Errorf("a server error must not be reported as a capability gap: %q", diagnostic.Detail())
		}
	})
}

func readMetrics(t *testing.T, d *MetricsDataSource, sch datasourceschema.Schema) *datasource.ReadResponse {
	t.Helper()

	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	d.Read(context.Background(), datasource.ReadRequest{
		Config: dataSourceConfig(t, sch, MetricsDataSourceModel{Metrics: types.StringNull()}),
	}, resp)
	return resp
}
