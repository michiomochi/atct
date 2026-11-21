CREATE TABLE ui_settings (
  id     INTEGER PRIMARY KEY CHECK (id = 1),
  locale TEXT NOT NULL CHECK (locale IN ('en', 'ja'))
);

INSERT INTO ui_settings (id, locale)
VALUES (1, 'en');
