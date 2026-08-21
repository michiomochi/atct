package domain

import (
	"strings"
	"time"
)

type Option struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Consequence string `json:"consequence"`
}

type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	RootPath  string    `json:"root_path"`
	CreatedAt time.Time `json:"created_at"`
}

type CompletionReport struct {
	WorkDone    string `json:"work_done"`
	NowPossible string `json:"now_possible"`
	HowToVerify string `json:"how_to_verify"`
	Surprises   string `json:"surprises"`
	NeedsReview string `json:"needs_review"`
	NextSteps   string `json:"next_steps"`
}

type Goal struct {
	ID          string     `json:"id"`
	ProjectID   string     `json:"project_id"`
	Content     string     `json:"content"`
	Status      GoalStatus `json:"status"`
	Creator     string     `json:"creator"`
	WorkDone    string     `json:"work_done"`
	NowPossible string     `json:"now_possible"`
	HowToVerify string     `json:"how_to_verify"`
	Surprises   string     `json:"surprises"`
	NeedsReview string     `json:"needs_review"`
	NextSteps   string     `json:"next_steps"`

	// ResultSummary preserves the legacy completion report field for API clients
	// that still use the pre-structured report format.
	ResultSummary string    `json:"result_summary"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func Headline(content string) string {
	if index := strings.IndexByte(content, '\n'); index >= 0 {
		return strings.TrimSpace(content[:index])
	}
	return strings.TrimSpace(content)
}

type Task struct {
	ID          string     `json:"id"`
	GoalID      string     `json:"goal_id"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      TaskStatus `json:"status"`
	Agent       string     `json:"agent"`
	Files       []string   `json:"files"`
	Order       int        `json:"order"`
	DeclareKey  string     `json:"declare_key"`
	ClaimedBy   string     `json:"claimed_by"`
	ClaimedAt   *time.Time `json:"claimed_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type TaskCommit struct {
	SHA          string    `json:"sha"`
	Subject      string    `json:"subject"`
	FilesChanged int       `json:"files_changed"`
	Insertions   int       `json:"insertions"`
	Deletions    int       `json:"deletions"`
	CreatedAt    time.Time `json:"created_at"`
}

type Decision struct {
	ID               string         `json:"id"`
	GoalID           string         `json:"goal_id"`
	TaskID           string         `json:"task_id,omitempty"`
	Kind             DecisionKind   `json:"kind"`
	Question         string         `json:"question"`
	Options          []Option       `json:"options"`
	Status           DecisionStatus `json:"status"`
	DefaultOption    string         `json:"default_option,omitempty"`
	DefaultAfterMs   *int64         `json:"default_after_ms,omitempty"`
	DefaultAppliedAt *time.Time     `json:"default_applied_at,omitempty"`
	AnswerLabel      string         `json:"answer_label,omitempty"`
	AnswerText       string         `json:"answer_text,omitempty"`
	AnsweredAt       *time.Time     `json:"answered_at,omitempty"`
	AppliedAt        *time.Time     `json:"applied_at,omitempty"`
	AgentSessionID   string         `json:"agent_session_id"`
	CreatedAt        time.Time      `json:"created_at"`
}
