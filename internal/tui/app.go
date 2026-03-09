package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/tianet/botafoc/internal/portforward"
)

type RouteManager interface {
	List() []RouteInfo
	Add(subdomain string, target int) error
	Remove(subdomain string) error
	Logs(subdomain string) []LogInfo
}

type RouteInfo struct {
	Subdomain string
	Target    int
	Healthy   bool
}

type LogInfo struct {
	Time      time.Time
	Subdomain string
	Method    string
	Path      string
	Status    int
	Duration  time.Duration
}

type mode int

const (
	modeTable mode = iota
	modeAdd
	modeEdit
	modeLogs
	modeConfirmQuit
)

type tickMsg time.Time

type tab int

const (
	tabRoutes tab = iota
	tabLogs
)

type Model struct {
	manager      RouteManager
	portForwards *portforward.Manager
	routeTable   routeTable
	addForm      addForm
	logViewer    logViewer
	mode         mode
	previousMode mode // mode before confirm quit
	activeTab    tab
	width        int
	height       int
	err          string
	isDaemon     bool
	listenPort   int
	routeSources map[string]routeSource // subdomain → source info
}

type routeSource struct {
	sourceType   string // "docker", "k8s-service", "k8s-pod"
	resourceName string // container/service/pod name
	namespace    string // k8s namespace; empty for docker
}

func New(manager RouteManager, pfManager *portforward.Manager, isDaemon bool, listenPort int) Model {
	return Model{
		manager:      manager,
		portForwards: pfManager,
		routeTable:   newRouteTable(),
		addForm:      newAddForm(),
		logViewer:    newLogViewer(),
		mode:         modeTable,
		activeTab:    tabRoutes,
		isDaemon:     isDaemon,
		listenPort:   listenPort,
		routeSources: make(map[string]routeSource),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.refreshRoutes(), tickCmd())
}

func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) refreshRoutes() tea.Cmd {
	return func() tea.Msg {
		return routesRefreshedMsg(m.manager.List())
	}
}

type routesRefreshedMsg []RouteInfo
type logsRefreshedMsg []LogInfo

func (m Model) refreshLogs() tea.Cmd {
	return func() tea.Msg {
		return logsRefreshedMsg(m.manager.Logs(m.logViewer.filter))
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.routeTable.table.SetWidth(msg.Width)
		m.routeTable.table.SetHeight(msg.Height - 6)
		m.logViewer.height = msg.Height - 10
		if m.logViewer.height < 5 {
			m.logViewer.height = 5
		}
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.refreshRoutes(), m.refreshLogs(), tickCmd())

	case routesRefreshedMsg:
		rows := make([]routeRow, len(msg))
		for i, r := range msg {
			src := m.routeSources[r.Subdomain]
			rows[i] = routeRow{
				subdomain:    r.Subdomain,
				port:         r.Target,
				healthy:      r.Healthy,
				source:       src.sourceType,
				resourceName: src.resourceName,
				namespace:    src.namespace,
			}
		}
		m.routeTable.updateRows(rows)
		return m, nil

	case logsRefreshedMsg:
		m.logViewer.setEntries(msg)
		return m, nil

	case resourcesLoadedMsg:
		if m.mode == modeAdd || m.mode == modeEdit {
			cmd := m.addForm.update(msg)
			return m, cmd
		}
		return m, nil

	case tea.KeyPressMsg:
		if m.mode == modeConfirmQuit {
			return m.updateConfirmQuitMode(msg)
		}
		if m.mode == modeAdd || m.mode == modeEdit {
			return m.updateFormMode(msg)
		}
		if m.mode == modeLogs {
			return m.updateLogsMode(msg)
		}
		return m.updateTableMode(msg)
	}

	if m.mode == modeTable {
		cmd := m.routeTable.update(msg)
		return m, cmd
	}

	return m, nil
}

func (m Model) tryQuit() (tea.Model, tea.Cmd) {
	if m.portForwards != nil && m.portForwards.Count() > 0 {
		m.previousMode = m.mode
		m.mode = modeConfirmQuit
		return m, nil
	}
	return m, tea.Quit
}

func (m Model) updateConfirmQuitMode(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		m.portForwards.StopAll()
		return m, tea.Quit
	case "n", "esc":
		m.mode = m.previousMode
		return m, nil
	}
	return m, nil
}

func (m Model) updateTableMode(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m.tryQuit()
	case "a":
		m.mode = modeAdd
		m.addForm.reset()
		m.addForm.usedPorts = m.usedPortsMap()
		return m, nil
	case "e":
		sub := m.routeTable.selectedSubdomain()
		port := m.routeTable.selectedPort()
		if sub != "" {
			m.mode = modeEdit
			m.addForm.prefill(sub, port)
			return m, nil
		}
		return m, nil
	case "enter":
		sub := m.routeTable.selectedSubdomain()
		if sub != "" {
			m.mode = modeLogs
			m.logViewer.filter = sub
			m.logViewer.offset = 0
			return m, m.refreshLogs()
		}
		return m, nil
	case "tab":
		m.mode = modeLogs
		m.logViewer.filter = ""
		m.logViewer.offset = 0
		return m, m.refreshLogs()
	case "d", "delete", "backspace":
		sub := m.routeTable.selectedSubdomain()
		if sub != "" {
			if m.portForwards != nil {
				m.portForwards.Stop(sub)
			}
			delete(m.routeSources, sub)
			if err := m.manager.Remove(sub); err != nil {
				m.err = err.Error()
			}
			return m, m.refreshRoutes()
		}
		return m, nil
	}

	cmd := m.routeTable.update(msg)
	return m, cmd
}

func (m Model) updateFormMode(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// When the picker or namespace prompt is active, delegate all keys to the form
	if m.addForm.picking || m.addForm.confirmingNamespace {
		cmd := m.addForm.update(msg)
		return m, cmd
	}

	// Port-forward confirmation prompt
	if m.addForm.confirmingPortForward {
		return m.handlePortForwardConfirm(msg)
	}

	switch msg.String() {
	case "esc":
		m.mode = modeTable
		m.addForm.reset()
		return m, nil
	case "enter":
		subdomain, port, err := m.addForm.validate()
		if err != nil {
			m.addForm.err = err.Error()
			return m, nil
		}

		// Check if this is a K8s resource that could use port-forwarding
		r := m.addForm.selectedResource
		if r != nil && (r.Type == "k8s-service" || r.Type == "k8s-pod") && m.portForwards != nil {
			m.addForm.confirmingPortForward = true
			return m, nil
		}

		return m.commitRoute(subdomain, port)
	}

	cmd := m.addForm.update(msg)
	return m, cmd
}

func (m Model) handlePortForwardConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		subdomain, port, _ := m.addForm.validate()
		r := m.addForm.selectedResource

		// Use the port selected from the port picker, or fall back to local port
		remotePort := m.addForm.selectedRemotePort
		if remotePort == 0 {
			remotePort = port
		}

		fwd := portforward.Forward{
			Subdomain:  subdomain,
			Resource:   r.Name,
			Type:       r.Type,
			Namespace:  r.Namespace,
			LocalPort:  port,
			RemotePort: remotePort,
		}
		if err := m.portForwards.Start(fwd); err != nil {
			m.addForm.err = fmt.Sprintf("port-forward: %v", err)
			m.addForm.confirmingPortForward = false
			return m, nil
		}
		return m.commitRoute(subdomain, port)
	case "n":
		subdomain, port, _ := m.addForm.validate()
		return m.commitRoute(subdomain, port)
	case "esc":
		m.addForm.confirmingPortForward = false
		return m, nil
	}
	return m, nil
}

func (m Model) commitRoute(subdomain string, port int) (tea.Model, tea.Cmd) {
	// When editing, remove the old route first
	if m.addForm.editing != "" {
		delete(m.routeSources, m.addForm.editing)
		if err := m.manager.Remove(m.addForm.editing); err != nil {
			m.addForm.err = err.Error()
			return m, nil
		}
	}
	if err := m.manager.Add(subdomain, port); err != nil {
		m.addForm.err = err.Error()
		return m, nil
	}

	// Record the source for display
	if r := m.addForm.selectedResource; r != nil {
		m.routeSources[subdomain] = routeSource{
			sourceType:   r.Type,
			resourceName: r.Name,
			namespace:    r.Namespace,
		}
	}

	m.mode = modeTable
	m.addForm.reset()
	return m, m.refreshRoutes()
}

func (m Model) updateLogsMode(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m.tryQuit()
	case "esc", "tab":
		m.mode = modeTable
		return m, nil
	case "up", "k":
		m.logViewer.scrollUp()
		return m, nil
	case "down", "j":
		m.logViewer.scrollDown()
		return m, nil
	}
	return m, nil
}

func (m Model) usedPortsMap() map[int]bool {
	ports := make(map[int]bool)
	for _, r := range m.manager.List() {
		ports[r.Target] = true
	}
	return ports
}

func (m Model) View() tea.View {
	var b strings.Builder

	b.WriteString(titleStyle.Render("🔥 botafoc") + "\n\n")

	// Tab bar (only in table/logs modes)
	if m.mode == modeTable || m.mode == modeLogs || m.mode == modeConfirmQuit {
		routesTab := tabInactiveStyle.Render("Routes")
		logsTab := tabInactiveStyle.Render("Logs")
		if m.mode == modeTable {
			routesTab = tabActiveStyle.Render("Routes")
		} else {
			logsTab = tabActiveStyle.Render("Logs")
		}
		b.WriteString(routesTab + " " + logsTab + "\n\n")
	}

	switch m.mode {
	case modeTable:
		b.WriteString(m.routeTable.view())
		b.WriteString("\n\n")
		if m.err != "" {
			b.WriteString(errorStyle.Render(m.err) + "\n")
		}
		help := "a: add • e: edit • d: delete • enter: view logs • tab: all logs • q: quit"
		if m.isDaemon {
			help = "a: add • e: edit • d: delete • enter: view logs • tab: all logs • q: quit"
		}
		b.WriteString(helpStyle.Render(help))
	case modeLogs:
		b.WriteString(m.logViewer.view())
		b.WriteString("\n\n")
		b.WriteString(helpStyle.Render("↑/↓: scroll • tab/esc: back to routes • q: quit"))
	case modeAdd, modeEdit:
		b.WriteString(m.addForm.view())
	case modeConfirmQuit:
		count := m.portForwards.Count()
		b.WriteString(confirmStyle.Render(
			fmt.Sprintf("There are %d active port-forward(s). Quit and stop all? (y/n)", count),
		))
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render(fmt.Sprintf("Listening on :%d", m.listenPort)))

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}
