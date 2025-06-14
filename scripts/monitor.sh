#!/bin/bash

# Binance Proxy Auto-Restart Monitor Script
# This script monitors the proxy health and restarts it when needed

# Configuration
HEALTH_URL="http://localhost:8092/health"
METRICS_URL="http://localhost:8092/metrics"
CHECK_INTERVAL=30
ERROR_THRESHOLD=5
CONSECUTIVE_FAILURES=0
MAX_CONSECUTIVE_FAILURES=3
LOG_FILE="/var/log/binance-proxy-monitor.log"
RESTART_COMMAND="docker-compose restart binance-proxy"
RESTART_COOLDOWN=120  # 2 minutes
LAST_RESTART=0

# Logging function
log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOG_FILE"
}

# Check if proxy is healthy
check_health() {
    local http_code
    http_code=$(curl -s -o /dev/null -w "%{http_code}" "$HEALTH_URL" --connect-timeout 10 --max-time 15)
    
    if [ "$http_code" -eq 200 ]; then
        return 0  # Healthy
    else
        return 1  # Unhealthy
    fi
}

# Check error rate from metrics
check_error_rate() {
    local metrics
    local total_requests
    local failed_requests
    local error_rate
    
    metrics=$(curl -s "$METRICS_URL" --connect-timeout 10 --max-time 15)
    
    if [ $? -ne 0 ]; then
        log "WARNING: Could not fetch metrics"
        return 1
    fi
    
    total_requests=$(echo "$metrics" | grep "^total_requests" | awk '{print $2}')
    failed_requests=$(echo "$metrics" | grep "^failed_requests" | awk '{print $2}')
    
    if [ -z "$total_requests" ] || [ -z "$failed_requests" ] || [ "$total_requests" -eq 0 ]; then
        return 0  # No data or no requests yet
    fi
    
    error_rate=$(( (failed_requests * 100) / total_requests ))
    
    log "INFO: Error rate: ${error_rate}% (${failed_requests}/${total_requests})"
    
    if [ "$error_rate" -gt "$ERROR_THRESHOLD" ]; then
        return 1  # High error rate
    fi
    
    return 0  # Acceptable error rate
}

# Restart the proxy
restart_proxy() {
    local current_time=$(date +%s)
    local time_since_restart=$((current_time - LAST_RESTART))
    
    if [ "$time_since_restart" -lt "$RESTART_COOLDOWN" ]; then
        log "WARNING: Restart needed but in cooldown period (${time_since_restart}s < ${RESTART_COOLDOWN}s)"
        return 1
    fi
    
    log "WARNING: Restarting Binance Proxy due to health issues"
    
    if eval "$RESTART_COMMAND"; then
        log "INFO: Restart command executed successfully"
        LAST_RESTART=$current_time
        CONSECUTIVE_FAILURES=0
        
        # Wait for service to come back up
        sleep 30
        
        if check_health; then
            log "INFO: Proxy is healthy after restart"
            return 0
        else
            log "ERROR: Proxy still unhealthy after restart"
            return 1
        fi
    else
        log "ERROR: Failed to execute restart command"
        return 1
    fi
}

# Send notification (customize as needed)
send_notification() {
    local message="$1"
    log "$message"
    
    # Example: Send to Slack webhook
    # curl -X POST -H 'Content-type: application/json' \
    #     --data "{\"text\":\"$message\"}" \
    #     "$SLACK_WEBHOOK_URL"
    
    # Example: Send email
    # echo "$message" | mail -s "Binance Proxy Alert" admin@example.com
}

# Main monitoring loop
monitor() {
    log "INFO: Starting Binance Proxy monitoring (PID: $$)"
    
    while true; do
        if check_health && check_error_rate; then
            # Healthy
            if [ "$CONSECUTIVE_FAILURES" -gt 0 ]; then
                log "INFO: Proxy is healthy again after $CONSECUTIVE_FAILURES failures"
                send_notification "✅ Binance Proxy recovered - health check passed"
            fi
            CONSECUTIVE_FAILURES=0
        else
            # Unhealthy
            CONSECUTIVE_FAILURES=$((CONSECUTIVE_FAILURES + 1))
            log "WARNING: Health check failed (attempt $CONSECUTIVE_FAILURES/$MAX_CONSECUTIVE_FAILURES)"
            
            if [ "$CONSECUTIVE_FAILURES" -ge "$MAX_CONSECUTIVE_FAILURES" ]; then
                send_notification "🚨 Binance Proxy unhealthy - attempting restart"
                
                if restart_proxy; then
                    send_notification "✅ Binance Proxy restarted successfully"
                else
                    send_notification "❌ Binance Proxy restart failed - manual intervention required"
                fi
            fi
        fi
        
        sleep "$CHECK_INTERVAL"
    done
}

# Signal handlers
cleanup() {
    log "INFO: Monitoring stopped"
    exit 0
}

trap cleanup SIGTERM SIGINT

# Start monitoring
monitor
