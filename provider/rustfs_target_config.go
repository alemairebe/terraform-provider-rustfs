package provider

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/caberdo/terraform-provider-rustfs/pkg/rustfs"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// targetConfigBlockName maps a target type spelling to its configuration
// block name. Audit targets use the "audit_<service>" subsystem spelling,
// notification targets use the bare "<service>" spelling and the admin API
// also accepts "notify_<service>"; all normalize to "<service>_config".
func targetConfigBlockName(targetType string) string {
	name := strings.TrimPrefix(targetType, "audit_")
	name = strings.TrimPrefix(name, "notify_")
	return name + "_config"
}

// targetServiceName maps a target type spelling to the bare service name the
// admin list endpoint reports in its "service" field. A subsystem spelling
// ("audit_webhook", "notify_amqp") normalizes to its bare service ("webhook",
// "amqp"); an already-bare value is returned unchanged.
func targetServiceName(targetType string) string {
	name := strings.TrimPrefix(targetType, "audit_")
	name = strings.TrimPrefix(name, "notify_")
	return name
}

// targetConfigBlocks holds the optional per-type configuration blocks. Exactly
// one block must be set, and it must match the target type.
type targetConfigBlocks struct {
	Webhook  *targetWebhookConfigModel
	Amqp     *targetAmqpConfigModel
	Kafka    *targetKafkaConfigModel
	Mqtt     *targetMqttConfigModel
	Nats     *targetNatsConfigModel
	Postgres *targetPostgresConfigModel
	Mysql    *targetMysqlConfigModel
	Redis    *targetRedisConfigModel
	Pulsar   *targetPulsarConfigModel
}

type targetWebhookConfigModel struct {
	Endpoint      types.String `tfsdk:"endpoint"`
	AuthToken     types.String `tfsdk:"auth_token"`
	QueueLimit    types.Int64  `tfsdk:"queue_limit"`
	QueueDir      types.String `tfsdk:"queue_dir"`
	ClientCert    types.String `tfsdk:"client_cert"`
	ClientKey     types.String `tfsdk:"client_key"`
	ClientCA      types.String `tfsdk:"client_ca"`
	SkipTLSVerify types.Bool   `tfsdk:"skip_tls_verify"`
}

type targetAmqpConfigModel struct {
	URL           types.String `tfsdk:"url"`
	Exchange      types.String `tfsdk:"exchange"`
	RoutingKey    types.String `tfsdk:"routing_key"`
	Mandatory     types.Bool   `tfsdk:"mandatory"`
	Persistent    types.Bool   `tfsdk:"persistent"`
	Username      types.String `tfsdk:"username"`
	Password      types.String `tfsdk:"password"`
	TLSCA         types.String `tfsdk:"tls_ca"`
	TLSClientCert types.String `tfsdk:"tls_client_cert"`
	TLSClientKey  types.String `tfsdk:"tls_client_key"`
	QueueDir      types.String `tfsdk:"queue_dir"`
	QueueLimit    types.Int64  `tfsdk:"queue_limit"`
}

type targetKafkaConfigModel struct {
	Brokers       types.String `tfsdk:"brokers"`
	Topic         types.String `tfsdk:"topic"`
	Acks          types.String `tfsdk:"acks"`
	TLSEnable     types.Bool   `tfsdk:"tls_enable"`
	TLSCA         types.String `tfsdk:"tls_ca"`
	TLSClientCert types.String `tfsdk:"tls_client_cert"`
	TLSClientKey  types.String `tfsdk:"tls_client_key"`
	SASLEnable    types.Bool   `tfsdk:"sasl_enable"`
	SASLMechanism types.String `tfsdk:"sasl_mechanism"`
	SASLUsername  types.String `tfsdk:"sasl_username"`
	SASLPassword  types.String `tfsdk:"sasl_password"`
	QueueDir      types.String `tfsdk:"queue_dir"`
	QueueLimit    types.Int64  `tfsdk:"queue_limit"`
}

type targetMqttConfigModel struct {
	Broker            types.String `tfsdk:"broker"`
	Topic             types.String `tfsdk:"topic"`
	Qos               types.Int64  `tfsdk:"qos"`
	Username          types.String `tfsdk:"username"`
	Password          types.String `tfsdk:"password"`
	ReconnectInterval types.Int64  `tfsdk:"reconnect_interval"`
	KeepAliveInterval types.Int64  `tfsdk:"keep_alive_interval"`
	QueueDir          types.String `tfsdk:"queue_dir"`
	QueueLimit        types.Int64  `tfsdk:"queue_limit"`
	TLSPolicy         types.String `tfsdk:"tls_policy"`
	TLSCA             types.String `tfsdk:"tls_ca"`
	TLSClientCert     types.String `tfsdk:"tls_client_cert"`
	TLSClientKey      types.String `tfsdk:"tls_client_key"`
	TLSTrustLeafAsCA  types.Bool   `tfsdk:"tls_trust_leaf_as_ca"`
	WSPathAllowlist   types.String `tfsdk:"ws_path_allowlist"`
}

type targetNatsConfigModel struct {
	Address                 types.String `tfsdk:"address"`
	Subject                 types.String `tfsdk:"subject"`
	Username                types.String `tfsdk:"username"`
	Password                types.String `tfsdk:"password"`
	Token                   types.String `tfsdk:"token"`
	CredentialsFile         types.String `tfsdk:"credentials_file"`
	TLSCA                   types.String `tfsdk:"tls_ca"`
	TLSClientCert           types.String `tfsdk:"tls_client_cert"`
	TLSClientKey            types.String `tfsdk:"tls_client_key"`
	TLSRequired             types.Bool   `tfsdk:"tls_required"`
	QueueDir                types.String `tfsdk:"queue_dir"`
	QueueLimit              types.Int64  `tfsdk:"queue_limit"`
	JetstreamEnable         types.Bool   `tfsdk:"jetstream_enable"`
	JetstreamStreamName     types.String `tfsdk:"jetstream_stream_name"`
	JetstreamAckTimeoutSecs types.Int64  `tfsdk:"jetstream_ack_timeout_secs"`
}

type targetPostgresConfigModel struct {
	DSNString     types.String `tfsdk:"dsn_string"`
	Table         types.String `tfsdk:"table"`
	Format        types.String `tfsdk:"format"`
	TLSRequired   types.Bool   `tfsdk:"tls_required"`
	TLSCA         types.String `tfsdk:"tls_ca"`
	TLSClientCert types.String `tfsdk:"tls_client_cert"`
	TLSClientKey  types.String `tfsdk:"tls_client_key"`
	QueueDir      types.String `tfsdk:"queue_dir"`
	QueueLimit    types.Int64  `tfsdk:"queue_limit"`
}

type targetMysqlConfigModel struct {
	DSNString          types.String `tfsdk:"dsn_string"`
	Table              types.String `tfsdk:"table"`
	Format             types.String `tfsdk:"format"`
	TLSCA              types.String `tfsdk:"tls_ca"`
	TLSClientCert      types.String `tfsdk:"tls_client_cert"`
	TLSClientKey       types.String `tfsdk:"tls_client_key"`
	QueueDir           types.String `tfsdk:"queue_dir"`
	QueueLimit         types.Int64  `tfsdk:"queue_limit"`
	MaxOpenConnections types.Int64  `tfsdk:"max_open_connections"`
}

type targetRedisConfigModel struct {
	URL                    types.String `tfsdk:"url"`
	Channel                types.String `tfsdk:"channel"`
	Username               types.String `tfsdk:"username"`
	Password               types.String `tfsdk:"password"`
	KeepAliveInterval      types.Int64  `tfsdk:"keep_alive_interval"`
	QueueDir               types.String `tfsdk:"queue_dir"`
	QueueLimit             types.Int64  `tfsdk:"queue_limit"`
	MaxRetryAttempts       types.Int64  `tfsdk:"max_retry_attempts"`
	ReconnectRetryAttempts types.Int64  `tfsdk:"reconnect_retry_attempts"`
	MinRetryDelay          types.Int64  `tfsdk:"min_retry_delay"`
	MaxRetryDelay          types.Int64  `tfsdk:"max_retry_delay"`
	ConnectionTimeout      types.Int64  `tfsdk:"connection_timeout"`
	ResponseTimeout        types.Int64  `tfsdk:"response_timeout"`
	PipelineBufferSize     types.Int64  `tfsdk:"pipeline_buffer_size"`
	TLSPolicy              types.String `tfsdk:"tls_policy"`
	TLSCA                  types.String `tfsdk:"tls_ca"`
	TLSClientCert          types.String `tfsdk:"tls_client_cert"`
	TLSClientKey           types.String `tfsdk:"tls_client_key"`
	TLSAllowInsecure       types.Bool   `tfsdk:"tls_allow_insecure"`
}

type targetPulsarConfigModel struct {
	Broker                  types.String `tfsdk:"broker"`
	Topic                   types.String `tfsdk:"topic"`
	AuthToken               types.String `tfsdk:"auth_token"`
	Username                types.String `tfsdk:"username"`
	Password                types.String `tfsdk:"password"`
	TLSCA                   types.String `tfsdk:"tls_ca"`
	TLSAllowInsecure        types.Bool   `tfsdk:"tls_allow_insecure"`
	TLSHostnameVerification types.Bool   `tfsdk:"tls_hostname_verification"`
	QueueDir                types.String `tfsdk:"queue_dir"`
	QueueLimit              types.Int64  `tfsdk:"queue_limit"`
}

// validate returns nil when exactly one block is set and it matches
// targetType, and a diagnostic-ready error otherwise.
func (b targetConfigBlocks) validate(targetType string) error {
	blockName := targetConfigBlockName(targetType)
	set := b.setBlocks()

	if len(set) != 1 {
		return fmt.Errorf("exactly one target configuration block may be set, got %s", strings.Join(set, ", "))
	}
	if set[0] != blockName {
		return fmt.Errorf("target type %q requires the %s block", targetType, blockName)
	}
	if len(b.blockKeyValues(set[0])) == 0 {
		return fmt.Errorf("%s must not be empty", blockName)
	}
	return nil
}

// keyValues maps the matching block to the ordered [{key,value}] pairs the
// admin API expects, appending the optional comment and the enable pair last.
func (b targetConfigBlocks) keyValues(targetType string, enable bool, comment string) ([]rustfs.TargetKeyValue, error) {
	if err := b.validate(targetType); err != nil {
		return nil, err
	}

	kvs := b.blockKeyValues(targetConfigBlockName(targetType))
	if comment != "" {
		kvs = append(kvs, rustfs.TargetKeyValue{Key: "comment", Value: comment})
	}
	enableValue := "off"
	if enable {
		enableValue = "on"
	}
	kvs = append(kvs, rustfs.TargetKeyValue{Key: "enable", Value: enableValue})
	return kvs, nil
}

// setBlocks returns the names of the non-nil configuration blocks in canonical
// order.
func (b targetConfigBlocks) setBlocks() []string {
	var set []string
	if b.Webhook != nil {
		set = append(set, "webhook_config")
	}
	if b.Amqp != nil {
		set = append(set, "amqp_config")
	}
	if b.Kafka != nil {
		set = append(set, "kafka_config")
	}
	if b.Mqtt != nil {
		set = append(set, "mqtt_config")
	}
	if b.Nats != nil {
		set = append(set, "nats_config")
	}
	if b.Postgres != nil {
		set = append(set, "postgres_config")
	}
	if b.Mysql != nil {
		set = append(set, "mysql_config")
	}
	if b.Redis != nil {
		set = append(set, "redis_config")
	}
	if b.Pulsar != nil {
		set = append(set, "pulsar_config")
	}
	return set
}

// blockKeyValues returns the ordered key/value pairs for the named block.
func (b targetConfigBlocks) blockKeyValues(blockName string) []rustfs.TargetKeyValue {
	switch blockName {
	case "webhook_config":
		return b.Webhook.keyValues()
	case "amqp_config":
		return b.Amqp.keyValues()
	case "kafka_config":
		return b.Kafka.keyValues()
	case "mqtt_config":
		return b.Mqtt.keyValues()
	case "nats_config":
		return b.Nats.keyValues()
	case "postgres_config":
		return b.Postgres.keyValues()
	case "mysql_config":
		return b.Mysql.keyValues()
	case "redis_config":
		return b.Redis.keyValues()
	case "pulsar_config":
		return b.Pulsar.keyValues()
	}
	return nil
}

// targetKeyValueBuilder accumulates key/value pairs in server table order.
type targetKeyValueBuilder struct {
	kvs []rustfs.TargetKeyValue
}

func (b *targetKeyValueBuilder) addString(key string, v types.String) {
	if v.IsNull() || v.IsUnknown() {
		return
	}
	b.kvs = append(b.kvs, rustfs.TargetKeyValue{Key: key, Value: v.ValueString()})
}

func (b *targetKeyValueBuilder) addBool(key string, v types.Bool) {
	if v.IsNull() || v.IsUnknown() {
		return
	}
	b.kvs = append(b.kvs, rustfs.TargetKeyValue{Key: key, Value: strconv.FormatBool(v.ValueBool())})
}

func (b *targetKeyValueBuilder) addInt64(key string, v types.Int64) {
	if v.IsNull() || v.IsUnknown() {
		return
	}
	b.kvs = append(b.kvs, rustfs.TargetKeyValue{Key: key, Value: strconv.FormatInt(v.ValueInt64(), 10)})
}

func (m *targetWebhookConfigModel) keyValues() []rustfs.TargetKeyValue {
	b := &targetKeyValueBuilder{}
	b.addString("endpoint", m.Endpoint)
	b.addString("auth_token", m.AuthToken)
	b.addInt64("queue_limit", m.QueueLimit)
	b.addString("queue_dir", m.QueueDir)
	b.addString("client_cert", m.ClientCert)
	b.addString("client_key", m.ClientKey)
	b.addString("client_ca", m.ClientCA)
	b.addBool("skip_tls_verify", m.SkipTLSVerify)
	return b.kvs
}

func (m *targetAmqpConfigModel) keyValues() []rustfs.TargetKeyValue {
	b := &targetKeyValueBuilder{}
	b.addString("url", m.URL)
	b.addString("exchange", m.Exchange)
	b.addString("routing_key", m.RoutingKey)
	b.addBool("mandatory", m.Mandatory)
	b.addBool("persistent", m.Persistent)
	b.addString("username", m.Username)
	b.addString("password", m.Password)
	b.addString("tls_ca", m.TLSCA)
	b.addString("tls_client_cert", m.TLSClientCert)
	b.addString("tls_client_key", m.TLSClientKey)
	b.addString("queue_dir", m.QueueDir)
	b.addInt64("queue_limit", m.QueueLimit)
	return b.kvs
}

func (m *targetKafkaConfigModel) keyValues() []rustfs.TargetKeyValue {
	b := &targetKeyValueBuilder{}
	b.addString("brokers", m.Brokers)
	b.addString("topic", m.Topic)
	b.addString("acks", m.Acks)
	b.addBool("tls_enable", m.TLSEnable)
	b.addString("tls_ca", m.TLSCA)
	b.addString("tls_client_cert", m.TLSClientCert)
	b.addString("tls_client_key", m.TLSClientKey)
	b.addBool("sasl_enable", m.SASLEnable)
	b.addString("sasl_mechanism", m.SASLMechanism)
	b.addString("sasl_username", m.SASLUsername)
	b.addString("sasl_password", m.SASLPassword)
	b.addString("queue_dir", m.QueueDir)
	b.addInt64("queue_limit", m.QueueLimit)
	return b.kvs
}

func (m *targetMqttConfigModel) keyValues() []rustfs.TargetKeyValue {
	b := &targetKeyValueBuilder{}
	b.addString("broker", m.Broker)
	b.addString("topic", m.Topic)
	b.addInt64("qos", m.Qos)
	b.addString("username", m.Username)
	b.addString("password", m.Password)
	b.addInt64("reconnect_interval", m.ReconnectInterval)
	b.addInt64("keep_alive_interval", m.KeepAliveInterval)
	b.addString("queue_dir", m.QueueDir)
	b.addInt64("queue_limit", m.QueueLimit)
	b.addString("tls_policy", m.TLSPolicy)
	b.addString("tls_ca", m.TLSCA)
	b.addString("tls_client_cert", m.TLSClientCert)
	b.addString("tls_client_key", m.TLSClientKey)
	b.addBool("tls_trust_leaf_as_ca", m.TLSTrustLeafAsCA)
	b.addString("ws_path_allowlist", m.WSPathAllowlist)
	return b.kvs
}

func (m *targetNatsConfigModel) keyValues() []rustfs.TargetKeyValue {
	b := &targetKeyValueBuilder{}
	b.addString("address", m.Address)
	b.addString("subject", m.Subject)
	b.addString("username", m.Username)
	b.addString("password", m.Password)
	b.addString("token", m.Token)
	b.addString("credentials_file", m.CredentialsFile)
	b.addString("tls_ca", m.TLSCA)
	b.addString("tls_client_cert", m.TLSClientCert)
	b.addString("tls_client_key", m.TLSClientKey)
	b.addBool("tls_required", m.TLSRequired)
	b.addString("queue_dir", m.QueueDir)
	b.addInt64("queue_limit", m.QueueLimit)
	b.addBool("jetstream_enable", m.JetstreamEnable)
	b.addString("jetstream_stream_name", m.JetstreamStreamName)
	b.addInt64("jetstream_ack_timeout_secs", m.JetstreamAckTimeoutSecs)
	return b.kvs
}

func (m *targetPostgresConfigModel) keyValues() []rustfs.TargetKeyValue {
	b := &targetKeyValueBuilder{}
	b.addString("dsn_string", m.DSNString)
	b.addString("table", m.Table)
	b.addString("format", m.Format)
	b.addBool("tls_required", m.TLSRequired)
	b.addString("tls_ca", m.TLSCA)
	b.addString("tls_client_cert", m.TLSClientCert)
	b.addString("tls_client_key", m.TLSClientKey)
	b.addString("queue_dir", m.QueueDir)
	b.addInt64("queue_limit", m.QueueLimit)
	return b.kvs
}

func (m *targetMysqlConfigModel) keyValues() []rustfs.TargetKeyValue {
	b := &targetKeyValueBuilder{}
	b.addString("dsn_string", m.DSNString)
	b.addString("table", m.Table)
	b.addString("format", m.Format)
	b.addString("tls_ca", m.TLSCA)
	b.addString("tls_client_cert", m.TLSClientCert)
	b.addString("tls_client_key", m.TLSClientKey)
	b.addString("queue_dir", m.QueueDir)
	b.addInt64("queue_limit", m.QueueLimit)
	b.addInt64("max_open_connections", m.MaxOpenConnections)
	return b.kvs
}

func (m *targetRedisConfigModel) keyValues() []rustfs.TargetKeyValue {
	b := &targetKeyValueBuilder{}
	b.addString("url", m.URL)
	b.addString("channel", m.Channel)
	b.addString("username", m.Username)
	b.addString("password", m.Password)
	b.addInt64("keep_alive_interval", m.KeepAliveInterval)
	b.addString("queue_dir", m.QueueDir)
	b.addInt64("queue_limit", m.QueueLimit)
	b.addInt64("max_retry_attempts", m.MaxRetryAttempts)
	b.addInt64("reconnect_retry_attempts", m.ReconnectRetryAttempts)
	b.addInt64("min_retry_delay", m.MinRetryDelay)
	b.addInt64("max_retry_delay", m.MaxRetryDelay)
	b.addInt64("connection_timeout", m.ConnectionTimeout)
	b.addInt64("response_timeout", m.ResponseTimeout)
	b.addInt64("pipeline_buffer_size", m.PipelineBufferSize)
	b.addString("tls_policy", m.TLSPolicy)
	b.addString("tls_ca", m.TLSCA)
	b.addString("tls_client_cert", m.TLSClientCert)
	b.addString("tls_client_key", m.TLSClientKey)
	b.addBool("tls_allow_insecure", m.TLSAllowInsecure)
	return b.kvs
}

func (m *targetPulsarConfigModel) keyValues() []rustfs.TargetKeyValue {
	b := &targetKeyValueBuilder{}
	b.addString("broker", m.Broker)
	b.addString("topic", m.Topic)
	b.addString("auth_token", m.AuthToken)
	b.addString("username", m.Username)
	b.addString("password", m.Password)
	b.addString("tls_ca", m.TLSCA)
	b.addBool("tls_allow_insecure", m.TLSAllowInsecure)
	b.addBool("tls_hostname_verification", m.TLSHostnameVerification)
	b.addString("queue_dir", m.QueueDir)
	b.addInt64("queue_limit", m.QueueLimit)
	return b.kvs
}

// targetConfigBlockSchema returns the nine per-type configuration blocks as
// SingleNestedAttributes, keyed webhook_config through pulsar_config.
func targetConfigBlockSchema() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"webhook_config":  webhookConfigAttribute(),
		"amqp_config":     amqpConfigAttribute(),
		"kafka_config":    kafkaConfigAttribute(),
		"mqtt_config":     mqttConfigAttribute(),
		"nats_config":     natsConfigAttribute(),
		"postgres_config": postgresConfigAttribute(),
		"mysql_config":    mysqlConfigAttribute(),
		"redis_config":    redisConfigAttribute(),
		"pulsar_config":   pulsarConfigAttribute(),
	}
}

func targetStringAttr(description string) schema.StringAttribute {
	return schema.StringAttribute{
		Optional:    true,
		Description: description,
	}
}

func targetSensitiveStringAttr(description string) schema.StringAttribute {
	return schema.StringAttribute{
		Optional:    true,
		Sensitive:   true,
		Description: description,
	}
}

func targetBoolAttr(description string) schema.BoolAttribute {
	return schema.BoolAttribute{
		Optional:    true,
		Description: description,
	}
}

func targetInt64Attr(description string) schema.Int64Attribute {
	return schema.Int64Attribute{
		Optional:    true,
		Description: description,
	}
}

func targetEnumStringAttr(description string, values ...string) schema.StringAttribute {
	return schema.StringAttribute{
		Optional:    true,
		Description: description,
		Validators:  []validator.String{stringvalidator.OneOf(values...)},
	}
}

// addTargetTLSFields adds the shared tls_ca / tls_client_cert / tls_client_key
// certificate fields in server table order.
func addTargetTLSFields(attrs map[string]schema.Attribute) {
	attrs["tls_ca"] = targetStringAttr("CA certificate used to verify the target.")
	attrs["tls_client_cert"] = targetStringAttr("Client certificate for mTLS to the target.")
	attrs["tls_client_key"] = targetSensitiveStringAttr("Client private key for mTLS to the target.")
}

// addTargetQueueFields adds the shared queue_dir / queue_limit fields in server
// table order.
func addTargetQueueFields(attrs map[string]schema.Attribute) {
	attrs["queue_dir"] = targetStringAttr("Absolute path of the on-disk queue directory for the target.")
	attrs["queue_limit"] = targetInt64Attr("In-memory queue limit for the target.")
}

// addTargetUsernamePassword adds the shared username / password fields.
func addTargetUsernamePassword(attrs map[string]schema.Attribute) {
	attrs["username"] = targetStringAttr("Username used to authenticate to the target.")
	attrs["password"] = targetSensitiveStringAttr("Password used to authenticate to the target.")
}

func webhookConfigAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:    true,
		Description: "Webhook (HTTP) event target configuration.",
		Attributes: map[string]schema.Attribute{
			"endpoint":        targetStringAttr("Endpoint URL the webhook target posts to."),
			"auth_token":      targetSensitiveStringAttr("Optional bearer token sent to the webhook target."),
			"queue_limit":     targetInt64Attr("In-memory queue limit for the webhook target."),
			"queue_dir":       targetStringAttr("Absolute path of the on-disk queue directory for the webhook target."),
			"client_cert":     targetStringAttr("Client certificate for mTLS to the webhook target."),
			"client_key":      targetSensitiveStringAttr("Client private key for mTLS to the webhook target."),
			"client_ca":       targetStringAttr("CA certificate used to verify the webhook target."),
			"skip_tls_verify": targetBoolAttr("Skip TLS certificate verification for the webhook target."),
		},
	}
}

func amqpConfigAttribute() schema.SingleNestedAttribute {
	attrs := map[string]schema.Attribute{
		"url":         targetStringAttr("AMQP broker URL."),
		"exchange":    targetStringAttr("AMQP exchange to publish events to."),
		"routing_key": targetStringAttr("AMQP routing key used when publishing events."),
		"mandatory":   targetBoolAttr("Whether the AMQP publisher uses the mandatory flag."),
		"persistent":  targetBoolAttr("Whether AMQP messages are marked persistent."),
	}
	addTargetUsernamePassword(attrs)
	addTargetTLSFields(attrs)
	addTargetQueueFields(attrs)
	return schema.SingleNestedAttribute{
		Optional:    true,
		Description: "AMQP event target configuration.",
		Attributes:  attrs,
	}
}

func kafkaConfigAttribute() schema.SingleNestedAttribute {
	attrs := map[string]schema.Attribute{
		"brokers":    targetStringAttr("Comma-separated list of Kafka broker addresses."),
		"topic":      targetStringAttr("Kafka topic to publish events to."),
		"acks":       targetEnumStringAttr("Kafka producer acknowledgement mode: 0, 1, -1 or all.", "0", "1", "-1", "all"),
		"tls_enable": targetBoolAttr("Whether TLS is enabled for the Kafka connection."),
	}
	addTargetTLSFields(attrs)
	attrs["sasl_enable"] = targetBoolAttr("Whether SASL authentication is enabled for the Kafka connection.")
	attrs["sasl_mechanism"] = targetStringAttr("SASL authentication mechanism for the Kafka connection.")
	attrs["sasl_username"] = targetStringAttr("SASL username for the Kafka connection.")
	attrs["sasl_password"] = targetSensitiveStringAttr("SASL password for the Kafka connection.")
	addTargetQueueFields(attrs)
	return schema.SingleNestedAttribute{
		Optional:    true,
		Description: "Kafka event target configuration.",
		Attributes:  attrs,
	}
}

func mqttConfigAttribute() schema.SingleNestedAttribute {
	attrs := map[string]schema.Attribute{
		"broker": targetStringAttr("MQTT broker address."),
		"topic":  targetStringAttr("MQTT topic to publish events to."),
		"qos":    targetInt64Attr("MQTT quality of service level."),
	}
	addTargetUsernamePassword(attrs)
	attrs["reconnect_interval"] = targetInt64Attr("Reconnect interval for the MQTT client.")
	attrs["keep_alive_interval"] = targetInt64Attr("Keep-alive interval for the MQTT client.")
	addTargetQueueFields(attrs)
	attrs["tls_policy"] = targetEnumStringAttr("TLS certificate policy: system_ca or custom_ca.", "system_ca", "custom_ca")
	addTargetTLSFields(attrs)
	attrs["tls_trust_leaf_as_ca"] = targetBoolAttr("Whether to trust the leaf certificate as the CA.")
	attrs["ws_path_allowlist"] = targetStringAttr("WebSocket path allowlist for the MQTT connection.")
	return schema.SingleNestedAttribute{
		Optional:    true,
		Description: "MQTT event target configuration.",
		Attributes:  attrs,
	}
}

func natsConfigAttribute() schema.SingleNestedAttribute {
	attrs := map[string]schema.Attribute{
		"address": targetStringAttr("NATS server address."),
		"subject": targetStringAttr("NATS subject to publish events to."),
	}
	addTargetUsernamePassword(attrs)
	attrs["token"] = targetSensitiveStringAttr("NATS authentication token.")
	attrs["credentials_file"] = targetStringAttr("Path to the NATS credentials file.")
	addTargetTLSFields(attrs)
	attrs["tls_required"] = targetBoolAttr("Whether TLS is required for the NATS connection.")
	addTargetQueueFields(attrs)
	attrs["jetstream_enable"] = targetBoolAttr("Whether JetStream is enabled for the NATS target.")
	attrs["jetstream_stream_name"] = targetStringAttr("NATS JetStream stream name.")
	attrs["jetstream_ack_timeout_secs"] = targetInt64Attr("NATS JetStream acknowledgement timeout in seconds.")
	return schema.SingleNestedAttribute{
		Optional:    true,
		Description: "NATS event target configuration.",
		Attributes:  attrs,
	}
}

func postgresConfigAttribute() schema.SingleNestedAttribute {
	attrs := map[string]schema.Attribute{
		"dsn_string":   targetSensitiveStringAttr("PostgreSQL connection DSN string."),
		"table":        targetStringAttr("PostgreSQL table to write events to."),
		"format":       targetEnumStringAttr("PostgreSQL event format: namespace or access.", "namespace", "access"),
		"tls_required": targetBoolAttr("Whether TLS is required for the PostgreSQL connection."),
	}
	addTargetTLSFields(attrs)
	addTargetQueueFields(attrs)
	return schema.SingleNestedAttribute{
		Optional:    true,
		Description: "PostgreSQL event target configuration.",
		Attributes:  attrs,
	}
}

func mysqlConfigAttribute() schema.SingleNestedAttribute {
	attrs := map[string]schema.Attribute{
		"dsn_string": targetSensitiveStringAttr("MySQL connection DSN string."),
		"table":      targetStringAttr("MySQL table to write events to."),
		"format":     targetStringAttr("MySQL event format."),
	}
	addTargetTLSFields(attrs)
	addTargetQueueFields(attrs)
	attrs["max_open_connections"] = targetInt64Attr("Maximum number of open MySQL connections.")
	return schema.SingleNestedAttribute{
		Optional:    true,
		Description: "MySQL event target configuration.",
		Attributes:  attrs,
	}
}

func redisConfigAttribute() schema.SingleNestedAttribute {
	attrs := map[string]schema.Attribute{
		"url":     targetStringAttr("Redis server URL."),
		"channel": targetStringAttr("Redis channel to publish events to."),
	}
	addTargetUsernamePassword(attrs)
	attrs["keep_alive_interval"] = targetInt64Attr("Keep-alive interval for the Redis connection.")
	addTargetQueueFields(attrs)
	attrs["max_retry_attempts"] = targetInt64Attr("Maximum number of retry attempts.")
	attrs["reconnect_retry_attempts"] = targetInt64Attr("Number of reconnect retry attempts.")
	attrs["min_retry_delay"] = targetInt64Attr("Minimum delay between retry attempts.")
	attrs["max_retry_delay"] = targetInt64Attr("Maximum delay between retry attempts.")
	attrs["connection_timeout"] = targetInt64Attr("Connection timeout for the Redis connection.")
	attrs["response_timeout"] = targetInt64Attr("Response timeout for the Redis connection.")
	attrs["pipeline_buffer_size"] = targetInt64Attr("Redis pipeline buffer size.")
	attrs["tls_policy"] = targetEnumStringAttr("TLS certificate policy: system_ca or custom_ca.", "system_ca", "custom_ca")
	addTargetTLSFields(attrs)
	attrs["tls_allow_insecure"] = targetBoolAttr("Whether to allow insecure TLS connections.")
	return schema.SingleNestedAttribute{
		Optional:    true,
		Description: "Redis event target configuration.",
		Attributes:  attrs,
	}
}

func pulsarConfigAttribute() schema.SingleNestedAttribute {
	attrs := map[string]schema.Attribute{
		"broker":     targetStringAttr("Pulsar broker address."),
		"topic":      targetStringAttr("Pulsar topic to publish events to."),
		"auth_token": targetSensitiveStringAttr("Authentication token for the Pulsar target."),
	}
	addTargetUsernamePassword(attrs)
	attrs["tls_ca"] = targetStringAttr("CA certificate used to verify the Pulsar target.")
	attrs["tls_allow_insecure"] = targetBoolAttr("Whether to allow insecure TLS connections.")
	attrs["tls_hostname_verification"] = targetBoolAttr("Whether TLS hostname verification is enabled.")
	addTargetQueueFields(attrs)
	return schema.SingleNestedAttribute{
		Optional:    true,
		Description: "Pulsar event target configuration.",
		Attributes:  attrs,
	}
}
