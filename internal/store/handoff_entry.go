package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

const (
	HandoffEntryBodyMaxBytes = 16 * 1024
	HandoffHistoryMaxLimit   = 200
)

const (
	HandoffEntryKindRequest         = "request"
	HandoffEntryKindReceived        = "received"
	HandoffEntryKindReviewRequested = "review_requested"
	HandoffEntryKindReviewReceived  = "review_received"
	HandoffEntryKindReviewRejected  = "review_rejected"
	HandoffEntryKindCompleted       = "completed"

	// Deprecated names are retained for source compatibility while callers
	// move to the canonical entry contract. Their values are canonical kinds;
	// the removed wire kinds remain invalid below.
	HandoffEntryKindReviewRequest  = HandoffEntryKindReviewRequested
	HandoffEntryKindReviewResponse = HandoffEntryKindReviewReceived
	HandoffEntryKindComplete       = HandoffEntryKindCompleted

	HandoffEntryKindProgress = "progress"
	HandoffEntryKindQuestion = "question"
	HandoffEntryKindAnswer   = "answer"
	HandoffEntryKindAmend    = "amend"
	HandoffEntryKindSystem   = "system"
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

// HandoffEntry is a persisted entry in one task or goal handoff thread.
//
// ID, HandoffID, Kind, Body, AuthorSessionID, InReplyToID, and CreatedAt are
// the canonical fields. The deprecated aliases are populated on reads and
// writes so older callers can be cut over independently.
type HandoffEntry struct {
	ID              int64     `json:"id"`
	HandoffID       string    `json:"handoff_id"`
	Kind            string    `json:"kind"`
	Body            string    `json:"body"`
	AuthorSessionID int64     `json:"author_session_id"`
	InReplyToID     *int64    `json:"in_reply_to_id,omitempty"`
	CreatedAt       time.Time `json:"created_at"`

	// Deprecated: use ID. Kept for downstream caller compatibility.
	EntryID string `json:"entry_id"`
	// Deprecated: use ID as the cursor. Kept for downstream caller compatibility.
	Sequence int64 `json:"sequence"`
	// Deprecated: use InReplyToID. Kept for downstream caller compatibility.
	RelatesTo string `json:"relates_to,omitempty"`
	// Deprecated: entry source is no longer persisted.
	Source string `json:"source,omitempty"`
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
		HandoffEntryKindReviewRequested,
		HandoffEntryKindReviewReceived,
		HandoffEntryKindReviewRejected,
		HandoffEntryKindCompleted:
		return true
	default:
		return false
	}
}

func genericHandoffEntryKind(kind string) bool {
	switch kind {
	case HandoffEntryKindReviewRequested, HandoffEntryKindReviewReceived, HandoffEntryKindReviewRejected:
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

func nullableEntryReply(value string) (sql.NullInt64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return sql.NullInt64{}, nil
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return sql.NullInt64{}, fmt.Errorf("%w: %q is not a positive integer id", ErrHandoffEntryRelatesToInvalid, value)
	}
	return sql.NullInt64{Int64: id, Valid: true}, nil
}

// canonicalInternalHandoffEntryKind adapts state-transition callers that have
// not yet been cut over. Removed amendment/system entries have no canonical
// representation and are intentionally ignored; their parent handoff update
// remains the compatibility record until those callers are migrated.
func canonicalInternalHandoffEntryKind(kind string) (canonical string, skip bool, err error) {
	switch kind {
	case HandoffEntryKindAmend, HandoffEntryKindSystem:
		return "", true, nil
	case "complete":
		return HandoffEntryKindCompleted, false, nil
	case "review_request":
		return HandoffEntryKindReviewRequested, false, nil
	case "review_response":
		return HandoffEntryKindReviewReceived, false, nil
	default:
		if !validHandoffEntryKind(kind) {
			return "", false, fmt.Errorf("%w: %q", ErrHandoffEntryKindInvalid, kind)
		}
		return kind, false, nil
	}
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
	canonicalKind, skip, err := canonicalInternalHandoffEntryKind(kind)
	if err != nil {
		return HandoffEntry{}, err
	}
	if skip {
		return HandoffEntry{}, nil
	}
	body, err = normalizeHandoffEntryBody(body)
	if err != nil {
		return HandoffEntry{}, err
	}
	// source is a compatibility parameter. It is no longer persisted.
	_ = source

	var requestedBy, receivedBy sql.NullInt64
	var completedAt sql.NullString
	if table == "goal_handoff_entries" {
		state, stateErr := q.GetGoalHandoffEntryState(ctx, handoffID)
		if stateErr != nil {
			if errors.Is(stateErr, sql.ErrNoRows) {
				return HandoffEntry{}, fmt.Errorf("%w: %s", ErrHandoffEntryNotFound, handoffID)
			}
			return HandoffEntry{}, fmt.Errorf("find handoff %q for entry: %w", handoffID, stateErr)
		}
		requestedBy, receivedBy, completedAt = state.RequestedBy, state.ReceivedBy, state.CompletedReportAt
	} else if table == "task_handoff_entries" {
		state, stateErr := q.GetTaskHandoffEntryState(ctx, handoffID)
		if stateErr != nil {
			if errors.Is(stateErr, sql.ErrNoRows) {
				return HandoffEntry{}, fmt.Errorf("%w: %s", ErrHandoffEntryNotFound, handoffID)
			}
			return HandoffEntry{}, fmt.Errorf("find handoff %q for entry: %w", handoffID, stateErr)
		}
		requestedBy, receivedBy, completedAt = state.RequestedBy, state.ReceivedBy, state.CompletedReportAt
	} else {
		return HandoffEntry{}, fmt.Errorf("unknown handoff entry table %q", table)
	}

	if !allowStateEntry {
		if completedAt.Valid && strings.TrimSpace(completedAt.String) != "" {
			return HandoffEntry{}, ErrHandoffEntryTerminal
		}
		if authorSessionID <= 0 ||
			(!requestedBy.Valid || requestedBy.Int64 != authorSessionID) &&
				(!receivedBy.Valid || receivedBy.Int64 != authorSessionID) {
			return HandoffEntry{}, fmt.Errorf("%w: author %d", ErrHandoffEntryParticipant, authorSessionID)
		}
	}

	inReplyToID, err := nullableEntryReply(relatesTo)
	if err != nil {
		return HandoffEntry{}, err
	}
	if inReplyToID.Valid {
		var count int64
		if table == "goal_handoff_entries" {
			count, err = q.GoalHandoffEntryExists(ctx, sqlcgen.GoalHandoffEntryExistsParams{HandoffID: handoffID, ID: inReplyToID.Int64})
		} else {
			count, err = q.TaskHandoffEntryExists(ctx, sqlcgen.TaskHandoffEntryExistsParams{HandoffID: handoffID, ID: inReplyToID.Int64})
		}
		if err != nil {
			return HandoffEntry{}, fmt.Errorf("validate handoff entry relation: %w", err)
		}
		if count == 0 {
			return HandoffEntry{}, fmt.Errorf("%w: entry %d is not in handoff %q", ErrHandoffEntryRelatesToInvalid, inReplyToID.Int64, handoffID)
		}
	}

	createdAt := now.UTC().Format(time.RFC3339Nano)
	var id int64
	if table == "goal_handoff_entries" {
		id, err = q.CreateGoalHandoffEntry(ctx, sqlcgen.CreateGoalHandoffEntryParams{
			HandoffID: handoffID, Kind: canonicalKind, Body: body,
			AuthorSessionID: nullableEntryAuthor(authorSessionID), InReplyToID: inReplyToID,
			CreatedAt: createdAt,
		})
	} else {
		id, err = q.CreateTaskHandoffEntry(ctx, sqlcgen.CreateTaskHandoffEntryParams{
			HandoffID: handoffID, Kind: canonicalKind, Body: body,
			AuthorSessionID: nullableEntryAuthor(authorSessionID), InReplyToID: inReplyToID,
			CreatedAt: createdAt,
		})
	}
	if err != nil {
		return HandoffEntry{}, fmt.Errorf("insert handoff entry: %w", err)
	}
	return newHandoffEntry(id, handoffID, canonicalKind, body, authorSessionID, inReplyToID, now.UTC()), nil
}

func newHandoffEntry(id int64, handoffID, kind, body string, authorSessionID int64, inReplyToID sql.NullInt64, createdAt time.Time) HandoffEntry {
	entry := HandoffEntry{
		ID:              id,
		HandoffID:       handoffID,
		Kind:            kind,
		Body:            body,
		AuthorSessionID: authorSessionID,
		CreatedAt:       createdAt,
		EntryID:         strconv.FormatInt(id, 10),
		Sequence:        id,
	}
	if inReplyToID.Valid {
		replyID := inReplyToID.Int64
		entry.InReplyToID = &replyID
		entry.RelatesTo = strconv.FormatInt(replyID, 10)
	}
	return entry
}

func handoffEntryFromFields(id int64, handoffID, kind, body string, author, inReplyTo sql.NullInt64, createdAt string) (HandoffEntry, error) {
	if id <= 0 {
		return HandoffEntry{}, fmt.Errorf("handoff entry has invalid id %d", id)
	}
	if createdAt == "" {
		return HandoffEntry{}, fmt.Errorf("handoff entry %d has no created_at", id)
	}
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return HandoffEntry{}, fmt.Errorf("parse handoff entry %d created_at: %w", id, err)
	}
	var authorID int64
	if author.Valid {
		authorID = author.Int64
	}
	return newHandoffEntry(id, handoffID, kind, body, authorID, inReplyTo, parsed), nil
}

func trimHandoffEntryPage(entries []HandoffEntry, limit int) HandoffEntryPage {
	page := HandoffEntryPage{}
	if len(entries) > limit {
		page.HasMore = true
		entries = entries[:limit]
	}
	page.Entries = entries
	if len(entries) > 0 {
		page.NextCursor = entries[len(entries)-1].ID
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
			entry, err := handoffEntryFromFields(row.ID, row.HandoffID, row.Kind, row.Body, row.AuthorSessionID, row.InReplyToID, row.CreatedAt)
			if err != nil {
				return HandoffEntryPage{}, fmt.Errorf("scan %s: %w", table, err)
			}
			entries = append(entries, entry)
		}
	} else {
		rows, err := q.ListTaskHandoffEntries(ctx, sqlcgen.ListTaskHandoffEntriesParams{HandoffID: handoffID, Cursor: cursor, Limit: int64(limit + 1)})
		if err != nil {
			return HandoffEntryPage{}, fmt.Errorf("list %s for handoff %q: %w", table, handoffID, err)
		}
		for _, row := range rows {
			entry, err := handoffEntryFromFields(row.ID, row.HandoffID, row.Kind, row.Body, row.AuthorSessionID, row.InReplyToID, row.CreatedAt)
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
	return entry, nil
}

func (s *Store) ListTaskHandoffEntries(ctx context.Context, handoffID string, cursor int64, limit int) (HandoffEntryPage, error) {
	return listHandoffEntries(ctx, sqlcgen.New(s.db), "task_handoff_entries", handoffID, cursor, limit)
}

func (s *Store) ListGoalHandoffEntries(ctx context.Context, handoffID string, cursor int64, limit int) (HandoffEntryPage, error) {
	return listHandoffEntries(ctx, sqlcgen.New(s.db), "goal_handoff_entries", handoffID, cursor, limit)
}
