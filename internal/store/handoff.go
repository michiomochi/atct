package store

// handoffIsDelegation reports whether a handoff records a delegation rather
// than the self-directed claim lock created by atct_task_claim. Unknown
// session identities remain reportable so an incomplete record is not hidden.
func handoffIsDelegation(requestedBy, receivedBy int64) bool {
	return requestedBy == 0 || receivedBy == 0 || requestedBy != receivedBy
}
