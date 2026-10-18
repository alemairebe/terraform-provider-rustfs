package provider

import (
	"reflect"
	"strings"
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestTargetConfigKeyValues(t *testing.T) {
	cases := []struct {
		name       string
		targetType string
		enable     bool
		comment    string
		blocks     targetConfigBlocks
		want       []rustfs.TargetKeyValue
		wantErr    string
	}{
		{
			name:       "webhook",
			targetType: "webhook",
			enable:     true,
			blocks: targetConfigBlocks{Webhook: &targetWebhookConfigModel{
				Endpoint:      types.StringValue("https://h/w"),
				AuthToken:     types.StringValue("t"),
				QueueLimit:    types.Int64Value(1000),
				SkipTLSVerify: types.BoolValue(true),
			}},
			want: []rustfs.TargetKeyValue{
				{Key: "endpoint", Value: "https://h/w"},
				{Key: "auth_token", Value: "t"},
				{Key: "queue_limit", Value: "1000"},
				{Key: "skip_tls_verify", Value: "true"},
				{Key: "enable", Value: "on"},
			},
		},
		{
			name:       "amqp",
			targetType: "amqp",
			enable:     true,
			blocks: targetConfigBlocks{Amqp: &targetAmqpConfigModel{
				URL:        types.StringValue("amqps://mq:5671/"),
				Exchange:   types.StringValue("ex"),
				RoutingKey: types.StringValue("rk"),
				Username:   types.StringValue("u"),
				Password:   types.StringValue("p"),
				Persistent: types.BoolValue(false),
			}},
			want: []rustfs.TargetKeyValue{
				{Key: "url", Value: "amqps://mq:5671/"},
				{Key: "exchange", Value: "ex"},
				{Key: "routing_key", Value: "rk"},
				{Key: "persistent", Value: "false"},
				{Key: "username", Value: "u"},
				{Key: "password", Value: "p"},
				{Key: "enable", Value: "on"},
			},
		},
		{
			name:       "redis",
			targetType: "redis",
			enable:     true,
			blocks: targetConfigBlocks{Redis: &targetRedisConfigModel{
				URL:                types.StringValue("redis://r:6379/"),
				Channel:            types.StringValue("events"),
				TLSPolicy:          types.StringValue("custom_ca"),
				PipelineBufferSize: types.Int64Value(5000),
			}},
			want: []rustfs.TargetKeyValue{
				{Key: "url", Value: "redis://r:6379/"},
				{Key: "channel", Value: "events"},
				{Key: "pipeline_buffer_size", Value: "5000"},
				{Key: "tls_policy", Value: "custom_ca"},
				{Key: "enable", Value: "on"},
			},
		},
		{
			name:       "enable off",
			targetType: "amqp",
			enable:     false,
			blocks: targetConfigBlocks{Amqp: &targetAmqpConfigModel{
				URL: types.StringValue("amqps://mq:5671/"),
			}},
			want: []rustfs.TargetKeyValue{
				{Key: "url", Value: "amqps://mq:5671/"},
				{Key: "enable", Value: "off"},
			},
		},
		{
			name:       "integer encoding",
			targetType: "webhook",
			enable:     true,
			blocks: targetConfigBlocks{Webhook: &targetWebhookConfigModel{
				Endpoint:      types.StringValue("https://h/w"),
				QueueLimit:    types.Int64Value(1000),
				SkipTLSVerify: types.BoolValue(false),
			}},
			want: []rustfs.TargetKeyValue{
				{Key: "endpoint", Value: "https://h/w"},
				{Key: "queue_limit", Value: "1000"},
				{Key: "skip_tls_verify", Value: "false"},
				{Key: "enable", Value: "on"},
			},
		},
		{
			name:       "comment emitted before enable",
			targetType: "amqp",
			enable:     true,
			comment:    "my target",
			blocks: targetConfigBlocks{Amqp: &targetAmqpConfigModel{
				URL: types.StringValue("amqps://mq:5671/"),
			}},
			want: []rustfs.TargetKeyValue{
				{Key: "url", Value: "amqps://mq:5671/"},
				{Key: "comment", Value: "my target"},
				{Key: "enable", Value: "on"},
			},
		},
		{
			name:       "audit prefix",
			targetType: "audit_mqtt",
			enable:     true,
			blocks: targetConfigBlocks{Mqtt: &targetMqttConfigModel{
				Broker: types.StringValue("mqtt://broker:1883"),
				Topic:  types.StringValue("events"),
			}},
			want: []rustfs.TargetKeyValue{
				{Key: "broker", Value: "mqtt://broker:1883"},
				{Key: "topic", Value: "events"},
				{Key: "enable", Value: "on"},
			},
		},
		{
			name:       "no block",
			targetType: "amqp",
			blocks:     targetConfigBlocks{},
			wantErr:    "target configuration block",
		},
		{
			name:       "two blocks",
			targetType: "amqp",
			blocks: targetConfigBlocks{
				Amqp:  &targetAmqpConfigModel{URL: types.StringValue("amqps://mq:5671/")},
				Redis: &targetRedisConfigModel{URL: types.StringValue("redis://r:6379/")},
			},
			wantErr: "exactly one",
		},
		{
			name:       "mismatch",
			targetType: "amqp",
			blocks: targetConfigBlocks{
				Redis: &targetRedisConfigModel{URL: types.StringValue("redis://r:6379/")},
			},
			wantErr: "amqp_config",
		},
		{
			name:       "empty block",
			targetType: "amqp",
			blocks:     targetConfigBlocks{Amqp: &targetAmqpConfigModel{}},
			wantErr:    "amqp_config",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.blocks.keyValues(tc.targetType, tc.enable, tc.comment)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				if got != nil {
					t.Fatalf("expected nil key values on error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("unexpected key values:\n got: %v\nwant: %v", got, tc.want)
			}
		})
	}
}

func TestTargetConfigValidate(t *testing.T) {
	cases := []struct {
		name       string
		targetType string
		blocks     targetConfigBlocks
		wantErr    string
	}{
		{
			name:       "zero blocks",
			targetType: "amqp",
			blocks:     targetConfigBlocks{},
			wantErr:    "target configuration block",
		},
		{
			name:       "two blocks",
			targetType: "amqp",
			blocks: targetConfigBlocks{
				Amqp:  &targetAmqpConfigModel{URL: types.StringValue("amqps://mq:5671/")},
				Redis: &targetRedisConfigModel{URL: types.StringValue("redis://r:6379/")},
			},
			wantErr: "exactly one",
		},
		{
			name:       "mismatched block",
			targetType: "amqp",
			blocks:     targetConfigBlocks{Redis: &targetRedisConfigModel{URL: types.StringValue("redis://r:6379/")}},
			wantErr:    "amqp_config",
		},
		{
			name:       "empty block",
			targetType: "amqp",
			blocks:     targetConfigBlocks{Amqp: &targetAmqpConfigModel{}},
			wantErr:    "amqp_config",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.blocks.validate(tc.targetType)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}

	// Success path: exactly one matching, non-empty block validates cleanly.
	blocks := targetConfigBlocks{Amqp: &targetAmqpConfigModel{URL: types.StringValue("amqps://mq:5671/")}}
	if err := blocks.validate("amqp"); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// A block whose only attribute is unknown is not empty: the value is not
	// resolvable yet, so it must not be rejected at plan time.
	unknownOnly := targetConfigBlocks{Amqp: &targetAmqpConfigModel{URL: types.StringUnknown()}}
	if err := unknownOnly.validate("amqp"); err != nil {
		t.Fatalf("expected nil error for unknown-only block, got %v", err)
	}
}

func TestTargetConfigSchema(t *testing.T) {
	attrs := targetConfigBlockSchema()

	wantBlocks := []string{
		"webhook_config", "amqp_config", "kafka_config", "mqtt_config", "nats_config",
		"postgres_config", "mysql_config", "redis_config", "pulsar_config",
	}
	if len(attrs) != len(wantBlocks) {
		t.Fatalf("expected %d blocks, got %d", len(wantBlocks), len(attrs))
	}
	for _, name := range wantBlocks {
		attr, ok := attrs[name]
		if !ok {
			t.Fatalf("missing block %s", name)
		}
		if _, ok := attr.(schema.SingleNestedAttribute); !ok {
			t.Fatalf("%s is not a SingleNestedAttribute", name)
		}
	}

	sensitive := []struct{ block, field string }{
		{"webhook_config", "auth_token"},
		{"webhook_config", "client_key"},
		{"amqp_config", "password"},
		{"amqp_config", "tls_client_key"},
		{"kafka_config", "sasl_password"},
		{"kafka_config", "tls_client_key"},
		{"mqtt_config", "password"},
		{"mqtt_config", "tls_client_key"},
		{"nats_config", "password"},
		{"nats_config", "token"},
		{"nats_config", "tls_client_key"},
		{"postgres_config", "dsn_string"},
		{"postgres_config", "tls_client_key"},
		{"mysql_config", "dsn_string"},
		{"mysql_config", "tls_client_key"},
		{"redis_config", "password"},
		{"redis_config", "tls_client_key"},
		{"pulsar_config", "auth_token"},
		{"pulsar_config", "password"},
	}
	for _, s := range sensitive {
		if attr := nestedStringAttr(t, attrs, s.block, s.field); !attr.Sensitive {
			t.Errorf("%s.%s should be sensitive", s.block, s.field)
		}
	}

	notSensitive := []struct{ block, field string }{
		{"webhook_config", "client_cert"},
		{"webhook_config", "client_ca"},
		{"amqp_config", "tls_ca"},
		{"amqp_config", "tls_client_cert"},
		{"kafka_config", "tls_client_cert"},
	}
	for _, s := range notSensitive {
		if attr := nestedStringAttr(t, attrs, s.block, s.field); attr.Sensitive {
			t.Errorf("%s.%s should not be sensitive", s.block, s.field)
		}
	}

	enumFields := []struct{ block, field string }{
		{"kafka_config", "acks"},
		{"postgres_config", "format"},
		{"mqtt_config", "tls_policy"},
		{"redis_config", "tls_policy"},
	}
	for _, s := range enumFields {
		if attr := nestedStringAttr(t, attrs, s.block, s.field); len(attr.Validators) != 1 {
			t.Errorf("%s.%s should have exactly one enum validator, got %d", s.block, s.field, len(attr.Validators))
		}
	}

	// mysql.format is free-form (no enum validator), unlike postgres.format.
	if attr := nestedStringAttr(t, attrs, "mysql_config", "format"); len(attr.Validators) != 0 {
		t.Errorf("mysql_config.format should have no validator, got %d", len(attr.Validators))
	}
}

func nestedStringAttr(t *testing.T, attrs map[string]schema.Attribute, block, field string) schema.StringAttribute {
	t.Helper()
	blockAttr, ok := attrs[block].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatalf("%s is not a SingleNestedAttribute", block)
	}
	fieldAttr, ok := blockAttr.Attributes[field].(schema.StringAttribute)
	if !ok {
		t.Fatalf("%s.%s is not a StringAttribute", block, field)
	}
	return fieldAttr
}
