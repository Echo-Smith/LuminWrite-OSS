package server

import (
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
)

func TestProjectMaterialGovernanceDoesNotClaimSnapshotBeforeRun(t *testing.T) {
	material := &services.UserMaterial{ID: "mat_test", Status: "active", DocID: "doc_source"}
	projectMaterialGovernance(material)
	if material.Governance == nil || !material.Governance.Eligible || material.Governance.SnapshotStatus != "pending_run_snapshot" || material.Governance.IntegrityStatus != "source_registered" || material.Governance.SourceRef != "kb://documents/doc_source" {
		t.Fatalf("governance=%#v", material.Governance)
	}
}

func TestProjectMaterialGovernanceFailsClosedWithoutSourceDocument(t *testing.T) {
	material := &services.UserMaterial{ID: "mat_test", Status: "active"}
	projectMaterialGovernance(material)
	if material.Governance == nil || material.Governance.Eligible || material.Governance.SnapshotStatus != "source_unavailable" || material.Governance.IntegrityStatus != "unverified" {
		t.Fatalf("governance=%#v", material.Governance)
	}
}
