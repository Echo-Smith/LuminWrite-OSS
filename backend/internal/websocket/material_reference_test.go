package websocket

import (
	"encoding/json"
	"testing"
)

func TestAgentStartPayloadPreservesMaterialIdentityWithoutBrowserContent(t *testing.T) {
	var payload AgentStartPayload
	if err := json.Unmarshal([]byte(`{"message":"write","material_refs":[{"material_id":"mat_test","source_ref":"kb://documents/doc_test","title":"Source"}]}`), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.MaterialRefs) != 1 || payload.MaterialRefs[0].MaterialID != "mat_test" || payload.MaterialRefs[0].SourceRef != "kb://documents/doc_test" || len(payload.UserMaterials) != 0 {
		t.Fatalf("payload=%#v", payload)
	}
}
