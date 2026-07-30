package schema_test

import (
	"encoding/json"
	"os"
	"testing"
)

func TestRuntimeV1SchemaIsValidJSONAndContainsPublicContracts(t *testing.T) {
	body, err := os.ReadFile("runtime-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Defs map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(body, &schema); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Target", "AvailableTargetList", "TokenPair", "Usage", "APIError", "StreamEvent"} {
		if len(schema.Defs[name]) == 0 {
			t.Errorf("missing definition %s", name)
		}
	}
}
