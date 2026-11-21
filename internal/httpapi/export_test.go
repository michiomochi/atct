package httpapi

// HandoffEntryScans reports how many times the event streams scanned handoff
// entries on this server.
func (s *Server) HandoffEntryScans() int64 { return s.handoffEntryScans.Load() }
