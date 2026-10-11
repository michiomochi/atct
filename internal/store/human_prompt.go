package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

// humanPromptMaxRunes caps a stored prompt. The text is only used to match a
// chat answer, so the head of a long prompt is enough.
const humanPromptMaxRunes = 8000

type HumanPromptInput struct {
	SessionKey     string
	PromptID       string
	TranscriptPath string
	Prompt         string
	RecordedAt     time.Time
}

type HumanPrompt struct {
	ID             int64
	SessionKey     string
	PromptID       string
	TranscriptPath string
	Prompt         string
	RecordedAt     time.Time
}

// RecordHumanPrompt stores a human prompt and keeps only the newest 50 prompts
// for its session (PruneHumanPrompts). The first prompt that names a transcript
// pins that path for the session (PinHumanPromptTranscript); later prompts cannot
// change it.
func (s *Store) RecordHumanPrompt(ctx context.Context, in HumanPromptInput) error {
	if in.RecordedAt.IsZero() {
		in.RecordedAt = time.Now()
	}
	runes := []rune(in.Prompt)
	if len(runes) > humanPromptMaxRunes {
		in.Prompt = string(runes[:humanPromptMaxRunes])
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin human prompt tx: %w", err)
	}
	defer tx.Rollback()
	q := sqlcgen.New(tx)
	if err := q.InsertHumanPrompt(ctx, sqlcgen.InsertHumanPromptParams{
		SessionKey:     in.SessionKey,
		PromptID:       in.PromptID,
		TranscriptPath: in.TranscriptPath,
		Prompt:         in.Prompt,
		RecordedAt:     formatTimestamp(in.RecordedAt),
	}); err != nil {
		return fmt.Errorf("insert human prompt: %w", err)
	}
	if err := q.PruneHumanPrompts(ctx, sqlcgen.PruneHumanPromptsParams{
		SessionKey:   in.SessionKey,
		SessionKey_2: in.SessionKey,
	}); err != nil {
		return fmt.Errorf("prune human prompts: %w", err)
	}
	if in.TranscriptPath != "" {
		if err := q.PinHumanPromptTranscript(ctx, sqlcgen.PinHumanPromptTranscriptParams{
			SessionKey:      in.SessionKey,
			TranscriptPath:  in.TranscriptPath,
			FirstRecordedAt: formatTimestamp(in.RecordedAt),
		}); err != nil {
			return fmt.Errorf("pin human prompt transcript: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit human prompt: %w", err)
	}
	return nil
}

// PinnedTranscript returns the transcript path the session's first recorded prompt
// named, or "" when no prompt that named a transcript has been recorded.
func (s *Store) PinnedTranscript(ctx context.Context, sessionKey string) (string, error) {
	path, err := sqlcgen.New(s.db).GetPinnedTranscript(ctx, sessionKey)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get pinned transcript: %w", err)
	}
	return path, nil
}

// HumanPromptsSince returns the session's prompts recorded at or after since,
// newest first.
func (s *Store) HumanPromptsSince(ctx context.Context, sessionKey string, since time.Time) ([]HumanPrompt, error) {
	rows, err := sqlcgen.New(s.db).ListHumanPromptsSince(ctx, sqlcgen.ListHumanPromptsSinceParams{
		SessionKey: sessionKey,
		RecordedAt: formatTimestamp(since),
	})
	if err != nil {
		return nil, fmt.Errorf("list human prompts: %w", err)
	}
	out := make([]HumanPrompt, 0, len(rows))
	for _, row := range rows {
		recordedAt, err := time.Parse(timestampLayout, row.RecordedAt)
		if err != nil {
			return nil, fmt.Errorf("parse human prompt recorded_at: %w", err)
		}
		out = append(out, HumanPrompt{
			ID:             row.ID,
			SessionKey:     row.SessionKey,
			PromptID:       row.PromptID,
			TranscriptPath: row.TranscriptPath,
			Prompt:         row.Prompt,
			RecordedAt:     recordedAt,
		})
	}
	return out, nil
}
