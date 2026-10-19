// Copyright (c) 2026 The terraform-provider-rustfs contributors
// SPDX-License-Identifier: Apache-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/defaults"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
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
	replacementName := fmt.Sprintf("tf-notify-%d", acctest.RandInt())
	endpoint := os.Getenv("RUSTFS_NOTIFY_TEST_ENDPOINT")
	updatedEndpoint := endpoint + "-updated"

	resource.ParallelTest(t, resource.TestCase{
		PreCheck:                 func() { testAccNotificationTargetPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckNotificationTargetDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccNotificationTargetConfig(name, endpoint, "managed by terraform"),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckNotificationTargetExists(name),
					resource.TestCheckResourceAttr(notificationTargetResourceName, "target_type", "notify_webhook"),
					resource.TestCheckResourceAttr(notificationTargetResourceName, "target_name", name),
					resource.TestCheckResourceAttr(notificationTargetResourceName, "webhook_config.endpoint", endpoint),
					resource.TestCheckResourceAttr(notificationTargetResourceName, "comment", "managed by terraform"),
					resource.TestCheckResourceAttr(notificationTargetResourceName, "enable", "true"),
					resource.TestCheckResourceAttrSet(notificationTargetResourceName, "health_state"),
					resource.TestCheckResourceAttrSet(notificationTargetResourceName, "status"),
					// The list endpoint does not echo the target configuration,
					// so the create is confirmed against get-config-kv, which
					// renders what the server actually stored.
					testAccCheckNotificationTargetConfigValue(name, "endpoint", endpoint),
				),
			},
			{
				// In-place change: the webhook endpoint and the comment are
				// mutable, so the plan is an update (never a replacement) and
				// the server has to hand the new endpoint back.
				Config: testAccNotificationTargetConfig(name, updatedEndpoint, "updated by terraform"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(notificationTargetResourceName, plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckNotificationTargetExists(name),
					resource.TestCheckResourceAttr(notificationTargetResourceName, "webhook_config.endpoint", updatedEndpoint),
					resource.TestCheckResourceAttr(notificationTargetResourceName, "comment", "updated by terraform"),
					resource.TestCheckResourceAttrSet(notificationTargetResourceName, "health_state"),
					resource.TestCheckResourceAttrSet(notificationTargetResourceName, "status"),
					testAccCheckNotificationTargetConfigValue(name, "endpoint", updatedEndpoint),
				),
			},
			{
				// Revert the update; the server has to end up on the original
				// endpoint again, which the config read-back proves.
				Config: testAccNotificationTargetConfig(name, endpoint, "managed by terraform"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(notificationTargetResourceName, plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckNotificationTargetExists(name),
					resource.TestCheckResourceAttr(notificationTargetResourceName, "webhook_config.endpoint", endpoint),
					resource.TestCheckResourceAttr(notificationTargetResourceName, "comment", "managed by terraform"),
					testAccCheckNotificationTargetConfigValue(name, "endpoint", endpoint),
				),
			},
			{
				// Explicit empty second plan: the state the revert wrote is the
				// configuration, so the plan made from that state proposes no
				// change. This is what catches a computed attribute (health
				// state, status) that the read path drops.
				Config:             testAccNotificationTargetConfig(name, endpoint, "managed by terraform"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// target_name carries RequiresReplace, so renaming the target
				// has to be planned as a replacement. The step only plans: the
				// replacement is asserted here and applied further down.
				Config:             testAccNotificationTargetConfig(replacementName, endpoint, "managed by terraform"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(notificationTargetResourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			{
				// target_type carries RequiresReplace as well, so switching the
				// subsystem (with its matching configuration block) replaces the
				// target too.
				Config:             testAccNotificationTargetRedisConfig(name, "managed by terraform"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(notificationTargetResourceName, plancheck.ResourceActionReplace),
					},
				},
			},
			{
				// Apply the target_name replacement so the destroy-and-recreate
				// path really runs, and check the old target is gone while the
				// new one is on the server with the configuration intact.
				Config: testAccNotificationTargetConfig(replacementName, endpoint, "managed by terraform"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(notificationTargetResourceName, plancheck.ResourceActionReplace),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckNotificationTargetExists(replacementName),
					testAccCheckNotificationTargetGone(name),
					resource.TestCheckResourceAttr(notificationTargetResourceName, "target_name", replacementName),
					resource.TestCheckResourceAttr(notificationTargetResourceName, "webhook_config.endpoint", endpoint),
					testAccCheckNotificationTargetConfigValue(replacementName, "endpoint", endpoint),
				),
			},
			{
				// Out-of-band drift: the Check function resets the target
				// straight through the admin API after the (no-op) apply, so the
				// plan made before the refresh is still empty while the plan
				// Terraform proposes after the refresh has to re-create it.
				Config:             testAccNotificationTargetConfig(replacementName, endpoint, "managed by terraform"),
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					PostApplyPostRefresh: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(notificationTargetResourceName, plancheck.ResourceActionCreate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckNotificationTargetExists(replacementName),
					testAccNotificationTargetResetOutOfBand(replacementName),
				),
			},
			{
				// Apply the drift the previous step discovered and assert the
				// re-created target is on the server again with its config.
				Config: testAccNotificationTargetConfig(replacementName, endpoint, "managed by terraform"),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckNotificationTargetExists(replacementName),
					resource.TestCheckResourceAttr(notificationTargetResourceName, "target_name", replacementName),
					testAccCheckNotificationTargetConfigValue(replacementName, "endpoint", endpoint),
				),
			},
			{
				ResourceName:      notificationTargetResourceName,
				ImportState:       true,
				ImportStateId:     "notify_webhook/" + replacementName,
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
			{
				// Import accepts the composite <target_type>/<target_name> ID
				// only. An ID without the separator has to produce the explicit
				// diagnostic rather than a panic or a half-filled state.
				ResourceName:  notificationTargetResourceName,
				ImportState:   true,
				ImportStateId: "no-separator-here",
				ExpectError:   regexp.MustCompile(`(?s)Invalid import ID.*Expected the import ID to be <target_type>/<target_name>`),
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

func testAccNotificationTargetConfig(name, endpoint, comment string) string {
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

// testAccNotificationTargetRedisConfig is the same notification target under a
// different target_type with the matching configuration block. Only the
// plan-only replacement check uses it, so no redis target is ever applied.
func testAccNotificationTargetRedisConfig(name, comment string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_notification_target" "test" {
  target_type = "notify_redis"
  target_name = "%s"

  redis_config = {
    url     = "redis://127.0.0.1:6379/0"
    channel = "tf-notify"
  }

  comment = "%s"
}
`, name, comment)
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

// testAccCheckNotificationTargetConfigValue asserts a configuration key of the
// target as the server renders it back through get-config-kv
// ("notify_webhook:<name>"). The admin list endpoint only reports identity and
// health, so this is the only server-side read-back of what an update wrote.
func testAccCheckNotificationTargetConfigValue(name, key, want string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		kvs, err := client.GetConfig("notify_webhook:" + name)
		if err != nil {
			return fmt.Errorf("failed to read notification target %s config from the server: %w", name, err)
		}
		for _, kv := range kvs {
			if kv.Key == key {
				if kv.Value != want {
					return fmt.Errorf("server reports notification target %s %s = %q, want %q", name, key, kv.Value, want)
				}
				return nil
			}
		}
		return fmt.Errorf("server does not report key %q for notification target %s: %+v", key, name, kvs)
	}
}

// testAccCheckNotificationTargetGone asserts the named notification target is
// not configured on the server any more, which is how a replacement proves the
// old instance was destroyed.
func testAccCheckNotificationTargetGone(name string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		targets, err := client.ListNotificationTargets()
		if err != nil {
			return err
		}
		for _, target := range targets {
			if strings.EqualFold(target.AccountID, name) {
				return fmt.Errorf("notification target %s still exists after the replacement", name)
			}
		}
		return nil
	}
}

// testAccNotificationTargetResetOutOfBand removes the target through the admin
// API behind Terraform's back, so the following refresh notices the drift.
func testAccNotificationTargetResetOutOfBand(name string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		client := testAccRustClient()
		if err := client.ResetNotificationTarget("notify_webhook", name); err != nil {
			return fmt.Errorf("failed to reset notification target %s out of band: %w", name, err)
		}
		return nil
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
