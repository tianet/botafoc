# botafoc

A TUI-based local reverse proxy manager. Maps subdomains (e.g., `app.localhost` → `localhost:3000`) and provides an interactive terminal UI for managing routes.

## Install

```bash
go install github.com/tianet/botafoc/cmd/botafoc@latest
```

Or build from source:

```bash
go build ./cmd/botafoc
```

## Usage

```bash
# Single-process mode (default): proxy + TUI in one process
botafoc

# Daemon mode: proxy runs in background, TUI connects via IPC
botafoc --daemon
```

### TUI Keybindings

| Key | Action |
|-----|--------|
| `a` | Add new route |
| `d` / `Delete` | Remove selected route |
| `q` / `Ctrl+C` | Quit |
| `Enter` | Confirm form |
| `Escape` | Cancel form |
| `↑/↓` | Navigate table |

## Configuration

Place config at `~/.config/botafoc/botafoc.yaml` or set `BOTAFOC_CONFIG` to a custom path.

```yaml
listen_port: 8080
base_domain: .localhost
daemon: false
routes:
  - subdomain: app
    target: 3000
  - subdomain: api
    target: 8081
```

All fields can be overridden via environment variables: `BOTAFOC_LISTEN_PORT`, `BOTAFOC_BASE_DOMAIN`, `BOTAFOC_DAEMON`.

## How It Works

- **Single-process mode** (default): The proxy and TUI run in the same process. Quitting the TUI stops the proxy.
- **Daemon mode** (`--daemon`): The proxy runs as a background process. The TUI connects over a Unix socket (`/tmp/botafoc.sock`). Closing the TUI leaves the proxy running — reconnect by running `botafoc` again. On quit, you're prompted to stop the daemon.
- **Health checks**: Botafoc periodically checks if target ports are reachable and shows status in the TUI.
- **Base domain**: Uses `.localhost` by default, which resolves to `127.0.0.1` per RFC 6761 — no `/etc/hosts` changes needed.

## Example

```bash
# Start botafoc
botafoc

# In another terminal, start a web server on port 3000
python3 -m http.server 3000

# In the TUI, press 'a' and add route: subdomain=app, port=3000

# Test the proxy
curl -H "Host: app.localhost" http://localhost:8080
```
