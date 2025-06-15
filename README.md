<h1 align="center">  Binance Proxy</h1>
<p align="center">
A fast and simple <b>Websocket Proxy</b> for the <b>Binance API</b> written in <b>GoLang</b>. Mimics the behavior of API endpoints to avoid rate limiting imposed on IP's when using REST queries. Intended Usage for multiple instances of applications querying the Binance API at a rate that might lead to banning or blocking, like for example the <a href="https://github.com/freqtrade/freqtrade">Freqtrade Trading Bot</a>, or any other similar application. </p>

<p align="center"><a href="https://github.com/stash86/binance-proxy/releases" target="_blank"><img src="https://img.shields.io/github/v/release/stash86/binance-proxy?style=for-the-badge" alt="latest version" /></a>&nbsp;<img src="https://img.shields.io/github/go-mod/go-version/stash86/binance-proxy?style=for-the-badge" alt="go version" />&nbsp;<img src="https://img.shields.io/tokei/lines/github/stash86/binance-proxy?color=pink&style=for-the-badge" />&nbsp;<a href="https://github.com/stash86/binance-proxy/issues" target="_blank"><img src="https://img.shields.io/github/issues/stash86/binance-proxy?color=purple&style=for-the-badge" alt="github issues" /></a>&nbsp;<img src="https://img.shields.io/github/license/stash86/binance-proxy?color=red&style=for-the-badge" alt="license" /></p>

## ⚡ Quick Start
You can download the pre-compiled binary for the architecture of your choice from the [relaseses page](https://github.com/stash86/binance-proxy/releases) on GitHub.

Unzip the package to a folder of choice, preferably one that's in `$PATH`
```bash
tar -xf binance-proxy_1.2.4_Linux_x86_64.tar.gz -C /usr/local/bin 
```
Starting the proxy:
```bash
binance-proxy
```
That's all you need to know to start! 🎉

Once running, you can check the proxy status at:
- SPOT: `http://localhost:8090/status`  
- FUTURES: `http://localhost:8091/status`

And restart the service remotely at:
- SPOT: `http://localhost:8090/restart`
- FUTURES: `http://localhost:8091/restart`

### 🐳 Docker-way to quick start

If you don't want to install or compile the binance-proxy to your system, feel free using the prebuild  [Docker images](https://hub.docker.com/r/stash86/binance-proxy) and run it from an isolated container:

```bash
docker run --rm -d stash86/binance-proxy:latest
```
ℹ️ Please pay attention to configuring network access, per default the ports `8090` and `8091` are exposed, if you specify different ports via parameters, you will need to re-configure your docker setup. Please refer to the [docker network documentation](https://docs.docker.com/network/), how to adjust this inside a container.

## ⚒️ Installing from source

First of all, [download](https://golang.org/dl/) and install **Go**. Version `1.17` or higher is required.

Installation is done by using the [`go install`](https://golang.org/cmd/go/#hdr-Compile_and_install_packages_and_dependencies) command and rename installed binary in `$GOPATH/bin`:

```bash
go install github.com/stash86/binance-proxy/cmd/binance-proxy
```

## 📖 Basic Usage
The proxy listens automatically on port **8090** for Spot markets, and port **8091** for Futures markets. Available options for parametrizations are available via `-h`
```
Usage:
  binance-proxy [OPTIONS]

Application Options:
  -v, --verbose                Verbose output (increase with -vv) [$BPX_VERBOSE]
  -p, --port-spot=             Port to which to bind for SPOT markets (default: 8090) [$BPX_PORT_SPOT]
  -t, --port-futures=          Port to which to bind for FUTURES markets (default: 8091) [$BPX_PORT_FUTURES]
  -c, --disable-fake-candles   Disable generation of fake candles (ohlcv) when sockets have not delivered data yet [$BPX_DISABLE_FAKE_CANDLES]
  -s, --disable-spot           Disable proxying spot markets [$BPX_DISABLE_SPOT]
  -f, --disable-futures        Disable proxying futures markets [$BPX_DISABLE_FUTURES]
  -a, --always-show-forwards   Always show requests forwarded via REST even if verbose is disabled [$BPX_ALWAYS_SHOW_FORWARDS]

Help Options:
  -h, --help                   Show this help message
```
### 🪙 Example Usage with Freqtrade
**Freqtrade** needs to be aware, that the **API endpoint** for querying the exchange is not the public endpoint, which is usually `https://api.binance.com` but instead queries are being proxied. To achieve that, the appropriate `config.json` needs to be adjusted in the `{ exchange: { urls: { api: public: "..."} } }` section.

```json
{
    "exchange": {
        "name": "binance",
        "key": "",
        "secret": "",
        "ccxt_config": {
            "enableRateLimit": false,
            "urls": {
                "api": {
                    "public": "http://127.0.0.1:8090/api/v3"
                }
            }
        },
        "ccxt_async_config": {
            "enableRateLimit": false
        }
    }
}
```
This example assumes, that `binance-proxy` is running on the same host as the consuming application, thus `localhost` or `127.0.0.1` is used as the target address. Should `binance-proxy` run in a separate 🐳 **Docker** container, a separate instance or a k8s pod, the target address has to be replaced respectively, and it needs to be ensured that the required ports (8090/8091 per default) are opened for requests.

## ➡️ Supported API endpoints for caching
| Endpoint | Market | Purpose |  Socket Update Interval | Comments |
|----------|--------|---------|----------|---------|
|`/api/v3/klines`<br/>`/fapi/v1/klines`| spot/futures | Kline/candlestick bars for a symbol|~ 2s|Websocket is closed if there is no following request after `2 * interval_time` (for example: A websocket for a symbol on `5m` timeframe is closed after 10 minutes.<br/><br/>Following requests for `klines` can not be delivered from the websocket cache:<br/><li>`limit` parameter is > 1000</ul><li>`startTime` or `endTime` have been specified</ul>|
|`/api/v3/depth`<br/>`/fapi/v1/depth`|spot/futures|Order Book (Depth)|100ms|Websocket is closed if there is no following request after 2 minutes.<br/><br/>The `depth` endpoint serves only a maximum depth of 20.|
|`/api/v3/ticker/24hr`|spot|24hr ticker price change statistics|2s/100ms (see comments)|Websocket is closed if there is no following request after 2 minutes.<br/><br/>For faster updates the values for <li>`lastPrice`</ul><li>`bidPrice`</ul><li>`askPrice`</ul><br>are taken from the `bookTicker` which is updated in an interval of 100ms.|
|`/api/v3/exchangeInfo`<br/>`/fapi/v1/exchangeInfo`| spot/futures| Current exchange trading rules and symbol information|60s (see comments)|`exchangeInfo` is fetched periodically via REST every 60 seconds. It is not a websocket endpoint but just being cached during runtime.|

> 🚨 Every **other** REST query to an endpoint is being **forwarded** 1:1 to the **API** at https://api.binance.com !

## 📊 Status Endpoint

The proxy includes a built-in status endpoint to monitor the health and performance of the service:

### 🔍 Accessing the Status
- **SPOT markets**: `http://localhost:8090/status`
- **FUTURES markets**: `http://localhost:8091/status`

### 📈 Status Information
The status endpoint provides comprehensive information about the proxy service:

```json
{
  "proxy_status": {
    "service": "binance-proxy",
    "healthy": true,
    "start_time": "2025-06-15T10:30:00Z",
    "uptime": "2h15m30s",
    "requests": 1542,
    "errors": 3,
    "error_rate": 0.19,
    "last_error": "connection timeout",
    "last_error_at": "2025-06-15T12:42:15Z",
    "timestamp": "2025-06-15T12:45:30Z"
  },
  "class": "SPOT",
  "ban_info": {
    "banned": false,
    "recovery_time": null
  },
  "config": {
    "fake_kline_enabled": true,
    "always_show_forwards": false
  }
}
```

### 📋 Status Fields
| Field | Description |
|-------|-------------|
| `service` | Service name identifier |
| `healthy` | Overall health status (becomes false if error rate > 10%) |
| `start_time` | When the service was started |
| `uptime` | How long the service has been running |
| `requests` | Total number of requests processed |
| `errors` | Total number of errors encountered |
| `error_rate` | Percentage of requests that resulted in errors |
| `last_error` | Most recent error message (if any) |
| `last_error_at` | Timestamp of the most recent error |
| `banned` | Whether the API is currently banned by Binance |
| `recovery_time` | Expected recovery time if banned |

### 🔧 Usage Examples
```bash
# Check SPOT market status
curl http://localhost:8090/status

# Check FUTURES market status  
curl http://localhost:8091/status

# Monitor in a loop (Linux/Mac)
watch -n 5 "curl -s http://localhost:8090/status | jq"
```

## 🔄 Restart Endpoint

The proxy includes a restart endpoint for remote service management:

### 🚀 Accessing the Restart
- **SPOT markets**: `http://localhost:8090/restart`
- **FUTURES markets**: `http://localhost:8091/restart`

### ⚡ Restart Response
```json
{
  "message": "Restart initiated",
  "status": "success",
  "class": "SPOT",
  "timestamp": "2025-06-15T12:45:30Z",
  "warning": "Service will restart in 2 seconds. This will interrupt all active connections."
}
```

### 🔧 How It Works
1. **Immediate response** sent to confirm restart initiation
2. **2-second delay** to ensure response is delivered
3. **Graceful shutdown** of the current process
4. **Automatic restart** (requires process manager or Docker restart policy)

### 🐳 Docker Setup for Automatic Restart

#### **Basic Setup (Recommended)**
```yaml
version: '3.8'
services:
  binance-proxy:
    build: .
    ports:
      - "8090:8090"
      - "8091:8091"
    restart: unless-stopped  # Enables automatic restart on crash/manual restart
```

#### **Advanced Setup with Health Monitoring**
```yaml
version: '3.8'
services:
  binance-proxy:
    build: .
    ports:
      - "8090:8090"
      - "8091:8091"
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8090/status"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 40s
```

#### **Complete Setup with Auto-Heal (Restart on Health Failure)**
```yaml
version: '3.8'
services:
  binance-proxy:
    build: .
    ports:
      - "8090:8090"
      - "8091:8091"
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8090/status"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 40s
    labels:
      - "autoheal=true"

  autoheal:
    image: willfarrell/autoheal:latest
    restart: always
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
    environment:
      - AUTOHEAL_CONTAINER_LABEL=autoheal
      - AUTOHEAL_INTERVAL=5
```

#### **Restart Policy Options**
| Policy | Description | Use Case |
|--------|-------------|----------|
| `no` | Never restart (default) | Development/testing |
| `always` | Always restart | Critical services |
| `unless-stopped` | Restart unless manually stopped | **Recommended for production** |
| `on-failure` | Restart only on non-zero exit | Services that shouldn't restart on normal stop |

#### **What Each Setup Provides**

**Basic Setup:**
- ✅ **Manual restart** via `/restart` endpoint works
- ✅ **Automatic restart** if container crashes
- ✅ **Survives Docker daemon restarts**

**Health Monitoring Setup:**
- ✅ All basic features
- ✅ **Health status** visible in `docker-compose ps`
- ✅ **Monitor service health** over time
- ❌ No automatic restart on health failure

**Complete Auto-Heal Setup:**
- ✅ All previous features
- ✅ **Automatic restart** when health checks fail
- ✅ **Full automation** - handles crashes AND hangs
- ✅ **Production-ready** monitoring and recovery

### 🖥️ Non-Docker Automatic Restart Setup

#### **Linux - Systemd (Recommended)**
Create a systemd service file:

```bash
# Create service file
sudo nano /etc/systemd/system/binance-proxy.service
```

```ini
[Unit]
Description=Binance Proxy Service
After=network.target
StartLimitIntervalSec=0

[Service]
Type=simple
Restart=always
RestartSec=5
User=binance-proxy
ExecStart=/usr/local/bin/binance-proxy
WorkingDirectory=/opt/binance-proxy
Environment=BPX_VERBOSE=true

# Health check and restart on failure
ExecReload=/bin/kill -HUP $MAINPID
TimeoutStopSec=10

[Install]
WantedBy=multi-user.target
```

```bash
# Enable and start service
sudo systemctl daemon-reload
sudo systemctl enable binance-proxy
sudo systemctl start binance-proxy

# Check status
sudo systemctl status binance-proxy
```

#### **Windows - NSSM (Non-Sucking Service Manager)**
```powershell
# Download and install NSSM
# https://nssm.cc/download

# Install as Windows service
nssm install BinanceProxy "C:\Path\To\binance-proxy.exe"
nssm set BinanceProxy AppDirectory "C:\Path\To"
nssm set BinanceProxy DisplayName "Binance Proxy Service"
nssm set BinanceProxy Description "Binance API Proxy with automatic restart"

# Configure automatic restart
nssm set BinanceProxy AppExit Default Restart
nssm set BinanceProxy AppRestartDelay 5000

# Start service
nssm start BinanceProxy
```

#### **Linux - Supervisor**
```bash
# Install supervisor
sudo apt-get install supervisor

# Create config file
sudo nano /etc/supervisor/conf.d/binance-proxy.conf
```

```ini
[program:binance-proxy]
command=/usr/local/bin/binance-proxy
directory=/opt/binance-proxy
user=binance-proxy
autostart=true
autorestart=true
startretries=3
redirect_stderr=true
stdout_logfile=/var/log/binance-proxy.log
environment=BPX_VERBOSE=true
```

```bash
# Update supervisor and start
sudo supervisorctl reread
sudo supervisorctl update
sudo supervisorctl start binance-proxy
```

#### **macOS - LaunchDaemon**
```bash
# Create launchd plist
sudo nano /Library/LaunchDaemons/com.binance.proxy.plist
```

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.binance.proxy</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/binance-proxy</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardErrorPath</key>
    <string>/var/log/binance-proxy.log</string>
    <key>StandardOutPath</key>
    <string>/var/log/binance-proxy.log</string>
</dict>
</plist>
```

```bash
# Load and start service
sudo launchctl load /Library/LaunchDaemons/com.binance.proxy.plist
sudo launchctl start com.binance.proxy
```

#### **Process Manager Comparison**

| Method | Platform | Complexity | Features | Production Ready |
|--------|----------|------------|----------|------------------|
| **Systemd** | Linux | Medium | ✅ Logs, ✅ Health checks, ✅ Auto-restart | ✅ Excellent |
| **NSSM** | Windows | Easy | ✅ GUI config, ✅ Auto-restart | ✅ Good |
| **Supervisor** | Linux | Easy | ✅ Web UI, ✅ Process groups | ✅ Good |
| **LaunchDaemon** | macOS | Medium | ✅ System integration | ✅ Good |

#### **Manual Script Alternative (Basic)**
For development or simple setups:

```bash
#!/bin/bash
# restart-loop.sh
while true; do
    echo "Starting binance-proxy..."
    ./binance-proxy
    echo "Process exited with code $?. Restarting in 5 seconds..."
    sleep 5
done
```

```bash
# Make executable and run
chmod +x restart-loop.sh
nohup ./restart-loop.sh > proxy.log 2>&1 &
```

### ⚠️ Important Notes
- **Security**: No authentication required - restrict network access in production
- **Scope**: Restarting either port restarts the entire service (both SPOT and FUTURES)
- **Downtime**: Expect 10-15 seconds total restart time
- **Fresh State**: Complete reset of connections, caches, and statistics

### 🔧 Usage Examples
```bash
# Restart from command line
curl http://localhost:8090/restart

# Or simply visit in browser:
# http://localhost:8090/restart
```


## ⚙️ Commands & Options

The following parameters are available to control the behavior of **binance-proxy**:
```bash
binance-proxy [OPTION]
```

| Option | Environment Variable | Description                                              | Type   | Default | Required? |
| ------ | ------------------|-------------------------------------- | ------ | ------- | --------- |
| `-v`   | `$BPX_VERBOSE` | Sets the verbosity to debug level. | `bool` | `false` | No        |
| `-vv`  |`$BPX_VERBOSE`| Sets the verbosity to trace level. | `bool` | `false` | No        |
| `-p`   |`$BPX_PORT_SPOT`| Specifies the listen port for **SPOT** market proxy. | `int` | `8090` | No        |
| `-t`   |`$BPX_PORT_FUTURES`| Specifies the listen port for **FUTURES** market proxy. | `int` | `8091` | No        |
| `-c`   |`$BPX_DISABLE_FAKE_CANDLES`| Disables the generation of fake candles, when not yet recieved through websockets. | `bool` | `false` | No        |
| `-s`   |`$BPX_DISABLE_SPOT`| Disables proxy for **SPOT** markets. | `bool` | `false` | No        |
| `-f`   |`$BPX_DISABLE_FUTURES`| Disables proxy for **FUTURES** markets. | `bool` | `false` | No        |
| `-a`   |`$BPX_ALWAYS_SHOW_FORWARDS`| Always show requests forwarded via REST even if verbose is disabled | `bool` | `false` | No        |

Instead of using command line switches environment variables can be used, there are several ways how those can be implemented. For example `.env` files could be used in combination with `docker-compose`. 

Passing variables to a docker container can also be achieved in different ways, please see the documentation for all available options [here](https://docs.docker.com/compose/environment-variables/).

## 🐞 Bug / Feature Request

If you find a bug (the proxy couldn't handle the query and / or gave undesired results), kindly open an issue [here](https://github.com/stash86/binance-proxy/issues/new) by including a **logfile** and a **meaningful description** of the problem.

If you'd like to request a new function, feel free to do so by opening an issue [here](https://github.com/stash86/binance-proxy/issues/new). 

## 💻 Development
Want to contribute? **Great!🥳**

To fix a bug or enhance an existing module, follow these steps:

- Fork the repo
- Create a new branch (`git checkout -b improve-feature`)
- Make the appropriate changes in the files
- Add changes to reflect the changes made
- Commit your changes (`git commit -am 'Improve feature'`)
- Push to the branch (`git push origin improve-feature`)
- Create a Pull Request

## 🙏 Credits
+ [@adrianceding](https://github.com/adrianceding) for creating the original version, available [here](https://github.com/adrianceding/binance-proxy).

## ⚠️ License

`binance-proxy` is free and open-source software licensed under the [MIT License](https://github.com/stash86/binance-proxy/blob/main/LICENSE). 

By submitting a pull request to this project, you agree to license your contribution under the MIT license to this project.

### 🧬 Third-party library licenses
+ [go-binance](https://github.com/adshao/go-binance/blob/master/LICENSE)
+ [go-flags](https://github.com/jessevdk/go-flags/blob/master/LICENSE)
+ [logrus](https://github.com/sirupsen/logrus/blob/master/LICENSE)
+ [go-time](https://cs.opensource.google/go/x/time/+/master:LICENSE)
+ [go-simplejson](https://github.com/bitly/go-simplejson/blob/master/LICENSE)
+ [websocket](https://github.com/gorilla/websocket/blob/master/LICENSE)
+ [objx](https://github.com/stretchr/objx/blob/master/LICENSE)
+ [testify](https://github.com/stretchr/testify/blob/master/LICENSE)