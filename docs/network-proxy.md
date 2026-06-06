# Network & Proxy Configuration

Reasonix makes HTTP requests for model inference, balance queries, updates, and web fetching. The network subsystem provides configurable proxy support, OS-level proxy detection, and SSRF protection. This document covers the proxy modes, the network client architecture, and the security boundaries between different HTTP consumers.

## Proxy Modes

Reasonix supports four proxy modes, configured in `reasonix.toml`:

| Mode | Description |
|------|-------------|
| `auto` | Try OS proxy first, fall back to environment variables, then direct |
| `env` | Use only `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` environment variables |
| `custom` | Use a manually configured proxy server |
| `off` | Never use a proxy, even if one is detected |

The default is `auto` for maximum compatibility — users behind corporate proxies typically don't need any configuration.

### Mode Resolution

```go
func NormalizeMode(mode string) string
```

Empty and unknown modes map to `auto`, preserving a fail-open default for older configs that don't specify a mode.

## Custom Proxy Configuration

When mode is `custom`, the proxy is built from individual fields:

```go
type ProxySpec struct {
    Mode        string
    URL         string    // advanced override — bypasses Type/Server/Port composition
    NoProxy     string    // hosts that bypass the proxy
    Type        string    // "http" or "socks5"
    Server      string    // proxy hostname
    Port        int       // proxy port
    Username    string    // basic auth username
    Password    string    // basic auth password
    DirectHosts []string  // always-bypass hosts (derived from no_proxy providers)
}
```

### URL Composition

If `URL` is set, it's used directly. Otherwise, `Type`, `Server`, `Port`, and `Username`/`Password` are composed into a proxy URL:

- HTTP proxy: `http://user:pass@server:port`
- SOCKS5 proxy: `socks5://user:pass@server:port`

### NoProxy

The `NoProxy` field lists hosts that bypass the proxy, using the standard format: comma-separated hostnames, with optional `*` wildcards. Common examples:

- `localhost,127.0.0.1`
- `*.internal.example.com`
- `api.deepseek.com`

## OS-Level Proxy Detection

### Windows (`sysproxy`)

On Windows, the `sysproxy` package reads the system proxy settings (Internet Explorer/WinHTTP configuration):

- **Auto-detect**: WPAD (Web Proxy Auto-Discovery) protocol
- **PAC (Proxy Auto-Config)**: Downloads and executes a PAC script to determine the proxy for a given URL
- **Static proxy**: Direct proxy configuration from Windows settings
- **Bypass list**: Hosts that bypass the proxy

The `ForURL` function returns the proxy URL for a given target:

```go
func ForURL(target string) *url.URL
```

### macOS & Linux

On macOS and Linux, `auto` mode falls back to environment variables (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`). There is no native system proxy API on these platforms.

## Network Client Architecture

### Shared Client (`netclient`)

The `netclient` package builds HTTP clients that share Reasonix's user-facing proxy settings:

```go
type TransportOptions struct {
    DialTimeout           time.Duration
    KeepAlive             time.Duration
    TLSHandshakeTimeout   time.Duration
    ResponseHeaderTimeout time.Duration
}
```

The shared client is used by:

- **Provider API calls** (model inference)
- **Balance queries** (`billing.Fetch`)
- **CodeGraph downloads** (`codegraph.Install`)
- **Update checks**

### Separate Client (`web_fetch`)

The `web_fetch` tool uses its **own** HTTP client with SSRF protection, separate from the shared `netclient`. This is a deliberate security boundary:

- The shared client makes requests to trusted endpoints (provider APIs, update servers)
- `web_fetch` makes requests to **arbitrary user-specified URLs** — a fundamentally different trust level
- SSRF (Server-Side Request Forgery) protection prevents the model from accessing internal network resources

### Transport Configuration

All HTTP clients share consistent timeout defaults:

| Setting | Default |
|---------|---------|
| Dial timeout | 30s |
| Keep-alive | 30s |
| TLS handshake timeout | 10s |
| Response header timeout | 30s |
| Idle conn timeout | 90s |

These can be overridden per-client via `TransportOptions`.

## Proxy Configuration in `reasonix.toml`

```toml
[network]
proxy_mode = "auto"          # auto, env, custom, off
proxy_url = ""               # direct URL override
proxy_type = "http"          # http or socks5
proxy_server = ""            # proxy hostname
proxy_port = 0               # proxy port
proxy_username = ""          # basic auth username
proxy_password = ""          # basic auth password
no_proxy = ""                # bypass list
```

### Examples

**Corporate HTTP proxy:**
```toml
[network]
proxy_mode = "custom"
proxy_type = "http"
proxy_server = "proxy.corp.example.com"
proxy_port = 8080
proxy_username = "domain\\user"
proxy_password = "secret"
no_proxy = "localhost,*.corp.example.com"
```

**SOCKS5 proxy:**
```toml
[network]
proxy_mode = "custom"
proxy_type = "socks5"
proxy_server = "127.0.0.1"
proxy_port = 1080
```

**Environment-only proxy:**
```toml
[network]
proxy_mode = "env"
```

This relies on `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` environment variables.

**Disable proxy:**
```toml
[network]
proxy_mode = "off"
```

Useful when the OS detects a proxy that doesn't actually work (common in VPN scenarios).

## Diagnostics

### `reasonix doctor` Network Report

The doctor command includes a network section:

```
network
  proxy_mode   auto
  proxy        http://proxy.corp.example.com:8080
  no_proxy     true
```

The `netclient.Summary()` function renders the resolved proxy configuration compactly, showing the effective proxy URL (redacted credentials) or "direct" when no proxy applies.

### Proxy Spec Summary

```go
func Summary(spec netclient.ProxySpec) string
```

Returns a human-readable summary:
- `direct` — no proxy in use
- `http://proxy:8080` — HTTP proxy
- `socks5://proxy:1080` — SOCKS5 proxy
- `env (HTTP_PROXY=http://proxy:8080)` — environment-detected proxy

## Security Considerations

### SSRF Protection

The `web_fetch` tool's separate HTTP client implements SSRF (Server-Side Request Forgery) protection:

- Blocks requests to private IP ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)
- Blocks requests to localhost (127.0.0.0/8, ::1)
- Blocks requests to link-local addresses (169.254.0.0/16)
- Uses a custom dialer that checks the resolved IP before connecting

This prevents the model from using `web_fetch` to probe internal network services.

### Credential Handling

Proxy credentials are stored in the configuration file but are **never** included in:

- Diagnostic reports (`reasonix doctor` redacts credentials)
- Log output
- Error messages

The `hostOnly` function in the doctor report strips credentials from URLs, showing only the hostname and port.

### Proxy Authentication

Proxy authentication uses HTTP Basic Auth, which sends credentials in base64 encoding (not encryption). For SOCKS5 proxies, username/password authentication is supported per RFC 1929. Users should ensure their proxy connections are over a secure channel (e.g., VPN or TLS) when using authentication.
