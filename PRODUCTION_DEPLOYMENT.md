# Production Deployment Guide

This guide provides comprehensive instructions for deploying Binance Proxy in production environments.

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [Environment Setup](#environment-setup)
3. [Security Configuration](#security-configuration)
4. [Performance Optimization](#performance-optimization)
5. [Monitoring and Observability](#monitoring-and-observability)
6. [Deployment Strategies](#deployment-strategies)
7. [Scaling and Load Balancing](#scaling-and-load-balancing)
8. [Backup and Recovery](#backup-and-recovery)
9. [Troubleshooting](#troubleshooting)
10. [Maintenance](#maintenance)

## Prerequisites

### System Requirements

- **Operating System**: Linux (Ubuntu 20.04+ recommended)
- **Memory**: Minimum 2GB RAM (4GB recommended for production)
- **CPU**: 2+ cores recommended
- **Disk Space**: 10GB+ for logs and data
- **Network**: Stable internet connection with low latency to Binance

### Software Dependencies

```bash
# Install Go 1.19+
wget https://golang.org/dl/go1.21.0.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.21.0.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin

# Install Docker (optional)
sudo apt-get update
sudo apt-get install docker.io docker-compose

# Install monitoring tools
sudo apt-get install htop iotop nethogs
```

## Environment Setup

### 1. Initialize Production Environment

```bash
# Create production user
sudo useradd -m -s /bin/bash binance-proxy
sudo usermod -aG docker binance-proxy

# Switch to production user
sudo su - binance-proxy

# Initialize application
./binance-proxy-cli init --environment production
```

### 2. Directory Structure

```
/opt/binance-proxy/
├── bin/
│   ├── binance-proxy
│   └── binance-proxy-cli
├── config/
│   ├── production.yaml
│   ├── staging.yaml
│   └── development.yaml
├── logs/
├── data/
├── certs/
├── scripts/
└── api_keys.txt
```

### 3. Environment Variables

```bash
# /etc/environment
BPX_ENVIRONMENT=production
BPX_SERVER_PORT_SPOT=8090
BPX_SERVER_PORT_FUTURES=8091
BPX_FEAT_METRICS_PORT=8092
BPX_LOG_OUTPUT=/opt/binance-proxy/logs/app.log
BPX_SEC_ENABLE_API_KEY_AUTH=true
BPX_SEC_API_KEYS_FILE=/opt/binance-proxy/api_keys.txt
```

## Security Configuration

### 1. API Key Management

```bash
# Create secure API keys file
cat > /opt/binance-proxy/api_keys.txt << 'EOF'
# Production API Keys
# Format: name:key:permissions
trading_system:$(openssl rand -hex 32):read,write
monitoring:$(openssl rand -hex 32):read
admin:$(openssl rand -hex 32):read,write,admin
EOF

# Secure the file
chmod 600 /opt/binance-proxy/api_keys.txt
chown binance-proxy:binance-proxy /opt/binance-proxy/api_keys.txt
```

### 2. TLS Configuration

```bash
# Generate self-signed certificates (for testing)
openssl req -x509 -newkey rsa:4096 -keyout /opt/binance-proxy/certs/server.key \
    -out /opt/binance-proxy/certs/server.crt -days 365 -nodes \
    -subj "/C=US/ST=State/L=City/O=Organization/CN=binance-proxy"

# Or use Let's Encrypt for production
# certbot certonly --standalone -d your-domain.com
```

### 3. Firewall Configuration

```bash
# Configure UFW firewall
sudo ufw allow 22/tcp    # SSH
sudo ufw allow 8090/tcp  # Spot market
sudo ufw allow 8091/tcp  # Futures market
sudo ufw allow 8092/tcp  # Metrics (restrict to monitoring network)
sudo ufw enable

# For internal metrics, restrict access
sudo ufw allow from 10.0.0.0/8 to any port 8092
```

### 4. System Hardening

```bash
# Disable unnecessary services
sudo systemctl disable avahi-daemon
sudo systemctl disable cups

# Configure limits
cat >> /etc/security/limits.conf << 'EOF'
binance-proxy soft nofile 65536
binance-proxy hard nofile 65536
binance-proxy soft nproc 4096
binance-proxy hard nproc 4096
EOF

# Kernel parameters
cat >> /etc/sysctl.conf << 'EOF'
net.core.somaxconn = 4096
net.ipv4.tcp_keepalive_time = 600
net.ipv4.tcp_keepalive_intvl = 60
net.ipv4.tcp_keepalive_probes = 3
EOF

sudo sysctl -p
```

## Performance Optimization

### 1. Application Configuration

```yaml
# config/production.yaml
environment: production

# Performance settings
performance:
  gc_percent: 50
  memory_limit_mb: 1024
  enable_gc_tuning: true
  enable_ballast_memory: true
  ballast_size_mb: 64

# Server optimization
server:
  read_timeout: 30s
  write_timeout: 30s
  idle_timeout: 120s
  max_header_size: 8192

# Cache optimization
cache:
  max_memory_mb: 512
  default_ttl: 5m
  enable_compression: true
  cleanup_interval: 1m

# Rate limiting
rate_limit:
  spot_rps: 30
  futures_rps: 30
  spot_burst: 60
  futures_burst: 60
```

### 2. System Optimization

```bash
# CPU governor
echo performance | sudo tee /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor

# Transparent huge pages
echo never | sudo tee /sys/kernel/mm/transparent_hugepage/enabled

# Swap configuration
sudo sysctl vm.swappiness=10
sudo sysctl vm.vfs_cache_pressure=50
```

## Monitoring and Observability

### 1. Prometheus Configuration

```yaml
# prometheus.yml
global:
  scrape_interval: 15s
  evaluation_interval: 15s

rule_files:
  - "alert_rules.yml"

scrape_configs:
  - job_name: 'binance-proxy'
    static_configs:
      - targets: ['localhost:8092']
    scrape_interval: 5s
    metrics_path: /metrics

  - job_name: 'binance-proxy-health'
    static_configs:
      - targets: ['localhost:8092']
    scrape_interval: 10s
    metrics_path: /health

alerting:
  alertmanagers:
    - static_configs:
        - targets: ['localhost:9093']
```

### 2. Alert Rules

```yaml
# alert_rules.yml
groups:
  - name: binance-proxy-alerts
    rules:
      - alert: HighErrorRate
        expr: rate(binance_proxy_errors_total[5m]) > 0.1
        for: 2m
        labels:
          severity: critical
        annotations:
          summary: "High error rate detected"
          description: "Error rate is {{ $value }} errors/sec"

      - alert: HighMemoryUsage
        expr: process_resident_memory_bytes / 1024 / 1024 > 800
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High memory usage"
          description: "Memory usage is {{ $value }}MB"

      - alert: ServiceDown
        expr: up == 0
        for: 30s
        labels:
          severity: critical
        annotations:
          summary: "Service is down"
          description: "Binance Proxy service is not responding"
```

### 3. Log Aggregation

```yaml
# docker-compose.yml for ELK stack
version: '3.8'
services:
  elasticsearch:
    image: docker.elastic.co/elasticsearch/elasticsearch:8.8.0
    environment:
      - discovery.type=single-node
      - xpack.security.enabled=false
    ports:
      - "9200:9200"
    volumes:
      - es_data:/usr/share/elasticsearch/data

  logstash:
    image: docker.elastic.co/logstash/logstash:8.8.0
    volumes:
      - ./logstash.conf:/usr/share/logstash/pipeline/logstash.conf
    ports:
      - "5044:5044"
    depends_on:
      - elasticsearch

  kibana:
    image: docker.elastic.co/kibana/kibana:8.8.0
    ports:
      - "5601:5601"
    depends_on:
      - elasticsearch

volumes:
  es_data:
```

## Deployment Strategies

### 1. Systemd Service

```ini
# /etc/systemd/system/binance-proxy.service
[Unit]
Description=Binance Proxy Service
After=network.target
Wants=network.target

[Service]
Type=simple
User=binance-proxy
Group=binance-proxy
WorkingDirectory=/opt/binance-proxy
ExecStart=/opt/binance-proxy/bin/binance-proxy
ExecReload=/bin/kill -HUP $MAINPID
Restart=always
RestartSec=5
LimitNOFILE=65536
Environment=BPX_ENVIRONMENT=production

# Security settings
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/opt/binance-proxy/logs /opt/binance-proxy/data

[Install]
WantedBy=multi-user.target
```

### 2. Docker Deployment

```dockerfile
# Dockerfile.production
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o binance-proxy cmd/binance-proxy/main.go

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /root/
COPY --from=builder /app/binance-proxy .
COPY --from=builder /app/config ./config/
EXPOSE 8090 8091 8092
CMD ["./binance-proxy"]
```

```yaml
# docker-compose.production.yml
version: '3.8'
services:
  binance-proxy:
    build:
      context: .
      dockerfile: Dockerfile.production
    ports:
      - "8090:8090"
      - "8091:8091"
      - "8092:8092"
    environment:
      - BPX_ENVIRONMENT=production
    volumes:
      - ./logs:/app/logs
      - ./data:/app/data
      - ./api_keys.txt:/app/api_keys.txt:ro
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "--quiet", "--tries=1", "--spider", "http://localhost:8092/health"]
      interval: 30s
      timeout: 10s
      retries: 3
    deploy:
      resources:
        limits:
          memory: 1G
          cpus: '2'
        reservations:
          memory: 512M
          cpus: '1'
```

### 3. Kubernetes Deployment

```yaml
# k8s/production-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: binance-proxy
  namespace: production
spec:
  replicas: 3
  selector:
    matchLabels:
      app: binance-proxy
  template:
    metadata:
      labels:
        app: binance-proxy
    spec:
      containers:
      - name: binance-proxy
        image: binance-proxy:latest
        ports:
        - containerPort: 8090
        - containerPort: 8091
        - containerPort: 8092
        env:
        - name: BPX_ENVIRONMENT
          value: "production"
        resources:
          requests:
            memory: "512Mi"
            cpu: "500m"
          limits:
            memory: "1Gi"
            cpu: "1000m"
        livenessProbe:
          httpGet:
            path: /health
            port: 8092
          initialDelaySeconds: 30
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /ready
            port: 8092
          initialDelaySeconds: 5
          periodSeconds: 5
        volumeMounts:
        - name: config
          mountPath: /app/config
        - name: logs
          mountPath: /app/logs
      volumes:
      - name: config
        configMap:
          name: binance-proxy-config
      - name: logs
        persistentVolumeClaim:
          claimName: binance-proxy-logs
```

## Scaling and Load Balancing

### 1. Horizontal Scaling

```yaml
# k8s/hpa.yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: binance-proxy-hpa
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: binance-proxy
  minReplicas: 3
  maxReplicas: 10
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 70
  - type: Resource
    resource:
      name: memory
      target:
        type: Utilization
        averageUtilization: 80
```

### 2. Load Balancer Configuration

```nginx
# nginx.conf
upstream binance_proxy_spot {
    least_conn;
    server 10.0.1.10:8090 max_fails=3 fail_timeout=30s;
    server 10.0.1.11:8090 max_fails=3 fail_timeout=30s;
    server 10.0.1.12:8090 max_fails=3 fail_timeout=30s;
}

upstream binance_proxy_futures {
    least_conn;
    server 10.0.1.10:8091 max_fails=3 fail_timeout=30s;
    server 10.0.1.11:8091 max_fails=3 fail_timeout=30s;
    server 10.0.1.12:8091 max_fails=3 fail_timeout=30s;
}

server {
    listen 80;
    server_name your-domain.com;

    location /spot/ {
        proxy_pass http://binance_proxy_spot/;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_connect_timeout 30s;
        proxy_read_timeout 30s;
        proxy_send_timeout 30s;
    }

    location /futures/ {
        proxy_pass http://binance_proxy_futures/;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_connect_timeout 30s;
        proxy_read_timeout 30s;
        proxy_send_timeout 30s;
    }

    location /metrics {
        proxy_pass http://binance_proxy_spot:8092/metrics;
        allow 10.0.0.0/8;
        deny all;
    }
}
```

## Backup and Recovery

### 1. Backup Strategy

```bash
#!/bin/bash
# scripts/backup.sh

BACKUP_DIR="/opt/backups/binance-proxy"
DATE=$(date +%Y%m%d_%H%M%S)

# Create backup directory
mkdir -p "$BACKUP_DIR/$DATE"

# Backup configuration
cp -r /opt/binance-proxy/config "$BACKUP_DIR/$DATE/"

# Backup API keys
cp /opt/binance-proxy/api_keys.txt "$BACKUP_DIR/$DATE/"

# Backup logs (last 7 days)
find /opt/binance-proxy/logs -name "*.log*" -mtime -7 -exec cp {} "$BACKUP_DIR/$DATE/" \;

# Compress backup
tar -czf "$BACKUP_DIR/binance-proxy-$DATE.tar.gz" -C "$BACKUP_DIR" "$DATE"
rm -rf "$BACKUP_DIR/$DATE"

# Keep only last 30 backups
find "$BACKUP_DIR" -name "*.tar.gz" -mtime +30 -delete

echo "Backup completed: $BACKUP_DIR/binance-proxy-$DATE.tar.gz"
```

### 2. Recovery Procedures

```bash
#!/bin/bash
# scripts/restore.sh

BACKUP_FILE="$1"
RESTORE_DIR="/opt/binance-proxy"

if [ -z "$BACKUP_FILE" ]; then
    echo "Usage: $0 <backup-file>"
    exit 1
fi

# Stop service
sudo systemctl stop binance-proxy

# Extract backup
tar -xzf "$BACKUP_FILE" -C /tmp/

# Restore configuration
cp -r /tmp/*/config/* "$RESTORE_DIR/config/"

# Restore API keys
cp /tmp/*/api_keys.txt "$RESTORE_DIR/"

# Set permissions
chown -R binance-proxy:binance-proxy "$RESTORE_DIR"

# Start service
sudo systemctl start binance-proxy

echo "Restoration completed"
```

## Troubleshooting

### 1. Common Issues

**High Memory Usage:**
```bash
# Check memory usage
free -h
ps aux --sort=-%mem | head

# Check GC stats
curl -s http://localhost:8092/stats | jq '.runtime'

# Adjust GC settings
export BPX_PERF_GC_PERCENT=30
```

**WebSocket Connection Issues:**
```bash
# Check connection status
curl -s http://localhost:8092/stats | jq '.websocket'

# Monitor connection logs
tail -f /opt/binance-proxy/logs/app.log | grep -i websocket

# Test connectivity
telnet stream.binance.com 9443
```

**Rate Limiting:**
```bash
# Check rate limit status
curl -s http://localhost:8092/stats | jq '.security'

# Monitor rate limit hits
tail -f /opt/binance-proxy/logs/app.log | grep -i "rate limit"
```

### 2. Performance Analysis

```bash
# CPU profiling
curl -s http://localhost:8092/debug/pprof/profile?seconds=30 > cpu.prof
go tool pprof cpu.prof

# Memory profiling
curl -s http://localhost:8092/debug/pprof/heap > heap.prof
go tool pprof heap.prof

# Goroutine analysis
curl -s http://localhost:8092/debug/pprof/goroutine > goroutine.prof
go tool pprof goroutine.prof
```

### 3. Log Analysis

```bash
# Error analysis
grep -i error /opt/binance-proxy/logs/app.log | tail -20

# Performance analysis
grep "duration" /opt/binance-proxy/logs/app.log | awk '{print $NF}' | sort -n | tail -10

# Connection analysis
grep "websocket" /opt/binance-proxy/logs/app.log | grep -c "connected\|disconnected"
```

## Maintenance

### 1. Regular Maintenance Tasks

```bash
# Weekly maintenance script
#!/bin/bash
# scripts/weekly-maintenance.sh

# Rotate logs
logrotate -f /etc/logrotate.d/binance-proxy

# Clean up old data
find /opt/binance-proxy/data -name "*.tmp" -mtime +7 -delete

# Update system packages
sudo apt update && sudo apt upgrade -y

# Restart service for memory cleanup
sudo systemctl restart binance-proxy

# Generate health report
./scripts/health-report.sh > /opt/binance-proxy/reports/weekly-$(date +%Y%m%d).txt
```

### 2. Updates and Upgrades

```bash
# Zero-downtime update script
#!/bin/bash
# scripts/update.sh

NEW_VERSION="$1"
if [ -z "$NEW_VERSION" ]; then
    echo "Usage: $0 <new-version>"
    exit 1
fi

# Download new version
wget "https://github.com/your-org/binance-proxy/releases/download/$NEW_VERSION/binance-proxy-$NEW_VERSION.tar.gz"

# Backup current version
cp /opt/binance-proxy/bin/binance-proxy /opt/binance-proxy/bin/binance-proxy.backup

# Extract new version
tar -xzf "binance-proxy-$NEW_VERSION.tar.gz"

# Install new version
cp binance-proxy /opt/binance-proxy/bin/

# Restart service
sudo systemctl restart binance-proxy

# Verify health
sleep 10
if curl -s http://localhost:8092/health | grep -q "healthy"; then
    echo "Update successful"
    rm /opt/binance-proxy/bin/binance-proxy.backup
else
    echo "Update failed, rolling back"
    cp /opt/binance-proxy/bin/binance-proxy.backup /opt/binance-proxy/bin/binance-proxy
    sudo systemctl restart binance-proxy
fi
```

### 3. Monitoring and Alerting

```bash
# Health check script for cron
#!/bin/bash
# scripts/health-check.sh

HEALTH_URL="http://localhost:8092/health"
ALERT_EMAIL="admin@your-domain.com"

if ! curl -s --fail "$HEALTH_URL" > /dev/null; then
    echo "Binance Proxy health check failed at $(date)" | \
        mail -s "Binance Proxy Alert" "$ALERT_EMAIL"
    
    # Auto-restart attempt
    sudo systemctl restart binance-proxy
fi
```

Add to crontab:
```bash
# Check every 5 minutes
*/5 * * * * /opt/binance-proxy/scripts/health-check.sh
```

## Conclusion

This production deployment guide provides a comprehensive approach to running Binance Proxy in production environments. Key points:

1. **Security First**: Always enable authentication, use TLS, and follow security best practices
2. **Monitor Everything**: Set up comprehensive monitoring and alerting
3. **Plan for Scale**: Design your deployment to handle growth
4. **Automate Operations**: Use scripts and automation for maintenance tasks
5. **Test Thoroughly**: Always test changes in staging before production

For additional support, refer to the main README.md and the troubleshooting section.
