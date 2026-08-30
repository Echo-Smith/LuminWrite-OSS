package tools

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newOSSSearchCapabilityClient(tavilyKey, anySearchKey string) *SearchClient {
	return NewSearchClient(
		tavilyKey, "https://paid-search.invalid/search", time.Second,
		false, "", "", time.Second,
		false, "", time.Second,
		false, "", "", "", "", time.Second,
		false, "", time.Second,
		false, "", time.Second,
		"", time.Second,
		anySearchKey, "https://paid-search.invalid", time.Second,
	)
}

func TestOSSDoesNotAdvertisePaidSearchProviders(t *testing.T) {
	client := newOSSSearchCapabilityClient("configured-looking-key", "configured-looking-key")
	if client.HasSources() {
		t.Fatal("OSS advertised paid search providers")
	}
	if sources := client.ActiveSources(); len(sources) != 0 {
		t.Fatalf("OSS active sources=%v", sources)
	}
}

func TestOSSPaidProviderFailsExplicitly(t *testing.T) {
	ctx := context.Background()
	if _, err := NewTavilyClient("key", "https://paid-search.invalid", time.Second).Search(ctx, "query", 1); !errors.Is(err, ErrProviderNotInstalled) {
		t.Fatalf("tavily error=%v", err)
	}
	if _, err := NewAnySearchClient("key", "https://paid-search.invalid", time.Second).Search(ctx, "query", 1); !errors.Is(err, ErrProviderNotInstalled) {
		t.Fatalf("anysearch error=%v", err)
	}
}
