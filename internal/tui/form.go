package tui

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/tianet/botafoc/internal/discovery"
)

type formField int

const (
	fieldSubdomain formField = iota
	fieldPort
)

type pickerTab int

const (
	pickerTabDocker pickerTab = iota
	pickerTabServices
	pickerTabPods
	pickerTabCount // sentinel for wrapping
)

type addForm struct {
	subdomainInput textinput.Model
	portInput      textinput.Model
	focused        formField
	err            string
	editing        string // non-empty = editing this subdomain

	// Picker state
	picking      bool
	resourceData discovery.ResourcesByType
	pickerTab    pickerTab
	cursors      [pickerTabCount]int // per-tab cursor
	usedPorts    map[int]bool       // ports already used by existing routes

	// Port selection state (when resource has multiple ports)
	pickingPort bool
	portCursor  int

	// Namespace subdomain prompt state
	confirmingNamespace bool
	namespaceCursor     int // 0 = name only, 1 = name.namespace

	// Port-forward confirmation state
	confirmingPortForward bool
	selectedResource      *discovery.Resource // nil if typed manually
	selectedRemotePort    int                 // the remote port chosen from the port picker
}

func newAddForm() addForm {
	sub := textinput.New()
	sub.Placeholder = "myapp"
	sub.Prompt = "Subdomain: "
	sub.CharLimit = 63
	sub.Focus()

	port := textinput.New()
	port.Placeholder = "5000"
	port.Prompt = "Port: "
	port.CharLimit = 5
	port.Validate = func(s string) error {
		if s == "" {
			return nil
		}
		_, err := strconv.Atoi(s)
		return err
	}

	return addForm{
		subdomainInput: sub,
		portInput:      port,
		focused:        fieldSubdomain,
	}
}

// resourcesLoadedMsg carries discovered resources back to the form.
type resourcesLoadedMsg struct {
	data discovery.ResourcesByType
}

func loadResources() tea.Cmd {
	return func() tea.Msg {
		return resourcesLoadedMsg{data: discovery.AllByType()}
	}
}

// activeResources returns the resource list for the current picker tab.
func (f *addForm) activeResources() []discovery.Resource {
	switch f.pickerTab {
	case pickerTabDocker:
		return f.resourceData.Docker
	case pickerTabServices:
		return f.resourceData.Services
	case pickerTabPods:
		return f.resourceData.Pods
	}
	return nil
}

func (f *addForm) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case resourcesLoadedMsg:
		f.resourceData = msg.data
		f.cursors = [pickerTabCount]int{}
		f.pickerTab = pickerTabDocker
		f.picking = true
		return nil

	case tea.KeyPressMsg:
		if f.confirmingNamespace {
			return f.updateNamespacePicker(msg)
		}
		if f.picking {
			return f.updatePicker(msg)
		}
		switch msg.String() {
		case "tab", "shift+tab":
			if f.focused == fieldSubdomain {
				f.focused = fieldPort
				f.subdomainInput.Blur()
				return f.portInput.Focus()
			}
			f.focused = fieldSubdomain
			f.portInput.Blur()
			return f.subdomainInput.Focus()
		case "p":
			if f.editing == "" && f.subdomainInput.Value() == "" && f.portInput.Value() == "" {
				return loadResources()
			}
		}
	}

	var cmd tea.Cmd
	if f.focused == fieldSubdomain {
		f.subdomainInput, cmd = f.subdomainInput.Update(msg)
	} else {
		f.portInput, cmd = f.portInput.Update(msg)
	}
	return cmd
}

func (f *addForm) updatePicker(msg tea.KeyPressMsg) tea.Cmd {
	if f.pickingPort {
		return f.updatePortPicker(msg)
	}

	resources := f.activeResources()

	switch msg.String() {
	case "esc":
		f.picking = false
		return nil
	case "tab", "right", "l":
		f.pickerTab = (f.pickerTab + 1) % pickerTabCount
		return nil
	case "shift+tab", "left", "h":
		f.pickerTab = (f.pickerTab + pickerTabCount - 1) % pickerTabCount
		return nil
	case "up", "k":
		if f.cursors[f.pickerTab] > 0 {
			f.cursors[f.pickerTab]--
		}
		return nil
	case "down", "j":
		if f.cursors[f.pickerTab] < len(resources)-1 {
			f.cursors[f.pickerTab]++
		}
		return nil
	case "enter":
		if len(resources) > 0 {
			r := resources[f.cursors[f.pickerTab]]
			f.selectedResource = &r
			f.subdomainInput.SetValue(discovery.SanitizeName(r.Name))

			if len(r.Ports) > 1 {
				// Multiple ports — ask which one
				f.pickingPort = true
				f.portCursor = 0
				return nil
			}

			// Single port or no ports
			if len(r.Ports) == 1 {
				f.selectedRemotePort = r.Ports[0]
			}
			if r.Type == "docker" && len(r.Ports) == 1 {
				f.portInput.SetValue(strconv.Itoa(r.Ports[0]))
			} else {
				f.portInput.SetValue(strconv.Itoa(f.randomAvailablePort()))
			}
			f.picking = false

			// For K8s resources with a namespace, prompt for namespace subdomain
			if r.Namespace != "" {
				f.confirmingNamespace = true
				f.namespaceCursor = 0
				return nil
			}
		}
		return nil
	}
	return nil
}

func (f *addForm) updatePortPicker(msg tea.KeyPressMsg) tea.Cmd {
	r := f.selectedResource
	if r == nil {
		return nil
	}

	switch msg.String() {
	case "esc":
		f.pickingPort = false
		f.selectedResource = nil
		return nil
	case "up", "k":
		if f.portCursor > 0 {
			f.portCursor--
		}
		return nil
	case "down", "j":
		if f.portCursor < len(r.Ports)-1 {
			f.portCursor++
		}
		return nil
	case "enter":
		chosenPort := r.Ports[f.portCursor]
		f.selectedRemotePort = chosenPort
		if r.Type == "docker" {
			// Docker: use existing host port directly
			f.portInput.SetValue(strconv.Itoa(chosenPort))
		} else {
			f.portInput.SetValue(strconv.Itoa(f.randomAvailablePort()))
		}
		f.pickingPort = false
		f.picking = false

		// For K8s resources with a namespace, prompt for namespace subdomain
		if r.Namespace != "" {
			f.confirmingNamespace = true
			f.namespaceCursor = 0
			return nil
		}
		return nil
	}
	return nil
}

func (f *addForm) updateNamespacePicker(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "up", "k":
		if f.namespaceCursor > 0 {
			f.namespaceCursor--
		}
		return nil
	case "down", "j":
		if f.namespaceCursor < 1 {
			f.namespaceCursor++
		}
		return nil
	case "enter":
		r := f.selectedResource
		if f.namespaceCursor == 1 && r != nil {
			// name.namespace
			f.subdomainInput.SetValue(discovery.SanitizeName(r.Name) + "." + discovery.SanitizeName(r.Namespace))
		}
		// namespaceCursor == 0: keep name only (already set)
		f.confirmingNamespace = false
		return nil
	case "esc":
		f.confirmingNamespace = false
		return nil
	}
	return nil
}

func (f *addForm) randomAvailablePort() int {
	for range 100 {
		p := 49152 + rand.IntN(65535-49152+1)
		if !f.usedPorts[p] {
			return p
		}
	}
	return 49152 + rand.IntN(65535-49152+1)
}

func (f *addForm) validate() (string, int, error) {
	subdomain := strings.TrimSpace(f.subdomainInput.Value())
	portStr := strings.TrimSpace(f.portInput.Value())

	if subdomain == "" {
		return "", 0, fmt.Errorf("subdomain is required")
	}
	if portStr == "" {
		return "", 0, fmt.Errorf("port is required")
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port number")
	}
	if port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("port must be 1-65535")
	}

	return subdomain, port, nil
}

func (f *addForm) reset() {
	f.subdomainInput.SetValue("")
	f.portInput.SetValue("")
	f.focused = fieldSubdomain
	f.portInput.Blur()
	f.subdomainInput.Focus()
	f.err = ""
	f.editing = ""
	f.picking = false
	f.resourceData = discovery.ResourcesByType{}
	f.cursors = [pickerTabCount]int{}
	f.pickerTab = pickerTabDocker
	f.pickingPort = false
	f.portCursor = 0
	f.confirmingNamespace = false
	f.namespaceCursor = 0
	f.confirmingPortForward = false
	f.selectedResource = nil
	f.selectedRemotePort = 0
}

func (f *addForm) prefill(subdomain string, port int) {
	f.reset()
	f.subdomainInput.SetValue(subdomain)
	f.portInput.SetValue(strconv.Itoa(port))
	f.editing = subdomain
}

func (f *addForm) view() string {
	if f.confirmingNamespace {
		return f.namespacePickerView()
	}
	if f.confirmingPortForward {
		return f.portForwardView()
	}
	if f.picking {
		return f.pickerView()
	}

	var b strings.Builder
	if f.editing != "" {
		b.WriteString(formLabelStyle.Render("Edit Route") + "\n\n")
	} else {
		b.WriteString(formLabelStyle.Render("Add Route") + "\n\n")
	}
	b.WriteString(f.subdomainInput.View() + "\n")
	b.WriteString(f.portInput.View() + "\n\n")

	if f.err != "" {
		b.WriteString(errorStyle.Render(f.err) + "\n\n")
	}

	hint := "tab: next field • enter: confirm • esc: cancel"
	if f.editing == "" {
		hint = "p: pick container/service • " + hint
	}
	b.WriteString(helpStyle.Render(hint))
	return b.String()
}

func (f *addForm) pickerView() string {
	if f.pickingPort {
		return f.portPickerView()
	}

	var b strings.Builder
	b.WriteString(formLabelStyle.Render("Pick Resource") + "\n\n")

	// Tab bar
	tabs := []struct {
		tab   pickerTab
		label string
		count int
	}{
		{pickerTabDocker, "Docker", len(f.resourceData.Docker)},
		{pickerTabServices, "Services", len(f.resourceData.Services)},
		{pickerTabPods, "Pods", len(f.resourceData.Pods)},
	}
	for i, t := range tabs {
		label := fmt.Sprintf("%s (%d)", t.label, t.count)
		if t.tab == f.pickerTab {
			b.WriteString(tabActiveStyle.Render(label))
		} else {
			b.WriteString(tabInactiveStyle.Render(label))
		}
		if i < len(tabs)-1 {
			b.WriteString(" ")
		}
	}
	b.WriteString("\n\n")

	// Resource list for active tab
	resources := f.activeResources()
	if len(resources) == 0 {
		b.WriteString(helpStyle.Render("No resources found.") + "\n")
	} else {
		cursor := f.cursors[f.pickerTab]
		for i, r := range resources {
			portsStr := ""
			if len(r.Ports) > 0 {
				ps := make([]string, len(r.Ports))
				for j, p := range r.Ports {
					ps[j] = strconv.Itoa(p)
				}
				portsStr = " [" + strings.Join(ps, ", ") + "]"
			}

			nameLabel := r.Name
			if r.Namespace != "" {
				nameLabel = r.Name + " (" + r.Namespace + ")"
			}
			line := nameLabel + portsStr
			if i == cursor {
				b.WriteString(pickerSelectedStyle.Render("▸ "+line) + "\n")
			} else {
				b.WriteString(pickerItemStyle.Render("  "+line) + "\n")
			}
		}
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("tab/←/→: switch tab • ↑/↓: navigate • enter: select • esc: back"))
	return b.String()
}

func (f *addForm) portPickerView() string {
	var b strings.Builder
	r := f.selectedResource
	b.WriteString(formLabelStyle.Render("Select Port") + "\n\n")
	b.WriteString(fmt.Sprintf("Resource: %s (%s)\n\n", r.Name, r.Type))

	for i, p := range r.Ports {
		line := strconv.Itoa(p)
		if i == f.portCursor {
			b.WriteString(pickerSelectedStyle.Render("▸ "+line) + "\n")
		} else {
			b.WriteString(pickerItemStyle.Render("  "+line) + "\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑/↓: navigate • enter: select • esc: back"))
	return b.String()
}

func (f *addForm) namespacePickerView() string {
	var b strings.Builder
	r := f.selectedResource
	b.WriteString(formLabelStyle.Render("Subdomain Format") + "\n\n")
	b.WriteString(fmt.Sprintf("Resource: %s (%s)\n\n", r.Name, r.Namespace))

	nameOnly := discovery.SanitizeName(r.Name)
	nameWithNs := discovery.SanitizeName(r.Name) + "." + discovery.SanitizeName(r.Namespace)

	options := []string{nameOnly, nameWithNs}
	for i, opt := range options {
		if i == f.namespaceCursor {
			b.WriteString(pickerSelectedStyle.Render("▸ "+opt) + "\n")
		} else {
			b.WriteString(pickerItemStyle.Render("  "+opt) + "\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑/↓: navigate • enter: select • esc: skip"))
	return b.String()
}

func (f *addForm) portForwardView() string {
	var b strings.Builder
	b.WriteString(formLabelStyle.Render("Port Forward") + "\n\n")

	r := f.selectedResource
	b.WriteString(fmt.Sprintf("Resource: %s (%s)\n", r.Name, r.Type))
	b.WriteString(fmt.Sprintf("Subdomain: %s\n", f.subdomainInput.Value()))
	b.WriteString(fmt.Sprintf("Local port: %s\n\n", f.portInput.Value()))

	b.WriteString(confirmStyle.Render("Set up port forward? (y/n)"))
	return b.String()
}
