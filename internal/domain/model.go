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
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	RootPath  string     `json:"root_path"`
	CreatedAt time.Time  `json:"created_at"`
	ClaimedBy int64      `json:"claimed_by"`
	ClaimedAt *time.Time `json:"claimed_at,omitempty"`
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
	ID                int64      `json:"id"`
	ProjectID         int64      `json:"project_id"`
	DerivedFromGoalID int64      `json:"derived_from_goal_id"`
	Content           string     `json:"content"`
	Status            GoalStatus `json:"status"`
	Creator           string     `json:"creator"`
	WorkDone          string     `json:"work_done"`
	NowPossible       string     `json:"now_possible"`
	HowToVerify       string     `json:"how_to_verify"`
	Surprises         string     `json:"surprises"`
	NeedsReview       string     `json:"needs_review"`
	NextSteps         string     `json:"next_steps"`

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

// Body returns everything after a Goal's headline. Empty when the content is a
// single line, which is what four goals in five look like.
func Body(content string) string {
	index := strings.IndexByte(content, '\n')
	if index < 0 {
		return ""
	}
	return strings.TrimSpace(content[index+1:])
}

type Task struct {
	ID           int64      `json:"id"`
	GoalID       int64      `json:"goal_id"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	Status       TaskStatus `json:"status"`
	Agent        string     `json:"agent"`
	Files        []string   `json:"files"`
	Order        int        `json:"order"`
	DeclareKey   string     `json:"declare_key"`
	Declared     *bool      `json:"declared,omitempty"`
	SnoozedUntil *time.Time `json:"snoozed_until,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
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
	ID               int64          `json:"id"`
	GoalID           int64          `json:"goal_id"`
	TaskID           int64          `json:"task_id,omitempty"`
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
	AgentSessionID   int64          `json:"agent_session_id"`
	CreatedAt        time.Time      `json:"created_at"`
}
