// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestObjectDataCacheFlushResourceSchema(t *testing.T) {
	sch := resourceSchemaOf(t, NewObjectDataCacheFlushResource())

	attrs := sch.GetAttributes()
	if len(attrs) != 7 {
		t.Errorf("expected 7 attributes, got %d: %v", len(attrs), attrs)
	}

	for _, name := range []string{"id", "scope", "outcome"} {
		attr, ok := attrs[name].(schema.StringAttribute)
		if !ok || !attr.Computed || attr.Required || attr.Optional {
			t.Errorf("%s must be Computed only: %+v", name, attrs[name])
		}
	}

	for _, name := range []string{"triggers", "bucket", "object"} {
		attr := attrs[name]
		if attr == nil || !attr.IsOptional() || attr.IsRequired() || attr.IsComputed() {
			t.Errorf("%s must be Optional only: %+v", name, attr)
		}
	}

	removed, ok := attrs["removed_keys"].(schema.Int64Attribute)
	if !ok || !removed.Computed || removed.Required || removed.Optional {
		t.Errorf("removed_keys must be Computed only: %+v", attrs["removed_keys"])
	}

	for name, attr := range attrs {
		if attr.GetDescription() == "" {
			t.Errorf("attribute %s has no description", name)
		}
	}
}

func TestObjectDataCacheFlushResourceMetadata(t *testing.T) {
	resp := &frameworkresource.MetadataResponse{}
	NewObjectDataCacheFlushResource().Metadata(context.Background(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_object_data_cache_flush" {
		t.Errorf("expected rustfs_object_data_cache_flush, got %s", resp.TypeName)
	}
}

func TestObjectDataCacheFlushResourceValidateConfig(t *testing.T) {
	t.Run("rejects an object without a bucket", func(t *testing.T) {
		h := newCRUDHarness(t, NewObjectDataCacheFlushResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.validateConfig(&ObjectDataCacheFlushResourceModel{
			Triggers: nullStringMap(),
			Bucket:   types.StringNull(),
			Object:   types.StringValue("a.log"),
		})

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Invalid object data cache flush")
		if !strings.Contains(diagnostic.Detail(), "object requires bucket") {
			t.Errorf("detail = %q", diagnostic.Detail())
		}
	})

	t.Run("accepts an object with a bucket", func(t *testing.T) {
		h := newCRUDHarness(t, NewObjectDataCacheFlushResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.validateConfig(&ObjectDataCacheFlushResourceModel{
			Triggers: nullStringMap(),
			Bucket:   types.StringValue("logs"),
			Object:   types.StringValue("a.log"),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
	})
}

func TestObjectDataCacheFlushResourceCreate(t *testing.T) {
	t.Run("flushes the whole cache", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, `{"scope":"all","bucket":null,"object":null,"outcome":"noop","removed_keys":0}`)
		h := newCRUDHarness(t, NewObjectDataCacheFlushResource(), newUnitAllClient(srv))

		resp := h.create(&ObjectDataCacheFlushResourceModel{
			Triggers: nullStringMap(),
			Bucket:   types.StringNull(),
			Object:   types.StringNull(),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		request := recorder.only(t)
		if request.Method != http.MethodPost || request.Path != "/rustfs/admin/v3/object-data-cache/flush" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}
		if request.Query != "" {
			t.Errorf("query = %q, want empty", request.Query)
		}
		assertStateString(t, resp.State, path.Root("id"), "object-data-cache-flush")
		assertStateString(t, resp.State, path.Root("scope"), "all")
		assertStateString(t, resp.State, path.Root("outcome"), "noop")
	})

	t.Run("flushes one object identity", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, `{"scope":"object","bucket":"logs","object":"2026/10/19/a.log","outcome":"removed","removed_keys":1}`)
		h := newCRUDHarness(t, NewObjectDataCacheFlushResource(), newUnitAllClient(srv))

		resp := h.create(&ObjectDataCacheFlushResourceModel{
			Triggers: nullStringMap(),
			Bucket:   types.StringValue("logs"),
			Object:   types.StringValue("2026/10/19/a.log"),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		request := recorder.only(t)
		if want := "bucket=logs&object=2026%2F10%2F19%2Fa.log"; request.Query != want {
			t.Errorf("query = %q, want %q", request.Query, want)
		}

		var got ObjectDataCacheFlushResourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.Scope.ValueString() != "object" || got.Outcome.ValueString() != "removed" {
			t.Errorf("result = %q/%q", got.Scope.ValueString(), got.Outcome.ValueString())
		}
		if got.RemovedKeys.ValueInt64() != 1 {
			t.Errorf("removed_keys = %d", got.RemovedKeys.ValueInt64())
		}
	})

	t.Run("reports a capability gap with the server version", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "object-data-cache/flush", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
			requestRoute{method: http.MethodGet, pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.3"}`},
		)
		h := newCRUDHarness(t, NewObjectDataCacheFlushResource(), client)
		plan := &ObjectDataCacheFlushResourceModel{
			Triggers: nullStringMap(),
			Bucket:   types.StringNull(),
			Object:   types.StringNull(),
		}

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Object data cache flush is not available on this RustFS server", "RustFS 1.0.3")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("reports a server failure and writes no state", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method:       http.MethodPost,
			pathContains: "object-data-cache/flush",
			status:       http.StatusInternalServerError,
			body:         s3ErrorXML("InternalError", "cache is not initialized"),
		})
		h := newCRUDHarness(t, NewObjectDataCacheFlushResource(), client)
		plan := &ObjectDataCacheFlushResourceModel{
			Triggers: nullStringMap(),
			Bucket:   types.StringNull(),
			Object:   types.StringNull(),
		}

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error flushing object data cache", "cache is not initialized")
		h.assertNoPartialState(resp, plan)
	})
}

func TestObjectDataCacheFlushResourceRead(t *testing.T) {
	client, rec := routedClient(t)
	h := newCRUDHarness(t, NewObjectDataCacheFlushResource(), client)

	resp := h.read(&ObjectDataCacheFlushResourceModel{
		ID:          types.StringValue("object-data-cache-flush"),
		Triggers:    nullStringMap(),
		Bucket:      types.StringValue("logs"),
		Object:      types.StringNull(),
		Scope:       types.StringValue("bucket"),
		Outcome:     types.StringValue("removed"),
		RemovedKeys: types.Int64Value(3),
	})

	requireNoDiagnostics(t, resp.Diagnostics)
	assertStatePresent(t, resp.State)
	assertStateString(t, resp.State, path.Root("scope"), "bucket")
	rec.assertNoRequest(t, "object-data-cache")
}

func TestObjectDataCacheFlushResourceDelete(t *testing.T) {
	client, rec := routedClient(t)
	h := newCRUDHarness(t, NewObjectDataCacheFlushResource(), client)

	resp := h.delete(&ObjectDataCacheFlushResourceModel{
		ID:       types.StringValue("object-data-cache-flush"),
		Triggers: nullStringMap(),
		Bucket:   types.StringNull(),
		Object:   types.StringNull(),
	})

	requireNoDiagnostics(t, resp.Diagnostics)
	rec.assertNoRequest(t, "object-data-cache")
}

func TestObjectDataCacheFlushResourceUpdate(t *testing.T) {
	client, rec := routedClient(t, requestRoute{
		method:       http.MethodPost,
		pathContains: "object-data-cache/flush",
		status:       http.StatusOK,
		body:         `{"scope":"bucket","bucket":"logs","object":null,"outcome":"removed","removed_keys":3}`,
	})
	h := newCRUDHarness(t, NewObjectDataCacheFlushResource(), client)

	state := &ObjectDataCacheFlushResourceModel{
		ID:       types.StringValue("object-data-cache-flush"),
		Triggers: nullStringMap(),
		Bucket:   types.StringNull(),
		Object:   types.StringNull(),
	}
	resp := h.update(&ObjectDataCacheFlushResourceModel{
		Triggers: triggersMap(t, "run", "2"),
		Bucket:   types.StringValue("logs"),
		Object:   types.StringNull(),
	}, state)

	requireNoDiagnostics(t, resp.Diagnostics)
	rec.assertRequest(t, "object-data-cache/flush")
	assertStateString(t, resp.State, path.Root("scope"), "bucket")
}

func TestObjectDataCacheFlushResourceConfigure(t *testing.T) {
	t.Run("rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewObjectDataCacheFlushResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

func TestObjectDataCacheFlushResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	t.Run("read: the flush has no server-side existence check and keeps state", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewObjectDataCacheFlushResource(), client)

		resp := h.read(&ObjectDataCacheFlushResourceModel{
			ID:       types.StringValue("object-data-cache-flush"),
			Triggers: nullStringMap(),
			Bucket:   types.StringNull(),
			Object:   types.StringNull(),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		rec.assertNoRequest(t, "object-data-cache")
	})

	t.Run("delete: the flush is not revertible and reports nothing", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewObjectDataCacheFlushResource(), client)

		resp := h.delete(&ObjectDataCacheFlushResourceModel{
			ID:       types.StringValue("object-data-cache-flush"),
			Triggers: nullStringMap(),
			Bucket:   types.StringNull(),
			Object:   types.StringNull(),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "object-data-cache")
	})

	t.Run("create: accepted flush records the server result", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method:       http.MethodPost,
			pathContains: "object-data-cache/flush",
			status:       http.StatusOK,
			body:         `{"scope":"all","bucket":null,"object":null,"outcome":"removed","removed_keys":7}`,
		})
		h := newCRUDHarness(t, NewObjectDataCacheFlushResource(), client)

		resp := h.create(&ObjectDataCacheFlushResourceModel{
			Triggers: nullStringMap(),
			Bucket:   types.StringNull(),
			Object:   types.StringNull(),
		})

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), "object-data-cache-flush")
		rec.assertRequest(t, "object-data-cache/flush")
	})

	t.Run("create: capability gap writes no state", func(t *testing.T) {
		client, _ := routedClient(t,
			requestRoute{method: http.MethodPost, pathContains: "object-data-cache/flush", status: http.StatusNotImplemented, body: s3ErrorXML("NotImplemented", "not implemented")},
			requestRoute{method: http.MethodGet, pathContains: "healthinfo", status: http.StatusOK, body: `{"version":"refs/tags/1.0.3"}`},
		)
		h := newCRUDHarness(t, NewObjectDataCacheFlushResource(), client)
		plan := &ObjectDataCacheFlushResourceModel{
			Triggers: nullStringMap(),
			Bucket:   types.StringNull(),
			Object:   types.StringNull(),
		}

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Object data cache flush is not available on this RustFS server", "POST /rustfs/admin/v3/object-data-cache/flush")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewObjectDataCacheFlushResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}

func TestAccObjectDataCacheFlushResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccObjectDataCacheStatsPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
resource "rustfs_object_data_cache_flush" "test" {
  triggers = {
    run = "acceptance"
  }
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("rustfs_object_data_cache_flush.test", "id", "object-data-cache-flush"),
					resource.TestCheckResourceAttrSet("rustfs_object_data_cache_flush.test", "scope"),
					resource.TestCheckResourceAttrSet("rustfs_object_data_cache_flush.test", "outcome"),
				),
			},
		},
	})
}
