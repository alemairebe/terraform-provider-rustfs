package rustfs

// TargetKeyValue is a single key/value pair in an audit or event-notification
// target config. Both target APIs accept the same key/value configuration
// shape, so the pair type is shared between them.
type TargetKeyValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
