package app

// #813: `nearest` is a third optional field of ActivityEvent, beside
// `latency_us` and `served`: declared, never required. The schema checks in
// activity_schema_test.go read optionalFields; this adds the field to it.
func init() {
	optionalFields["ActivityEvent"] = append(optionalFields["ActivityEvent"], "nearest")
}
