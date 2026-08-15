package domain

import "time"

type Option struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Consequence string `json:"consequence"`
}

type Namespace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	RootPath  string    `json:"root_path"`
	CreatedAt time.Time `json:"created_at"`
}

type Goal struct {
	ID            string     `json:"id"`
	NamespaceID   string     `json:"namespace_id"`
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	Status        GoalStatus `json:"status"`
	ResultSummary string     `json:"result_summary"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type Task struct {
	ID         string     `json:"id"`
	GoalID     string     `json:"goal_id"`
	Title      string     `json:"title"`
	Status     TaskStatus `json:"status"`
	Agent      string     `json:"agent"`
	Order      int        `json:"order"`
	DeclareKey string     `json:"declare_key"`
	ClaimedBy  string     `json:"claimed_by"`
	ClaimedAt  *time.Time `json:"claimed_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type Decision struct {
	ID          string         `json:"id"`
	GoalID      string         `json:"goal_id"`
	TaskID      string         `json:"task_id,omitempty"`
	Kind        DecisionKind   `json:"kind"`
	Question    string         `json:"question"`
	Options     []Option       `json:"options"`
	Status      DecisionStatus `json:"status"`
	AnswerLabel string         `json:"answer_label,omitempty"`
	AnswerText  string         `json:"answer_text,omitempty"`
	AnsweredBy  string         `json:"answered_by,omitempty"`
	AnsweredAt  *time.Time     `json:"answered_at,omitempty"`
	AppliedAt   *time.Time     `json:"applied_at,omitempty"`
	RunID       string         `json:"run_id"`
	CreatedAt   time.Time      `json:"created_at"`
}
