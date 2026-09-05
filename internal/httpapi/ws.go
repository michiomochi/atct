package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/michiomochi/atct/internal/store"
)

type wsEventFrame struct {
	Name string `json:"name"`
	Data any    `json:"data"`
	ID   string `json:"id,omitempty"`
}

func (s *Server) handleWebSocketEvents(w http.ResponseWriter, r *http.Request) {
	filter, ok := s.parseEventFilter(w, r)
	if !ok {
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	baselineAt := time.Now().UTC()
	ctx := conn.CloseRead(r.Context())
	ch, cancel := s.store.SubscribeEvents()
	defer cancel()
	records, err := s.scanHandoffEntryRecords(ctx, filter)
	if err != nil {
		return
	}
	tracker := newHandoffEntryTracker(records, r.Header.Get("Last-Event-ID"), baselineAt)
	ticker := time.NewTicker(handoffEntryPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-ch:
			var eventID string
			if event.Name == EventHandoffEntryAdded {
				data, ok := normalizeHandoffEntryEvent(event.Data)
				if !ok {
					continue
				}
				event.Data = data
				eventID = data.EntryID
				if !tracker.mark(data) {
					continue
				}
			}
			if !s.eventPasses(ctx, filter, event) {
				continue
			}
			payload, err := json.Marshal(wsEventFrame{Name: event.Name, Data: event.Data, ID: eventID})
			if err != nil {
				return
			}
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = conn.Write(writeCtx, websocket.MessageText, payload)
			cancel()
			if err != nil {
				return
			}
		case <-ticker.C:
			records, err := s.scanHandoffEntryRecords(ctx, filter)
			if err != nil {
				continue
			}
			for _, record := range records {
				if !tracker.mark(record.event) {
					continue
				}
				event := store.DecisionEvent{Name: EventHandoffEntryAdded, Data: record.event}
				if !s.eventPasses(ctx, filter, event) {
					continue
				}
				payload, err := json.Marshal(wsEventFrame{Name: event.Name, Data: event.Data, ID: record.event.EntryID})
				if err != nil {
					return
				}
				writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				err = conn.Write(writeCtx, websocket.MessageText, payload)
				cancel()
				if err != nil {
					return
				}
			}
		}
	}
}
