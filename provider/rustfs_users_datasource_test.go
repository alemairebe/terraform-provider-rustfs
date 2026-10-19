// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const usersDataSourceName = "data.rustfs_users.all"

const usersBucketDataSourceName = "data.rustfs_users.bucket"

// usersDataSourcePayload is what GET /rustfs/admin/v3/list-users returns on
// RustFS 1.0.x: an object keyed by access key, in no particular order.
const usersDataSourcePayload = `{"bob":{"status":"disabled"},` +
	`"alice":{"status":"enabled","policyName":"readwrite"}}`

func TestUsersDataSourceSchema(t *testing.T) {
	d := NewUsersDataSource()
	sch := dataSourceSchemaOf(t, d)

	attrs := sch.GetAttributes()
	if len(attrs) != 2 {
		t.Errorf("expected 2 attributes, got %d: %v", len(attrs), attrs)
	}

	bucket, ok := attrs["bucket"].(datasourceschema.StringAttribute)
	if !ok {
		t.Fatalf("bucket is not a StringAttribute")
	}
	if !bucket.Optional || bucket.Required || bucket.Computed {
		t.Errorf("bucket must be Optional only: %+v", bucket)
	}

	accessKeys, ok := attrs["access_keys"].(datasourceschema.ListAttribute)
	if !ok {
		t.Fatalf("access_keys is not a ListAttribute")
	}
	if !accessKeys.Computed || accessKeys.Optional || accessKeys.Required {
		t.Errorf("access_keys must be Computed only: %+v", accessKeys)
	}
	if !accessKeys.ElementType.Equal(types.StringType) {
		t.Errorf("access_keys must hold strings, got %s", accessKeys.ElementType)
	}

	for name, attr := range attrs {
		if attr.GetDescription() == "" {
			t.Errorf("attribute %s has no description, tfplugindocs would publish an empty row", name)
		}
	}
}

func TestUsersDataSourceMetadata(t *testing.T) {
	d := NewUsersDataSource()
	resp := &datasource.MetadataResponse{}
	d.Metadata(context.TODO(), datasource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_users" {
		t.Errorf("expected rustfs_users, got %s", resp.TypeName)
	}
}

func TestUsersDataSourceConfigure(t *testing.T) {
	t.Run("nil provider data", func(t *testing.T) {
		d := NewUsersDataSource().(*UsersDataSource)
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
		d := NewUsersDataSource().(*UsersDataSource)
		resp := &datasource.ConfigureResponse{}
		d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "not-a-client"}, resp)
		assertDiagnosticSummary(t, resp.Diagnostics, "Unexpected Data Source Configure Type")
	})

	t.Run("all client", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusOK, "")
		d := NewUsersDataSource().(*UsersDataSource)
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

func TestUsersDataSourceRead(t *testing.T) {
	t.Run("lists the users sorted by access key", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, usersDataSourcePayload)
		d := NewUsersDataSource().(*UsersDataSource)
		d.client = newUnitAllClient(srv)
		sch := dataSourceSchemaOf(t, d)

		resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
		d.Read(context.Background(), datasource.ReadRequest{
			Config: dataSourceConfig(t, sch, UsersDataSourceModel{
				Bucket:     types.StringValue("my-bucket"),
				AccessKeys: types.ListNull(types.StringType),
			}),
		}, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		request := recorder.only(t)
		if request.Method != http.MethodGet || request.Path != "/rustfs/admin/v3/list-users" {
			t.Errorf("unexpected request %s %s", request.Method, request.Path)
		}
		if !strings.Contains(request.Query, "bucket=my-bucket") {
			t.Errorf("bucket was not forwarded to the admin API: %q", request.Query)
		}

		var got UsersDataSourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.Bucket.ValueString() != "my-bucket" {
			t.Errorf("bucket was not carried into the state: %+v", got.Bucket)
		}
		var keys []string
		if diags := got.AccessKeys.ElementsAs(context.Background(), &keys, false); diags.HasError() {
			t.Fatalf("reading the access keys: %v", diags)
		}
		// The admin API answers with a map, so the sort is what makes the list
		// stable across reads.
		if len(keys) != 2 || keys[0] != "alice" || keys[1] != "bob" {
			t.Errorf("expected the access keys sorted by key, got %v", keys)
		}
	})

	t.Run("accepts an empty answer", func(t *testing.T) {
		srv, recorder := newRecorderServer(t, http.StatusOK, "{}")
		d := NewUsersDataSource().(*UsersDataSource)
		d.client = newUnitAllClient(srv)
		sch := dataSourceSchemaOf(t, d)

		resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
		d.Read(context.Background(), datasource.ReadRequest{
			Config: dataSourceConfig(t, sch, UsersDataSourceModel{
				Bucket:     types.StringNull(),
				AccessKeys: types.ListNull(types.StringType),
			}),
		}, resp)

		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
		}
		if query := recorder.only(t).Query; query != "" {
			t.Errorf("an unset bucket must not add a query filter, got %q", query)
		}

		var got UsersDataSourceModel
		if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
			t.Fatalf("reading the state back: %v", diags)
		}
		if got.AccessKeys.IsUnknown() {
			t.Fatal("an empty answer must not leave access_keys unknown")
		}
		// The data source collects the access keys into a nil slice and the
		// framework renders a nil slice as null rather than as an empty list,
		// so the empty answer arrives as null with no elements. The element
		// count is what the data source guarantees here.
		if elements := got.AccessKeys.Elements(); len(elements) != 0 {
			t.Errorf("expected no access keys, got %v", elements)
		}
	})

	t.Run("reports a server failure", func(t *testing.T) {
		srv, _ := newRecorderServer(t, http.StatusInternalServerError, `{"code":"InternalError","message":"user listing is unavailable"}`)
		d := NewUsersDataSource().(*UsersDataSource)
		d.client = newUnitAllClient(srv)
		sch := dataSourceSchemaOf(t, d)

		resp := &datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
		d.Read(context.Background(), datasource.ReadRequest{
			Config: dataSourceConfig(t, sch, UsersDataSourceModel{
				Bucket:     types.StringNull(),
				AccessKeys: types.ListNull(types.StringType),
			}),
		}, resp)

		diagnostic := assertDiagnosticSummary(t, resp.Diagnostics, "Error listing users")
		if !strings.Contains(diagnostic.Detail(), "user listing is unavailable") {
			t.Errorf("diagnostic detail lost the server payload: %s", diagnostic.Detail())
		}
	})
}

// Users acceptance coverage. The list-users route is registered on the RustFS
// 1.0.x admin API and answers with the deployment's IAM users, so the check
// proves the created user is listed without pinning the user count: the
// deployment usually holds an admin account and any user another test created.

func TestAccUsersDataSource(t *testing.T) {
	accessKey := fmt.Sprintf("tf-acc-users-ds-%d", acctest.RandInt())
	bucket := fmt.Sprintf("tf-acc-users-ds-bucket-%d", acctest.RandInt())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckUsersDataSourceDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccUsersDataSourceConfig(accessKey, bucket),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(usersDataSourceName, "access_keys.#"),
					testAccCheckUsersDataSourceContainsAccessKey(usersDataSourceName, accessKey),
					// A bucket with no user attached answers with an empty
					// object, so the filtered data source lists nothing. The
					// count key is absent from the state for both an empty and
					// a null list, which the check treats as zero.
					resource.TestCheckResourceAttr(usersBucketDataSourceName, "access_keys.#", "0"),
				),
			},
			{
				// The fixture creates the IAM user the data source reads, so
				// its import round trip is worth covering here too.
				ResourceName:                         usersTestResourceName,
				ImportState:                          true,
				ImportStateId:                        accessKey,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "access_key",
				ImportStateVerifyIgnore:              []string{"secret_key"},
			},
		},
	})
}

const usersTestResourceName = "rustfs_user.test"

// testAccCheckUsersDataSourceDestroy requires both halves of the fixture — the
// IAM user and the bucket — to be gone once the test case is destroyed.
func testAccCheckUsersDataSourceDestroy(s *terraform.State) error {
	if err := testAccCheckUserDestroy(s); err != nil {
		return err
	}
	return testAccCheckBucketDestroy(s)
}

func testAccUsersDataSourceConfig(accessKey, bucket string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_user" "test" {
  access_key = %q
  secret_key = "superSecret123!"
  status     = "enabled"
  policy     = ""
}

resource "rustfs_bucket" "test" {
  name = %q
}

data "rustfs_users" "all" {
  depends_on = [rustfs_user.test]
}

data "rustfs_users" "bucket" {
  bucket     = rustfs_bucket.test.name
  depends_on = [rustfs_bucket.test]
}
`, accessKey, bucket)
}

// testAccCheckUsersDataSourceContainsAccessKey requires the data source's list
// to hold accessKey. A ListAttribute has no set-style check helper, so the
// state entries are walked by index.
func testAccCheckUsersDataSourceContainsAccessKey(dataSourceName, accessKey string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[dataSourceName]
		if !ok {
			return fmt.Errorf("not found: %s", dataSourceName)
		}

		count, err := strconv.Atoi(rs.Primary.Attributes["access_keys.#"])
		if err != nil {
			return fmt.Errorf("%s: access_keys is not a list: %v", dataSourceName, err)
		}

		for i := range count {
			if rs.Primary.Attributes[fmt.Sprintf("access_keys.%d", i)] == accessKey {
				return nil
			}
		}
		return fmt.Errorf("%s: access_keys does not list %s", dataSourceName, accessKey)
	}
}
