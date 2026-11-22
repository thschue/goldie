package contract

import (
	"strings"
	"testing"
)

func TestValidProjectIDReadsTheLengthFromTheSpec(t *testing.T) {
	if projectID.maxLength != 512 {
		t.Fatalf("maxLength = %d, want the spec's 512", projectID.maxLength)
	}
	head := "github.com/acme/"
	if id := head + strings.Repeat("r", projectID.maxLength-len(head)); !ValidProjectID(id) {
		t.Errorf("a ProjectId of exactly maxLength bytes is rejected")
	}
	if id := head + strings.Repeat("r", projectID.maxLength-len(head)+1); ValidProjectID(id) {
		t.Errorf("a ProjectId longer than maxLength is accepted")
	}
}

func TestParseProjectIDSchemaNeedsPatternAndLength(t *testing.T) {
	for _, spec := range []string{
		"components: {schemas: {ProjectId: {maxLength: 5}}}",
		"components: {schemas: {ProjectId: {pattern: '^a$'}}}",
		"components: {schemas: {ProjectId: {pattern: '(', maxLength: 5}}}",
	} {
		if _, err := parseProjectIDSchema([]byte(spec)); err == nil {
			t.Errorf("parseProjectIDSchema(%q) succeeded", spec)
		}
	}
}
