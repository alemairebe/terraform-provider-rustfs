// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func configRestoreModel(restoreID, confirm string) *ConfigRestoreResourceModel {
	return &ConfigRestoreResourceModel{
		ID:         types.StringNull(),
		RestoreID:  types.StringValue(restoreID),
		Confirm:    types.StringValue(confirm),
		Triggers:   nullStringMap(),
		RestoredAt: types.StringNull(),
	}
}

func TestConfigRestoreResourceSchema(t *testing.T) {
	sch := resourceSchemaOf(t, NewConfigRestoreResource())

	attrs := sch.GetAttributes()
	if len(attrs) != 5 {
		t.Errorf("expected 5 attributes, got %d: %v", len(attrs), attrs)
	}

	restoreID, ok := attrs["restore_id"].(schema.StringAttribute)
	if !ok || !restoreID.Required || restoreID.Optional || restoreID.Computed {
		t.Errorf("restore_id must be Required only: %+v", attrs["restore_id"])
	}
	if !hasRequiresReplace(restoreID.PlanModifiers) {
		t.Error("restore_id must force replacement")
	}

	confirm, ok := attrs["confirm"].(schema.StringAttribute)
	if !ok || !confirm.Required || confirm.Optional || confirm.Computed {
		t.Errorf("confirm must be Required only: %+v", attrs["confirm"])
	}

	triggers, ok := attrs["triggers"].(schema.MapAttribute)
	if !ok || !triggers.Optional || triggers.Required || triggers.Computed {
		t.Errorf("triggers must be Optional only: %+v", attrs["triggers"])
	}

	for _, name := range []string{"id", "restored_at"} {
		attr, ok := attrs[name].(schema.StringAttribute)
		if !ok || !attr.Computed || attr.Required || attr.Optional {
			t.Errorf("%s must be Computed only: %+v", name, attrs[name])
		}
	}

	for name, attr := range attrs {
		if attr.GetDescription() == "" {
			t.Errorf("attribute %s has no description", name)
		}
	}
}

func TestConfigRestoreResourceMetadata(t *testing.T) {
	resp := &frameworkresource.MetadataResponse{}
	NewConfigRestoreResource().Metadata(context.Background(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_config_restore" {
		t.Errorf("expected rustfs_config_restore, got %s", resp.TypeName)
	}
}

func TestConfigRestoreResourceValidateConfig(t *testing.T) {
	t.Run("requires the confirmation to repeat the restore id", func(t *testing.T) {
		h := newCRUDHarness(t, NewConfigRestoreResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.validateConfig(configRestoreModel("4a1c", "restore"))

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Invalid config restore confirmation")
		if !strings.Contains(diagnostic.Detail(), "whole live configuration") {
			t.Errorf("detail = %q", diagnostic.Detail())
		}
	})

	t.Run("accepts a matching confirmation", func(t *testing.T) {
		h := newCRUDHarness(t, NewConfigRestoreResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.validateConfig(configRestoreModel("4a1c", "4a1c"))

		requireNoDiagnostics(t, resp.Diagnostics)
	})
}

func TestConfigRestoreResourceCreate(t *testing.T) {
	t.Run("restores the entry and records the result", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		h := newCRUDHarness(t, NewConfigRestoreResource(), newUnitAllClient(srv))

		resp := h.create(configRestoreModel("4a1c", "4a1c"))
		requireNoDiagnostics(t, resp.Diagnostics)

		request := recorder.only(t)
		if request.Method != http.MethodPut || request.Path != "/rustfs/admin/v3/restore-config-history-kv" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}
		if request.Query != "restoreId=4a1c" {
			t.Errorf("query = %q, want restoreId=4a1c", request.Query)
		}

		var got ConfigRestoreResourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.ID.ValueString() != "4a1c" {
			t.Errorf("id = %q", got.ID.ValueString())
		}
		if got.RestoredAt.ValueString() == "" {
			t.Error("restored_at was not recorded")
		}
	})

	t.Run("refuses a mismatched confirmation without calling the server", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewConfigRestoreResource(), client)
		plan := configRestoreModel("4a1c", "nope")

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Invalid config restore confirmation", "must repeat restore_id")
		rec.assertNoRequest(t, "restore-config-history-kv")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("reports a missing history entry and writes no state", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method:       http.MethodPut,
			pathContains: "restore-config-history-kv",
			status:       http.StatusBadRequest,
			body:         s3ErrorXML("InvalidRequest", "config history entry not found"),
		})
		h := newCRUDHarness(t, NewConfigRestoreResource(), client)
		plan := configRestoreModel("missing", "missing")

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error restoring config history", "config history entry not found")
		h.assertNoPartialState(resp, plan)
	})

	t.Run("reports a server failure and writes no state", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{
			method:       http.MethodPut,
			pathContains: "restore-config-history-kv",
			status:       http.StatusConflict,
			body:         s3ErrorXML("OperationAborted", "config mutation is busy"),
		})
		h := newCRUDHarness(t, NewConfigRestoreResource(), client)
		plan := configRestoreModel("4a1c", "4a1c")

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error restoring config history", "config mutation is busy")
		h.assertNoPartialState(resp, plan)
	})
}

func TestConfigRestoreResourceUpdate(t *testing.T) {
	t.Run("reports a failed re-run without moving state", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method:       http.MethodPut,
			pathContains: "restore-config-history-kv",
			status:       http.StatusInternalServerError,
			body:         s3ErrorXML("InternalError", "restore failed again"),
		})
		h := newCRUDHarness(t, NewConfigRestoreResource(), client)

		state := configRestoreModel("4a1c", "4a1c")
		state.ID = types.StringValue("4a1c")
		state.RestoredAt = types.StringValue("2026-10-20T09:15:00Z")

		plan := configRestoreModel("4a1c", "4a1c")
		plan.Triggers = triggersMap(t, "run", "3")

		resp := h.update(plan, state)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error restoring config history", "restore failed again")
		assertStateString(t, resp.State, path.Root("restored_at"), "2026-10-20T09:15:00Z")
		rec.assertRequest(t, "restore-config-history-kv")
	})

	t.Run("re-runs the same restore when a trigger changes", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method:       http.MethodPut,
			pathContains: "restore-config-history-kv",
			status:       http.StatusOK,
			body:         "",
		})
		h := newCRUDHarness(t, NewConfigRestoreResource(), client)

		state := configRestoreModel("4a1c", "4a1c")
		state.ID = types.StringValue("4a1c")
		state.RestoredAt = types.StringValue("2026-10-20T09:15:00Z")

		plan := configRestoreModel("4a1c", "4a1c")
		plan.Triggers = triggersMap(t, "run", "2")

		resp := h.update(plan, state)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "restore-config-history-kv")
		assertStateString(t, resp.State, path.Root("id"), "4a1c")
	})
}

func TestConfigRestoreResourceRead(t *testing.T) {
	client, rec := routedClient(t)
	h := newCRUDHarness(t, NewConfigRestoreResource(), client)

	state := configRestoreModel("4a1c", "4a1c")
	state.ID = types.StringValue("4a1c")
	state.RestoredAt = types.StringValue("2026-10-20T09:15:00Z")

	resp := h.read(state)

	requireNoDiagnostics(t, resp.Diagnostics)
	assertStatePresent(t, resp.State)
	assertStateString(t, resp.State, path.Root("id"), "4a1c")
	rec.assertNoRequest(t, "restore-config-history-kv")
}

func TestConfigRestoreResourceDelete(t *testing.T) {
	client, rec := routedClient(t)
	h := newCRUDHarness(t, NewConfigRestoreResource(), client)

	state := configRestoreModel("4a1c", "4a1c")
	state.ID = types.StringValue("4a1c")
	state.RestoredAt = types.StringValue("2026-10-20T09:15:00Z")

	resp := h.delete(state)

	requireNoDiagnostics(t, resp.Diagnostics)
	rec.assertNoRequest(t, "restore-config-history-kv")
}

func TestConfigRestoreResourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewConfigRestoreResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure(nil)

		requireNoDiagnostics(t, resp.Diagnostics)
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewConfigRestoreResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})

	t.Run("all client", func(t *testing.T) {
		h := newCRUDHarness(t, NewConfigRestoreResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure(cannedClient(t, http.StatusOK, ""))

		requireNoDiagnostics(t, resp.Diagnostics)
	})
}

func TestConfigRestoreResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	t.Run("read: a refresh never re-runs the restore and keeps state", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewConfigRestoreResource(), client)

		state := configRestoreModel("4a1c", "4a1c")
		state.ID = types.StringValue("4a1c")
		state.RestoredAt = types.StringValue("2026-10-20T09:15:00Z")

		resp := h.read(state)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		rec.assertNoRequest(t, "restore-config-history-kv")
	})

	t.Run("delete: the restore is not revertible and reports nothing", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewConfigRestoreResource(), client)

		state := configRestoreModel("4a1c", "4a1c")
		state.ID = types.StringValue("4a1c")
		state.RestoredAt = types.StringValue("2026-10-20T09:15:00Z")

		resp := h.delete(state)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "restore-config-history-kv")
	})

	t.Run("create: accepted restore records the entry", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method:       http.MethodPut,
			pathContains: "restore-config-history-kv",
			status:       http.StatusOK,
			body:         "",
		})
		h := newCRUDHarness(t, NewConfigRestoreResource(), client)

		resp := h.create(configRestoreModel("4a1c", "4a1c"))

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("id"), "4a1c")
		rec.assertRequest(t, "restore-config-history-kv")
	})

	t.Run("create: a failed restore does not re-run or write partial state", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{
			method:       http.MethodPut,
			pathContains: "restore-config-history-kv",
			status:       http.StatusInternalServerError,
			body:         s3ErrorXML("InternalError", "restore failed"),
		})
		h := newCRUDHarness(t, NewConfigRestoreResource(), client)
		plan := configRestoreModel("4a1c", "4a1c")

		resp := h.create(plan)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error restoring config history", "restore failed")
		h.assertNoPartialState(resp, plan)
		rec.assertRequest(t, "restore-config-history-kv")
	})
}

// TestAccConfigRestoreResourceValidation runs the real Terraform planner
// without applying, so it proves the type is registered and the explicit
// confirmation is enforced end to end. The restore itself is destructive and
// has no converging acceptance case; it is covered by the lifecycle tests
// above and documented as a one-shot action.
func TestAccConfigRestoreResourceValidation(t *testing.T) {
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		PreCheck:                 func() { testAccPreCheck(t) },
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
resource "rustfs_config_restore" "test" {
  restore_id = "4a1c9f4e-6d0b-4f5e-9f4c-2b7a1d8c3e10"
  confirm    = "not-the-restore-id"
}
`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`confirm must repeat restore_id`),
			},
		},
	})
}
