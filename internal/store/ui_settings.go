package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/michiomochi/atct/internal/store/sqlcgen"
)

const defaultUILocale = "en"

var ErrUnsupportedUILocale = errors.New("unsupported UI locale")

func (s *Store) GetUILocale(ctx context.Context) (string, error) {
	locale, err := sqlcgen.New(s.db).GetUILocale(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return defaultUILocale, nil
		}
		return "", fmt.Errorf("get UI locale: %w", err)
	}
	if !isSupportedUILocale(locale) {
		return defaultUILocale, nil
	}
	return locale, nil
}

func (s *Store) SetUILocale(ctx context.Context, locale string) error {
	if !isSupportedUILocale(locale) {
		return fmt.Errorf("%w: %q", ErrUnsupportedUILocale, locale)
	}

	result, err := sqlcgen.New(s.db).SetUILocale(ctx, locale)
	if err != nil {
		return fmt.Errorf("set UI locale: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect UI locale update: %w", err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func isSupportedUILocale(locale string) bool {
	return locale == "en" || locale == "ja"
}
