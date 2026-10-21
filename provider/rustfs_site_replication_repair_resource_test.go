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

func TestSiteReplicationRepairResourceMetadata(t *testing.T) {
	resp := &frameworkresource.MetadataResponse{}
	NewSiteReplicationRepairResource().Metadata(
		context.Background(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_site_replication_repair" {
		t.Errorf("type name = %q, want rustfs_site_replication_repair", resp.TypeName)
	}
}

func TestSiteReplicationRepairResourceSchema(t *testing.T) {
	r := NewSiteReplicationRepairResource()
	sch := resourceSchemaOf(t, r)

	for _, name := range []string{
		"id", "confirm", "operation_id", "status", "retry_events",
		"created_at", "updated_at", "completed_at", "sites",
	} {
		if _, ok := sch.Attributes[name]; !ok {
			t.Errorf("missing schema attribute %q", name)
		}
	}

	confirm, ok := sch.Attributes["confirm"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("confirm is %T, want schema.StringAttribute", sch.Attributes["confirm"])
	}
	if !confirm.Required || confirm.Optional || confirm.Computed {
		t.Errorf("confirm must be Required only: %+v", confirm)
	}
	if confirm.GetDescription() == "" {
		t.Error("confirm has no description, tfplugindocs would publish an empty row")
	}

	status, ok := sch.Attributes["status"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("status is %T, want schema.StringAttribute", sch.Attributes["status"])
	}
	if status.Required || status.Optional || !status.Computed {
		t.Errorf("status must be Computed only: %+v", status)
	}

	sites, ok := sch.Attributes["sites"].(schema.ListNestedAttribute)
	if !ok {
		t.Fatalf("sites is %T, want schema.ListNestedAttribute", sch.Attributes["sites"])
	}
	if !sites.Computed || sites.Optional || sites.Required {
		t.Errorf("sites must be Computed only: %+v", sites)
	}
	for _, name := range []string{
		"deployment_id", "name", "family", "planned", "succeeded", "failed", "retry_events", "errors",
	} {
		if _, ok := sites.NestedObject.Attributes[name]; !ok {
			t.Errorf("missing nested attribute %q on sites", name)
		}
	}
}

func siteReplicationRepairPlanModel(confirm types.String) *SiteReplicationRepairResourceModel {
	return &SiteReplicationRepairResourceModel{
		ID:          types.StringUnknown(),
		Confirm:     confirm,
		OperationID: types.StringUnknown(),
		Status:      types.StringUnknown(),
		RetryEvents: types.Int64Unknown(),
		CreatedAt:   types.StringUnknown(),
		UpdatedAt:   types.StringUnknown(),
		CompletedAt: types.StringUnknown(),
		Sites:       types.ListUnknown(types.ObjectType{AttrTypes: siteReplicationRepairSitesAttrTypes()}),
	}
}

func siteReplicationRepairStateModel() *SiteReplicationRepairResourceModel {
	return &SiteReplicationRepairResourceModel{
		ID:          types.StringValue("op-1"),
		Confirm:     types.StringValue("unused-by-read"),
		OperationID: types.StringValue("op-1"),
		Status:      types.StringValue("success"),
		RetryEvents: types.Int64Value(0),
		CreatedAt:   types.StringNull(),
		UpdatedAt:   types.StringNull(),
		CompletedAt: types.StringNull(),
		Sites:       types.ListNull(types.ObjectType{AttrTypes: siteReplicationRepairSitesAttrTypes()}),
	}
}

// repairHandler answers the repair route by request mode, so one server can
// serve the dry-run and the execute of a single Create.
type repairHandler struct {
	t            *testing.T
	preflight    func(w http.ResponseWriter)
	execute      func(w http.ResponseWriter)
	status       func(w http.ResponseWriter)
	requestModes []string
}

func (h *repairHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.Contains(r.URL.Path, "/repair/status"):
		h.status(w)
		return
	case strings.Contains(r.URL.Path, "/repair"):
		var body struct {
			Mode string `json:"mode"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		h.requestModes = append(h.requestModes, body.Mode)
		switch body.Mode {
		case "dry-run":
			h.preflight(w)
		case "execute":
			h.execute(w)
		default:
			h.t.Errorf("unexpected repair mode %q", body.Mode)
			w.WriteHeader(http.StatusBadRequest)
		}
		return
	}
	h.t.Errorf("unrouted request: %s %s", r.Method, r.URL.Path)
	w.WriteHeader(http.StatusInternalServerError)
}

func writeRepairJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

const repairPreflightReply = `{"mode":"dry-run","status":"planned","preflightToken":"preflight-token",` +
	`"retryEvents":2,"sites":{"dep-peer":{"deploymentId":"dep-peer","name":"peer",` +
	`"families":{"replication":{"planned":1,"succeeded":0,"failed":0,"retryEvents":2,` +
	`"tasks":[],"errors":[]}}}}}`

const repairSuccessOperation = `{"mode":"execute","operationId":"op-generated","status":"success",` +
	`"sites":{"dep-peer":{"deploymentId":"dep-peer","name":"peer","families":{"replication":` +
	`{"planned":2,"succeeded":2,"failed":0,"retryEvents":0,"tasks":[],"errors":[]}}}},` +
	`"createdAt":"2026-10-20T10:00:00Z","updatedAt":"2026-10-20T10:00:02Z","completedAt":"2026-10-20T10:00:02Z"}`

// repairStatusReply is the success operation as the status route returns it for
// a given id; the server echoes the id the operation is stored under.
func repairStatusReply(operationID string) string {
	return strings.Replace(repairSuccessOperation, "op-generated", operationID, 1)
}

func TestSiteReplicationRepairResourceCreate(t *testing.T) {
	h := &repairHandler{
		t:         t,
		preflight: func(w http.ResponseWriter) { writeRepairJSON(w, repairPreflightReply) },
		execute:   func(w http.ResponseWriter) { writeRepairJSON(w, repairSuccessOperation) },
	}
	client := httptestClient(t, h)
	crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), client)
	plan := siteReplicationRepairPlanModel(types.StringValue(client.RustClient.Endpoint()))

	resp := crud.create(plan)
	requireNoDiagnostics(t, resp.Diagnostics)

	var got SiteReplicationRepairResourceModel
	requireNoDiagnostics(t, resp.State.Get(context.Background(), &got))
	if got.OperationID.ValueString() == "" {
		t.Error("operation_id is empty, want the generated id")
	}
	if got.ID.ValueString() != "op-generated" {
		t.Errorf("id = %q, want the server's canonical id", got.ID.ValueString())
	}
	if got.Status.ValueString() != "success" {
		t.Errorf("status = %q, want success", got.Status.ValueString())
	}
	if got.RetryEvents.ValueInt64() != 2 {
		t.Errorf("retry_events = %d, want 2 from the dry-run", got.RetryEvents.ValueInt64())
	}
	if got.CompletedAt.ValueString() != "2026-10-20T10:00:02Z" {
		t.Errorf("completed_at = %q, want the server timestamp", got.CompletedAt.ValueString())
	}
	if len(h.requestModes) != 2 || h.requestModes[0] != "dry-run" || h.requestModes[1] != "execute" {
		t.Fatalf("request modes = %v, want [dry-run execute]", h.requestModes)
	}

	rows := got.Sites.Elements()
	if len(rows) != 1 {
		t.Fatalf("sites rows = %d, want one", len(rows))
	}
	row := rows[0].(types.Object).Attributes()
	if row["deployment_id"].(types.String).ValueString() != "dep-peer" {
		t.Errorf("deployment_id = %v, want dep-peer", row["deployment_id"])
	}
	if row["family"].(types.String).ValueString() != "replication" {
		t.Errorf("family = %v, want replication", row["family"])
	}
	if row["succeeded"].(types.Int64).ValueInt64() != 2 {
		t.Errorf("succeeded = %v, want 2", row["succeeded"])
	}
}

func TestSiteReplicationRepairResourceCreatePartialWarnsAndKeepsState(t *testing.T) {
	operation := `{"mode":"execute","operationId":"op-generated","status":"partial",` +
		`"sites":{"dep-peer":{"deploymentId":"dep-peer","name":"peer","families":{"replication":` +
		`{"planned":2,"succeeded":1,"failed":1,"retryEvents":0,"tasks":[],"errors":["denied"]}}}}}`

	h := &repairHandler{
		t:         t,
		preflight: func(w http.ResponseWriter) { writeRepairJSON(w, repairPreflightReply) },
		execute:   func(w http.ResponseWriter) { writeRepairJSON(w, operation) },
	}
	client := httptestClient(t, h)
	crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), client)
	plan := siteReplicationRepairPlanModel(types.StringValue(client.RustClient.Endpoint()))

	resp := crud.create(plan)
	if resp.Diagnostics.HasError() {
		t.Fatalf("a partial repair must not fail the apply: %v", resp.Diagnostics)
	}
	if len(resp.Diagnostics.Warnings()) != 1 ||
		resp.Diagnostics.Warnings()[0].Summary() != "Site replication repair completed with failures" {
		t.Fatalf("warnings = %v, want the partial-repair warning", resp.Diagnostics.Warnings())
	}

	var got SiteReplicationRepairResourceModel
	requireNoDiagnostics(t, resp.State.Get(context.Background(), &got))
	if got.Status.ValueString() != "partial" {
		t.Errorf("status = %q, want partial", got.Status.ValueString())
	}
	row := got.Sites.Elements()[0].(types.Object).Attributes()
	if row["failed"].(types.Int64).ValueInt64() != 1 {
		t.Errorf("failed = %v, want 1", row["failed"])
	}
	errors := row["errors"].(types.List).Elements()
	if len(errors) != 1 || errors[0].(types.String).ValueString() != "denied" {
		t.Errorf("errors = %v, want [denied]", errors)
	}
}

// The requested operation id is what the plan promised and what RequiresReplace
// compares, so a normalised server id must go to id instead of rewriting it.
func TestSiteReplicationRepairResourceCreateKeepsRequestedOperationID(t *testing.T) {
	h := &repairHandler{
		t:         t,
		preflight: func(w http.ResponseWriter) { writeRepairJSON(w, repairPreflightReply) },
		execute:   func(w http.ResponseWriter) { writeRepairJSON(w, repairSuccessOperation) },
	}
	client := httptestClient(t, h)
	crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), client)
	plan := siteReplicationRepairPlanModel(types.StringValue(client.RustClient.Endpoint()))
	plan.OperationID = types.StringValue("op-requested")

	resp := crud.create(plan)
	requireNoDiagnostics(t, resp.Diagnostics)
	assertStateString(t, resp.State, path.Root("operation_id"), "op-requested")
	assertStateString(t, resp.State, path.Root("id"), "op-generated")
}

func TestSiteReplicationRepairResourceCreatePreflightError(t *testing.T) {
	h := &repairHandler{
		t: t,
		preflight: func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(s3ErrorXML("InvalidRequest", "site replication is not configured")))
		},
		execute: func(w http.ResponseWriter) { t.Error("execute must not run after a failed preflight") },
	}
	client := httptestClient(t, h)
	crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), client)
	plan := siteReplicationRepairPlanModel(types.StringValue(client.RustClient.Endpoint()))

	resp := crud.create(plan)
	assertErrorDiagnostic(t, resp.Diagnostics, "Error planning site replication repair",
		"Could not plan site replication repair: ")
	assertDiagnosticDetail(t, resp.Diagnostics, "site replication is not configured")
	crud.assertNoPartialState(resp, plan)
}

func TestSiteReplicationRepairResourceCreateExecuteError(t *testing.T) {
	h := &repairHandler{
		t:         t,
		preflight: func(w http.ResponseWriter) { writeRepairJSON(w, repairPreflightReply) },
		execute: func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = w.Write([]byte(s3ErrorXML("PreconditionFailed", "site replication repair preflight is stale")))
		},
	}
	client := httptestClient(t, h)
	crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), client)
	plan := siteReplicationRepairPlanModel(types.StringValue(client.RustClient.Endpoint()))

	resp := crud.create(plan)
	assertErrorDiagnostic(t, resp.Diagnostics, "Error repairing site replication",
		"Could not repair site replication: ")
	assertDiagnosticDetail(t, resp.Diagnostics, "preflight is stale")
	crud.assertNoPartialState(resp, plan)
}

func TestSiteReplicationRepairResourceCreateEmptyStatus(t *testing.T) {
	h := &repairHandler{
		t:         t,
		preflight: func(w http.ResponseWriter) { writeRepairJSON(w, repairPreflightReply) },
		execute:   func(w http.ResponseWriter) { writeRepairJSON(w, `{}`) },
	}
	client := httptestClient(t, h)
	crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), client)
	plan := siteReplicationRepairPlanModel(types.StringValue(client.RustClient.Endpoint()))

	resp := crud.create(plan)
	assertErrorDiagnostic(t, resp.Diagnostics, "Error repairing site replication", "returned no operation status")
	crud.assertNoPartialState(resp, plan)
}

func TestSiteReplicationRepairResourceValidateConfig(t *testing.T) {
	client := cannedClient(t, http.StatusOK, "")
	crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), client)
	expected := client.RustClient.Endpoint()

	cases := []struct {
		name        string
		confirm     types.String
		wantSummary string
	}{
		{name: "the provider endpoint is accepted", confirm: types.StringValue(expected)},
		{name: "unknown is deferred to create", confirm: types.StringUnknown()},
		{
			name:        "the generic literal is refused",
			confirm:     types.StringValue("repair"),
			wantSummary: "Invalid site replication repair confirmation",
		},
		{
			name:        "another endpoint is refused",
			confirm:     types.StringValue("https://other:9000"),
			wantSummary: "Invalid site replication repair confirmation",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := crud.validateConfig(siteReplicationRepairPlanModel(tc.confirm))
			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, "confirm must repeat the provider endpoint")
		})
	}
}

func TestSiteReplicationRepairResourceCreateRefusesWrongConfirmation(t *testing.T) {
	h := &repairHandler{
		t:         t,
		preflight: func(w http.ResponseWriter) { t.Error("preflight must not run without confirmation") },
		execute:   func(w http.ResponseWriter) { t.Error("execute must not run without confirmation") },
	}
	crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), httptestClient(t, h))
	plan := siteReplicationRepairPlanModel(types.StringValue("yes"))

	resp := crud.create(plan)
	assertErrorDiagnostic(t, resp.Diagnostics, "Invalid site replication repair confirmation", "confirm must repeat the provider endpoint")
	crud.assertNoPartialState(resp, plan)
}

func TestSiteReplicationRepairResourceRead(t *testing.T) {
	h := &repairHandler{
		t: t,
		status: func(w http.ResponseWriter) {
			writeRepairJSON(w, repairStatusReply("op-1"))
		},
	}
	crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), httptestClient(t, h))

	resp := crud.read(siteReplicationRepairStateModel())
	requireNoDiagnostics(t, resp.Diagnostics)

	var got SiteReplicationRepairResourceModel
	requireNoDiagnostics(t, resp.State.Get(context.Background(), &got))
	if got.Status.ValueString() != "success" {
		t.Errorf("status = %q, want success", got.Status.ValueString())
	}
	if got.CompletedAt.ValueString() != "2026-10-20T10:00:02Z" {
		t.Errorf("completed_at = %q, want the server timestamp", got.CompletedAt.ValueString())
	}
	if len(got.Sites.Elements()) != 1 {
		t.Errorf("sites rows = %d, want one", len(got.Sites.Elements()))
	}
}

func TestSiteReplicationRepairResourceReadPrunedRecordKeepsState(t *testing.T) {
	h := &repairHandler{
		t: t,
		status: func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(s3ErrorXML("InvalidRequest", "repair operation was not found")))
		},
	}
	crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), httptestClient(t, h))
	state := siteReplicationRepairStateModel()

	resp := crud.read(state)
	requireNoDiagnostics(t, resp.Diagnostics)
	assertStatePresent(t, resp.State)
	assertStateString(t, resp.State, path.Root("status"), "success")
}

// An import starts with an empty status. If the operation id does not exist,
// silently keeping an empty state would leave Terraform with no idea what the
// resource is managing, so that path fails loudly.
func TestSiteReplicationRepairResourceReadUnknownImportedOperation(t *testing.T) {
	h := &repairHandler{
		t: t,
		status: func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(s3ErrorXML("InvalidRequest", "repair operation was not found")))
		},
	}
	crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), httptestClient(t, h))
	state := siteReplicationRepairStateModel()
	state.Status = types.StringNull()
	state.Sites = types.ListNull(types.ObjectType{AttrTypes: siteReplicationRepairSitesAttrTypes()})

	resp := crud.read(state)
	assertErrorDiagnostic(t, resp.Diagnostics, "Error reading site replication repair",
		"The server does not hold a repair operation op-1.")
}

func TestSiteReplicationRepairResourceReadError(t *testing.T) {
	h := &repairHandler{
		t: t,
		status: func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(s3ErrorXML("AccessDenied", "nope")))
		},
	}
	crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), httptestClient(t, h))

	resp := crud.read(siteReplicationRepairStateModel())
	assertErrorDiagnostic(t, resp.Diagnostics, "Error reading site replication repair",
		"Could not read site replication repair: ")
	assertDiagnosticDetail(t, resp.Diagnostics, "AccessDenied")
	assertStatePresent(t, resp.State)
}

func TestSiteReplicationRepairResourceDeleteIsANoOp(t *testing.T) {
	rec := &recordingServer{t: t}
	crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), httptestClient(t, rec))

	resp := crud.delete(siteReplicationRepairStateModel())
	requireNoDiagnostics(t, resp.Diagnostics)
	if len(rec.requests) != 0 {
		t.Errorf("delete sent %v, want no request", rec.requests)
	}
}

func TestSiteReplicationRepairResourceImportState(t *testing.T) {
	r := NewSiteReplicationRepairResource().(*SiteReplicationRepairResource)
	sch := resourceSchemaOf(t, r)

	resp := &frameworkresource.ImportStateResponse{State: resourceState(t, sch, SiteReplicationRepairResourceModel{
		ID:          types.StringNull(),
		Confirm:     types.StringNull(),
		OperationID: types.StringNull(),
		Status:      types.StringNull(),
		RetryEvents: types.Int64Null(),
		CreatedAt:   types.StringNull(),
		UpdatedAt:   types.StringNull(),
		CompletedAt: types.StringNull(),
		Sites:       types.ListNull(types.ObjectType{AttrTypes: siteReplicationRepairSitesAttrTypes()}),
	})}
	r.ImportState(context.Background(), frameworkresource.ImportStateRequest{ID: "op-imported"}, resp)
	requireNoDiagnostics(t, resp.Diagnostics)

	var operationID, id types.String
	requireNoDiagnostics(t, resp.State.GetAttribute(context.Background(), path.Root("operation_id"), &operationID))
	requireNoDiagnostics(t, resp.State.GetAttribute(context.Background(), path.Root("id"), &id))
	if operationID.ValueString() != "op-imported" || id.ValueString() != "op-imported" {
		t.Errorf("operation_id/id = %q/%q, want op-imported twice", operationID.ValueString(), id.ValueString())
	}
}

func TestSiteReplicationRepairResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	t.Run("read: pruned record keeps the last observed state", func(t *testing.T) {
		h := &repairHandler{
			t: t,
			status: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(s3ErrorXML("InvalidRequest", "repair operation was not found")))
			},
		}
		crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), httptestClient(t, h))
		resp := crud.read(siteReplicationRepairStateModel())
		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("status"), "success")
	})

	t.Run("read: falls back to id when operation_id is absent", func(t *testing.T) {
		h := &repairHandler{
			t: t,
			status: func(w http.ResponseWriter) {
				writeRepairJSON(w, repairStatusReply("op-1"))
			},
		}
		crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), httptestClient(t, h))
		state := siteReplicationRepairStateModel()
		state.OperationID = types.StringNull()
		resp := crud.read(state)
		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("operation_id"), "op-1")
		assertStateString(t, resp.State, path.Root("status"), "success")
	})

	t.Run("read: empty operation id drops the resource", func(t *testing.T) {
		crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), cannedClient(t, http.StatusOK, ""))
		state := siteReplicationRepairStateModel()
		state.ID = types.StringNull()
		state.OperationID = types.StringNull()
		resp := crud.read(state)
		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateRemoved(t, resp.State)
	})

	t.Run("read: keeps a configured operation_id when the server normalises it", func(t *testing.T) {
		h := &repairHandler{
			t: t,
			status: func(w http.ResponseWriter) {
				writeRepairJSON(w, repairStatusReply("op-canonical"))
			},
		}
		crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), httptestClient(t, h))
		state := siteReplicationRepairStateModel()
		state.ID = types.StringValue("op-canonical")
		state.OperationID = types.StringValue("op-requested")
		resp := crud.read(state)
		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("operation_id"), "op-requested")
		assertStateString(t, resp.State, path.Root("id"), "op-canonical")
	})

	t.Run("read: a response without status is an error", func(t *testing.T) {
		h := &repairHandler{
			t:      t,
			status: func(w http.ResponseWriter) { writeRepairJSON(w, `{}`) },
		}
		crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), httptestClient(t, h))
		resp := crud.read(siteReplicationRepairStateModel())
		assertErrorDiagnostic(t, resp.Diagnostics, "Error reading site replication repair", "without a status")
	})

	t.Run("update: mismatched confirmation is refused", func(t *testing.T) {
		crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), cannedClient(t, http.StatusOK, ""))
		plan := siteReplicationRepairStateModel()
		plan.Confirm = types.StringValue("repair")
		resp := crud.update(plan, siteReplicationRepairStateModel())
		assertErrorDiagnostic(t, resp.Diagnostics, "Invalid site replication repair confirmation",
			"confirm must repeat the provider endpoint")
	})

	t.Run("update: confirmation changes are written to state", func(t *testing.T) {
		client := cannedClient(t, http.StatusOK, "")
		crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), client)
		plan := siteReplicationRepairStateModel()
		plan.Confirm = types.StringValue(client.RustClient.Endpoint())
		resp := crud.update(plan, siteReplicationRepairStateModel())
		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("confirm"), client.RustClient.Endpoint())
	})

	t.Run("create: accepted repair writes state", func(t *testing.T) {
		h := &repairHandler{
			t:         t,
			preflight: func(w http.ResponseWriter) { writeRepairJSON(w, repairPreflightReply) },
			execute:   func(w http.ResponseWriter) { writeRepairJSON(w, repairSuccessOperation) },
		}
		client := httptestClient(t, h)
		crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), client)
		resp := crud.create(siteReplicationRepairPlanModel(types.StringValue(client.RustClient.Endpoint())))
		requireNoDiagnostics(t, resp.Diagnostics)
		assertStatePresent(t, resp.State)
		assertStateString(t, resp.State, path.Root("status"), "success")
	})

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		crud := newCRUDHarness(t, NewSiteReplicationRepairResource(), cannedClient(t, http.StatusOK, ""))
		resp := crud.configure("not-a-client")
		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type",
			"Expected *AllClient, got: string.")
	})
}

// The repair rows are sorted so a refresh produces the same state even though
// the server sends a map.
func TestSiteReplicationRepairSitesValueIsSorted(t *testing.T) {
	value := siteReplicationRepairSitesValue(map[string]rustfs.SiteReplicationRepairSite{
		"dep-b": {
			DeploymentID: "dep-b",
			Name:         "b",
			Families: map[string]rustfs.SiteReplicationRepairFamily{
				"zeta":  {Planned: 1},
				"alpha": {Planned: 2},
			},
		},
		"dep-a": {DeploymentID: "dep-a", Name: "a"},
	})

	if len(value.Elements()) != 3 {
		t.Fatalf("rows = %d, want 3", len(value.Elements()))
	}
	want := []struct{ deploymentID, family string }{
		{"dep-a", ""},
		{"dep-b", "alpha"},
		{"dep-b", "zeta"},
	}
	for i, row := range value.Elements() {
		attrs := row.(types.Object).Attributes()
		if attrs["deployment_id"].(types.String).ValueString() != want[i].deploymentID ||
			attrs["family"].(types.String).ValueString() != want[i].family {
			t.Errorf("row %d = %v/%v, want %s/%s", i,
				attrs["deployment_id"], attrs["family"], want[i].deploymentID, want[i].family)
		}
	}
}
