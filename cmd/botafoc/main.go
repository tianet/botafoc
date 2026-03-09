package main

import (
	"fmt"
	"os"
	"os/exec"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
	"github.com/tianet/botafoc/internal/config"
	"github.com/tianet/botafoc/internal/ipc"
	"github.com/tianet/botafoc/internal/portforward"
	"github.com/tianet/botafoc/internal/proxy"
	"github.com/tianet/botafoc/internal/tui"
)

// proxyAdapter wraps *proxy.Proxy to satisfy tui.RouteManager for single-process mode.
type proxyAdapter struct {
	p *proxy.Proxy
}

func (a *proxyAdapter) List() []tui.RouteInfo {
	routes := a.p.Routes()
	infos := make([]tui.RouteInfo, len(routes))
	for i, r := range routes {
		infos[i] = tui.RouteInfo{
			Subdomain: r.Subdomain,
			Target:    r.Target,
			Healthy:   r.Healthy,
		}
	}
	return infos
}

func (a *proxyAdapter) Add(subdomain string, target int) error {
	a.p.AddRoute(subdomain, target)
	return nil
}

func (a *proxyAdapter) Remove(subdomain string) error {
	a.p.RemoveRoute(subdomain)
	return nil
}

func (a *proxyAdapter) Logs(subdomain string) []tui.LogInfo {
	entries := a.p.Logs(subdomain)
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
	return infos
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "botafoc",
		Short: "Local reverse proxy manager",
		RunE:  runRoot,
	}

	rootCmd.Flags().Bool("daemon", false, "Run proxy as a background daemon")

	// Hidden subcommand used for daemon re-exec
	daemonCmd := &cobra.Command{
		Use:    "__daemon",
		Hidden: true,
		RunE:   runDaemon,
	}
	rootCmd.AddCommand(daemonCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runRoot(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	daemonFlag, _ := cmd.Flags().GetBool("daemon")
	isDaemon := daemonFlag || cfg.Daemon

	if isDaemon {
		return runWithDaemon(cfg)
	}
	return runSingleProcess(cfg)
}

func runSingleProcess(cfg *config.Config) error {
	p := proxy.New(cfg.BaseDomain, cfg.ListenPort)

	for _, r := range cfg.Routes {
		p.AddRoute(r.Subdomain, r.Target)
	}

	go func() {
		if err := p.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "proxy error: %v\n", err)
		}
	}()
	defer p.Stop()

	pfManager := portforward.NewManager()
	defer pfManager.StopAll()

	manager := &proxyAdapter{p: p}
	model := tui.New(manager, pfManager, false, cfg.ListenPort)
	program := tea.NewProgram(model)

	if _, err := program.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}

func runWithDaemon(cfg *config.Config) error {
	// Try connecting to existing daemon
	client, err := ipc.NewClient()
	if err != nil {
		// No daemon running — spawn one
		if err := spawnDaemon(); err != nil {
			return fmt.Errorf("spawn daemon: %w", err)
		}

		// Retry connecting
		client, err = ipc.NewClient()
		if err != nil {
			return fmt.Errorf("connect to new daemon: %w", err)
		}
	}
	defer client.Close()

	pfManager := portforward.NewManager()
	defer pfManager.StopAll()

	model := tui.New(client, pfManager, true, cfg.ListenPort)
	program := tea.NewProgram(model)

	if _, err := program.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}

	// Prompt to stop daemon
	fmt.Print("Stop the daemon? [y/N] ")
	var answer string
	fmt.Scanln(&answer)
	if answer == "y" || answer == "Y" {
		// Reconnect to send shutdown
		c2, err := ipc.NewClient()
		if err == nil {
			c2.Shutdown()
			c2.Close()
		}
	}

	return nil
}

func spawnDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command(exe, "__daemon")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil

	if err := cmd.Start(); err != nil {
		return err
	}

	// Detach — don't wait for the child
	cmd.Process.Release()

	// Give it a moment to start up
	// We'll retry connection in the caller
	return nil
}

// runDaemon is the hidden __daemon subcommand that runs the proxy + IPC server.
func runDaemon(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	p := proxy.New(cfg.BaseDomain, cfg.ListenPort)
	for _, r := range cfg.Routes {
		p.AddRoute(r.Subdomain, r.Target)
	}

	server := ipc.NewServer(p)
	if err := server.Start(); err != nil {
		return fmt.Errorf("ipc server: %w", err)
	}

	// Start proxy (blocks until stopped)
	return p.Start()
}
