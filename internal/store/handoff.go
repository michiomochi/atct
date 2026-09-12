package store

// handoffIsDelegation reports whether a handoff records a delegation rather
// than a same-session recovery lock. Unknown
// session identities remain reportable so an incomplete record is not hidden.
func handoffIsDelegation(requestedBy, receivedBy int64) bool {
	return requestedBy == 0 || receivedBy == 0 || requestedBy != receivedBy
}
