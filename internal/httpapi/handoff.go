package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/michiomochi/atct/internal/store"
)

const (
	EventHandoffEntryAdded      = "handoff_entry_added"
	HandoffEntryPreviewMaxBytes = 256
	handoffEntryPollInterval    = 100 * time.Millisecond
	handoffEntryEventScopeGoal  = "goal"
	handoffEntryEventScopeTask  = "task"
)

// HandoffEntryAddedEvent is the transport-safe notification for one newly
// appended entry. The body is deliberately limited to a UTF-8-safe preview so
// event consumers can react without receiving the full handoff transcript.
type HandoffEntryAddedEvent struct {
	ProjectID       int64  `json:"project_id"`
	Scope           string `json:"scope"`
	GoalID          int64  `json:"goal_id"`
	TaskID          int64  `json:"task_id,omitempty"`
	HandoffID       string `json:"handoff_id"`
	ID              int64  `json:"id"`
	Kind            string `json:"kind"`
	AuthorSessionID int64  `json:"author_session_id"`
	InReplyToID     *int64 `json:"in_reply_to_id,omitempty"`
	BodyPreview     string `json:"body_preview"`
	// parties are the sessions that hold a role in the handoff. Empty means
	// unknown (e.g. a published event), which is delivered to everyone.
	parties []int64
}

// HandoffEntryView is the canonical HTTP representation of one handoff entry.
// The store still carries deprecated compatibility aliases, so the transport
// maps through this type instead of serializing store.HandoffEntry directly.
type HandoffEntryView struct {
	ID              int64     `json:"id"`
	HandoffID       string    `json:"handoff_id"`
	Kind            string    `json:"kind"`
	Body            string    `json:"body"`
	AuthorSessionID int64     `json:"author_session_id"`
	InReplyToID     *int64    `json:"in_reply_to_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// HandoffView is the read-only HTTP representation of a goal or task
// handoff. Entries contain the requested after_id page in oldest-first order.
type HandoffView struct {
	ID                string             `json:"id"`
	Scope             string             `json:"scope"`
	ProjectID         int64              `json:"project_id"`
	GoalID            int64              `json:"goal_id"`
	TaskID            int64              `json:"task_id,omitempty"`
	RequestedBy       int64              `json:"requested_by"`
	ReceivedBy        int64              `json:"received_by"`
	RequestReport     string             `json:"request_report"`
	CompleteReport    string             `json:"complete_report"`
	RequestedAt       *time.Time         `json:"requested_at"`
	ReceivedAt        *time.Time         `json:"received_at"`
	CompletedReportAt *time.Time         `json:"completed_report_at"`
	Entries           []HandoffEntryView `json:"entries"`
	HasMore           bool               `json:"has_more"`
	NextAfterID       int64              `json:"next_after_id"`
}

type handoffEntryRecord struct {
	event     HandoffEntryAddedEvent
	createdAt time.Time
}

func truncateHandoffEntryPreview(body string) string {
	if !utf8.ValidString(body) {
		body = strings.ToValidUTF8(body, "\uFFFD")
	}
	if len([]byte(body)) <= HandoffEntryPreviewMaxBytes {
		return body
	}
	var preview []byte
	for _, r := range body {
		runeBytes := []byte(string(r))
		if len(preview)+len(runeBytes) > HandoffEntryPreviewMaxBytes {
			break
		}
		preview = append(preview, runeBytes...)
	}
	return string(preview)
}

func normalizeHandoffEntryEvent(data any) (HandoffEntryAddedEvent, bool) {
	var event HandoffEntryAddedEvent
	switch value := data.(type) {
	case HandoffEntryAddedEvent:
		event = value
	case *HandoffEntryAddedEvent:
		if value == nil {
			return HandoffEntryAddedEvent{}, false
		}
		event = *value
	default:
		encoded, err := json.Marshal(data)
		if err != nil || json.Unmarshal(encoded, &event) != nil {
			return HandoffEntryAddedEvent{}, false
		}
	}
	event.BodyPreview = truncateHandoffEntryPreview(event.BodyPreview)
	if event.Scope == "" {
		switch {
		case event.TaskID != 0:
			event.Scope = handoffEntryEventScopeTask
		case event.GoalID != 0:
			event.Scope = handoffEntryEventScopeGoal
		}
	}
	if event.HandoffID == "" || event.ID <= 0 || event.GoalID == 0 || !canonicalHandoffEntryKind(event.Kind) {
		return HandoffEntryAddedEvent{}, false
	}
	return event, true
}

func canonicalHandoffEntryKind(kind string) bool {
	switch kind {
	case store.HandoffEntryKindRequest,
		store.HandoffEntryKindReceived,
		store.HandoffEntryKindReviewRequested,
		store.HandoffEntryKindReviewReceived,
		store.HandoffEntryKindReviewRejected,
		store.HandoffEntryKindCompleted:
		return true
	default:
		return false
	}
}

func handoffEventKey(event HandoffEntryAddedEvent) string {
	if event.ID <= 0 {
		return ""
	}
	return event.Scope + ":" + event.HandoffID + ":" + strconv.FormatInt(event.ID, 10)
}

type handoffEntryTracker struct {
	known map[string]struct{}
}

func newHandoffEntryTracker(records []handoffEntryRecord, lastEventID string, baselineAt time.Time) *handoffEntryTracker {
	tracker := &handoffEntryTracker{known: make(map[string]struct{}, len(records))}
	if strings.TrimSpace(lastEventID) == "" {
		for _, record := range records {
			if !baselineAt.IsZero() && !record.createdAt.Before(baselineAt) {
				continue
			}
			tracker.mark(record.event)
		}
		return tracker
	}

	lastID, parseErr := strconv.ParseInt(strings.TrimSpace(lastEventID), 10, 64)
	if parseErr != nil || lastID <= 0 {
		for _, record := range records {
			if !baselineAt.IsZero() && !record.createdAt.Before(baselineAt) {
				continue
			}
			tracker.mark(record.event)
		}
		return tracker
	}

	lastFound := false
	for _, record := range records {
		if !baselineAt.IsZero() && !record.createdAt.Before(baselineAt) {
			continue
		}
		if !lastFound {
			tracker.mark(record.event)
		}
		if record.event.ID == lastID {
			lastFound = true
		}
	}
	if !lastFound {
		for _, record := range records {
			if !baselineAt.IsZero() && !record.createdAt.Before(baselineAt) {
				continue
			}
			tracker.mark(record.event)
		}
	}
	return tracker
}

func (t *handoffEntryTracker) mark(event HandoffEntryAddedEvent) bool {
	key := handoffEventKey(event)
	if key == "" {
		return true
	}
	if _, exists := t.known[key]; exists {
		return false
	}
	t.known[key] = struct{}{}
	return true
}

func parseHandoffPage(w http.ResponseWriter, r *http.Request) (int64, int, bool) {
	afterID := int64(0)
	limit := store.HandoffHistoryMaxLimit
	if raw := r.URL.Query().Get("after_id"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, store.ErrHandoffHistoryCursorInvalid.Error())
			return 0, 0, false
		}
		afterID = parsed
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > store.HandoffHistoryMaxLimit {
			writeError(w, http.StatusBadRequest, store.ErrHandoffHistoryLimitInvalid.Error())
			return 0, 0, false
		}
		limit = parsed
	}
	return afterID, limit, true
}

func writeHandoffHistoryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrGoalHandoffNotFound), errors.Is(err, store.ErrTaskHandoffNotFound), errors.Is(err, store.ErrHandoffEntryNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrHandoffHistoryCursorInvalid), errors.Is(err, store.ErrHandoffHistoryLimitInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeStoreError(w, err)
	}
}

func newGoalHandoffView(projectID int64, handoff store.GoalHandoff, page store.HandoffEntryPage) HandoffView {
	return HandoffView{
		ID:                handoff.ID,
		Scope:             handoffEntryEventScopeGoal,
		ProjectID:         projectID,
		GoalID:            handoff.GoalID,
		RequestedBy:       handoff.RequestedBy,
		ReceivedBy:        handoff.ReceivedBy,
		RequestReport:     handoff.RequestReport,
		CompleteReport:    handoff.CompleteReport,
		RequestedAt:       handoff.RequestedAt,
		ReceivedAt:        handoff.ReceivedAt,
		CompletedReportAt: handoff.CompletedReportAt,
		Entries:           canonicalHandoffEntries(page.Entries),
		HasMore:           page.HasMore,
		NextAfterID:       page.NextCursor,
	}
}

func newTaskHandoffView(projectID, goalID int64, handoff store.TaskHandoff, page store.HandoffEntryPage) HandoffView {
	return HandoffView{
		ID:                handoff.ID,
		Scope:             handoffEntryEventScopeTask,
		ProjectID:         projectID,
		GoalID:            goalID,
		TaskID:            handoff.TaskID,
		RequestedBy:       handoff.RequestedBy,
		ReceivedBy:        handoff.ReceivedBy,
		RequestReport:     handoff.RequestReport,
		CompleteReport:    handoff.CompleteReport,
		RequestedAt:       handoff.RequestedAt,
		ReceivedAt:        handoff.ReceivedAt,
		CompletedReportAt: handoff.CompletedReportAt,
		Entries:           canonicalHandoffEntries(page.Entries),
		HasMore:           page.HasMore,
		NextAfterID:       page.NextCursor,
	}
}

func canonicalHandoffEntry(entry store.HandoffEntry) HandoffEntryView {
	return HandoffEntryView{
		ID:              entry.ID,
		HandoffID:       entry.HandoffID,
		Kind:            entry.Kind,
		Body:            entry.Body,
		AuthorSessionID: entry.AuthorSessionID,
		InReplyToID:     entry.InReplyToID,
		CreatedAt:       entry.CreatedAt,
	}
}

func canonicalHandoffEntries(entries []store.HandoffEntry) []HandoffEntryView {
	views := make([]HandoffEntryView, 0, len(entries))
	for _, entry := range entries {
		views = append(views, canonicalHandoffEntry(entry))
	}
	return views
}

func (s *Server) listGoalHandoffViews(ctx context.Context, goalID, projectID int64, afterID int64, limit int) ([]HandoffView, error) {
	handoffs, err := s.store.ListGoalHandoffs(ctx, goalID)
	if err != nil {
		return nil, err
	}
	views := make([]HandoffView, 0, len(handoffs))
	for _, handoff := range handoffs {
		page, err := s.store.ListGoalHandoffEntries(ctx, handoff.ID, afterID, limit)
		if err != nil {
			return nil, err
		}
		views = append(views, newGoalHandoffView(projectID, handoff, page))
	}
	return views, nil
}

func (s *Server) listTaskHandoffViews(ctx context.Context, taskID, goalID, projectID int64, afterID int64, limit int) ([]HandoffView, error) {
	handoffs, err := s.store.ListTaskHandoffs(ctx, taskID)
	if err != nil {
		return nil, err
	}
	views := make([]HandoffView, 0, len(handoffs))
	for _, handoff := range handoffs {
		page, err := s.store.ListTaskHandoffEntries(ctx, handoff.ID, afterID, limit)
		if err != nil {
			return nil, err
		}
		views = append(views, newTaskHandoffView(projectID, goalID, handoff, page))
	}
	return views, nil
}

func (s *Server) handleGoalHandoffs(w http.ResponseWriter, r *http.Request, goalID, handoffID string) {
	afterID, limit, ok := parseHandoffPage(w, r)
	if !ok {
		return
	}
	canonicalGoalID, ok := s.resolveGoalID(w, r.Context(), goalID)
	if !ok {
		return
	}
	goal, err := s.store.GetGoal(r.Context(), canonicalGoalID)
	if err != nil {
		writeHandoffHistoryError(w, err)
		return
	}
	if handoffID == "" {
		views, err := s.listGoalHandoffViews(r.Context(), canonicalGoalID, goal.ProjectID, afterID, limit)
		if err != nil {
			writeHandoffHistoryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, views)
		return
	}

	handoff, err := s.store.GetGoalHandoff(r.Context(), handoffID)
	if errors.Is(err, store.ErrGoalHandoffNotFound) || (err == nil && handoff.GoalID != canonicalGoalID) {
		writeError(w, http.StatusNotFound, store.ErrGoalHandoffNotFound.Error())
		return
	}
	if err != nil {
		writeHandoffHistoryError(w, err)
		return
	}
	page, err := s.store.ListGoalHandoffEntries(r.Context(), handoff.ID, afterID, limit)
	if err != nil {
		writeHandoffHistoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newGoalHandoffView(goal.ProjectID, handoff, page))
}

func (s *Server) handleTaskHandoffs(w http.ResponseWriter, r *http.Request, taskID, handoffID string) {
	afterID, limit, ok := parseHandoffPage(w, r)
	if !ok {
		return
	}
	canonicalTaskID, ok := s.resolveTaskID(w, r.Context(), taskID)
	if !ok {
		return
	}
	goalID, err := s.store.GetTaskGoalID(r.Context(), canonicalTaskID)
	if err != nil {
		writeHandoffHistoryError(w, err)
		return
	}
	goal, err := s.store.GetGoal(r.Context(), goalID)
	if err != nil {
		writeHandoffHistoryError(w, err)
		return
	}
	if handoffID == "" {
		views, err := s.listTaskHandoffViews(r.Context(), canonicalTaskID, goalID, goal.ProjectID, afterID, limit)
		if err != nil {
			writeHandoffHistoryError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, views)
		return
	}

	handoff, err := s.store.GetTaskHandoff(r.Context(), handoffID)
	if errors.Is(err, store.ErrTaskHandoffNotFound) || (err == nil && handoff.TaskID != canonicalTaskID) {
		writeError(w, http.StatusNotFound, store.ErrTaskHandoffNotFound.Error())
		return
	}
	if err != nil {
		writeHandoffHistoryError(w, err)
		return
	}
	page, err := s.store.ListTaskHandoffEntries(r.Context(), handoff.ID, afterID, limit)
	if err != nil {
		writeHandoffHistoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, newTaskHandoffView(goal.ProjectID, goalID, handoff, page))
}

func handoffEntryRecordForEntry(projectID, goalID, taskID int64, scope, handoffID string, parties []int64, entry store.HandoffEntry) handoffEntryRecord {
	return handoffEntryRecord{
		event: HandoffEntryAddedEvent{
			ProjectID:       projectID,
			Scope:           scope,
			GoalID:          goalID,
			TaskID:          taskID,
			HandoffID:       handoffID,
			ID:              entry.ID,
			Kind:            entry.Kind,
			AuthorSessionID: entry.AuthorSessionID,
			InReplyToID:     entry.InReplyToID,
			BodyPreview:     truncateHandoffEntryPreview(entry.Body),
			parties:         parties,
		},
		createdAt: entry.CreatedAt,
	}
}

// handoffParties returns the distinct non-zero session ids.
func handoffParties(ids ...int64) []int64 {
	parties := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id != 0 && !slices.Contains(parties, id) {
			parties = append(parties, id)
		}
	}
	return parties
}

func (s *Server) appendGoalHandoffEntryRecords(ctx context.Context, records *[]handoffEntryRecord, projectID int64, handoff store.GoalHandoff) error {
	parties := handoffParties(handoff.RequestedBy, handoff.ReceivedBy, handoff.ReviewRequestedBy, handoff.ReviewReceivedBy, handoff.ReviewRejectionReceivedBy)
	return s.appendHandoffEntryRecords(ctx, records, projectID, handoff.GoalID, 0, handoffEntryEventScopeGoal, handoff.ID, parties, func(afterID int64) (store.HandoffEntryPage, error) {
		return s.store.ListGoalHandoffEntries(ctx, handoff.ID, afterID, store.HandoffHistoryMaxLimit)
	})
}

func (s *Server) appendTaskHandoffEntryRecords(ctx context.Context, records *[]handoffEntryRecord, projectID, goalID int64, handoff store.TaskHandoff) error {
	parties := handoffParties(handoff.RequestedBy, handoff.ReceivedBy, handoff.ReviewRequestedBy, handoff.ReviewReceivedBy, handoff.ReviewRejectionReceivedBy)
	return s.appendHandoffEntryRecords(ctx, records, projectID, goalID, handoff.TaskID, handoffEntryEventScopeTask, handoff.ID, parties, func(afterID int64) (store.HandoffEntryPage, error) {
		return s.store.ListTaskHandoffEntries(ctx, handoff.ID, afterID, store.HandoffHistoryMaxLimit)
	})
}

func (s *Server) appendHandoffEntryRecords(ctx context.Context, records *[]handoffEntryRecord, projectID, goalID, taskID int64, scope, handoffID string, parties []int64, listPage func(int64) (store.HandoffEntryPage, error)) error {
	var afterID int64
	for {
		page, err := listPage(afterID)
		if err != nil {
			return err
		}
		for _, entry := range page.Entries {
			*records = append(*records, handoffEntryRecordForEntry(projectID, goalID, taskID, scope, handoffID, parties, entry))
		}
		if !page.HasMore {
			return nil
		}
		if page.NextCursor <= afterID {
			return fmt.Errorf("handoff history after_id did not advance for %s", handoffID)
		}
		afterID = page.NextCursor
	}
}

func (s *Server) scanHandoffEntryRecords(ctx context.Context, filter eventFilter) ([]handoffEntryRecord, error) {
	records := make([]handoffEntryRecord, 0)
	if filter.taskID != "" {
		goalID, err := s.store.GetTaskGoalID(ctx, filter.canonicalTaskID)
		if err != nil {
			return nil, err
		}
		goal, err := s.store.GetGoal(ctx, goalID)
		if err != nil {
			return nil, err
		}
		handoffs, err := s.store.ListTaskHandoffs(ctx, filter.canonicalTaskID)
		if err != nil {
			return nil, err
		}
		for _, handoff := range handoffs {
			if err := s.appendTaskHandoffEntryRecords(ctx, &records, goal.ProjectID, goalID, handoff); err != nil {
				return nil, err
			}
		}
		return sortHandoffEntryRecords(records), nil
	}

	goals, err := s.store.ListAllGoals(ctx)
	if err != nil {
		return nil, err
	}
	for _, goal := range goals {
		if filter.goalID != "" && goal.ID != filter.canonicalGoalID {
			continue
		}
		if filter.projectID != "" && goal.ProjectID != filter.canonicalProjectID {
			continue
		}
		goalHandoffs, err := s.store.ListGoalHandoffs(ctx, goal.ID)
		if err != nil {
			return nil, err
		}
		for _, handoff := range goalHandoffs {
			if err := s.appendGoalHandoffEntryRecords(ctx, &records, goal.ProjectID, handoff); err != nil {
				return nil, err
			}
		}
		tasks, err := s.store.ListTasks(ctx, goal.ID)
		if err != nil {
			return nil, err
		}
		for _, task := range tasks {
			taskHandoffs, err := s.store.ListTaskHandoffs(ctx, task.ID)
			if err != nil {
				return nil, err
			}
			for _, handoff := range taskHandoffs {
				if err := s.appendTaskHandoffEntryRecords(ctx, &records, goal.ProjectID, goal.ID, handoff); err != nil {
					return nil, err
				}
			}
		}
	}
	return sortHandoffEntryRecords(records), nil
}

func sortHandoffEntryRecords(records []handoffEntryRecord) []handoffEntryRecord {
	sort.SliceStable(records, func(i, j int) bool {
		left, right := records[i].event, records[j].event
		return left.ID < right.ID
	})
	return records
}
