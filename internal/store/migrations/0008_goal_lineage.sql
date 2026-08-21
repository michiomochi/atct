ALTER TABLE goals
  ADD COLUMN derived_from_goal_id TEXT REFERENCES goals(id);
