package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"strings"
)

type governedResearchStep struct{ query, search, relevance, compress engine.Step }

func (governedResearchStep) Name() engine.StepName { return "governed_research" }
func (governedResearchStep) CanPause() bool        { return false }
func (step governedResearchStep) Execute(ctx context.Context, exec *engine.ExecutionContext, emitter engine.EventEmitter) error {
	for _, part := range []engine.Step{step.query, step.search} {
		if err := part.Execute(ctx, exec, emitter); err != nil {
			return err
		}
	}
	actual := []engine.SearchResult{}
	for _, source := range exec.SearchResults {
		if !source.IsMock && strings.TrimSpace(source.Snippet) != "" && source.URL != "" {
			actual = append(actual, source)
		}
	}
	for _, body := range exec.UserMaterials {
		if strings.TrimSpace(body) == "" {
			continue
		}
		hash := sha256.Sum256([]byte(body))
		// This URI identifies actual snapshotted material content, not a web claim.
		ref := "material://sha256:" + hex.EncodeToString(hash[:])
		if start := strings.Index(body, " source:kb://"); start >= 0 {
			ref = strings.Fields(body[start+8:])[0]
		}
		actual = append(actual, engine.SearchResult{Title: "用户选定材料", Snippet: body, URL: ref, Source: "user_material", Score: 1})
	}
	exec.SearchResults = actual
	if len(actual) == 0 {
		return fmt.Errorf("governed research requires real source evidence; select materials or configure a search source")
	}
	for _, part := range []engine.Step{step.relevance, step.compress} {
		if err := part.Execute(ctx, exec, emitter); err != nil {
			return err
		}
	}
	return nil
}
