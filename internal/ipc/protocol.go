package ipc

import "github.com/tianet/botafoc/internal/tui"

type Request struct {
	Method    string `json:"method"`
	Subdomain string `json:"subdomain,omitempty"`
	Target    int    `json:"target,omitempty"`
}

type Response struct {
	OK     bool            `json:"ok"`
	Error  string          `json:"error,omitempty"`
	Routes []tui.RouteInfo `json:"routes,omitempty"`
	Logs   []tui.LogInfo   `json:"logs,omitempty"`
}

const SocketPath = "/tmp/botafoc.sock"
