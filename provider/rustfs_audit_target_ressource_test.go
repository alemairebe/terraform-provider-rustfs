package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/defaults"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const auditTargetResourceName = "rustfs_audit_target.test"

func TestAuditTargetSchema(t *testing.T) {
	r := NewAuditTargetRessource()
	resp := &frameworkresource.SchemaResponse{}
	r.Schema(context.Background(), frameworkresource.SchemaRequest{}, resp)

	if diags := resp.Diagnostics; diags.HasError() {
		t.Fatalf("schema diagnostics: %v", diags)
	}

	attrs := resp.Schema.GetAttributes()

	// The legacy flat attributes must keep working verbatim.
	for _, name := range []string{
		"target_type", "target_name", "endpoint", "auth_token", "comment",
		"queue_limit", "queue_dir", "client_cert", "client_key", "client_ca",
		"skip_tls_verify", "health_state", "health_reason", "status",
	} {
		if _, ok := attrs[name]; !ok {
			t.Errorf("missing legacy attribute %s", name)
		}
	}

	// enable is Optional+Computed with a true default.
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

	// The nine per-type configuration blocks must all be present.
	for _, name := range []string{
		"webhook_config", "amqp_config", "kafka_config", "mqtt_config", "nats_config",
		"postgres_config", "mysql_config", "redis_config", "pulsar_config",
	} {
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

func TestAuditTargetKeyValuesLegacy(t *testing.T) {
	r := &auditTargetRessource{}
	model := auditTargetRessourceModel{
		TargetType:    types.StringValue("audit_webhook"),
		TargetName:    types.StringValue("legacy"),
		Enable:        types.BoolValue(true),
		Endpoint:      types.StringValue("https://hooks.example.com/webhook/x"),
		AuthToken:     types.StringValue("superSecret"),
		Comment:       types.StringValue("managed by terraform"),
		QueueDir:      types.StringValue("/var/lib/rustfs/queue"),
		ClientCert:    types.StringValue("client-cert"),
		ClientKey:     types.StringValue("client-key"),
		ClientCA:      types.StringValue("client-ca"),
		QueueLimit:    types.Int64Value(100),
		SkipTLSVerify: types.BoolValue(true),
	}

	kvs, err := r.auditTargetKeyValues(&model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Regression pin: the legacy flat path must keep its exact ordering,
	// including the trailing enable pair the server already defaulted to on.
	want := []rustfs.TargetKeyValue{
		{Key: "endpoint", Value: "https://hooks.example.com/webhook/x"},
		{Key: "auth_token", Value: "superSecret"},
		{Key: "comment", Value: "managed by terraform"},
		{Key: "queue_dir", Value: "/var/lib/rustfs/queue"},
		{Key: "client_cert", Value: "client-cert"},
		{Key: "client_key", Value: "client-key"},
		{Key: "client_ca", Value: "client-ca"},
		{Key: "queue_limit", Value: "100"},
		{Key: "skip_tls_verify", Value: "true"},
		{Key: "enable", Value: "on"},
	}

	if len(kvs) != len(want) {
		t.Fatalf("expected %d key/value pairs, got %d: %+v", len(want), len(kvs), kvs)
	}
	for i := range want {
		if kvs[i] != want[i] {
			t.Errorf("pair %d: got %+v, want %+v", i, kvs[i], want[i])
		}
	}
}

func TestAuditTargetKeyValuesTyped(t *testing.T) {
	r := &auditTargetRessource{}
	model := auditTargetRessourceModel{
		TargetType: types.StringValue("audit_redis"),
		TargetName: types.StringValue("typed"),
		Enable:     types.BoolValue(true),
		Comment:    types.StringValue("typed comment"),
		Redis: &targetRedisConfigModel{
			URL:     types.StringValue("redis://127.0.0.1:6379/"),
			Channel: types.StringValue("tf-audit"),
		},
	}

	kvs, err := r.auditTargetKeyValues(&model)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []rustfs.TargetKeyValue{
		{Key: "url", Value: "redis://127.0.0.1:6379/"},
		{Key: "channel", Value: "tf-audit"},
		{Key: "comment", Value: "typed comment"},
		{Key: "enable", Value: "on"},
	}

	if len(kvs) != len(want) {
		t.Fatalf("expected %d key/value pairs, got %d: %+v", len(want), len(kvs), kvs)
	}
	for i := range want {
		if kvs[i] != want[i] {
			t.Errorf("pair %d: got %+v, want %+v", i, kvs[i], want[i])
		}
	}
}

func TestAuditTargetValidate(t *testing.T) {
	tests := []struct {
		name    string
		model   auditTargetRessourceModel
		wantErr string
	}{
		{
			name: "block for wrong type",
			model: auditTargetRessourceModel{
				TargetType: types.StringValue("audit_amqp"),
				Redis: &targetRedisConfigModel{
					URL: types.StringValue("redis://127.0.0.1:6379/"),
				},
			},
			wantErr: `target type "audit_amqp" requires the amqp_config block`,
		},
		{
			name: "no block with non-webhook type",
			model: auditTargetRessourceModel{
				TargetType: types.StringValue("audit_amqp"),
			},
			wantErr: `target type "audit_amqp" requires the amqp_config block`,
		},
		{
			name: "no block with webhook type is ok",
			model: auditTargetRessourceModel{
				TargetType: types.StringValue("audit_webhook"),
				Endpoint:   types.StringValue("https://hooks.example.com/webhook/x"),
			},
		},
		{
			name: "flat webhook attributes combined with block",
			model: auditTargetRessourceModel{
				TargetType: types.StringValue("audit_webhook"),
				Endpoint:   types.StringValue("https://hooks.example.com/webhook/x"),
				Webhook: &targetWebhookConfigModel{
					Endpoint: types.StringValue("https://hooks.example.com/webhook/x"),
				},
			},
			wantErr: "the flat webhook attributes cannot be combined with a configuration block",
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
			if err.Error() != tc.wantErr {
				t.Errorf("expected error %q, got %q", tc.wantErr, err.Error())
			}
		})
	}
}

func TestAccAuditTargetResource(t *testing.T) {
	name := fmt.Sprintf("tf-audit-%d", acctest.RandInt())
	endpoint := fmt.Sprintf("https://hooks.example.com/webhook/%s", name)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccAuditTargetPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAuditTargetDestroy,
		Steps: []resource.TestStep{
			{
				Config: testAccAuditTargetConfig(name, endpoint),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckAuditTargetExists(name),
					resource.TestCheckResourceAttr(auditTargetResourceName, "target_type", "audit_webhook"),
					resource.TestCheckResourceAttr(auditTargetResourceName, "target_name", name),
					resource.TestCheckResourceAttr(auditTargetResourceName, "endpoint", endpoint),
					resource.TestCheckResourceAttr(auditTargetResourceName, "auth_token", "superSecret"),
					resource.TestCheckResourceAttr(auditTargetResourceName, "comment", "managed by terraform"),
					resource.TestCheckResourceAttrSet(auditTargetResourceName, "health_state"),
				),
			},
			{
				Config: testAccAuditTargetConfigUpdated(name, endpoint),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckAuditTargetExists(name),
					resource.TestCheckResourceAttr(auditTargetResourceName, "comment", "updated by terraform"),
					resource.TestCheckResourceAttrSet(auditTargetResourceName, "health_state"),
				),
			},
			{
				ResourceName:      auditTargetResourceName,
				ImportState:       true,
				ImportStateId:     "audit_webhook/" + name,
				ImportStateVerify: true,
				// The admin list endpoint does not echo config values, so these
				// attributes cannot be verified after import.
				ImportStateVerifyIdentifierAttribute: "target_name",
				ImportStateVerifyIgnore:              []string{"enable", "endpoint", "auth_token", "comment", "queue_limit", "queue_dir", "client_cert", "client_key", "client_ca", "skip_tls_verify", "health_state", "health_reason", "status"},
			},
		},
	})
}

// testAccAuditTargetPreCheck verifies the environment and that the running
// RustFS has the audit module enabled (the admin API rejects target management
// otherwise). The list endpoint succeeds even when the module is disabled, so
// the check probes an actual create and skips when the server reports the
// audit module is disabled.
func testAccAuditTargetPreCheck(t *testing.T) {
	testAccPreCheck(t)

	client := testAccRustClient()
	probe := fmt.Sprintf("tf-audit-probe-%d", acctest.RandInt())
	probeConfig := []rustfs.TargetKeyValue{
		{Key: "endpoint", Value: "https://hooks.example.com/webhook/" + probe},
	}
	err := client.SetAuditTarget("audit_webhook", probe, probeConfig)
	if err != nil {
		if strings.Contains(err.Error(), "audit module is disabled") {
			t.Skipf("audit module is disabled on the running RustFS; skipping: %v", err)
		}
		t.Fatalf("audit target management is not available on the running RustFS, skipping: %v", err)
	}
	// Clean up the probe target so it does not linger in the server config.
	if err := client.ResetAuditTarget("audit_webhook", probe); err != nil {
		t.Logf("could not clean up audit target probe %s: %v", probe, err)
	}
}

func testAccAuditTargetConfig(name, endpoint string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_audit_target" "test" {
  target_type = "audit_webhook"
  target_name = "%s"
  endpoint    = "%s"
  auth_token  = "superSecret"
  comment     = "managed by terraform"
}
`, name, endpoint)
}

func testAccAuditTargetConfigUpdated(name, endpoint string) string {
	return testAccProviderConfig() + fmt.Sprintf(`
resource "rustfs_audit_target" "test" {
  target_type = "audit_webhook"
  target_name = "%s"
  endpoint    = "%s"
  auth_token  = "superSecret"
  comment     = "updated by terraform"
}
`, name, endpoint)
}

func testAccCheckAuditTargetExists(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[auditTargetResourceName]
		if !ok {
			return fmt.Errorf("not found: %s", auditTargetResourceName)
		}
		targetName := rs.Primary.Attributes["target_name"]
		if targetName == "" {
			return fmt.Errorf("no target_name set")
		}

		client := testAccRustClient()
		targets, err := client.ListAuditTargets()
		if err != nil {
			return err
		}
		for _, target := range targets {
			if target.AccountID == name && target.Service == "webhook" {
				return nil
			}
		}
		return fmt.Errorf("audit target %s not found in list", name)
	}
}

func testAccCheckAuditTargetDestroy(s *terraform.State) error {
	client := testAccRustClient()
	targets, err := client.ListAuditTargets()
	if err != nil {
		return err
	}
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "rustfs_audit_target" {
			continue
		}
		name := rs.Primary.Attributes["target_name"]
		for _, target := range targets {
			if strings.EqualFold(target.AccountID, name) {
				return fmt.Errorf("audit target %s still exists", name)
			}
		}
	}
	return nil
}
