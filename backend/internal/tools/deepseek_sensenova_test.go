package tools

import (
	"testing"
	"time"
)

func TestSenseNovaDisabledThinkingParameters(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, opts := range [][]ChatOption{
			{WithThinking(false)},
			{WithThinking(false), WithReasoningEffort("high")},
			{WithReasoningEffort("high"), WithThinking(false)},
		} {
			client := NewLLMClient("https://token.sensenova.cn/v1", "test", "deepseek-v4-flash", 4096, .2, time.Second)
			request := client.buildRequest(nil, stream, opts...)
			if request.ReasoningEffort != "none" || request.Thinking.Type != "disabled" {
				t.Fatalf("invalid disabled-thinking combination: effort=%q thinking=%v", request.ReasoningEffort, request.Thinking)
			}
		}
	}
}

func TestThinkingCompatibilityDoesNotChangeOtherRequests(t *testing.T) {
	for _, endpoint := range []string{"https://api.deepseek.com/v1", "https://token.sensenova.cn.example.com/v1"} {
		client := NewLLMClient(endpoint, "test", "deepseek-v4-flash", 4096, .2, time.Second)
		request := client.buildRequest(nil, false, WithThinking(false))
		if request.ReasoningEffort != "high" {
			t.Fatal("changed another provider")
		}
	}
	client := NewLLMClient("https://token.sensenova.cn/v1", "test", "deepseek-v4-flash", 4096, .2, time.Second)
	request := client.buildRequest(nil, false, WithThinking(true), WithReasoningEffort("medium"))
	if request.ReasoningEffort != "medium" || request.Thinking.Type != "enabled" {
		t.Fatal("changed enabled thinking")
	}
}
