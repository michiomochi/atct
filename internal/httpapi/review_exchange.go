package httpapi

import (
	"errors"
	"net/http"

	"github.com/michiomochi/atct/internal/store"
)

// handleReviewExchanges serves a goal's review exchanges (taskID == "") or one
// task's. Read-only, like the goal and task GETs it sits beside.
func (s *Server) handleReviewExchanges(w http.ResponseWriter, r *http.Request, goalID, taskID string) {
	ctx := r.Context()
	var canonicalGoalID, canonicalTaskID int64
	var ok bool
	if taskID == "" {
		if canonicalGoalID, ok = s.resolveGoalID(w, ctx, goalID); !ok {
			return
		}
	} else {
		if canonicalTaskID, ok = s.resolveTaskID(w, ctx, taskID); !ok {
			return
		}
		var err error
		if canonicalGoalID, err = s.store.GetTaskGoalID(ctx, canonicalTaskID); err != nil {
			writeReviewExchangeError(w, err)
			return
		}
	}
	history, err := s.store.ListReviewExchanges(ctx, canonicalGoalID, canonicalTaskID)
	if err != nil {
		writeReviewExchangeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, history)
}

func writeReviewExchangeError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrGoalNotFound) || errors.Is(err, store.ErrTaskNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeStoreError(w, err)
}
