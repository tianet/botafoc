package ipc

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/tianet/botafoc/internal/proxy"
	"github.com/tianet/botafoc/internal/tui"
)

type Server struct {
	proxy    *proxy.Proxy
	listener net.Listener
	wg       sync.WaitGroup
	quit     chan struct{}
}

func NewServer(p *proxy.Proxy) *Server {
	return &Server{
		proxy: p,
		quit:  make(chan struct{}),
	}
}

func (s *Server) Start() error {
	os.Remove(SocketPath)

	ln, err := net.Listen("unix", SocketPath)
	if err != nil {
		return fmt.Errorf("listen unix: %w", err)
	}
	s.listener = ln

	go s.acceptLoop()
	return nil
}

func (s *Server) Stop() error {
	close(s.quit)
	if s.listener != nil {
		s.listener.Close()
	}
	os.Remove(SocketPath)
	s.wg.Wait()
	return nil
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.quit:
				return
			default:
				continue
			}
		}
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		var req Request
		if err := decoder.Decode(&req); err != nil {
			return
		}

		resp := s.dispatch(req)
		if err := encoder.Encode(resp); err != nil {
			return
		}
	}
}

func (s *Server) dispatch(req Request) Response {
	switch req.Method {
	case "list":
		routes := s.proxy.Routes()
		infos := make([]tui.RouteInfo, len(routes))
		for i, r := range routes {
			infos[i] = tui.RouteInfo{
				Subdomain: r.Subdomain,
				Target:    r.Target,
				Healthy:   r.Healthy,
			}
		}
		return Response{OK: true, Routes: infos}

	case "logs":
		entries := s.proxy.Logs(req.Subdomain)
		infos := make([]tui.LogInfo, len(entries))
		for i, e := range entries {
			infos[i] = tui.LogInfo{
				Time:      e.Time,
				Subdomain: e.Subdomain,
				Method:    e.Method,
				Path:      e.Path,
				Status:    e.Status,
				Duration:  e.Duration,
			}
		}
		return Response{OK: true, Logs: infos}

	case "add":
		s.proxy.AddRoute(req.Subdomain, req.Target)
		return Response{OK: true}

	case "remove":
		s.proxy.RemoveRoute(req.Subdomain)
		return Response{OK: true}

	case "shutdown":
		go func() {
			s.proxy.Stop()
			s.Stop()
		}()
		return Response{OK: true}

	default:
		return Response{OK: false, Error: fmt.Sprintf("unknown method: %s", req.Method)}
	}
}
