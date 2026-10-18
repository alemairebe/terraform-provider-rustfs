package provider

import (
	"testing"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// FuzzTargetConfig fuzzes the mapping from the typed *_config blocks onto the
// ordered key/value table the audit and notification target admin APIs expect.
// targetType and the block set are both fuzzed, so the mutually-exclusive-block
// validation runs on arbitrary combinations and the value/number/flag inputs
// drive every block's attribute encoding.
func FuzzTargetConfig(f *testing.F) {
	seeds := []struct {
		targetType string
		kind       uint8
		value      string
		number     int64
		flag       bool
		comment    string
	}{
		{"webhook", 1, "https://example.com/hook", 1000, true, "primary"},
		{"notify_webhook", 1, "https://example.com/hook", 0, false, ""},
		{"audit_amqp", 2, "amqps://mq:5671/", 5000, true, ""},
		{"kafka", 3, "broker-1:9092", -1, false, "kafka"},
		{"mqtt", 4, "tcp://mq:1883", 1, true, ""},
		{"nats", 5, "nats://nats:4222", 0, false, ""},
		{"postgres", 6, "postgres://user@host/db", 100, true, ""},
		{"mysql", 7, "user@tcp(host:3306)/db", 100, false, ""},
		{"redis", 8, "redis://redis:6379/", 0, true, ""},
		{"pulsar", 9, "pulsar://pulsar:6650", 0, false, ""},
		{"webhook", 0, "", 0, false, ""},                       // no block at all
		{"webhook", 10, "https://example.com", 0, false, ""},   // two blocks at once
		{"redis", 1, "https://example.com/hook", 0, false, ""}, // mismatched block
		{"", 1, "value", 0, false, ""},
		{"notify_", 1, "\x00\xff\n\t", -9223372036854775808, true, "c"},
	}
	for _, seed := range seeds {
		f.Add(seed.targetType, seed.kind, seed.value, seed.number, seed.flag, seed.comment)
	}

	f.Fuzz(func(t *testing.T, targetType string, kind uint8, value string, number int64, flag bool, comment string) {
		blocks, blockName := fuzzTargetConfigBlocks(kind, value, number, flag)

		kvs, err := blocks.keyValues(targetType, flag, comment)
		if err != nil {
			if kvs != nil {
				t.Fatalf("keyValues returned %d pairs alongside the error %v", len(kvs), err)
			}
			if blocks.validate(targetType) == nil {
				t.Fatalf("keyValues rejected target type %q but validate accepted it (error %v)", targetType, err)
			}
			if blockName != "" && blockName == targetConfigBlockName(targetType) {
				t.Fatalf("the required %s block is set for target type %q but was rejected: %v", blockName, targetType, err)
			}
			return
		}

		if err := blocks.validate(targetType); err != nil {
			t.Fatalf("keyValues accepted target type %q but validate rejected it: %v", targetType, err)
		}

		// The rendered table always ends with the enable pair; the optional
		// comment pair is emitted directly before it.
		if len(kvs) == 0 {
			t.Fatal("successful keyValues returned no pairs")
		}
		wantEnable := "off"
		if flag {
			wantEnable = "on"
		}
		if last := kvs[len(kvs)-1]; last != (rustfs.TargetKeyValue{Key: "enable", Value: wantEnable}) {
			t.Fatalf("last pair = %+v, want enable=%s", last, wantEnable)
		}
		if comment != "" {
			if len(kvs) < 2 {
				t.Fatalf("comment %q was dropped from %+v", comment, kvs)
			}
			if commentPair := kvs[len(kvs)-2]; commentPair != (rustfs.TargetKeyValue{Key: "comment", Value: comment}) {
				t.Fatalf("pair before enable = %+v, want comment=%q", commentPair, comment)
			}
		}
		for _, kv := range kvs {
			if kv.Key == "" {
				t.Fatalf("pair %+v has an empty key", kv)
			}
		}
	})
}

// fuzzTargetConfigBlocks returns a block set selected by kind together with the
// name of the single block it sets ("" when it sets none or two at once), so a
// fuzz run covers the whole block set instead of one block. The values are
// always known, never null, so the selected block is never empty.
func fuzzTargetConfigBlocks(kind uint8, value string, number int64, flag bool) (targetConfigBlocks, string) {
	s := types.StringValue(value)
	i := types.Int64Value(number)
	b := types.BoolValue(flag)

	switch kind % 11 {
	case 0:
		return targetConfigBlocks{}, ""
	case 1:
		return targetConfigBlocks{Webhook: &targetWebhookConfigModel{Endpoint: s, QueueLimit: i, SkipTLSVerify: b}}, "webhook_config"
	case 2:
		return targetConfigBlocks{Amqp: &targetAmqpConfigModel{URL: s, QueueLimit: i, Persistent: b}}, "amqp_config"
	case 3:
		return targetConfigBlocks{Kafka: &targetKafkaConfigModel{Brokers: s, QueueLimit: i, TLSEnable: b}}, "kafka_config"
	case 4:
		return targetConfigBlocks{Mqtt: &targetMqttConfigModel{Broker: s, Qos: i, TLSTrustLeafAsCA: b}}, "mqtt_config"
	case 5:
		return targetConfigBlocks{Nats: &targetNatsConfigModel{Address: s, QueueLimit: i, TLSRequired: b}}, "nats_config"
	case 6:
		return targetConfigBlocks{Postgres: &targetPostgresConfigModel{DSNString: s, QueueLimit: i, TLSRequired: b}}, "postgres_config"
	case 7:
		return targetConfigBlocks{Mysql: &targetMysqlConfigModel{DSNString: s, QueueLimit: i, MaxOpenConnections: i}}, "mysql_config"
	case 8:
		return targetConfigBlocks{Redis: &targetRedisConfigModel{URL: s, KeepAliveInterval: i, TLSAllowInsecure: b}}, "redis_config"
	case 9:
		return targetConfigBlocks{Pulsar: &targetPulsarConfigModel{Broker: s, QueueLimit: i, TLSAllowInsecure: b}}, "pulsar_config"
	default:
		return targetConfigBlocks{
			Webhook: &targetWebhookConfigModel{Endpoint: s},
			Redis:   &targetRedisConfigModel{URL: s},
		}, ""
	}
}
