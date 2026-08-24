package editorial

import (
	"context"
	"time"
)

// OrchestratorEvent 编排器发出的事件
type OrchestratorEvent struct {
	Type      string      `json:"type"`
	TaskID    string      `json:"task_id"`
	Payload   interface{} `json:"payload"`
	Timestamp time.Time   `json:"timestamp"`
}

// EventEmitter 事件发射器接口
type EventEmitter interface {
	Emit(evt OrchestratorEvent)
}

// AgentExecutorAdapter 适配器接口
type AgentExecutorAdapter interface {
	Role() AgentRole
	Execute(ctx context.Context, ac *AgentContext, task *Task) (*Artifact, error)
}

// defaultAssignee 根据任务状态返回默认的 assignee 类型
func defaultAssignee(status TaskStatus) AssigneeType {
	switch status {
	case StatusResearch:
		return AssigneeResearchAgent
	case StatusWriting:
		return AssigneeWritingAgent
	case StatusReview:
		return AssigneeReviewAgent
	case StatusPendingApproval, StatusPendingPublish, StatusPublished:
		return AssigneeHuman
	default:
		return AssigneeHuman
	}
}
