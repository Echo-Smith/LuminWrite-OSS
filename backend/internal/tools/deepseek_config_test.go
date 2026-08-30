package tools

import (
	"testing"
	"time"
)

func TestLLMClientConfiguredRejectsMissingAndPlaceholderKeys(t *testing.T) {
	for _, key := range []string{"", "your-api-key", "your-deepseek-api-key", "placeholder"} {
		client := NewLLMClient("https://example.test", key, "model", 8, 0, time.Second)
		if client.IsConfigured() {
			t.Fatalf("key %q reported configured", key)
		}
	}
	if !NewLLMClient("https://example.test", "real-looking-key", "model", 8, 0, time.Second).IsConfigured() {
		t.Fatal("non-placeholder key reported unconfigured")
	}
}
