package mcpshim

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"

	"github.com/michiomochi/atct/internal/rpc"
)

// Client opens one connection per call.
// The shim is stateless; all writes converge on the single daemon process.
type Client struct {
	socketPath string
}

func NewClient(socketPath string) *Client {
	return &Client{socketPath: socketPath}
}

func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal params: %w", err)
	}

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return fmt.Errorf("dial daemon at %s (is `atct` running?): %w", c.socketPath, err)
	}
	defer conn.Close()

	req, err := json.Marshal(rpc.Request{Method: method, Params: raw})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return fmt.Errorf("write request: %w", err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	var resp rpc.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("unmarshal response: %w", err)
	}
	if resp.Error != "" {
		return errors.New(resp.Error)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(resp.Result, out)
}
