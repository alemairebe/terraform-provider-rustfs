// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccServiceAccountResource_basic(t *testing.T) {
	accessKey := fmt.Sprintf("tf-test-sa-%d", acctest.RandInt())
	resourceName := "rustfs_serviceaccount.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServiceAccountDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccServiceAccountConfig(accessKey, "test-service"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServiceAccountExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "access_key", accessKey),
				),
			},
			{
				ResourceName:                         resourceName,
				ImportState:                          true,
				ImportStateId:                        accessKey,
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "access_key",
				ImportStateVerifyIgnore:              []string{"secret_key"},
			},
		},
	})
}

func TestAccServiceAccountResource_update(t *testing.T) {
	accessKey := fmt.Sprintf("tf-test-sa-%d", acctest.RandInt())
	resourceName := "rustfs_serviceaccount.test"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServiceAccountDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccServiceAccountConfig(accessKey, "original-name"),
				Check:  resource.TestCheckResourceAttr(resourceName, "name", "original-name"),
			},
			{
				Config: testAccServiceAccountConfig(accessKey, "updated-name"),
				Check:  resource.TestCheckResourceAttr(resourceName, "name", "updated-name"),
			},
		},
	})
}

func testAccServiceAccountConfig(accessKey, name string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_serviceaccount" "test" {
  access_key  = "%s"
  secret_key  = "superSecret123!"
  name        = "%s"
  description = "acceptance test service account"
}
`, accessKey, name)
}

func testAccCheckServiceAccountExists(n string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[n]
		if !ok {
			return fmt.Errorf("not found: %s", n)
		}
		accessKey := rs.Primary.Attributes["access_key"]
		if accessKey == "" {
			return fmt.Errorf("no access_key set")
		}
		client := testAccSAClient()
		_, err := client.ReadServiceAccount(accessKey)
		if err != nil {
			return fmt.Errorf("service account not found: %s", err)
		}
		return nil
	}
}

func testAccCheckServiceAccountDestroy(s *terraform.State) error {
	client := testAccSAClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_serviceaccount" {
			continue
		}
		accessKey := rs.Primary.Attributes["access_key"]
		if accessKey == "" {
			continue
		}
		_, err := client.ReadServiceAccount(accessKey)
		if err == nil {
			return fmt.Errorf("service account %s still exists", accessKey)
		}
	}
	return nil
}

func testAccSAClient() rustfs.RustfsAdmin {
	return rustfs.New(&rustfs.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
	})
}

func TestAccServiceAccountExpiryPolicyResource(t *testing.T) {
	accessKey := fmt.Sprintf("tf-test-saexp-%d", acctest.RandInt())
	resourceName := "rustfs_serviceaccount.test"
	policyDoc := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::*"]}]}`

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServiceAccountDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccServiceAccountExpiryPolicyConfig(accessKey, "2030-01-01T00:00:00Z", policyDoc),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckServiceAccountExists(resourceName),
					resource.TestCheckResourceAttr(resourceName, "access_key", accessKey),
					resource.TestCheckResourceAttr(resourceName, "expiration", "2030-01-01T00:00:00Z"),
					resource.TestCheckResourceAttr(resourceName, "policy", policyDoc),
					resource.TestCheckResourceAttr(resourceName, "implied_policy", "false"),
				),
			},
			{
				Config: testAccServiceAccountExpiryPolicyConfig(accessKey, "2031-01-01T00:00:00Z", policyDoc),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "expiration", "2031-01-01T00:00:00Z"),
				),
			},
		},
	})
}

func testAccServiceAccountExpiryPolicyConfig(accessKey, expiration, policy string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_serviceaccount" "test" {
  access_key  = "%s"
  secret_key  = "superSecret123!"
  name        = "expiryaccount"
  expiration  = "%s"
  policy      = %q
}
`, accessKey, expiration, policy)
}

var (
	lifecycleSAAccessKey = unitTestCredential("RUSTFS_TEST_SA_ACCESS_KEY", 'k')
	lifecycleSASecretKey = unitTestCredential("RUSTFS_TEST_SA_SECRET_KEY", 'm')
)

// TestServiceAccountResourceLifecyclePaths covers the paths the acceptance
// suite cannot reach with a live server: a service account that disappeared
// behind Terraform's back, a delete that arrives after the account is already
// gone, and every failing API call in Create, Read and Delete.
func TestServiceAccountResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	// The base model every call starts from. Unset optional attributes use
	// typed nulls so tfsdk conversion keeps the attribute types.
	model := func() *serviceAccountResourceModel {
		return &serviceAccountResourceModel{
			AccessKey:     types.StringValue(lifecycleSAAccessKey),
			SecretKey:     types.StringValue(lifecycleSASecretKey),
			Name:          types.StringValue("tf-test sa"),
			Description:   types.StringNull(),
			TargetUser:    types.StringNull(),
			Expiration:    types.StringValue("9999-01-01T00:00:00Z"),
			Policy:        types.StringNull(),
			ImpliedPolicy: types.BoolNull(),
		}
	}

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantName    string
		wantSummary string
		wantDetail  string
	}{
		{
			// Probed against RustFS latest: ReadServiceAccount answers
			// 404 NoSuchResource, message "service account '<ak>' does not exist".
			name:        "missing account is removed from state",
			routes:      []requestRoute{{status: http.StatusNotFound, body: s3ErrorXML("NoSuchResource", "service account 'tf-test-sa' does not exist")}},
			wantRemoved: true,
		},
		{
			name:     "present account refreshes state",
			routes:   []requestRoute{{status: http.StatusOK, body: `{"accessKey":"tf-test-sa","name":"refreshed name","impliedPolicy":true}`}},
			wantName: "refreshed name",
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading service account",
			wantDetail:  "Could not read service account, unexpected error: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewServiceAccountResource(), client)

			resp := h.read(model())

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
				assertStateString(t, resp.State, path.Root("name"), tc.wantName)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertStatePresent(t, resp.State)
		})
	}

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			// Probed against RustFS latest: DeleteServiceAccount answers
			// 400 InvalidRequest with message "service account not exist".
			name:   "already deleted account is not an error",
			routes: []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "service account not exist")}},
		},
		{
			// The same status code with an unrelated message must still fail:
			// RustFS reuses InvalidRequest for many request failures.
			name:        "unrelated invalid request is still an error",
			routes:      []requestRoute{{status: http.StatusBadRequest, body: s3ErrorXML("InvalidRequest", "access key is required")}},
			wantSummary: "Error deleting service account",
			wantDetail:  "Could not delete service account, unexpected error: ",
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error deleting service account",
			wantDetail:  "Could not delete service account, unexpected error: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewServiceAccountResource(), client)

			resp := h.delete(model())

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
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
			name:   "accepted account writes state",
			routes: []requestRoute{{method: http.MethodPut, pathContains: "add-service-accounts", status: http.StatusOK, body: `{"credentials":{"accessKey":"tf-test-sa","secretKey":"superSecret123!","expiration":"9999-01-01T00:00:00Z"}}`}},
		},
		{
			name:        "rejected account reports the server error and writes no state",
			routes:      []requestRoute{{method: http.MethodPut, pathContains: "add-service-accounts", status: http.StatusConflict, body: s3ErrorXML("InvalidRequest", "access key already exists")}},
			wantSummary: "Error creating service account",
			wantDetail:  "Could not create service account, unexpected error: ",
		},
		{
			name:        "forbidden create names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error creating service account",
			wantDetail:  "Could not create service account, unexpected error: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewServiceAccountResource(), client)
			plan := model()

			resp := h.create(plan)

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
				assertStateString(t, resp.State, path.Root("access_key"), lifecycleSAAccessKey)
				// implied_policy is a bool, so assert it through the model
				// rather than the string helper.
				var created serviceAccountResourceModel
				if diags := resp.State.Get(context.Background(), &created); diags.HasError() {
					t.Fatalf("state get: %v", diags)
				}
				if !created.ImpliedPolicy.ValueBool() {
					t.Errorf("implied_policy = %v, want true for an account without a policy", created.ImpliedPolicy)
				}
				rec.assertRequest(t, "add-service-accounts")
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, plan)
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewServiceAccountResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
