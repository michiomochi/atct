package store

import "fmt"

const completionReportMaxLength = 2000

const completionReportCheckFormat = `CHECK (
  status <> 'done' OR (
    length(trim(work_done)) > 0 AND length(work_done) <= %[1]d AND
    length(trim(now_possible)) > 0 AND length(now_possible) <= %[1]d AND
    length(trim(how_to_verify)) > 0 AND length(how_to_verify) <= %[1]d AND
    length(trim(surprises)) > 0 AND length(surprises) <= %[1]d AND
    length(trim(needs_review)) > 0 AND length(needs_review) <= %[1]d AND
    length(trim(next_steps)) > 0 AND length(next_steps) <= %[1]d
  )
)`

func completionReportCheckSQL() string {
	return fmt.Sprintf(completionReportCheckFormat, completionReportMaxLength)
}
