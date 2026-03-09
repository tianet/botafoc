package ipc

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/tianet/botafoc/internal/tui"
)

type Client struct {
	conn    net.Conn
	encoder *json.Encoder
	decoder *json.Decoder
}

func NewClient() (*Client, error) {
	conn, err := net.Dial("unix", SocketPath)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon: %w", err)
	}
	return &Client{
		conn:    conn,
		encoder: json.NewEncoder(conn),
		decoder: json.NewDecoder(conn),
	}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) call(req Request) (Response, error) {
	if err := c.encoder.Encode(req); err != nil {
		return Response{}, fmt.Errorf("send: %w", err)
	}
	var resp Response
	if err := c.decoder.Decode(&resp); err != nil {
		return Response{}, fmt.Errorf("recv: %w", err)
	}
	if !resp.OK {
		return resp, fmt.Errorf("server: %s", resp.Error)
	}
	return resp, nil
}

// RouteManager interface implementation

func (c *Client) List() []tui.RouteInfo {
	resp, err := c.call(Request{Method: "list"})
	if err != nil {
		return nil
	}
	return resp.Routes
}

func (c *Client) Add(subdomain string, target int) error {
	_, err := c.call(Request{Method: "add", Subdomain: subdomain, Target: target})
	return err
}

func (c *Client) Remove(subdomain string) error {
	_, err := c.call(Request{Method: "remove", Subdomain: subdomain})
	return err
}

func (c *Client) Logs(subdomain string) []tui.LogInfo {
	resp, err := c.call(Request{Method: "logs", Subdomain: subdomain})
	if err != nil {
		return nil
	}
	return resp.Logs
}

func (c *Client) Shutdown() error {
	_, err := c.call(Request{Method: "shutdown"})
	return err
}
