package editorial

import (
	"context"
	"testing"
)

func TestRoleAgentRunnerFailsClosedWhenDependenciesAreMissing(t *testing.T) {
	runner := NewRoleAgentRunner(nil, nil, nil, nil, nil, nil)
	if _, err := runner.Run(context.Background(), RoleRunConfig{}); err == nil {
		t.Fatal("missing runner dependencies must return an error instead of panicking")
	}
}
