package routing

import (
	"hash/fnv"
	"sort"
	"strings"
	"sync/atomic"
)

// RolloutConfig defines how a profile is rolled out to users.
type RolloutConfig struct {
	Type            string   `json:"type"`             // full | whitelist | percentage
	WhitelistUIDs   []string `json:"whitelist_uids"`   // users who get the new version
	RolloutPercent  int      `json:"rollout_percent"`  // 0-100, percentage of users
	FallbackVersion int      `json:"fallback_version"` // version to serve to non-selected users
}

// RolloutMetrics holds lightweight atomic counters for grayscale routing decisions.
// These are read by the server's /metrics endpoint.
var RolloutMetrics = struct {
	NewVersion  atomic.Int64
	OldVersion  atomic.Int64
	Whitelist   atomic.Int64
	Percentage  atomic.Int64
}{}

// ShouldUseNewVersion determines whether a user should get the new profile version.
// Uses a two-level routing strategy:
//   1. Whitelist: if the user is in the whitelist, they always get the new version
//   2. Percentage: FNV-1a hash of the UID determines if they fall in the rollout percentage
func ShouldUseNewVersion(uid string, config RolloutConfig) bool {
	switch config.Type {
	case "full":
		RolloutMetrics.NewVersion.Add(1)
		return true
	case "whitelist":
		if isInWhitelist(uid, config.WhitelistUIDs) {
			RolloutMetrics.NewVersion.Add(1)
			RolloutMetrics.Whitelist.Add(1)
			return true
		}
		RolloutMetrics.OldVersion.Add(1)
		return false
	case "percentage":
		// First check whitelist
		if isInWhitelist(uid, config.WhitelistUIDs) {
			RolloutMetrics.NewVersion.Add(1)
			RolloutMetrics.Whitelist.Add(1)
			return true
		}
		// Then check percentage via hash
		if hashInPercentile(uid, config.RolloutPercent) {
			RolloutMetrics.NewVersion.Add(1)
			RolloutMetrics.Percentage.Add(1)
			return true
		}
		RolloutMetrics.OldVersion.Add(1)
		return false
	default:
		RolloutMetrics.NewVersion.Add(1)
		return true
	}
}

// isInWhitelist checks if a UID is in the whitelist (binary search on sorted list).
func isInWhitelist(uid string, whitelist []string) bool {
	if len(whitelist) == 0 {
		return false
	}
	// Sort for binary search (assumes whitelist might not be sorted)
	sorted := make([]string, len(whitelist))
	copy(sorted, whitelist)
	sort.Strings(sorted)
	idx := sort.SearchStrings(sorted, uid)
	return idx < len(sorted) && sorted[idx] == uid
}

// hashInPercentile uses FNV-1a hash to deterministically assign users to buckets.
// Returns true if the user falls within the given percentage.
func hashInPercentile(uid string, percent int) bool {
	if percent >= 100 {
		return true
	}
	if percent <= 0 {
		return false
	}

	h := fnv.New32a()
	h.Write([]byte(uid))
	hashVal := h.Sum32()

	// Map hash to 0-99 bucket
	bucket := int(hashVal % 100)

	return bucket < percent
}

// PreviewRollout simulates the routing decision for a list of UIDs.
// Returns the percentage of users who would get the new version.
func PreviewRollout(uids []string, config RolloutConfig) RolloutPreview {
	if len(uids) == 0 {
		return RolloutPreview{
			Total:         0,
			NewVersion:    0,
			OldVersion:    0,
			NewVersionPct: 0,
		}
	}

	newCount := 0
	oldCount := 0
	var newUIDs, oldUIDs []string

	for _, uid := range uids {
		if ShouldUseNewVersion(uid, config) {
			newCount++
			newUIDs = append(newUIDs, uid)
		} else {
			oldCount++
			oldUIDs = append(oldUIDs, uid)
		}
	}

	return RolloutPreview{
		Total:         len(uids),
		NewVersion:    newCount,
		OldVersion:    oldCount,
		NewVersionPct: float64(newCount) / float64(len(uids)) * 100,
		NewUIDs:       newUIDs[:min(len(newUIDs), 100)], // limit preview to 100
		OldUIDs:       oldUIDs[:min(len(oldUIDs), 100)],
	}
}

// RolloutPreview holds the result of a rollout preview.
type RolloutPreview struct {
	Total         int      `json:"total"`
	NewVersion    int      `json:"new_version"`
	OldVersion    int      `json:"old_version"`
	NewVersionPct float64  `json:"new_version_pct"`
	NewUIDs       []string `json:"new_uids,omitempty"`
	OldUIDs       []string `json:"old_uids,omitempty"`
}

// ParseWhitelist parses a comma-separated whitelist string into a slice.
func ParseWhitelist(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
