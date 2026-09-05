package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

const (
	HandoffEntryBodyMaxBytes = 16 * 1024
	HandoffHistoryMaxLimit   = 200
)

const (
	HandoffEntryKindRequest        = "request"
	HandoffEntryKindReceived       = "received"
	HandoffEntryKindProgress       = "progress"
	HandoffEntryKindQuestion       = "question"
	HandoffEntryKindAnswer         = "answer"
	HandoffEntryKindReviewRequest  = "review_request"
	HandoffEntryKindReviewResponse = "review_response"
	HandoffEntryKindComplete       = "complete"
	HandoffEntryKindAmend          = "amend"
	HandoffEntryKindSystem         = "system"
)

var (
	ErrHandoffEntryNotFound         = errors.New("handoff entry not found")
	ErrHandoffEntryKindInvalid      = errors.New("invalid handoff entry kind")
	ErrHandoffEntryBodyEmpty        = errors.New("handoff entry body is empty")
	ErrHandoffEntryBodyTooLarge     = errors.New("handoff entry body exceeds 16 KiB")
	ErrHandoffEntryBodyInvalidUTF8  = errors.New("handoff entry body is not valid UTF-8")
	ErrHandoffEntryParticipant      = errors.New("handoff entry author is not a thread participant")
	ErrHandoffEntryRelatesToInvalid = errors.New("handoff entry relates_to is invalid")
	ErrHandoffEntryTerminal         = errors.New("handoff thread is terminal")
	ErrHandoffHistoryLimitInvalid   = errors.New("handoff history limit must be between 1 and 200")
	ErrHandoffHistoryCursorInvalid  = errors.New("handoff history cursor must not be negative")
)

// HandoffEntry is an immutable entry in one task or goal handoff thread.
type HandoffEntry struct {
	EntryID         string    `json:"entry_id"`
	HandoffID       string    `json:"handoff_id"`
	Sequence        int64     `json:"sequence"`
	Kind            string    `json:"kind"`
	Body            string    `json:"body"`
	AuthorSessionID int64     `json:"author_session_id"`
	RelatesTo       string    `json:"relates_to,omitempty"`
	Source          string    `json:"source,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// TaskHandoffEntry and GoalHandoffEntry are kept as descriptive aliases for
// callers that work with one handoff scope at a time.
type TaskHandoffEntry = HandoffEntry
type GoalHandoffEntry = HandoffEntry

// HandoffEntryPage is a cursor page ordered from the oldest entry forward.
type HandoffEntryPage struct {
	Entries    []HandoffEntry `json:"entries"`
	HasMore    bool           `json:"has_more"`
	NextCursor int64          `json:"next_cursor"`
}

type HandoffHistory = HandoffEntryPage

func normalizeHandoffEntryBody(body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", ErrHandoffEntryBodyEmpty
	}
	if !utf8.ValidString(body) {
		return "", ErrHandoffEntryBodyInvalidUTF8
	}
	if len([]byte(body)) > HandoffEntryBodyMaxBytes {
		return "", ErrHandoffEntryBodyTooLarge
	}
	return body, nil
}

func validHandoffEntryKind(kind string) bool {
	switch kind {
	case HandoffEntryKindRequest,
		HandoffEntryKindReceived,
		HandoffEntryKindProgress,
		HandoffEntryKindQuestion,
		HandoffEntryKindAnswer,
		HandoffEntryKindReviewRequest,
		HandoffEntryKindReviewResponse,
		HandoffEntryKindComplete,
		HandoffEntryKindAmend,
		HandoffEntryKindSystem:
		return true
	default:
		return false
	}
}

func genericHandoffEntryKind(kind string) bool {
	switch kind {
	case HandoffEntryKindProgress, HandoffEntryKindQuestion, HandoffEntryKindAnswer,
		HandoffEntryKindReviewRequest, HandoffEntryKindReviewResponse:
		return true
	default:
		return false
	}
}

func validateHandoffEntryRequest(kind, body string) (string, error) {
	if !validHandoffEntryKind(kind) {
		return "", fmt.Errorf("%w: %q", ErrHandoffEntryKindInvalid, kind)
	}
	if !genericHandoffEntryKind(kind) {
		return "", fmt.Errorf("%w: %q is reserved for a handoff state transition", ErrHandoffEntryKindInvalid, kind)
	}
	return normalizeHandoffEntryBody(body)
}

func nullableEntryAuthor(id int64) sql.NullInt64 {
	return sql.NullInt64{Int64: id, Valid: id > 0}
}

func nullableEntryRelatesTo(value string) sql.NullString {
	value = strings.TrimSpace(value)
	return sql.NullString{String: value, Valid: value != ""}
}

func appendHandoffEntryTx(
	ctx context.Context,
	q *sqlcgen.Queries,
	table, handoffID, kind, body string,
	authorSessionID int64,
	relatesTo, source string,
	allowStateEntry bool,
	now time.Time,
) (HandoffEntry, error) {
	body, err := normalizeHandoffEntryBody(body)
	if err != nil {
		return HandoffEntry{}, err
	}
	if !validHandoffEntryKind(kind) {
		return HandoffEntry{}, fmt.Errorf("%w: %q", ErrHandoffEntryKindInvalid, kind)
	}

	var requestedBy, receivedBy sql.NullInt64
	var completedAt sql.NullString
	if table == "goal_handoff_entries" {
		state, err := q.GetGoalHandoffEntryState(ctx, handoffID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return HandoffEntry{}, fmt.Errorf("%w: %s", ErrHandoffEntryNotFound, handoffID)
			}
			return HandoffEntry{}, fmt.Errorf("find handoff %q for entry: %w", handoffID, err)
		}
		requestedBy, receivedBy, completedAt = state.RequestedBy, state.ReceivedBy, state.CompletedReportAt
	} else if table == "task_handoff_entries" {
		state, err := q.GetTaskHandoffEntryState(ctx, handoffID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return HandoffEntry{}, fmt.Errorf("%w: %s", ErrHandoffEntryNotFound, handoffID)
			}
			return HandoffEntry{}, fmt.Errorf("find handoff %q for entry: %w", handoffID, err)
		}
		requestedBy, receivedBy, completedAt = state.RequestedBy, state.ReceivedBy, state.CompletedReportAt
	} else {
		return HandoffEntry{}, fmt.Errorf("unknown handoff entry table %q", table)
	}

	if !allowStateEntry {
		if completedAt.Valid && completedAt.String != "" {
			if kind != HandoffEntryKindAmend || !receivedBy.Valid || receivedBy.Int64 != authorSessionID {
				return HandoffEntry{}, fmt.Errorf("%w: completed handoff only accepts an amend by received_by", ErrHandoffEntryParticipant)
			}
		} else if authorSessionID <= 0 ||
			(!requestedBy.Valid || requestedBy.Int64 != authorSessionID) &&
				(!receivedBy.Valid || receivedBy.Int64 != authorSessionID) {
			return HandoffEntry{}, fmt.Errorf("%w: author %d", ErrHandoffEntryParticipant, authorSessionID)
		}
	}

	if strings.TrimSpace(relatesTo) != "" {
		var count int64
		var err error
		if table == "goal_handoff_entries" {
			count, err = q.GoalHandoffEntryExists(ctx, sqlcgen.GoalHandoffEntryExistsParams{HandoffID: handoffID, EntryID: strings.TrimSpace(relatesTo)})
		} else {
			count, err = q.TaskHandoffEntryExists(ctx, sqlcgen.TaskHandoffEntryExistsParams{HandoffID: handoffID, EntryID: strings.TrimSpace(relatesTo)})
		}
		if err != nil {
			return HandoffEntry{}, fmt.Errorf("validate handoff entry relation: %w", err)
		}
		if count == 0 {
			return HandoffEntry{}, fmt.Errorf("%w: entry %q is not in handoff %q", ErrHandoffEntryRelatesToInvalid, relatesTo, handoffID)
		}
	}

	var sequence int64
	if table == "goal_handoff_entries" {
		sequence, err = q.MaxGoalHandoffEntrySequence(ctx, handoffID)
	} else {
		sequence, err = q.MaxTaskHandoffEntrySequence(ctx, handoffID)
	}
	if err != nil {
		return HandoffEntry{}, fmt.Errorf("allocate handoff entry sequence: %w", err)
	}
	entryID, err := uuid.NewV7()
	if err != nil {
		return HandoffEntry{}, fmt.Errorf("generate handoff entry id: %w", err)
	}
	createdAt := now.UTC().Format(time.RFC3339Nano)
	if table == "goal_handoff_entries" {
		err = q.CreateGoalHandoffEntry(ctx, sqlcgen.CreateGoalHandoffEntryParams{
			EntryID: entryID.String(), HandoffID: handoffID, Sequence: sequence, Kind: kind,
			Body: body, AuthorSessionID: nullableEntryAuthor(authorSessionID),
			RelatesTo: nullableEntryRelatesTo(relatesTo), Source: source, CreatedAt: createdAt,
		})
	} else {
		err = q.CreateTaskHandoffEntry(ctx, sqlcgen.CreateTaskHandoffEntryParams{
			EntryID: entryID.String(), HandoffID: handoffID, Sequence: sequence, Kind: kind,
			Body: body, AuthorSessionID: nullableEntryAuthor(authorSessionID),
			RelatesTo: nullableEntryRelatesTo(relatesTo), Source: source, CreatedAt: createdAt,
		})
	}
	if err != nil {
		return HandoffEntry{}, fmt.Errorf("insert handoff entry: %w", err)
	}
	return HandoffEntry{
		EntryID:         entryID.String(),
		HandoffID:       handoffID,
		Sequence:        sequence,
		Kind:            kind,
		Body:            body,
		AuthorSessionID: authorSessionID,
		RelatesTo:       strings.TrimSpace(relatesTo),
		Source:          source,
		CreatedAt:       now.UTC(),
	}, nil
}

func handoffEntryFromFields(entryID, handoffID string, sequence int64, kind, body string, author sql.NullInt64, relatesTo sql.NullString, source, createdAt string) (HandoffEntry, error) {
	entry := HandoffEntry{EntryID: entryID, HandoffID: handoffID, Sequence: sequence, Kind: kind, Body: body, Source: source}
	if author.Valid {
		entry.AuthorSessionID = author.Int64
	}
	if relatesTo.Valid {
		entry.RelatesTo = relatesTo.String
	}
	if createdAt == "" {
		return HandoffEntry{}, fmt.Errorf("handoff entry %q has no created_at", entry.EntryID)
	}
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return HandoffEntry{}, fmt.Errorf("parse handoff entry %q created_at: %w", entry.EntryID, err)
	}
	entry.CreatedAt = parsed
	return entry, nil
}

func trimHandoffEntryPage(entries []HandoffEntry, limit int) HandoffEntryPage {
	page := HandoffEntryPage{}
	if len(entries) > limit {
		page.HasMore = true
		entries = entries[:limit]
	}
	page.Entries = entries
	if len(entries) > 0 {
		page.NextCursor = entries[len(entries)-1].Sequence
	}
	return page
}

func listHandoffEntries(ctx context.Context, q *sqlcgen.Queries, table, handoffID string, cursor int64, limit int) (HandoffEntryPage, error) {
	if cursor < 0 {
		return HandoffEntryPage{}, ErrHandoffHistoryCursorInvalid
	}
	if limit < 1 || limit > HandoffHistoryMaxLimit {
		return HandoffEntryPage{}, ErrHandoffHistoryLimitInvalid
	}
	switch table {
	case "goal_handoff_entries":
		if _, err := q.GetGoalHandoffEntryState(ctx, handoffID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return HandoffEntryPage{}, fmt.Errorf("%w: %s", ErrHandoffEntryNotFound, handoffID)
			}
			return HandoffEntryPage{}, fmt.Errorf("find goal handoff for history: %w", err)
		}
	case "task_handoff_entries":
		if _, err := q.GetTaskHandoffEntryState(ctx, handoffID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return HandoffEntryPage{}, fmt.Errorf("%w: %s", ErrHandoffEntryNotFound, handoffID)
			}
			return HandoffEntryPage{}, fmt.Errorf("find task handoff for history: %w", err)
		}
	default:
		return HandoffEntryPage{}, fmt.Errorf("unknown handoff entry table %q", table)
	}
	entries := make([]HandoffEntry, 0, limit)
	if table == "goal_handoff_entries" {
		rows, err := q.ListGoalHandoffEntries(ctx, sqlcgen.ListGoalHandoffEntriesParams{HandoffID: handoffID, Cursor: cursor, Limit: int64(limit + 1)})
		if err != nil {
			return HandoffEntryPage{}, fmt.Errorf("list %s for handoff %q: %w", table, handoffID, err)
		}
		for _, row := range rows {
			entry, err := handoffEntryFromFields(row.EntryID, row.HandoffID, row.Sequence, row.Kind, row.Body, row.AuthorSessionID, row.RelatesTo, row.Source, row.CreatedAt)
			if err != nil {
				return HandoffEntryPage{}, fmt.Errorf("scan %s: %w", table, err)
			}
			entries = append(entries, entry)
		}
	} else if table == "task_handoff_entries" {
		rows, err := q.ListTaskHandoffEntries(ctx, sqlcgen.ListTaskHandoffEntriesParams{HandoffID: handoffID, Cursor: cursor, Limit: int64(limit + 1)})
		if err != nil {
			return HandoffEntryPage{}, fmt.Errorf("list %s for handoff %q: %w", table, handoffID, err)
		}
		for _, row := range rows {
			entry, err := handoffEntryFromFields(row.EntryID, row.HandoffID, row.Sequence, row.Kind, row.Body, row.AuthorSessionID, row.RelatesTo, row.Source, row.CreatedAt)
			if err != nil {
				return HandoffEntryPage{}, fmt.Errorf("scan %s: %w", table, err)
			}
			entries = append(entries, entry)
		}
	}
	return trimHandoffEntryPage(entries, limit), nil
}

func (s *Store) AppendTaskHandoffEntry(ctx context.Context, handoffID, kind, body string, authorSessionID int64, relatesTo string) (TaskHandoffEntry, error) {
	body, err := validateHandoffEntryRequest(kind, body)
	if err != nil {
		return TaskHandoffEntry{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskHandoffEntry{}, fmt.Errorf("begin task handoff entry tx: %w", err)
	}
	defer tx.Rollback()
	entry, err := appendHandoffEntryTx(ctx, sqlcgen.New(tx), "task_handoff_entries", handoffID, kind, body, authorSessionID, relatesTo, "", false, time.Now().UTC())
	if err != nil {
		return TaskHandoffEntry{}, err
	}
	if err := tx.Commit(); err != nil {
		return TaskHandoffEntry{}, fmt.Errorf("commit task handoff entry: %w", err)
	}
	entry.Body = body
	return entry, nil
}

func (s *Store) AppendGoalHandoffEntry(ctx context.Context, handoffID, kind, body string, authorSessionID int64, relatesTo string) (GoalHandoffEntry, error) {
	body, err := validateHandoffEntryRequest(kind, body)
	if err != nil {
		return GoalHandoffEntry{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return GoalHandoffEntry{}, fmt.Errorf("begin goal handoff entry tx: %w", err)
	}
	defer tx.Rollback()
	entry, err := appendHandoffEntryTx(ctx, sqlcgen.New(tx), "goal_handoff_entries", handoffID, kind, body, authorSessionID, relatesTo, "", false, time.Now().UTC())
	if err != nil {
		return GoalHandoffEntry{}, err
	}
	if err := tx.Commit(); err != nil {
		return GoalHandoffEntry{}, fmt.Errorf("commit goal handoff entry: %w", err)
	}
	entry.Body = body
	return entry, nil
}

func (s *Store) ListTaskHandoffEntries(ctx context.Context, handoffID string, cursor int64, limit int) (HandoffEntryPage, error) {
	return listHandoffEntries(ctx, sqlcgen.New(s.db), "task_handoff_entries", handoffID, cursor, limit)
}

func (s *Store) ListGoalHandoffEntries(ctx context.Context, handoffID string, cursor int64, limit int) (HandoffEntryPage, error) {
	return listHandoffEntries(ctx, sqlcgen.New(s.db), "goal_handoff_entries", handoffID, cursor, limit)
}
