// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
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

const kmsKeyTestResourceName = "rustfs_kms_key.test"

func TestAccKmsKeyResource(t *testing.T) {
	name := fmt.Sprintf("tf-kms-key-%d", acctest.RandInt())

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccKmsKeyPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckKmsKeyDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccKmsKeyConfig(name, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckKmsKeyExists(name),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "name", name),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "enabled", "true"),
					resource.TestCheckResourceAttrSet(kmsKeyTestResourceName, "key_id"),
					resource.TestCheckResourceAttrSet(kmsKeyTestResourceName, "created_at"),
				),
			},
			{
				Config: testAccKmsKeyConfig(name, false),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckKmsKeyExists(name),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "enabled", "false"),
				),
			},
			{
				Config: testAccKmsKeyConfig(name, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckKmsKeyExists(name),
					resource.TestCheckResourceAttr(kmsKeyTestResourceName, "enabled", "true"),
				),
			},
			{
				ResourceName:  kmsKeyTestResourceName,
				ImportState:   true,
				ImportStateId: name,
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckKmsKeyExists(name),
				),
			},
		},
	})
}

// testAccKmsKeyPreCheck verifies the environment and that the running RustFS
// backend supports KMS key creation. The Local dev backend does; the Static
// (read-only) backend does not. When the running backend cannot create keys
// the whole test skips gracefully instead of failing.
func testAccKmsKeyPreCheck(t *testing.T) {
	testAccPreCheck(t)

	client := newKmsKeyAccClient()
	//#nosec G117 — the probe key name is a public identifier, not a secret
	probeName := fmt.Sprintf("tf-kms-precheck-%d", acctest.RandInt())
	probeKey, err := client.CreateKmsKey(probeName)
	if err != nil {
		t.Skipf("KMS key creation is not supported on the running backend, skipping: %v", err)
	}
	// Clean up the probe key (scheduled deletion; cancellable server-side).
	if delErr := client.DeleteKmsKey(probeKey.KeyID); delErr != nil {
		t.Logf("warning: failed to clean up probe key %s: %v", probeKey.KeyID, delErr)
	}
}

func testAccKmsKeyConfig(name string, enabled bool) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_kms_key" "test" {
  name         = %q
  enabled      = %t
  skip_destroy = false
}
`, name, enabled)
}

func testAccCheckKmsKeyExists(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[kmsKeyTestResourceName]
		if !ok {
			return fmt.Errorf("not found: %s", kmsKeyTestResourceName)
		}
		if rs.Primary.Attributes["name"] != name {
			return fmt.Errorf("wrong name in state: %s", rs.Primary.Attributes["name"])
		}
		keyID := rs.Primary.Attributes["key_id"]
		if keyID == "" {
			return fmt.Errorf("no key_id set in state")
		}

		key, err := newKmsKeyAccClient().DescribeKmsKey(keyID)
		if err != nil {
			return fmt.Errorf("error describing KMS key %s: %s", keyID, err)
		}
		if key.KeyID != keyID {
			return fmt.Errorf("described key id mismatch: got %s, want %s", key.KeyID, keyID)
		}
		return nil
	}
}

func testAccCheckKmsKeyDestroy(s *terraform.State) error {
	client := newKmsKeyAccClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_kms_key" {
			continue
		}
		keyID := rs.Primary.Attributes["key_id"]
		if keyID == "" {
			continue
		}
		key, err := client.DescribeKmsKey(keyID)
		if err != nil {
			// Key gone (or describe refused): that satisfies destruction.
			continue
		}
		if key.KeyState != "PendingDeletion" {
			return fmt.Errorf("KMS key %s still present in state %s", keyID, key.KeyState)
		}
	}
	return nil
}

func newKmsKeyAccClient() *rustfs.RustfsAdmin {
	client := rustfs.New(&rustfs.RustfsAdminConfig{
		Endpoint:     os.Getenv("RUSTFS_ENDPOINT"),
		AccessKey:    os.Getenv("RUSTFS_USER"),
		AccessSecret: os.Getenv("RUSTFS_SECRET"),
	})
	return &client
}

// kmsKeyLifecycleModel is a plan/state value with every attribute set; the
// computed key_id and created_at carry the values a previous apply would have
// stored.
func kmsKeyLifecycleModel() *kmsKeyRessourceModel {
	return &kmsKeyRessourceModel{
		Name:        types.StringValue("tf-key"),
		KeyID:       types.StringValue("key-1"),
		CreatedAt:   types.StringValue("2026-01-01T00:00:00Z"),
		Enabled:     types.BoolValue(true),
		SkipDestroy: types.BoolValue(false),
	}
}

// kmsKeyResponse renders the key_metadata object the KMS admin API returns for
// create/describe (pkg/rustfs/kms.go).
func kmsKeyResponse(keyID, state, created string) string {
	return fmt.Sprintf(`{"success":true,"key_metadata":{"key_id":%q,"key_state":%q,"key_usage":"ENCRYPT_DECRYPT","creation_date":%q,"origin":"RustFS_KMS","key_manager":"CUSTOMER"}}`, keyID, state, created)
}

// TestKmsKeyRessourceLifecyclePaths covers the paths the acceptance suite
// cannot reach with a live server: a key that disappeared behind Terraform's
// back, a delete that arrives after the key is already gone, and every failing
// API call in Create, Read and Delete.
//
// The KMS service was not running on the probe host (GET kms/status answered
// 500 InternalError "KMS service not initialized"), so these cases drive the
// paths from canned responses in the shape pkg/rustfs/kms.go documents.
func TestKmsKeyRessourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		model       *kmsKeyRessourceModel
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
		wantKeyID   string
		wantCreated string
	}{
		{
			// pkg/rustfs DescribeKmsKey turns a 200 body without key_metadata
			// into the response message, so "not found" is the missing-key text.
			name:        "key gone from the server is removed from state",
			routes:      []requestRoute{{status: http.StatusOK, body: `{"success":false,"message":"key not found","key_metadata":null}`}},
			wantRemoved: true,
		},
		{
			name:        "key present is refreshed from the server",
			routes:      []requestRoute{{status: http.StatusOK, body: kmsKeyResponse("key-1", "Disabled", "2026-02-02T00:00:00Z")}},
			wantCreated: "2026-02-02T00:00:00Z",
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error reading KMS key",
			wantDetail:  "Could not read KMS key, unexpected error: ",
		},
		{
			// Import path: no key_id in state, so the listing resolves one.
			name:        "import with no key id resolves the listing and is removed when unmatched",
			model:       &kmsKeyRessourceModel{Name: types.StringValue("tf-key"), KeyID: types.StringNull(), CreatedAt: types.StringNull(), Enabled: types.BoolValue(true), SkipDestroy: types.BoolValue(false)},
			routes:      []requestRoute{{status: http.StatusOK, body: `{"success":true,"keys":[]}`}},
			wantRemoved: true,
		},
		{
			name:        "import with no key id reports a failed listing",
			model:       &kmsKeyRessourceModel{Name: types.StringValue("tf-key"), KeyID: types.StringNull(), CreatedAt: types.StringNull(), Enabled: types.BoolValue(true), SkipDestroy: types.BoolValue(false)},
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error listing KMS keys",
			wantDetail:  "Could not resolve key id from name, unexpected error: ",
		},
		{
			name:  "import with no key id resolves the tagged key",
			model: &kmsKeyRessourceModel{Name: types.StringValue("tf-key"), KeyID: types.StringNull(), CreatedAt: types.StringNull(), Enabled: types.BoolValue(true), SkipDestroy: types.BoolValue(false)},
			routes: []requestRoute{
				{method: http.MethodGet, pathContains: "kms/keys/key-9f3", status: http.StatusOK, body: kmsKeyResponse("key-9f3", "Enabled", "2026-03-03T00:00:00Z")},
				{method: http.MethodGet, pathContains: "kms/keys", status: http.StatusOK, body: `{"success":true,"keys":[{"key_id":"key-9f3","tags":{"name":"tf-key"}}]}`},
			},
			wantKeyID: "key-9f3",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewKmsKeyRessource(), client)

			model := tc.model
			if model == nil {
				model = kmsKeyLifecycleModel()
			}
			resp := h.read(model)

			switch {
			case tc.wantRemoved:
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
			case tc.wantSummary != "":
				assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
				assertStatePresent(t, resp.State)
			default:
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStatePresent(t, resp.State)
			}
			if tc.wantCreated != "" {
				assertStateString(t, resp.State, path.Root("created_at"), tc.wantCreated)
			}
			if tc.wantKeyID != "" {
				assertStateString(t, resp.State, path.Root("key_id"), tc.wantKeyID)
			}
		})
	}

	deleteCases := []struct {
		name        string
		model       *kmsKeyRessourceModel
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			name:   "already deleted key is not an error",
			routes: []requestRoute{{status: http.StatusNotFound, body: `{"success":false,"message":"key not found","key_metadata":null}`}},
		},
		{
			name:        "real server error is reported",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "kms service is not running")}},
			wantSummary: "Error deleting KMS key",
			wantDetail:  "Could not delete KMS key, unexpected error: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewKmsKeyRessource(), client)

			model := tc.model
			if model == nil {
				model = kmsKeyLifecycleModel()
			}
			resp := h.delete(model)

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
		})
	}

	t.Run("delete: skip_destroy leaves the key alone", func(t *testing.T) {
		// No route at all: any request the resource made would be unrouted and
		// fail the test.
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewKmsKeyRessource(), client)
		model := kmsKeyLifecycleModel()
		model.SkipDestroy = types.BoolValue(true)

		resp := h.delete(model)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "kms/keys/delete")
	})

	t.Run("delete: successful destroy is silent", func(t *testing.T) {
		client, rec := routedClient(t, requestRoute{status: http.StatusOK, body: `{"success":true}`})
		h := newCRUDHarness(t, NewKmsKeyRessource(), client)

		resp := h.delete(kmsKeyLifecycleModel())

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertRequest(t, "kms/keys/delete")
	})

	createCases := []struct {
		name        string
		model       *kmsKeyRessourceModel
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "rejected key reports the server error and writes no state",
			routes:      []requestRoute{{status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "kms service is not running")}},
			wantSummary: "Error creating KMS key",
			wantDetail:  "Could not create KMS key, unexpected error: ",
		},
		{
			name: "failed disable of a new key is reported",
			model: &kmsKeyRessourceModel{
				Name:        types.StringValue("tf-key"),
				KeyID:       types.StringNull(),
				CreatedAt:   types.StringNull(),
				Enabled:     types.BoolValue(false),
				SkipDestroy: types.BoolValue(false),
			},
			routes: []requestRoute{
				{method: http.MethodPost, pathContains: "kms/keys/disable", status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "disable exploded")},
				{method: http.MethodPost, pathContains: "kms/keys", status: http.StatusOK, body: kmsKeyResponse("key-1", "Enabled", "2026-01-01T00:00:00Z")},
			},
			wantSummary: "Error disabling KMS key",
			wantDetail:  "Could not disable KMS key, unexpected error: ",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewKmsKeyRessource(), client)

			model := tc.model
			if model == nil {
				model = kmsKeyLifecycleModel()
			}
			resp := h.create(model)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, model)
		})
	}

	t.Run("create: accepted key writes the server identity", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{status: http.StatusOK, body: kmsKeyResponse("key-1", "Enabled", "2026-01-01T00:00:00Z")})
		h := newCRUDHarness(t, NewKmsKeyRessource(), client)
		plan := kmsKeyLifecycleModel()
		plan.KeyID = types.StringNull()
		plan.CreatedAt = types.StringNull()

		resp := h.create(plan)

		requireNoDiagnostics(t, resp.Diagnostics)
		assertStateString(t, resp.State, path.Root("key_id"), "key-1")
		assertStateString(t, resp.State, path.Root("created_at"), "2026-01-01T00:00:00Z")
	})

	t.Run("update: rejected enable reports a diagnostic and keeps the prior state", func(t *testing.T) {
		client, _ := routedClient(t, requestRoute{method: http.MethodPost, pathContains: "kms/keys/disable", status: http.StatusInternalServerError, body: s3ErrorXML("InternalError", "disable exploded")})
		h := newCRUDHarness(t, NewKmsKeyRessource(), client)

		plan := kmsKeyLifecycleModel()
		plan.Enabled = types.BoolValue(false)
		state := kmsKeyLifecycleModel()

		resp := h.update(plan, state)

		assertErrorDiagnostic(t, resp.Diagnostics, "Error updating KMS key", "Could not update KMS key, unexpected error: ")
		assertStatePresent(t, resp.State)
	})

	t.Run("update: unchanged enable switch makes no request", func(t *testing.T) {
		client, rec := routedClient(t)
		h := newCRUDHarness(t, NewKmsKeyRessource(), client)
		plan := kmsKeyLifecycleModel()
		state := kmsKeyLifecycleModel()

		resp := h.update(plan, state)

		requireNoDiagnostics(t, resp.Diagnostics)
		rec.assertNoRequest(t, "kms/keys")
	})

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewKmsKeyRessource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
