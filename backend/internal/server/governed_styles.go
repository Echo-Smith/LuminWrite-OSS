package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"strings"
	"time"
)

// Owner comes from the persisted document, never a caller-supplied rollout subject.
type governedStyleResolver struct{ server *Server }

func (resolver governedStyleResolver) ResolveProfile(slug, userID string) (result *profile.StyleProfile, err error) {
	s := resolver.server
	if s == nil {
		return nil, nil
	}
	kind := "global"
	if strings.HasPrefix(slug, "my_") {
		kind = "personal"
	}
	defer func() {
		status := "resolved"
		if result == nil || err != nil {
			status = "fallback"
		}
		if s.metrics != nil {
			s.metrics.GovernedStyleTotal.Inc(kind, status)
		}
	}()
	if kind == "global" {
		return (writingruntime.LoaderStyleResolver{Loader: s.profiles}).ResolveProfile(slug, userID)
	}
	if s.userStyleStore == nil || userID == "" || userID == "anonymous" {
		return nil, fmt.Errorf("personal style owner unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	owned, err := s.userStyleStore.GetProfileBySlugAndOwner(ctx, strings.TrimPrefix(slug, "my_"), userID)
	if err != nil {
		return nil, err
	}
	if owned.CurrentVersion < 1 {
		return nil, fmt.Errorf("personal style has no published version")
	}
	version, err := s.userStyleStore.GetVersionByNumber(ctx, owned.ID, owned.CurrentVersion)
	if err != nil {
		return nil, err
	}
	var p profile.StyleProfile
	if err := json.Unmarshal([]byte(version.Config), &p); err != nil {
		return nil, err
	}
	return &p, nil
}
