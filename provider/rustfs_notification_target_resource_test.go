// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/defaults"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const notificationTargetResourceName = "rustfs_notification_target.test"

var notificationServiceNames = []string{
	"notify_webhook", "notify_amqp", "notify_kafka", "notify_mqtt", "notify_nats",
	"notify_postgres", "notify_mysql", "notify_redis", "notify_pulsar",
}

var notificationConfigBlockNames = []string{
	"webhook_config", "amqp_config", "kafka_config", "mqtt_config", "nats_config",
	"postgres_config", "mysql_config", "redis_config", "pulsar_config",
}

func TestNotificationTargetSchema(t *testing.T) {
	r := NewNotificationTargetResource()
	resp := &frameworkresource.SchemaResponse{}
	r.Schema(context.Background(), frameworkresource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()

	targetType, ok := attrs["target_type"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("expected target_type to be a StringAttribute, got %T", attrs["target_type"])
	}
	if !targetType.Required {
		t.Error("target_type should be Required")
	}
	if len(targetType.PlanModifiers) != 1 {
		t.Fatalf("target_type should have exactly one plan modifier, got %d", len(targetType.PlanModifiers))
	}
	requireReplacement(t, targetType.PlanModifiers[0], "target_type")
	if len(targetType.Validators) != 1 {
		t.Fatalf("target_type should have exactly one validator, got %d", len(targetType.Validators))
	}
	targetTypeDesc := targetType.Validators[0].Description(context.Background())
	for _, name := range notificationServiceNames {
		if !strings.Contains(targetTypeDesc, name) {
			t.Errorf("target_type validator should include %q, got description %q", name, targetTypeDesc)
		}
	}

	targetName, ok := attrs["target_name"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("expected target_name to be a StringAttribute, got %T", attrs["target_name"])
	}
	if !targetName.Required {
		t.Error("target_name should be Required")
	}
	if len(targetName.PlanModifiers) != 1 {
		t.Fatalf("target_name should have exactly one plan modifier, got %d", len(targetName.PlanModifiers))
	}
	requireReplacement(t, targetName.PlanModifiers[0], "target_name")

	enable, ok := attrs["enable"].(schema.BoolAttribute)
	if !ok {
		t.Fatalf("expected enable to be a BoolAttribute, got %T", attrs["enable"])
	}
	if !enable.Optional {
		t.Error("enable should be Optional")
	}
	if !enable.Computed {
		t.Error("enable should be Computed")
	}
	if enable.Default == nil {
		t.Fatal("enable should have a default")
	}
	enableDefault := &defaults.BoolResponse{}
	enable.Default.DefaultBool(context.Background(), defaults.BoolRequest{}, enableDefault)
	if !enableDefault.PlanValue.ValueBool() {
		t.Error("enable default should be true")
	}

	for _, name := range notificationConfigBlockNames {
		attr, ok := attrs[name]
		if !ok {
			t.Errorf("missing %s attribute", name)
			continue
		}
		if _, ok := attr.(schema.SingleNestedAttribute); !ok {
			t.Errorf("%s should be a SingleNestedAttribute, got %T", name, attr)
		}
	}
}

func TestNotificationTargetMetadata(t *testing.T) {
	r := NewNotificationTargetResource()
	resp := &frameworkresource.MetadataResponse{}
	r.Metadata(context.Background(), frameworkresource.MetadataRequest{ProviderTypeName: "rustfs"}, resp)

	if resp.TypeName != "rustfs_notification_target" {
		t.Errorf("expected rustfs_notification_target, got %s", resp.TypeName)
	}
}

func TestNotificationTargetValidate(t *testing.T) {
	tests := []struct {
		name    string
		model   notificationTargetResourceModel
		wantErr string
	}{
		{
			name: "unknown target_type with matching block is ok",
			model: notificationTargetResourceModel{
				TargetType: types.StringUnknown(),
				Webhook: &targetWebhookConfigModel{
					Endpoint: types.StringValue("https://hooks.example.com/webhook/x"),
				},
			},
		},
		{
			name: "null target_type with matching block is ok",
			model: notificationTargetResourceModel{
				TargetType: types.StringNull(),
				Webhook: &targetWebhookConfigModel{
					Endpoint: types.StringValue("https://hooks.example.com/webhook/x"),
				},
			},
		},
		{
			name: "block for wrong type",
			model: notificationTargetResourceModel{
				TargetType: types.StringValue("notify_amqp"),
				Webhook: &targetWebhookConfigModel{
					Endpoint: types.StringValue("https://hooks.example.com/webhook/x"),
				},
			},
			wantErr: `target type "notify_amqp" requires the amqp_config block`,
		},
		{
			name: "no block with non-webhook type",
			model: notificationTargetResourceModel{
				TargetType: types.StringValue("notify_amqp"),
			},
			wantErr: "target configuration block",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.model.validateConfig()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func requireReplacement(t *testing.T, modifier planmodifier.String, name string) {
	t.Helper()
	want := stringplanmodifier.RequiresReplace().Description(context.Background())
	if got := modifier.Description(context.Background()); got != want {
		t.Errorf("%s plan modifier should be RequiresReplace, got description %q", name, got)
	}
}

func TestAccNotificationTargetResource(t *testing.T) {
	name := fmt.Sprintf("tf-notify-%d", acctest.RandInt())
	endpoint := os.Getenv("RUSTFS_NOTIFY_TEST_ENDPOINT")

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccNotificationTargetPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckNotificationTargetDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccNotificationTargetConfig(name, "managed by terraform"),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckNotificationTargetExists(name),
					resource.TestCheckResourceAttr(notificationTargetResourceName, "target_type", "notify_webhook"),
					resource.TestCheckResourceAttr(notificationTargetResourceName, "target_name", name),
					resource.TestCheckResourceAttr(notificationTargetResourceName, "webhook_config.endpoint", endpoint),
					resource.TestCheckResourceAttr(notificationTargetResourceName, "comment", "managed by terraform"),
					resource.TestCheckResourceAttr(notificationTargetResourceName, "enable", "true"),
					resource.TestCheckResourceAttrSet(notificationTargetResourceName, "health_state"),
					resource.TestCheckResourceAttrSet(notificationTargetResourceName, "status"),
				),
			},
			{
				Config: testAccNotificationTargetConfig(name, "updated by terraform"),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckNotificationTargetExists(name),
					resource.TestCheckResourceAttr(notificationTargetResourceName, "webhook_config.endpoint", endpoint),
					resource.TestCheckResourceAttr(notificationTargetResourceName, "comment", "updated by terraform"),
					resource.TestCheckResourceAttrSet(notificationTargetResourceName, "health_state"),
					resource.TestCheckResourceAttrSet(notificationTargetResourceName, "status"),
				),
			},
			{
				ResourceName:      notificationTargetResourceName,
				ImportState:       true,
				ImportStateId:     "notify_webhook/" + name,
				ImportStateVerify: true,
				// The admin list endpoint does not echo config values, so these
				// attributes cannot be verified after import.
				ImportStateVerifyIdentifierAttribute: "target_name",
				ImportStateVerifyIgnore: []string{
					"enable", "comment",
					"webhook_config", "amqp_config", "kafka_config", "mqtt_config", "nats_config",
					"postgres_config", "mysql_config", "redis_config", "pulsar_config",
					"health_state", "health_reason", "status",
				},
			},
		},
	})
}

// testAccNotificationTargetPreCheck verifies the environment and that the
// running RustFS has the notification module enabled (the admin API rejects
// target management otherwise). The list endpoint succeeds even when the module
// is disabled, so the check enables the module when it is console/default
// sourced and probes an actual create; when the module is pinned by an
// environment variable the test skips (testAccEnsureModuleEnabled). The probe
// uses the webhook target (the only type that activates without an external
// service) pointed at RUSTFS_NOTIFY_TEST_ENDPOINT.
func testAccNotificationTargetPreCheck(t *testing.T) {
	testAccPreCheck(t)

	endpoint := os.Getenv("RUSTFS_NOTIFY_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("RUSTFS_NOTIFY_TEST_ENDPOINT must be set for the notification target acceptance test")
	}
	testAccEnsureModuleEnabled(t, "notify")

	client := testAccRustClient()
	probe := fmt.Sprintf("tf-notify-probe-%d", acctest.RandInt())
	probeConfig := []rustfs.TargetKeyValue{
		{Key: "endpoint", Value: endpoint},
	}
	err := client.SetNotificationTarget("notify_webhook", probe, probeConfig)
	if err != nil {
		if strings.Contains(err.Error(), "module is disabled") {
			t.Skipf("notification module is disabled on the running RustFS; skipping: %v", err)
		}
		t.Fatalf("notification target management is not available on the running RustFS: %v", err)
	}
	// Clean up the probe target so it does not linger in the server config.
	if err := client.ResetNotificationTarget("notify_webhook", probe); err != nil {
		t.Logf("could not clean up notification target probe %s: %v", probe, err)
	}
}

func testAccNotificationTargetConfig(name, comment string) string {
	endpoint := os.Getenv("RUSTFS_NOTIFY_TEST_ENDPOINT")
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_notification_target" "test" {
  target_type = "notify_webhook"
  target_name = "%s"

  webhook_config = {
    endpoint = "%s"
  }

  comment = "%s"
}
`, name, endpoint, comment)
}

func testAccCheckNotificationTargetExists(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[notificationTargetResourceName]
		if !ok {
			return fmt.Errorf("not found: %s", notificationTargetResourceName)
		}
		targetName := rs.Primary.Attributes["target_name"]
		if targetName == "" {
			return fmt.Errorf("no target_name set")
		}

		client := testAccRustClient()
		targets, err := client.ListNotificationTargets()
		if err != nil {
			return err
		}
		for _, target := range targets {
			if target.AccountID == name && target.Service == "webhook" {
				return nil
			}
		}
		return fmt.Errorf("notification target %s not found in list", name)
	}
}

func testAccCheckNotificationTargetDestroy(s *terraform.State) error {
	client := testAccRustClient()
	targets, err := client.ListNotificationTargets()
	if err != nil {
		return err
	}
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_notification_target" {
			continue
		}
		name := rs.Primary.Attributes["target_name"]
		for _, target := range targets {
			if strings.EqualFold(target.AccountID, name) {
				return fmt.Errorf("notification target %s still exists", name)
			}
		}
	}
	return nil
}

// TestNotificationTargetResourceLifecyclePaths covers the paths the acceptance
// suite cannot reach with a live server: a target that disappeared behind
// Terraform's back, a destroy that arrives after the target is already gone,
// and every failing API call in Create, Read and Delete.
//
// The reset endpoint is idempotent: the RustFS handler for
// DELETE target/{type}/{name}/reset calls remove_target_config, which only
// rewrites the server config when the entry exists and otherwise returns
// success, so a reset of an already-removed target is 200 OK and must not
// report a diagnostic. The live server at 127.0.0.1:9301 cannot show that path
// because its notify module is disabled and the handler rejects every target
// mutation with 400 InvalidRequest before it looks the target up (probed
// 2026-10-19: "notify module is disabled; enable the notify module first ...").
func TestNotificationTargetResourceLifecyclePaths(t *testing.T) {
	t.Parallel()

	readCases := []struct {
		name        string
		routes      []requestRoute
		wantRemoved bool
		wantSummary string
		wantDetail  string
	}{
		{
			// The list endpoint answers 200 with an empty
			// notification_endpoints array for a target that is gone.
			name: "target absent from the list is removed from state",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "target/list",
				status: http.StatusOK, body: `{"notify_enabled":true,"notification_endpoints":[]}`,
			}},
			wantRemoved: true,
		},
		{
			name: "listed target keeps state and mirrors its health",
			routes: []requestRoute{{
				method: http.MethodGet, pathContains: "target/list", status: http.StatusOK,
				body: `{"notify_enabled":true,"notification_endpoints":[{"account_id":"tf-test","service":"webhook","status":"online","health_state":"online","health_reason":"reachable"}]}`,
			}},
		},
		{
			name:        "server error keeps state and names the failing call",
			routes:      []requestRoute{{status: http.StatusForbidden, body: s3ErrorXML("AccessDenied", "nope")}},
			wantSummary: "Error listing notification targets",
			wantDetail:  "Could not list notification targets, unexpected error: ",
		},
	}

	for _, tc := range readCases {
		t.Run("read: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewNotificationTargetResource(), client)

			resp := h.read(&notificationTargetResourceModel{
				TargetType: types.StringValue("notify_webhook"),
				TargetName: types.StringValue("tf-test"),
			})

			if tc.wantRemoved {
				requireNoDiagnostics(t, resp.Diagnostics)
				assertStateRemoved(t, resp.State)
				return
			}
			if tc.wantSummary != "" {
				assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
				assertDiagnosticDetail(t, resp.Diagnostics, "AccessDenied")
				assertStatePresent(t, resp.State)
				return
			}
			requireNoDiagnostics(t, resp.Diagnostics)
			assertStatePresent(t, resp.State)
			assertStateString(t, resp.State, path.Root("health_state"), "online")
			assertStateString(t, resp.State, path.Root("status"), "online")
		})
	}

	deleteCases := []struct {
		name        string
		routes      []requestRoute
		wantSummary string
		wantDetail  string
	}{
		{
			// A reset of a target that is already gone is 200 OK, so a
			// repeated destroy stays a no-op instead of failing the apply.
			name:   "already removed target is not an error",
			routes: []requestRoute{{method: http.MethodDelete, pathContains: "/reset", status: http.StatusOK}},
		},
		{
			// A disabled notify module is a real server error, not a missing
			// target, and must reach the user.
			name: "real server error is reported",
			routes: []requestRoute{{
				method: http.MethodDelete, pathContains: "/reset", status: http.StatusBadRequest,
				body: s3ErrorXML("InvalidRequest", "notify module is disabled; enable the notify module first"),
			}},
			wantSummary: "Error resetting notification target",
			wantDetail:  "Could not reset notification target, unexpected error: ",
		},
	}

	for _, tc := range deleteCases {
		t.Run("delete: "+tc.name, func(t *testing.T) {
			client, _ := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewNotificationTargetResource(), client)

			resp := h.delete(&notificationTargetResourceModel{
				TargetType: types.StringValue("notify_webhook"),
				TargetName: types.StringValue("tf-test"),
			})

			if tc.wantSummary == "" {
				requireNoDiagnostics(t, resp.Diagnostics)
				return
			}
			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			assertDiagnosticDetail(t, resp.Diagnostics, "notify module is disabled")
		})
	}

	createCases := []struct {
		name        string
		routes      []requestRoute
		plan        *notificationTargetResourceModel
		wantSummary string
		wantDetail  string
	}{
		{
			// The schema alone cannot express "exactly one matching block", so
			// a plan with no block at all is refused before any API call.
			name: "missing configuration block writes no state",
			plan: &notificationTargetResourceModel{
				TargetType: types.StringValue("notify_webhook"),
				TargetName: types.StringValue("tf-test"),
			},
			wantSummary: "Invalid notification target configuration",
			wantDetail:  "exactly one target configuration block may be set",
		},
		{
			name: "invalid key/value set is rejected by the server",
			routes: []requestRoute{{
				method: http.MethodPut, pathContains: "target/notify_webhook", status: http.StatusBadRequest,
				body: s3ErrorXML("InvalidArgument", "invalid key values: endpoint is required"),
			}},
			plan: &notificationTargetResourceModel{
				TargetType: types.StringValue("notify_webhook"),
				TargetName: types.StringValue("tf-test"),
				Webhook: &targetWebhookConfigModel{
					Endpoint: types.StringValue("https://hooks.example.com/webhook/tf-test"),
				},
			},
			wantSummary: "Error creating notification target",
			wantDetail:  "invalid key values: endpoint is required",
		},
	}

	for _, tc := range createCases {
		t.Run("create: "+tc.name, func(t *testing.T) {
			client, rec := routedClient(t, tc.routes...)
			h := newCRUDHarness(t, NewNotificationTargetResource(), client)

			resp := h.create(tc.plan)

			assertErrorDiagnostic(t, resp.Diagnostics, tc.wantSummary, tc.wantDetail)
			h.assertNoPartialState(resp, tc.plan)
			// A failed create must not go on to list and refresh health.
			rec.assertNoRequest(t, "target/list")
		})
	}

	t.Run("configure: rejects foreign provider data", func(t *testing.T) {
		h := newCRUDHarness(t, NewNotificationTargetResource(), cannedClient(t, http.StatusOK, ""))

		resp := h.configure("not-a-client")

		assertErrorDiagnostic(t, resp.Diagnostics, "Unexpected Resource Configure Type", "Expected *AllClient, got: string.")
	})
}
