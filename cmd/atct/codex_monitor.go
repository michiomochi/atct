package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

const (
	codexAppServerURL             = "ws://127.0.0.1/"
	codexThreadDiscoveryInterval  = 100 * time.Millisecond
	codexThreadDiscoveryTimeout   = 30 * time.Second
	codexMonitorClientName        = "atct-codex-monitor"
	codexAppServerClosedMessage   = "codex app server connection closed"
	codexAppServerMessageMaxBytes = 128 << 20
)

var errCodexAppServerClosed = errors.New(codexAppServerClosedMessage)

type codexWebSocket interface {
	Read(context.Context) (websocket.MessageType, []byte, error)
	Write(context.Context, websocket.MessageType, []byte) error
	Close(websocket.StatusCode, string) error
}

type codexDialContext func(context.Context, string, string) (net.Conn, error)

func newCodexAppServerHTTPClient(socketPath string) *http.Client {
	return newCodexAppServerHTTPClientWithDialer(socketPath, nil)
}

func newCodexAppServerHTTPClientWithDialer(socketPath string, dialer codexDialContext) *http.Client {
	if dialer == nil {
		netDialer := &net.Dialer{}
		dialer = netDialer.DialContext
	}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _ string, _ string) (net.Conn, error) {
			return dialer(ctx, "unix", socketPath)
		},
	}
	return &http.Client{Transport: transport}
}

func newCodexAppServer(ctx context.Context, socketPath string) (*codexAppServer, error) {
	if strings.TrimSpace(socketPath) == "" {
		return nil, errors.New("app server socket path is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	client := newCodexAppServerHTTPClient(socketPath)
	conn, _, err := websocket.Dial(ctx, codexAppServerURL, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		return nil, fmt.Errorf("dial app server socket %q: %w", socketPath, err)
	}
	return newCodexAppServerWithLifetime(ctx, ctx, conn), nil
}

func newCodexAppServerWithLifetime(_ context.Context, lifetimeCtx context.Context, conn codexWebSocket) *codexAppServer {
	return newCodexAppServerWithConn(lifetimeCtx, conn)
}

func dialCodexAppServer(ctx, lifetimeCtx context.Context, socketPath string) (*codexAppServer, error) {
	if strings.TrimSpace(socketPath) == "" {
		return nil, errors.New("app server socket path is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	client := newCodexAppServerHTTPClient(socketPath)
	conn, _, err := websocket.Dial(ctx, codexAppServerURL, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		return nil, fmt.Errorf("dial app server socket %q: %w", socketPath, err)
	}
	return newCodexAppServerWithLifetime(ctx, lifetimeCtx, conn), nil
}

type codexRPCRequest struct {
	ID     *int64 `json:"id,omitempty"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type codexRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type codexRPCMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *codexRPCError  `json:"error"`
}

type codexAppServerNotification struct {
	Method string
	Params json.RawMessage
}

type codexRPCResponse struct {
	Result json.RawMessage
	Error  *codexRPCError
}

type codexAppServer struct {
	ctx    context.Context
	cancel context.CancelFunc
	conn   codexWebSocket

	writeMu sync.Mutex
	nextID  atomic.Int64

	pendingMu sync.Mutex
	pending   map[int64]chan codexRPCResponse

	notifications chan codexAppServerNotification
	done          chan struct{}
	closeOnce     sync.Once

	stateMu sync.Mutex
	err     error
}

func newCodexAppServerWithConn(ctx context.Context, conn codexWebSocket) *codexAppServer {
	if ctx == nil {
		ctx = context.Background()
	}
	appCtx, cancel := context.WithCancel(ctx)
	app := &codexAppServer{
		ctx:           appCtx,
		cancel:        cancel,
		conn:          conn,
		pending:       make(map[int64]chan codexRPCResponse),
		notifications: make(chan codexAppServerNotification, 128),
		done:          make(chan struct{}),
	}
	if conn == nil {
		app.fail(errors.New("app server WebSocket connection is nil"))
		return app
	}
	go app.readLoop()
	return app
}

func (c *codexAppServer) readLoop() {
	for {
		messageType, payload, err := c.conn.Read(c.ctx)
		if err != nil {
			if c.ctx.Err() != nil {
				c.fail(errCodexAppServerClosed)
			} else {
				c.fail(fmt.Errorf("read app server message: %w", err))
			}
			return
		}
		if messageType != websocket.MessageText {
			c.fail(fmt.Errorf("app server sent non-text websocket message: %v", messageType))
			return
		}
		if len(payload) > codexAppServerMessageMaxBytes {
			c.fail(fmt.Errorf("app server message exceeds %d bytes", codexAppServerMessageMaxBytes))
			return
		}
		var message codexRPCMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			c.fail(fmt.Errorf("decode app server message: %w", err))
			return
		}
		if message.Method != "" {
			select {
			case c.notifications <- codexAppServerNotification{Method: message.Method, Params: append(json.RawMessage(nil), message.Params...)}:
			case <-c.ctx.Done():
				c.fail(errCodexAppServerClosed)
				return
			}
			continue
		}
		if len(message.ID) == 0 {
			c.fail(errors.New("app server message has neither method nor id"))
			return
		}
		id, err := decodeCodexRPCID(message.ID)
		if err != nil {
			c.fail(err)
			return
		}
		c.pendingMu.Lock()
		response, ok := c.pending[id]
		if ok {
			delete(c.pending, id)
		}
		c.pendingMu.Unlock()
		if !ok {
			c.fail(fmt.Errorf("app server response has unknown request id %d", id))
			return
		}
		response <- codexRPCResponse{Result: append(json.RawMessage(nil), message.Result...), Error: message.Error}
	}
}

func decodeCodexRPCID(raw json.RawMessage) (int64, error) {
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, fmt.Errorf("decode app server response id: %w", err)
	}
	id, err := number.Int64()
	if err != nil {
		return 0, fmt.Errorf("app server response id is not an integer: %w", err)
	}
	return id, nil
}

func (c *codexAppServer) fail(err error) {
	if err == nil {
		err = errCodexAppServerClosed
	}
	c.closeOnce.Do(func() {
		c.cancel()
		if c.conn != nil {
			_ = c.conn.Close(websocket.StatusGoingAway, err.Error())
		}

		c.stateMu.Lock()
		c.err = err
		c.stateMu.Unlock()

		c.pendingMu.Lock()
		for id, response := range c.pending {
			response <- codexRPCResponse{Error: &codexRPCError{Code: -1, Message: err.Error()}}
			delete(c.pending, id)
		}
		c.pendingMu.Unlock()

		close(c.done)
	})
}

func (c *codexAppServer) Close() error {
	c.fail(errCodexAppServerClosed)
	return nil
}

func (c *codexAppServer) Err() error {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return c.err
}

func (c *codexAppServer) call(ctx context.Context, method string, params any, result any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-c.done:
		if err := c.Err(); err != nil {
			return err
		}
		return errCodexAppServerClosed
	default:
	}
	id := c.nextID.Add(1)
	response := make(chan codexRPCResponse, 1)
	c.pendingMu.Lock()
	c.pending[id] = response
	c.pendingMu.Unlock()

	request, err := json.Marshal(codexRPCRequest{ID: &id, Method: method, Params: params})
	if err != nil {
		c.removePending(id)
		return fmt.Errorf("encode app server request %s: %w", method, err)
	}
	if err := c.write(ctx, request); err != nil {
		c.removePending(id)
		return fmt.Errorf("write app server request %s: %w", method, err)
	}

	select {
	case reply := <-response:
		if reply.Error != nil {
			return fmt.Errorf("app server request %s failed (%d): %s", method, reply.Error.Code, reply.Error.Message)
		}
		if result == nil {
			return nil
		}
		if len(reply.Result) == 0 || string(reply.Result) == "null" {
			return fmt.Errorf("app server response %s has no result", method)
		}
		if err := json.Unmarshal(reply.Result, result); err != nil {
			return fmt.Errorf("decode app server response %s: %w", method, err)
		}
		return nil
	case <-ctx.Done():
		c.removePending(id)
		return ctx.Err()
	case <-c.done:
		c.removePending(id)
		if err := c.Err(); err != nil {
			return err
		}
		return errCodexAppServerClosed
	}
}

func (c *codexAppServer) notify(ctx context.Context, method string, params any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	request, err := json.Marshal(codexRPCRequest{Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("encode app server notification %s: %w", method, err)
	}
	return c.write(ctx, request)
}

func (c *codexAppServer) write(ctx context.Context, payload []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if c.conn == nil {
		err := errors.New("app server WebSocket connection is nil")
		c.fail(err)
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.Write(ctx, websocket.MessageText, payload); err != nil {
		c.fail(fmt.Errorf("write app server message: %w", err))
		return err
	}
	return nil
}

func (c *codexAppServer) removePending(id int64) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

func (c *codexAppServer) NextNotification(ctx context.Context) (codexAppServerNotification, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-c.done:
		if err := c.Err(); err != nil {
			return codexAppServerNotification{}, err
		}
		return codexAppServerNotification{}, errCodexAppServerClosed
	default:
	}
	select {
	case notification := <-c.notifications:
		return notification, nil
	case <-c.done:
		if err := c.Err(); err != nil {
			return codexAppServerNotification{}, err
		}
		return codexAppServerNotification{}, errCodexAppServerClosed
	case <-ctx.Done():
		return codexAppServerNotification{}, ctx.Err()
	}
}

func (c *codexAppServer) Initialize(ctx context.Context) error {
	params := map[string]any{
		"clientInfo": map[string]string{
			"name":    codexMonitorClientName,
			"version": version,
		},
	}
	var result map[string]json.RawMessage
	if err := c.call(ctx, "initialize", params, &result); err != nil {
		return err
	}
	return c.notify(ctx, "initialized", nil)
}

type codexThread struct {
	ID         string            `json:"id"`
	CWD        string            `json:"cwd"`
	Source     string            `json:"source"`
	SourceKind string            `json:"sourceKind"`
	Status     codexThreadStatus `json:"status"`
}

type codexThreadStatus struct {
	Type string `json:"type"`
}

type codexThreadListResult struct {
	Data       []codexThread `json:"data"`
	Threads    []codexThread `json:"threads"`
	NextCursor *string       `json:"nextCursor"`
}

func (r *codexThreadListResult) UnmarshalJSON(data []byte) error {
	type plain codexThreadListResult
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, ok := fields["data"]; !ok {
		if _, ok := fields["threads"]; !ok {
			return errors.New("thread/list response has no thread list")
		}
	}
	*r = codexThreadListResult(decoded)
	return nil
}

func (c *codexAppServer) ListThreads(ctx context.Context, cwd string) ([]codexThread, error) {
	exactCWD, err := codexExactCWD(cwd)
	if err != nil {
		return nil, err
	}
	var all []codexThread
	var cursor *string
	for {
		params := map[string]any{
			"cwd":         []string{exactCWD},
			"sourceKinds": []string{"cli"},
		}
		if cursor != nil {
			params["cursor"] = *cursor
		}
		var result codexThreadListResult
		if err := c.call(ctx, "thread/list", params, &result); err != nil {
			return nil, err
		}
		threads := result.Data
		if threads == nil {
			threads = result.Threads
		}
		all = append(all, threads...)
		if result.NextCursor == nil || strings.TrimSpace(*result.NextCursor) == "" {
			return all, nil
		}
		cursor = result.NextCursor
	}
}

func codexExactCWD(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return "", errors.New("codex thread cwd is empty")
	}
	absolute, err := filepath.Abs(filepath.Clean(cwd))
	if err != nil {
		return "", fmt.Errorf("resolve codex thread cwd: %w", err)
	}
	return absolute, nil
}

func codexThreadIDs(threads []codexThread) map[string]struct{} {
	ids := make(map[string]struct{}, len(threads))
	for _, thread := range threads {
		if thread.ID != "" {
			ids[thread.ID] = struct{}{}
		}
	}
	return ids
}

func (c *codexAppServer) DiscoverThread(ctx context.Context, cwd string, before map[string]struct{}, pollInterval, timeout time.Duration) (codexThread, error) {
	exactCWD, err := codexExactCWD(cwd)
	if err != nil {
		return codexThread{}, err
	}
	if pollInterval <= 0 {
		pollInterval = codexThreadDiscoveryInterval
	}
	if timeout <= 0 {
		timeout = codexThreadDiscoveryTimeout
	}
	if ctx == nil {
		ctx = context.Background()
	}
	discoveryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		threads, err := c.ListThreads(discoveryCtx, exactCWD)
		if err != nil {
			return codexThread{}, err
		}
		for _, thread := range threads {
			if _, existed := before[thread.ID]; existed {
				continue
			}
			if thread.ID == "" || thread.CWD != exactCWD {
				continue
			}
			if thread.Source != "" && thread.Source != "cli" {
				continue
			}
			if thread.SourceKind != "" && thread.SourceKind != "cli" {
				continue
			}
			return thread, nil
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-discoveryCtx.Done():
			timer.Stop()
			return codexThread{}, fmt.Errorf("discover new Codex CLI thread: %w", discoveryCtx.Err())
		case <-timer.C:
		}
	}
}

func (c *codexAppServer) ResumeThread(ctx context.Context, threadID string) error {
	if strings.TrimSpace(threadID) == "" {
		return errors.New("Codex thread ID is empty")
	}
	var result struct {
		Thread codexThread `json:"thread"`
	}
	if err := c.call(ctx, "thread/resume", map[string]string{"threadId": threadID}, &result); err != nil {
		return err
	}
	if result.Thread.ID == "" {
		return errors.New("thread/resume response has no thread ID")
	}
	if result.Thread.ID != threadID {
		return fmt.Errorf("thread/resume returned thread %q, want %q", result.Thread.ID, threadID)
	}
	return nil
}

type codexTurn struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type codexUserInput struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (c *codexAppServer) StartTurn(ctx context.Context, threadID, text string) (codexTurn, error) {
	if strings.TrimSpace(threadID) == "" {
		return codexTurn{}, errors.New("Codex thread ID is empty")
	}
	var result struct {
		Turn codexTurn `json:"turn"`
	}
	params := map[string]any{
		"threadId": threadID,
		"input": []codexUserInput{{
			Type: "text",
			Text: text,
		}},
	}
	if err := c.call(ctx, "turn/start", params, &result); err != nil {
		return codexTurn{}, err
	}
	if result.Turn.ID == "" {
		return codexTurn{}, errors.New("turn/start response has no turn ID")
	}
	return result.Turn, nil
}

type codexTurnStarter interface {
	StartTurn(context.Context, string, string) (codexTurn, error)
}

type codexMonitorApp interface {
	codexTurnStarter
	Initialize(context.Context) error
	ListThreads(context.Context, string) ([]codexThread, error)
	DiscoverThread(context.Context, string, map[string]struct{}, time.Duration, time.Duration) (codexThread, error)
	ResumeThread(context.Context, string) error
	NextNotification(context.Context) (codexAppServerNotification, error)
	Close() error
	Err() error
}

type codexMonitorBridge struct {
	starter  codexTurnStarter
	app      codexMonitorApp
	threadID string

	stateMu  sync.Mutex
	active   bool
	queue    []string
	disabled bool
	submitMu sync.Mutex
}

func newCodexMonitorBridge(starter codexTurnStarter, threadID string) *codexMonitorBridge {
	bridge := &codexMonitorBridge{starter: starter, threadID: threadID}
	if app, ok := starter.(codexMonitorApp); ok {
		bridge.app = app
	}
	return bridge
}

func (b *codexMonitorBridge) Enqueue(ctx context.Context, line string) error {
	if strings.TrimSpace(line) == "" {
		return nil
	}
	b.stateMu.Lock()
	if b.disabled {
		b.stateMu.Unlock()
		return errCodexAppServerClosed
	}
	b.queue = append(b.queue, line)
	b.stateMu.Unlock()
	return b.pump(ctx)
}

func (b *codexMonitorBridge) pump(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	b.submitMu.Lock()
	defer b.submitMu.Unlock()

	for {
		b.stateMu.Lock()
		if b.disabled || b.active || b.threadID == "" || len(b.queue) == 0 {
			b.stateMu.Unlock()
			return nil
		}
		line := b.queue[0]
		// Reserve the turn before making the request so a fast turn/started
		// notification cannot race with another queued submission.
		b.active = true
		threadID := b.threadID
		starter := b.starter
		b.stateMu.Unlock()

		if starter == nil {
			b.stateMu.Lock()
			b.active = false
			b.stateMu.Unlock()
			return errors.New("Codex monitor bridge has no turn starter")
		}
		_, err := starter.StartTurn(ctx, threadID, line)
		if err != nil {
			b.stateMu.Lock()
			b.active = false
			if b.app != nil && b.app.Err() != nil {
				b.disabled = true
				b.queue = nil
			}
			b.stateMu.Unlock()
			return err
		}

		b.stateMu.Lock()
		b.queue = b.queue[1:]
		b.stateMu.Unlock()
		// A completion notification can arrive while StartTurn is waiting for
		// its response. Loop once more so a queued item is not stranded when
		// that notification made the bridge idle during the request.
	}
}

func (b *codexMonitorBridge) SetActive(active bool) {
	b.stateMu.Lock()
	b.active = active
	b.stateMu.Unlock()
}

func (b *codexMonitorBridge) AttachThread(ctx context.Context, threadID string, active bool) error {
	if strings.TrimSpace(threadID) == "" {
		return errors.New("Codex thread ID is empty")
	}
	b.stateMu.Lock()
	b.threadID = threadID
	b.active = active
	b.stateMu.Unlock()
	if active {
		return nil
	}
	return b.pump(ctx)
}

func (b *codexMonitorBridge) Active() bool {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	return b.active
}

func (b *codexMonitorBridge) QueueLen() int {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	return len(b.queue)
}

func (b *codexMonitorBridge) LineSink() func(string) error {
	return b.LineSinkWithContext(context.Background())
}

func (b *codexMonitorBridge) LineSinkWithContext(ctx context.Context) func(string) error {
	return func(line string) error {
		if !isCodexMonitorActionLine(line) {
			return nil
		}
		// A failed turn submission stays in the bridge queue. The watcher must
		// keep its SSE delivery state and continue consuming events; a later
		// idle notification retries the queued item.
		if err := b.Enqueue(ctx, line); err != nil {
			b.stateMu.Lock()
			disabled := b.disabled
			b.stateMu.Unlock()
			if disabled {
				return err
			}
		}
		return nil
	}
}

func isCodexMonitorActionLine(line string) bool {
	line = strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(line, "atct decision answered (decision_id: "):
		return true
	case strings.HasPrefix(line, "atct decision approved (decision_id: "):
		return true
	case strings.HasPrefix(line, "atct decision rejected (decision_id: "):
		return true
	case strings.HasPrefix(line, "atct goal created (goal_id: "):
		return true
	case strings.HasPrefix(line, "atct wakeup: "):
		return true
	case strings.HasPrefix(line, "atct detection: goal "):
		return true
	case strings.HasPrefix(line, "atct handoff reported: goal "):
		return true
	case strings.HasPrefix(line, "atct wakeup discrepancy: "):
		return true
	case strings.HasPrefix(line, "atct wakeup evaluate failed: "):
		return true
	default:
		return false
	}
}

func (b *codexMonitorBridge) HandleNotification(ctx context.Context, notification codexAppServerNotification) error {
	var params struct {
		ThreadID string            `json:"threadId"`
		Status   codexThreadStatus `json:"status"`
		Turn     codexTurn         `json:"turn"`
	}
	if len(notification.Params) > 0 && string(notification.Params) != "null" {
		if err := json.Unmarshal(notification.Params, &params); err != nil {
			return fmt.Errorf("decode Codex %s notification: %w", notification.Method, err)
		}
	}
	b.stateMu.Lock()
	threadID := b.threadID
	b.stateMu.Unlock()
	if params.ThreadID == "" || params.ThreadID != threadID {
		return nil
	}
	switch notification.Method {
	case "turn/started":
		b.stateMu.Lock()
		b.active = true
		b.stateMu.Unlock()
	case "turn/completed":
		b.stateMu.Lock()
		b.active = false
		b.stateMu.Unlock()
		return b.pumpAfterIdle(ctx)
	case "thread/status/changed":
		if !codexThreadStatusIsIdle(params.Status) {
			return nil
		}
		b.stateMu.Lock()
		b.active = false
		b.stateMu.Unlock()
		return b.pumpAfterIdle(ctx)
	}
	return nil
}

func (b *codexMonitorBridge) pumpAfterIdle(ctx context.Context) error {
	if err := b.pump(ctx); err != nil {
		b.stateMu.Lock()
		disabled := b.disabled
		b.stateMu.Unlock()
		if disabled {
			return err
		}
	}
	return nil
}

func codexThreadStatusIsIdle(status codexThreadStatus) bool {
	switch strings.ToLower(status.Type) {
	case "idle", "notloaded", "not_loaded", "completed", "failed", "interrupted":
		return true
	default:
		return false
	}
}

func (b *codexMonitorBridge) Run(ctx context.Context) error {
	if b.app == nil {
		return errors.New("Codex monitor bridge requires an app server connection")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		notification, err := b.app.NextNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if err := b.HandleNotification(ctx, notification); err != nil {
			return err
		}
	}
}

func runCodexMonitorWatch(ctx context.Context, client *http.Client, urls []string, cwd string, bridge *codexMonitorBridge) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if bridge == nil {
		return errors.New("Codex monitor watcher bridge is nil")
	}
	if client == nil {
		client = &http.Client{}
	}
	snapshot, projectID := watchSnapshotWithProject(client, urls, cwd)
	return watchLoopWithEnsureAndProjectIDAndGoalAndSink(
		ctx,
		codexMonitorWatchOutput{},
		client,
		watchReconnectInterval,
		snapshot,
		nil,
		projectID,
		"",
		bridge.LineSinkWithContext(ctx),
	)
}
