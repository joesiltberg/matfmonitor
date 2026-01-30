# matfmonitor

A web-based monitoring service for MATF (Mutually Authenticating TLS Federation) server health status.

matfmonitor regularly checks the health of all servers registered in a MATF federation's metadata by:
- Performing TLS handshakes against each server
- Verifying server certificates against the published metadata pins
- Checking certificate validity (expiry, CN/SAN matching)
- Displaying results on a web dashboard

## Features

- **Automatic metadata sync**: Uses [bowness](https://github.com/joesiltberg/bowness) to download and verify federation metadata
- **Rate-limited health checks**: Configurable parallel checks, rate limits, and check intervals
- **Persistent storage**: SQLite database to persist status across restarts
- **Certificate verification**: Validates fingerprints against metadata pins, checks expiry and hostname matching
- **Client cert tolerance**: Servers requiring client certificates are still verified (certificate is obtained during TLS handshake)
- **Graceful shutdown**: Clean shutdown on Ctrl-C or SIGTERM/SIGQUIT

## Installation

```bash
go install github.com/joesiltberg/matfmonitor/cmd/matfmonitor@latest
```

Or build from source:

```bash
git clone https://github.com/joesiltberg/matfmonitor
cd matfmonitor
go build ./cmd/matfmonitor
```

## Configuration

Create a configuration file (e.g., `config.yaml`):

```yaml
# Federation metadata settings (required)
metadataURL: https://md.example.com/federation.jws
jwksPath: /path/to/jwks
cachePath: /path/to/metadata-cache.json

# Database settings
databasePath: ./matfmonitor.db

# Web server settings
listenAddress: :8080

# Health check limits
maxParallelChecks: 5    # Maximum concurrent TLS checks (default: 5)
checksPerMinute: 20     # Rate limit for checks per minute (default: 20)
minCheckInterval: 5h    # Minimum time between checks of the same server (default: 5h)

# TLS settings
tlsTimeout: 10s         # Timeout for TLS handshake (default: 10s)
```

### Environment Variable Overrides

All configuration options can be overridden using environment variables with the `MATFMONITOR_` prefix:

```bash
export MATFMONITOR_LISTENADDRESS=:9090
export MATFMONITOR_MAXPARALLELCHECKS=10
```

## Usage

```bash
matfmonitor -config config.yaml
```

Then open `http://localhost:8080` in your browser to view the status dashboard.

## Status Page

The web dashboard shows:

- **Summary counts**: Healthy, unhealthy, and unchecked servers
- **Entities**: Sorted alphabetically by organization name
  - Organization name and ID
  - Health status (green = all healthy, red = at least one unhealthy, gray = pending)
- **Servers** (for each entity):
  - Base URI and tags
  - Health status indicator
  - Last checked time
  - Certificate CN and expiry date
  - Error messages for unhealthy servers

### Health Status

| Status | Condition |
|--------|-----------|
| 🟢 Healthy | TLS handshake succeeded, certificate valid, fingerprint matches metadata |
| 🔴 Unhealthy | Connection failed, certificate expired, fingerprint mismatch, or CN/SAN mismatch |
| ⚪ Not Checked | Server hasn't been checked yet |

## How It Works

1. **Metadata sync**: matfmonitor uses bowness's MetadataStore to regularly download and verify the federation metadata
2. **Server discovery**: When metadata changes, servers are synced to the SQLite database
3. **Health checks**: A scheduler runs periodic checks, prioritizing servers that haven't been checked for the longest time
4. **TLS verification**:
   - Connect to server using TLS
   - Retrieve server certificate (even if client cert is required)
   - Verify certificate hasn't expired
   - Verify CN or SAN matches hostname
   - Calculate fingerprint and verify against metadata pins
5. **Web display**: The status page reads from the database and metadata to render the current status

## License

MIT License - see [LICENSE](LICENSE) file for details.
