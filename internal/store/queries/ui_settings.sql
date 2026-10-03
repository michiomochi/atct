-- name: GetUILocale :one
SELECT locale
FROM ui_settings
WHERE id = 1;

-- name: SetUILocale :execresult
UPDATE ui_settings
SET locale = ?
WHERE id = 1;
