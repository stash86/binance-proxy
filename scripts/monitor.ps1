# Binance Proxy Auto-Restart Monitor for Windows
# PowerShell script to monitor and restart the proxy

param(
    [string]$HealthUrl = "http://localhost:8092/health",
    [string]$MetricsUrl = "http://localhost:8092/metrics",
    [int]$CheckInterval = 30,
    [int]$ErrorThreshold = 5,
    [int]$MaxConsecutiveFailures = 3,
    [string]$LogFile = "$env:TEMP\binance-proxy-monitor.log",
    [string]$RestartCommand = "docker-compose restart binance-proxy",
    [int]$RestartCooldown = 120
)

$consecutiveFailures = 0
$lastRestart = [DateTime]::MinValue

# Logging function
function Write-Log {
    param([string]$Message)
    $timestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
    $logEntry = "[$timestamp] $Message"
    Write-Host $logEntry
    Add-Content -Path $LogFile -Value $logEntry
}

# Check if proxy is healthy
function Test-ProxyHealth {
    try {
        $response = Invoke-WebRequest -Uri $HealthUrl -TimeoutSec 15 -UseBasicParsing
        return $response.StatusCode -eq 200
    }
    catch {
        Write-Log "WARNING: Health check failed - $($_.Exception.Message)"
        return $false
    }
}

# Check error rate from metrics
function Test-ErrorRate {
    try {
        $response = Invoke-WebRequest -Uri $MetricsUrl -TimeoutSec 15 -UseBasicParsing
        $metrics = $response.Content
        
        $totalRequests = [regex]::Match($metrics, "total_requests (\d+)").Groups[1].Value
        $failedRequests = [regex]::Match($metrics, "failed_requests (\d+)").Groups[1].Value
        
        if ([string]::IsNullOrEmpty($totalRequests) -or [string]::IsNullOrEmpty($failedRequests) -or [int]$totalRequests -eq 0) {
            return $true  # No data or no requests yet
        }
        
        $errorRate = ([int]$failedRequests * 100) / [int]$totalRequests
        Write-Log "INFO: Error rate: $([math]::Round($errorRate, 2))% ($failedRequests/$totalRequests)"
        
        return $errorRate -le $ErrorThreshold
    }
    catch {
        Write-Log "WARNING: Could not fetch metrics - $($_.Exception.Message)"
        return $true  # Assume OK if we can't check
    }
}

# Restart the proxy
function Restart-Proxy {
    $currentTime = Get-Date
    $timeSinceRestart = ($currentTime - $lastRestart).TotalSeconds
    
    if ($timeSinceRestart -lt $RestartCooldown) {
        Write-Log "WARNING: Restart needed but in cooldown period ($([math]::Round($timeSinceRestart, 0))s < $RestartCooldown s)"
        return $false
    }
    
    Write-Log "WARNING: Restarting Binance Proxy due to health issues"
    
    try {
        $process = Start-Process -FilePath "powershell" -ArgumentList "-Command", $RestartCommand -Wait -PassThru -NoNewWindow
        
        if ($process.ExitCode -eq 0) {
            Write-Log "INFO: Restart command executed successfully"
            $script:lastRestart = $currentTime
            $script:consecutiveFailures = 0
            
            # Wait for service to come back up
            Start-Sleep -Seconds 30
            
            if (Test-ProxyHealth) {
                Write-Log "INFO: Proxy is healthy after restart"
                return $true
            }
            else {
                Write-Log "ERROR: Proxy still unhealthy after restart"
                return $false
            }
        }
        else {
            Write-Log "ERROR: Restart command failed with exit code $($process.ExitCode)"
            return $false
        }
    }
    catch {
        Write-Log "ERROR: Failed to execute restart command - $($_.Exception.Message)"
        return $false
    }
}

# Send notification
function Send-Notification {
    param([string]$Message)
    Write-Log $Message
    
    # Example: Send Windows notification
    try {
        Add-Type -AssemblyName System.Windows.Forms
        $notification = New-Object System.Windows.Forms.NotifyIcon
        $notification.Icon = [System.Drawing.SystemIcons]::Information
        $notification.BalloonTipTitle = "Binance Proxy Monitor"
        $notification.BalloonTipText = $Message
        $notification.Visible = $true
        $notification.ShowBalloonTip(5000)
        Start-Sleep -Seconds 1
        $notification.Dispose()
    }
    catch {
        # Ignore notification errors
    }
}

# Main monitoring function
function Start-Monitoring {
    Write-Log "INFO: Starting Binance Proxy monitoring (PID: $PID)"
    Send-Notification "🔍 Binance Proxy monitoring started"
    
    while ($true) {
        $isHealthy = Test-ProxyHealth
        $errorRateOk = Test-ErrorRate
        
        if ($isHealthy -and $errorRateOk) {
            # Healthy
            if ($consecutiveFailures -gt 0) {
                Write-Log "INFO: Proxy is healthy again after $consecutiveFailures failures"
                Send-Notification "✅ Binance Proxy recovered - health check passed"
            }
            $consecutiveFailures = 0
        }
        else {
            # Unhealthy
            $consecutiveFailures++
            Write-Log "WARNING: Health check failed (attempt $consecutiveFailures/$MaxConsecutiveFailures)"
            
            if ($consecutiveFailures -ge $MaxConsecutiveFailures) {
                Send-Notification "🚨 Binance Proxy unhealthy - attempting restart"
                
                if (Restart-Proxy) {
                    Send-Notification "✅ Binance Proxy restarted successfully"
                }
                else {
                    Send-Notification "❌ Binance Proxy restart failed - manual intervention required"
                }
            }
        }
        
        Start-Sleep -Seconds $CheckInterval
    }
}

# Handle Ctrl+C
$null = Register-EngineEvent -SourceIdentifier PowerShell.Exiting -Action {
    Write-Log "INFO: Monitoring stopped"
}

# Start monitoring
try {
    Start-Monitoring
}
catch {
    Write-Log "ERROR: Monitoring failed - $($_.Exception.Message)"
    exit 1
}
