// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"

	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const iamBackupImportResourceName = "rustfs_iam_backup_import.test"

// iamBackupImportPayload is the decoded archive the tests feed the resource:
// the ZIP local-file magic plus a body, so the assertion compares real bytes
// rather than a round-tripped string.
const iamBackupImportPayload = "PK\x03\x04archive"

func TestIamBackupImportResourceSchema(t *testing.T) {
	sch := resourceSchemaOf(t, NewIamBackupImportResource())

	attrs := sch.GetAttributes()
	if len(attrs) != 1 {
		t.Errorf("expected 1 attribute, got %d: %v", len(attrs), attrs)
	}

	content, ok := attrs["content_base64"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("content_base64 is not a StringAttribute")
	}
	if !content.Required || content.Optional || content.Computed {
		t.Errorf("content_base64 must be Required only: %+v", content)
	}
	if !content.Sensitive {
		t.Error("content_base64 carries the whole IAM backup and must be Sensitive")
	}
	if !hasRequiresReplace(content.PlanModifiers) {
		t.Error("content_base64 must force recreation, an import is a one-shot action")
	}
	if content.Description == "" {
		t.Error("content_base64 has no description, tfplugindocs would publish an empty row")
	}
}

func TestIamBackupImportResourceMetadata(t *testing.T) {
	resp := &frameworkresource.MetadataResponse{}
	NewIamBackupImportResource().Metadata(context.Background(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_iam_backup_import" {
		t.Errorf("expected rustfs_iam_backup_import, got %s", resp.TypeName)
	}
}

func TestIamBackupImportResourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		r := NewIamBackupImportResource().(*IamBackupImportResource)
		resp := &frameworkresource.ConfigureResponse{}
		r.Configure(context.Background(), frameworkresource.ConfigureRequest{}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if r.client != nil {
			t.Error("client must stay unset without provider data")
		}
	})

	t.Run("unexpected provider data", func(t *testing.T) {
		r := NewIamBackupImportResource().(*IamBackupImportResource)
		resp := &frameworkresource.ConfigureResponse{}
		r.Configure(context.Background(), frameworkresource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("expected a diagnostic for unexpected provider data")
		}
		if got := resp.Diagnostics[0].Summary(); got != "Unexpected Resource Configure Type" {
			t.Errorf("unexpected diagnostic summary: %s", got)
		}
	})

	t.Run("all client", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, "")
		r := NewIamBackupImportResource().(*IamBackupImportResource)
		resp := &frameworkresource.ConfigureResponse{}
		r.Configure(context.Background(), frameworkresource.ConfigureRequest{ProviderData: newUnitAllClient(srv)}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if r.client == nil {
			t.Error("client was not set")
		}
	})
}

func TestIamBackupImportResourceCreate(t *testing.T) {
	t.Run("imports the archive", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		r := NewIamBackupImportResource().(*IamBackupImportResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		model := IamBackupImportResourceModel{
			ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(iamBackupImportPayload))),
		}
		resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, IamBackupImportResourceModel{})}
		r.Create(context.Background(), frameworkresource.CreateRequest{Plan: resourcePlan(t, sch, model)}, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		request := recorder.only(t)
		if request.Method != http.MethodPut || request.Path != "/rustfs/admin/v3/import-iam" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}
		if string(request.Body) != iamBackupImportPayload {
			t.Errorf("the decoded archive was not sent: %q", request.Body)
		}

		var got IamBackupImportResourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.ContentBase64.ValueString() != model.ContentBase64.ValueString() {
			t.Errorf("state was not written from the plan: %+v", got)
		}
	})

	t.Run("rejects invalid base64 before calling the API", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		r := NewIamBackupImportResource().(*IamBackupImportResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := resourcePlan(t, sch, IamBackupImportResourceModel{
			ContentBase64: types.StringValue("not base64!"),
		})
		resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, IamBackupImportResourceModel{})}
		r.Create(context.Background(), frameworkresource.CreateRequest{Plan: plan}, resp)

		assertDiagnosticSummary(t, resp.Diagnostics, "Invalid base64 data")
		if got := recorder.count(); got != 0 {
			t.Errorf("expected no admin API request, got %d", got)
		}
	})

	t.Run("reports a server failure", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusInternalServerError, "<Error><Code>IamImportFailed</Code><Message>import failed</Message></Error>")
		r := NewIamBackupImportResource().(*IamBackupImportResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := resourcePlan(t, sch, IamBackupImportResourceModel{
			ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(iamBackupImportPayload))),
		})
		resp := &frameworkresource.CreateResponse{State: resourceState(t, sch, IamBackupImportResourceModel{})}
		r.Create(context.Background(), frameworkresource.CreateRequest{Plan: plan}, resp)

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error importing IAM data")
		if !strings.Contains(diagnostic.Detail(), "IamImportFailed") {
			t.Errorf("diagnostic detail lost the server payload: %s", diagnostic.Detail())
		}
	})
}

func TestIamBackupImportResourceUpdate(t *testing.T) {
	t.Run("imports the archive", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		r := NewIamBackupImportResource().(*IamBackupImportResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := IamBackupImportResourceModel{
			ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(iamBackupImportPayload))),
		}
		resp := &frameworkresource.UpdateResponse{State: resourceState(t, sch, IamBackupImportResourceModel{})}
		r.Update(context.Background(), frameworkresource.UpdateRequest{
			Plan:  resourcePlan(t, sch, plan),
			State: resourceState(t, sch, IamBackupImportResourceModel{}),
		}, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		request := recorder.only(t)
		if request.Method != http.MethodPut || request.Path != "/rustfs/admin/v3/import-iam" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}
		if string(request.Body) != iamBackupImportPayload {
			t.Errorf("the decoded archive was not sent: %q", request.Body)
		}

		var got IamBackupImportResourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.ContentBase64.ValueString() != plan.ContentBase64.ValueString() {
			t.Errorf("state was not written from the plan: %+v", got)
		}
	})

	t.Run("rejects invalid base64 before calling the API", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "")
		r := NewIamBackupImportResource().(*IamBackupImportResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := resourcePlan(t, sch, IamBackupImportResourceModel{
			ContentBase64: types.StringValue("not base64!"),
		})
		resp := &frameworkresource.UpdateResponse{State: resourceState(t, sch, IamBackupImportResourceModel{})}
		r.Update(context.Background(), frameworkresource.UpdateRequest{
			Plan:  plan,
			State: resourceState(t, sch, IamBackupImportResourceModel{}),
		}, resp)

		assertDiagnosticSummary(t, resp.Diagnostics, "Invalid base64 data")
		if got := recorder.count(); got != 0 {
			t.Errorf("expected no admin API request, got %d", got)
		}
	})

	t.Run("reports a server failure", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusInternalServerError, "<Error><Code>IamImportFailed</Code></Error>")
		r := NewIamBackupImportResource().(*IamBackupImportResource)
		r.client = newUnitAllClient(srv)
		sch := resourceSchemaOf(t, r)

		plan := resourcePlan(t, sch, IamBackupImportResourceModel{
			ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(iamBackupImportPayload))),
		})
		resp := &frameworkresource.UpdateResponse{State: resourceState(t, sch, IamBackupImportResourceModel{})}
		r.Update(context.Background(), frameworkresource.UpdateRequest{
			Plan:  plan,
			State: resourceState(t, sch, IamBackupImportResourceModel{}),
		}, resp)

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error importing IAM data")
		if !strings.Contains(diagnostic.Detail(), "IamImportFailed") {
			t.Errorf("diagnostic detail lost the server payload: %s", diagnostic.Detail())
		}
	})
}

func TestIamBackupImportResourceRead(t *testing.T) {
	srv, recorder := newRecorderServer(t, http.StatusOK, "")
	r := NewIamBackupImportResource().(*IamBackupImportResource)
	r.client = newUnitAllClient(srv)
	sch := resourceSchemaOf(t, r)

	state := IamBackupImportResourceModel{
		ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(iamBackupImportPayload))),
	}
	resp := &frameworkresource.ReadResponse{State: resourceState(t, sch, IamBackupImportResourceModel{})}
	r.Read(context.Background(), frameworkresource.ReadRequest{State: resourceState(t, sch, state)}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	var got IamBackupImportResourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("reading the state back: %v", diags)
	}
	if got.ContentBase64.ValueString() != state.ContentBase64.ValueString() {
		t.Errorf("Read dropped state: %+v", got)
	}
	if got := recorder.count(); got != 0 {
		t.Errorf("Read must not call the admin API, saw %d requests", got)
	}
}

func TestIamBackupImportResourceDelete(t *testing.T) {
	srv, recorder := newRecorderServer(t, http.StatusOK, "")
	r := NewIamBackupImportResource().(*IamBackupImportResource)
	r.client = newUnitAllClient(srv)
	sch := resourceSchemaOf(t, r)

	resp := &frameworkresource.DeleteResponse{}
	r.Delete(context.Background(), frameworkresource.DeleteRequest{
		State: resourceState(t, sch, IamBackupImportResourceModel{
			ContentBase64: types.StringValue(base64.StdEncoding.EncodeToString([]byte(iamBackupImportPayload))),
		}),
	}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}
	if got := recorder.count(); got != 0 {
		t.Errorf("Delete is a no-op and must not call the admin API, saw %d requests", got)
	}
}

// IAM import acceptance coverage. The import route is registered on RustFS
// 1.0.x: importing is a write the server performs immediately, so the resource
// has no readable state beyond the archive it was given and no ImportState.

func TestAccIamBackupImportResource(t *testing.T) {
	archive := testAccIamBackupArchive(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_iam_backup_import" "test" {
  content_base64 = %q
}
`, archive),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(iamBackupImportResourceName, "content_base64"),
					resource.TestCheckResourceAttr(iamBackupImportResourceName, "content_base64", archive),
				),
			},
		},
	})
}

// testAccIamBackupArchive exports the live IAM data once so the configuration
// carries a stable archive. It cannot come from the rustfs_iam_backup data
// source: two consecutive exports differ byte for byte (the archive embeds
// per-call metadata), and content_base64 forces replacement, so the plan after
// apply would never be empty.
func testAccIamBackupArchive(t *testing.T) string {
	t.Helper()
	testAccPreCheck(t)
	client := testAccRustClient()
	data, err := client.ExportIam()
	if err != nil {
		t.Fatalf("exporting the live IAM data: %v", err)
	}
	return base64.StdEncoding.EncodeToString(data)
}
