package tui

import (
	"fmt"
	"strings"
	"time"
)

type logViewer struct {
	entries   []LogInfo
	offset    int
	height    int
	filter    string // subdomain filter, empty = all
}

func newLogViewer() logViewer {
	return logViewer{height: 15}
}

func (lv *logViewer) setEntries(entries []LogInfo) {
	lv.entries = entries
	// Auto-scroll to bottom if near the end
	maxOffset := lv.maxOffset()
	if lv.offset >= maxOffset-1 || maxOffset == 0 {
		lv.offset = maxOffset
	}
}

func (lv *logViewer) maxOffset() int {
	if len(lv.entries) <= lv.height {
		return 0
	}
	return len(lv.entries) - lv.height
}

func (lv *logViewer) scrollUp() {
	if lv.offset > 0 {
		lv.offset--
	}
}

func (lv *logViewer) scrollDown() {
	if lv.offset < lv.maxOffset() {
		lv.offset++
	}
}

func (lv *logViewer) view() string {
	var b strings.Builder

	title := "All Logs"
	if lv.filter != "" {
		title = fmt.Sprintf("Logs: %s", lv.filter)
	}
	b.WriteString(formLabelStyle.Render(title) + "\n\n")

	if len(lv.entries) == 0 {
		b.WriteString(helpStyle.Render("  No requests logged yet."))
		return b.String()
	}

	// Header
	header := fmt.Sprintf("  %-8s %-12s %-7s %-30s %6s %10s",
		"Time", "Subdomain", "Method", "Path", "Status", "Duration")
	b.WriteString(logHeaderStyle.Render(header) + "\n")

	end := lv.offset + lv.height
	if end > len(lv.entries) {
		end = len(lv.entries)
	}
	visible := lv.entries[lv.offset:end]

	for _, e := range visible {
		ts := e.Time.Format("15:04:05")
		path := e.Path
		if len(path) > 30 {
			path = path[:27] + "..."
		}
		statusStr := fmt.Sprintf("%d", e.Status)
		style := logLineStyle
		if e.Status >= 400 && e.Status < 500 {
			style = logWarnStyle
		} else if e.Status >= 500 {
			style = logErrorStyle
		}
		line := fmt.Sprintf("  %-8s %-12s %-7s %-30s %6s %10s",
			ts, e.Subdomain, e.Method, path, statusStr, e.Duration.Truncate(100*time.Microsecond))
		b.WriteString(style.Render(line) + "\n")
	}

	// Scroll indicator
	if lv.maxOffset() > 0 {
		b.WriteString(helpStyle.Render(fmt.Sprintf("\n  [%d/%d]", lv.offset+1, lv.maxOffset()+1)))
	}

	return b.String()
}
