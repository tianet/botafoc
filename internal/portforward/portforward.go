package portforward

import (
	"fmt"
	"os/exec"
	"sync"
)

// Forward represents a running port-forward process.
type Forward struct {
	Subdomain  string
	Resource   string // resource name
	Type       string // "docker", "k8s-service", "k8s-pod"
	Namespace  string // k8s namespace; empty uses kubectl default
	LocalPort  int
	RemotePort int
	cmd        *exec.Cmd
}

// Manager tracks running port-forward processes.
type Manager struct {
	mu       sync.Mutex
	forwards map[string]*Forward // keyed by subdomain
}

// NewManager creates a new port-forward manager.
func NewManager() *Manager {
	return &Manager{
		forwards: make(map[string]*Forward),
	}
}

// Start begins a port-forward for the given forward configuration.
// Docker forwards are no-ops (containers already have host ports).
func (m *Manager) Start(f Forward) error {
	if f.Type == "docker" {
		return nil
	}

	var prefix string
	switch f.Type {
	case "k8s-service":
		prefix = "svc/"
	case "k8s-pod":
		prefix = "pod/"
	default:
		return fmt.Errorf("unsupported forward type: %s", f.Type)
	}

	portMapping := fmt.Sprintf("%d:%d", f.LocalPort, f.RemotePort)
	args := []string{}
	if f.Namespace != "" {
		args = append(args, "-n", f.Namespace)
	}
	args = append(args, "port-forward", prefix+f.Resource, portMapping)
	cmd := exec.Command("kubectl", args...)
	f.cmd = cmd

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start port-forward: %w", err)
	}

	m.mu.Lock()
	m.forwards[f.Subdomain] = &f
	m.mu.Unlock()

	// Clean up when process exits
	go func() {
		cmd.Wait()
		m.mu.Lock()
		// Only delete if this is still the same forward
		if cur, ok := m.forwards[f.Subdomain]; ok && cur.cmd == cmd {
			delete(m.forwards, f.Subdomain)
		}
		m.mu.Unlock()
	}()

	return nil
}

// Stop kills the port-forward for the given subdomain.
func (m *Manager) Stop(subdomain string) {
	m.mu.Lock()
	f, ok := m.forwards[subdomain]
	if ok {
		delete(m.forwards, subdomain)
	}
	m.mu.Unlock()

	if ok && f.cmd != nil && f.cmd.Process != nil {
		f.cmd.Process.Kill()
	}
}

// StopAll kills all running port-forwards.
func (m *Manager) StopAll() {
	m.mu.Lock()
	forwards := make(map[string]*Forward, len(m.forwards))
	for k, v := range m.forwards {
		forwards[k] = v
	}
	m.forwards = make(map[string]*Forward)
	m.mu.Unlock()

	for _, f := range forwards {
		if f.cmd != nil && f.cmd.Process != nil {
			f.cmd.Process.Kill()
		}
	}
}

// List returns a snapshot of all active forwards.
func (m *Manager) List() []*Forward {
	m.mu.Lock()
	defer m.mu.Unlock()

	list := make([]*Forward, 0, len(m.forwards))
	for _, f := range m.forwards {
		list = append(list, f)
	}
	return list
}

// Count returns the number of active port-forwards.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.forwards)
}
