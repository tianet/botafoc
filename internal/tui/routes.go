package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
)

type routeTable struct {
	table table.Model
}

func targetLabel(source, name, namespace string, port int) string {
	if name == "" {
		return fmt.Sprintf("localhost:%d", port)
	}
	switch source {
	case "docker":
		return fmt.Sprintf("docker/%s:%d", name, port)
	case "k8s-service":
		if namespace != "" {
			return fmt.Sprintf("svc/%s/%s:%d", namespace, name, port)
		}
		return fmt.Sprintf("svc/%s:%d", name, port)
	case "k8s-pod":
		if namespace != "" {
			return fmt.Sprintf("pod/%s/%s:%d", namespace, name, port)
		}
		return fmt.Sprintf("pod/%s:%d", name, port)
	default:
		return fmt.Sprintf("localhost:%d", port)
	}
}

func newRouteTable() routeTable {
	columns := []table.Column{
		{Title: "Subdomain", Width: 30},
		{Title: "Target", Width: 45},
		{Title: "Status", Width: 10},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows([]table.Row{}),
		table.WithFocused(true),
		table.WithHeight(10),
	)

	return routeTable{table: t}
}

func (rt *routeTable) updateRows(routes []routeRow) {
	rows := make([]table.Row, len(routes))
	for i, r := range routes {
		status := statusUnhealthy.Render("●")
		if r.healthy {
			status = statusHealthy.Render("●")
		}
		target := targetLabel(r.source, r.resourceName, r.namespace, r.port)
		rows[i] = table.Row{r.subdomain, target, status}
	}
	rt.table.SetRows(rows)
}

func (rt *routeTable) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	rt.table, cmd = rt.table.Update(msg)
	return cmd
}

func (rt *routeTable) view() string {
	return rt.table.View()
}

func (rt *routeTable) selectedSubdomain() string {
	row := rt.table.SelectedRow()
	if row == nil {
		return ""
	}
	return row[0]
}

func (rt *routeTable) selectedPort() int {
	row := rt.table.SelectedRow()
	if row == nil {
		return 0
	}
	// Target column is "Source :port" — extract the port after the colon
	target := row[1]
	if idx := strings.LastIndex(target, ":"); idx != -1 {
		port, _ := strconv.Atoi(strings.TrimSpace(target[idx+1:]))
		return port
	}
	return 0
}

type routeRow struct {
	subdomain    string
	port         int
	healthy      bool
	source       string // e.g. "docker", "k8s-service", "k8s-pod", or ""
	resourceName string // name of the container/service/pod
	namespace    string // k8s namespace; empty for docker
}
