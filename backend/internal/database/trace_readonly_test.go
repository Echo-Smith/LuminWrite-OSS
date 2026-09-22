package database_test

// TraceRepo read-only boundary (⑥D acceptance).
//
// The legacy writing lifecycle writes were deleted once their business
// consumers were gone (agent_traces is a read-only history projection;
// governed run state lives in writingstore). This test fails CI if the
// deleted write methods come back — the only permitted writes are
// user-surface maintenance on legacy rows (title rename, cancel,
// soft-delete, feedback, article versions).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTraceRepoWritingLifecycleWritesAreGone(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(repoRoot)
		if parent == repoRoot {
			t.Fatal("go.mod not found")
		}
		repoRoot = parent
	}
	source := filepath.Join(repoRoot, "internal", "database", "trace.go")
	parsed, err := parser.ParseFile(token.NewFileSet(), source, nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	deleted := []string{"CreateTrace", "UpdateTraceStep", "PauseTrace", "UpdateTaskName", "FailTrace", "LinkEditorialTask"}
	allowed := map[string]bool{"UpdateTraceTitle": true, "CompleteTrace": true, "CancelTrace": true,
		"RecoverStaleRunningTraces": true, "SoftDeleteTrace": true, "SaveFeedback": true,
		"UpdateTraceArticle": true, "SaveArticleVersion": true, "CreateTopic": true, "UpdateTopic": true,
		"DeleteTopic": true, "UpsertHotTopics": true}

	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
			continue
		}
		receiver, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		ident, ok := receiver.X.(*ast.Ident)
		if !ok || ident.Name != "TraceRepo" {
			continue
		}
		for _, name := range deleted {
			if fn.Name.Name == name {
				t.Fatalf("deleted write method %s reappeared in trace.go — legacy writing-lifecycle writes must stay deleted", name)
			}
		}
	}

	// The methods that must survive are still present (guard against
	// accidental deletion of user-surface maintenance).
	for name := range allowed {
		found := false
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil {
				continue
			}
			if fn.Name.Name == name && strings.Contains(fmtReceiver(fn), "TraceRepo") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected user-surface method %s missing from TraceRepo", name)
		}
	}
}

func fmtReceiver(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) != 1 {
		return ""
	}
	switch typ := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if ident, ok := typ.X.(*ast.Ident); ok {
			return ident.Name
		}
	case *ast.Ident:
		return typ.Name
	}
	return ""
}
