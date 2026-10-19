// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const iamBackupDataSourceName = "data.rustfs_iam_backup.test"

func TestIamBackupDataSourceSchema(t *testing.T) {
	d := NewIamBackupDataSource()
	sch := dataSourceSchemaOf(t, d)

	attrs := sch.GetAttributes()
	if len(attrs) != 1 {
		t.Errorf("expected 1 attribute, got %d: %v", len(attrs), attrs)
	}

	contentBase64, ok := attrs["content_base64"].(datasourceschema.StringAttribute)
	if !ok {
		t.Fatalf("content_base64 is not a StringAttribute")
	}
	if !contentBase64.Computed || contentBase64.Required || contentBase64.Optional {
		t.Errorf("content_base64 must be Computed only: %+v", contentBase64)
	}
	if !contentBase64.Sensitive {
		t.Error("content_base64 carries the exported IAM material and must be Sensitive")
	}
	if contentBase64.Description == "" {
		t.Error("content_base64 has no description, tfplugindocs would publish an empty row")
	}
}

func TestIamBackupDataSourceMetadata(t *testing.T) {
	resp := &datasource.MetadataResponse{}
	NewIamBackupDataSource().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)
	if resp.TypeName != "rustfs_iam_backup" {
		t.Errorf("expected rustfs_iam_backup, got %s", resp.TypeName)
	}
}

func TestIamBackupDataSourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		d := NewIamBackupDataSource().(*IamBackupDataSource)
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
		d := NewIamBackupDataSource().(*IamBackupDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
	})

	t.Run("all client", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, "")
		d := NewIamBackupDataSource().(*IamBackupDataSource)
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

func TestIamBackupDataSourceRead(t *testing.T) {
	t.Run("exports the archive", func(t *testing.T) {
		payload := []byte("PK\x03\x04archive")
		srv, recorder := newRecorderServer(t, http.StatusOK, string(payload))
		d := NewIamBackupDataSource().(*IamBackupDataSource)
		d.client = newUnitAllClient(srv)
		sch := dataSourceSchemaOf(t, d)

		resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
		d.Read(context.Background(), datasource.ReadRequest{
			Config: dataSourceConfig(t, sch, IamBackupDataSourceModel{}),
		}, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		request := recorder.only(t)
		if request.Method != http.MethodGet || request.Path != "/rustfs/admin/v3/export-iam" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}

		var got IamBackupDataSourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		want := base64.StdEncoding.EncodeToString(payload)
		if got.ContentBase64.ValueString() != want {
			t.Errorf("content_base64 was not the base64 of the exported bytes: got %q, want %q", got.ContentBase64.ValueString(), want)
		}
	})

	t.Run("reports a server failure", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusInternalServerError, "<Error><Code>InternalError</Code><Message>iam export failed</Message></Error>")
		d := NewIamBackupDataSource().(*IamBackupDataSource)
		d.client = newUnitAllClient(srv)
		sch := dataSourceSchemaOf(t, d)

		resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
		d.Read(context.Background(), datasource.ReadRequest{
			Config: dataSourceConfig(t, sch, IamBackupDataSourceModel{}),
		}, resp)

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error exporting IAM data")
		if !strings.Contains(diagnostic.Detail(), "iam export failed") {
			t.Errorf("diagnostic detail lost the server payload: %s", diagnostic.Detail())
		}
	})
}

// IAM backup acceptance coverage. The export route is registered on the RustFS
// admin API and answers with a ZIP archive built from the live IAM entities, so
// the check only verifies that content_base64 decodes to a ZIP: the archive's
// size and entries depend on the deployment.

func TestAccIamBackupDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig() + `
data "rustfs_iam_backup" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(iamBackupDataSourceName, "content_base64"),
					testAccCheckIamBackupIsZip(iamBackupDataSourceName),
				),
			},
		},
	})
}

// testAccCheckIamBackupIsZip requires content_base64 to be a valid base64
// encoding of a ZIP archive. Only the ZIP magic is asserted: the archive is
// built from whatever IAM entities the deployment holds.
func testAccCheckIamBackupIsZip(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("not found: %s", name)
		}
		encoded := rs.Primary.Attributes["content_base64"]
		if encoded == "" {
			return fmt.Errorf("no content_base64 set")
		}
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return fmt.Errorf("content_base64 is not valid base64: %w", err)
		}
		if !bytes.HasPrefix(decoded, []byte("PK")) {
			return fmt.Errorf("content_base64 does not decode to a ZIP archive")
		}
		return nil
	}
}
