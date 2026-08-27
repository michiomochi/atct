package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

type wsEventFrame struct {
	Name string `json:"name"`
	Data any    `json:"data"`
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

	ctx := conn.CloseRead(r.Context())
	ch, cancel := s.store.SubscribeEvents()
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-ch:
			if !s.eventPasses(ctx, filter, event) {
				continue
			}
			payload, err := json.Marshal(wsEventFrame{Name: event.Name, Data: event.Data})
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
