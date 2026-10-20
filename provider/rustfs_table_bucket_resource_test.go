// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestTableBucketResourceSchema(t *testing.T) {
	r := NewTableBucketResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", resp.Diagnostics)
	}

	attrs := resp.Schema.GetAttributes()
	for _, name := range []string{
		"bucket", "enabled", "catalog_type", "warehouse", "catalog_entry_present", "catalog_uri",
	} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("missing schema attribute %q", name)
		}
	}

	bucket, ok := attrs["bucket"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("bucket attribute is %T, want schema.StringAttribute", attrs["bucket"])
	}
	if !bucket.Required {
		t.Error("bucket must be required")
	}
	if !hasRequiresReplace(bucket.PlanModifiers) {
		t.Error("bucket must carry RequiresReplace: the catalog switch is bound to the bucket")
	}

	for _, name := range []string{"enabled", "catalog_type", "warehouse", "catalog_entry_present", "catalog_uri"} {
		if !attrs[name].IsComputed() {
			t.Errorf("%s must be computed", name)
		}
	}
}

func TestTableBucketResourceMetadata(t *testing.T) {
	r := NewTableBucketResource()
	resp := &resource.MetadataResponse{}
	r.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_table_bucket" {
		t.Errorf("expected rustfs_table_bucket, got %s", resp.TypeName)
	}
}

// tableBucketLifecycleModel is a plan/state value with every attribute set; it
// is what a refresh sees after a successful apply.
func tableBucketLifecycleModel() *TableBucketResourceModel {
	return &TableBucketResourceModel{
		Bucket:              types.StringValue("lakehouse"),
		Enabled:             types.BoolValue(true),
		CatalogType:         types.StringValue("iceberg-rest"),
		Warehouse:           types.StringValue("lakehouse"),
		CatalogEntryPresent: types.BoolValue(true),
		CatalogURI:          types.StringValue("/iceberg/v1/lakehouse"),
	}
}

// tableBucketCreatePlan leaves every computed attribute null, the way the
// framework plans a create.
func tableBucketCreatePlan() *TableBucketResourceModel {
	return &TableBucketResourceModel{
		Bucket:              types.StringValue("lakehouse"),
		Enabled:             types.BoolNull(),
		CatalogType:         types.StringNull(),
		Warehouse:           types.StringNull(),
		CatalogEntryPresent: types.BoolNull(),
		CatalogURI:          types.StringNull(),
	}
}

const tableBucketEnabledBody = `{"table-bucket":"lakehouse","enabled":true,"catalog-type":"iceberg-rest",` +
	`"warehouse":"lakehouse","catalog-entry-present":true,"catalog-uri":"/iceberg/v1/lakehouse"}`

func assertTableBucketStateBool(t *testing.T, state tfsdk.State, attrPath path.Path, want bool) {
	t.Helper()

	var got types.Bool
	if diags := state.GetAttribute(context.Background(), attrPath, &got); diags.HasError() {
		t.Fatalf("state attribute %s: %v", attrPath, diags)
	}
	if got.ValueBool() != want {
		t.Fatalf("state attribute %s = %v, want %v", attrPath, got.ValueBool(), want)
	}
}

// TestTableBucketResourceLifecyclePaths covers the paths the acceptance suite
// cannot reach with a live server: a catalog switch cleared behind Terraform's
// back, the 1.0.1 answers (missing metadata and an unimplemented destroy), and
// every failing API call in Create, Read and Delete.
func TestTableBucketResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			name:   "enabled catalog keeps state and mirrors the server",
			routes: []requestRoute{{status: http.StatusOK, body: tableBucketEnabledBody}},
		},
		{
			// The switch is the resource: a bucket that was never enabled, or
			// was disabled outside Terraform, has nothing left to manage.
			name:        "disabled catalog is removed from state",
			routes:      []requestRoute{{status: http.StatusOK, body: `{"table-bucket":"lakehouse","enabled":false,"catalog-type":"iceberg-rest","warehouse":"lakehouse","catalog-entry-present":false,"catalog-uri":"/iceberg/v1/lakehouse"}`}},
			wantRemoved: true,
		},
		{
			// Probed live on 1.0.1: GET for a missing bucket answers 400 with
			// "Config not found" instead of the 200/disabled answer 1.0.2+ give.
			name:        "1.0.1 missing bucket metadata is removed from state",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: `{"error":{"message":"failed to load table bucket metadata for lakehouse: Config not found","type":"BadRequestException","code":400}}`}},
			wantRemoved: true,
		},
		{
			// Same status, unrelated message: the matcher must stay narrow.
			name:        "unrelated bad request keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: `{"error":{"message":"bucket lakehouse is not a table bucket","type":"BadRequestException","code":400}}`}},
			wantSummary: "Error reading table bucket",
			wantDetail:  "Could not read the S3 Tables catalog for bucket lakehouse: ",
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading table bucket",
			wantDetail:  "Could not read the S3 Tables catalog for bucket lakehouse: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewTableBucketResource(), client)

			resp := h.read(tableBucketLifecycleModel())

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
				assertTableBucketStateBool(t, resp.State, path.Root("enabled"), true)
				assertStateString(t, resp.State, path.Root("catalog_uri"), "/iceberg/v1/lakehouse")
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertStatePresent(t, resp.State)
		})
	}

	deleteCases := []struct {
		name         string
		routes       []requestRoute
		wantSummary  string
		wantDetail   string
		wantWarnings bool
	}{
		{
			// Probed live on 1.0.1: DELETE answers 501 NotImplemented. Destroy
			// must warn (naming the route) and drop the resource from state.
			name: "1.0.1 unimplemented disable warns instead of failing",
			routes: []requestRoute{
				{method: http.MethodDelete, status: http.StatusNotImplemented, body: `{"error":{"message":"A header you provided implies functionality that is not implemented.","type":"RESTException","code":501}}`},
				{pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.1"}`},
			},
			wantWarnings: true,
		},
		{
			// Probed live on 1.0.2/1.0.3: deleting the switch of a bucket that
			// no longer exists answers 500 RESTException "Bucket not found".
			name:   "already deleted bucket is not an error",
			routes: []requestRoute{{status: http.StatusInternalServerError, body: `{"error":{"message":"failed to disable table bucket: Bucket not found: lakehouse","type":"RESTException","code":500}}`}},
		},
		{
			name:        "unrelated 500 is still reported",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: `{"error":{"message":"failed to disable table bucket: disk offline","type":"RESTException","code":500}}`}},
			wantSummary: "Error deleting table bucket",
			wantDetail:  "Could not disable the S3 Tables catalog for bucket lakehouse: ",
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error deleting table bucket",
			wantDetail:  "Could not disable the S3 Tables catalog for bucket lakehouse: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewTableBucketResource(), client)

			resp := h.delete(tableBucketLifecycleModel())

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				if !tc.wantWarnings {
					return
				}
				warnings := resp.Diagnostics.Warnings()
				if len(warnings) != 1 {
					t.Fatalf("expected exactly one warning, got %d: %v", len(warnings), resp.Diagnostics)
				}
				if got := warnings[0].Summary(); got != "S3 Tables catalog switch left enabled" {
					t.Errorf("warning summary = %q", got)
				}
				if got := warnings[0].Detail(); !containsAll(got, "DELETE /iceberg/v1/buckets/{bucket}", "1.0.2") {
					t.Errorf("warning detail does not name the route and required version: %q", got)
				}
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
			// Probed live: PUT for a missing bucket answers 400 "Bucket not
			// found".
			name:        "missing bucket reports a diagnostic and writes no state",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: `{"error":{"message":"failed to enable table bucket lakehouse: Bucket not found: lakehouse","type":"BadRequestException","code":400}}`}},
			wantSummary: "Error creating table bucket",
			wantDetail:  "Could not enable the S3 Tables catalog for bucket lakehouse: ",
		},
		{
			name:        "rejected request reports the server error",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error creating table bucket",
			wantDetail:  "Could not enable the S3 Tables catalog for bucket lakehouse: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewTableBucketResource(), client)
			plan := tableBucketCreatePlan()

			resp := h.create(plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, plan)
		})
	}

	t.Run("create: accepted request writes the catalog state the server returned", func(t *testing.T) {
		client, recorder := routedClient(t, requestRoute{method: http.MethodPut, status: http.StatusOK, body: tableBucketEnabledBody})
		h := newCRUDHarness(t, NewTableBucketResource(), client)

		resp := h.create(tableBucketCreatePlan())

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("bucket"), "lakehouse")
		assertStateString(t, resp.State, path.Root("catalog_type"), "iceberg-rest")
		assertStateString(t, resp.State, path.Root("warehouse"), "lakehouse")
		assertStateString(t, resp.State, path.Root("catalog_uri"), "/iceberg/v1/lakehouse")
		assertTableBucketStateBool(t, resp.State, path.Root("enabled"), true)
		assertTableBucketStateBool(t, resp.State, path.Root("catalog_entry_present"), true)
		recorder.assertRequest(t, "PUT /iceberg/v1/buckets/lakehouse")
	})

	t.Run("delete: disables the catalog switch and leaves the bucket untouched", func(t *testing.T) {
		client, recorder := routedClient(t, requestRoute{
			method:       http.MethodDelete,
			pathContains: "/iceberg/v1/buckets/lakehouse",
			status:       http.StatusOK,
			body:         `{"table-bucket":"lakehouse","enabled":false,"catalog-type":"iceberg-rest","warehouse":"lakehouse","catalog-entry-present":true,"catalog-uri":"/iceberg/v1/lakehouse"}`,
		})
		h := newCRUDHarness(t, NewTableBucketResource(), client)

		resp := h.delete(tableBucketLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		recorder.assertRequest(t, "DELETE /iceberg/v1/buckets/lakehouse")
		// Disabling the catalog must not delete the bucket itself.
		recorder.assertNoRequest(t, "DELETE /lakehouse")
	})

	t.Run("update: writes the plan back without an API call", func(t *testing.T) {
		client, recorder := routedClient(t)
		h := newCRUDHarness(t, NewTableBucketResource(), client)

		plan := tableBucketLifecycleModel()
		state := tableBucketLifecycleModel()
		state.CatalogURI = types.StringValue("/iceberg/v1/old")

		resp := h.update(plan, state)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("catalog_uri"), "/iceberg/v1/lakehouse")
		recorder.assertNoRequest(t, "/iceberg/v1/buckets")
	})

	t.Run("import: the bucket name is the import ID", func(t *testing.T) {
		r := NewTableBucketResource()
		sch := resourceSchemaOf(t, r)
		resp := &resource.ImportStateResponse{State: resourceState(t, sch, TableBucketResourceModel{})}

		importer, ok := r.(resource.ResourceWithImportState)
		if !ok {
			t.Fatal("resource does not implement resource.ResourceWithImportState")
		}
		importer.ImportState(context.Background(), resource.ImportStateRequest{ID: "lakehouse"}, resp)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("bucket"), "lakehouse")
	})

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewTableBucketResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

// containsAll reports whether s contains every substring.
func containsAll(s string, wants ...string) bool {
	for _, want := range wants {
		if !strings.Contains(s, want) {
			return false
		}
	}
	return true
}
