package database

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const wabenchPrivateHoldoutPrefix = "wabench:private-holdout/"

type WABenchPrivateInputResolver interface {
	ResolveWABenchInput(ctx context.Context, ref, expectedHash string) (string, error)
}

type JSONLWABenchPrivateInputResolver struct {
	inputs map[string]WABenchPrivateHoldoutRecord
}

func NewJSONLWABenchPrivateInputResolver(path string) (*JSONLWABenchPrivateInputResolver, error) {
	file, err := os.Open(strings.TrimSpace(path))
	if err != nil {
		return nil, fmt.Errorf("open WABench private input JSONL: %w", err)
	}
	defer file.Close()
	resolver := &JSONLWABenchPrivateInputResolver{inputs: map[string]WABenchPrivateHoldoutRecord{}}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		var row WABenchPrivateHoldoutRecord
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, fmt.Errorf("decode WABench private input JSONL: %w", err)
		}
		if row.HoldoutID == "" || strings.TrimSpace(row.InputRedacted) == "" || row.RedactedInputHash == "" {
			return nil, fmt.Errorf("invalid WABench private holdout record")
		}
		if _, exists := resolver.inputs[row.HoldoutID]; exists {
			return nil, fmt.Errorf("duplicate WABench private holdout id %s", row.HoldoutID)
		}
		actual := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(row.InputRedacted)))
		if actual != row.RedactedInputHash {
			return nil, fmt.Errorf("redacted input hash mismatch for %s", row.HoldoutID)
		}
		resolver.inputs[row.HoldoutID] = row
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return resolver, nil
}

func (r *JSONLWABenchPrivateInputResolver) ResolveWABenchInput(_ context.Context, ref, expectedHash string) (string, error) {
	if r == nil || !strings.HasPrefix(ref, wabenchPrivateHoldoutPrefix) {
		return "", fmt.Errorf("%w: unsupported WABench private input ref", ErrWABenchInputUnavailable)
	}
	id := strings.TrimPrefix(ref, wabenchPrivateHoldoutPrefix)
	row, ok := r.inputs[id]
	if !ok || row.RedactedInputHash != expectedHash {
		return "", fmt.Errorf("%w: WABench private input identity mismatch", ErrWABenchInputUnavailable)
	}
	return row.InputRedacted, nil
}
