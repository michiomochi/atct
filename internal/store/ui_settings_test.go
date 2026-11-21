package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestUISettingsDefaultsToEnglish(t *testing.T) {
	s := newTestStore(t)

	got, err := s.GetUILocale(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "en" {
		t.Fatalf("locale = %q, want en", got)
	}
}

func TestUISettingsSetAndReadJapanese(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SetUILocale(ctx, "ja"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetUILocale(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ja" {
		t.Fatalf("locale = %q, want ja", got)
	}
}

func TestUISettingsRejectsUnsupportedWithoutChangingValue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.SetUILocale(ctx, "ja"); err != nil {
		t.Fatal(err)
	}

	err := s.SetUILocale(ctx, "fr")
	if !errors.Is(err, ErrUnsupportedUILocale) {
		t.Fatalf("SetUILocale error = %v, want %v", err, ErrUnsupportedUILocale)
	}
	got, err := s.GetUILocale(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ja" {
		t.Fatalf("locale after rejected write = %q, want ja", got)
	}
}

func TestUISettingsMissingRowFallsBackToEnglish(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.DB().Exec(`DELETE FROM ui_settings WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetUILocale(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "en" {
		t.Fatalf("locale without singleton row = %q, want en", got)
	}
	if err := s.SetUILocale(context.Background(), "ja"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("SetUILocale without singleton row error = %v, want %v", err, sql.ErrNoRows)
	}
}
