package main

import (
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/jessevdk/go-flags"
	log "github.com/sirupsen/logrus"
)

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "default ports",
			cfg: Config{
				SpotAddress:    8090,
				FuturesAddress: 8091,
			},
		},
		{
			name: "spot disabled ignores spot port",
			cfg: Config{
				SpotAddress:    0,
				FuturesAddress: 8091,
				DisableSpot:    true,
			},
		},
		{
			name: "futures disabled ignores futures port",
			cfg: Config{
				SpotAddress:    8090,
				FuturesAddress: 0,
				DisableFutures: true,
			},
		},
		{
			name: "both disabled",
			cfg: Config{
				SpotAddress:      8090,
				FuturesAddress:   8091,
				DisableSpot:      true,
				DisableFutures:   true,
				DisableFakeKline: true,
			},
			wantErr: true,
		},
		{
			name: "invalid spot port",
			cfg: Config{
				SpotAddress:    0,
				FuturesAddress: 8091,
			},
			wantErr: true,
		},
		{
			name: "invalid futures port",
			cfg: Config{
				SpotAddress:    8090,
				FuturesAddress: 65536,
			},
			wantErr: true,
		},
		{
			name: "same enabled ports",
			cfg: Config{
				SpotAddress:    8090,
				FuturesAddress: 8090,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateConfig() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}

func TestValidTCPPort(t *testing.T) {
	tests := []struct {
		port int
		want bool
	}{
		{port: -1},
		{port: 0},
		{port: 1, want: true},
		{port: 8090, want: true},
		{port: maxTCPPort, want: true},
		{port: maxTCPPort + 1},
	}

	for _, tt := range tests {
		if got := validTCPPort(tt.port); got != tt.want {
			t.Fatalf("validTCPPort(%d) = %t, want %t", tt.port, got, tt.want)
		}
	}
}

func TestLogLevelForVerbosity(t *testing.T) {
	tests := []struct {
		name    string
		verbose []bool
		want    log.Level
	}{
		{name: "default", want: log.InfoLevel},
		{name: "debug", verbose: []bool{true}, want: log.DebugLevel},
		{name: "trace", verbose: []bool{true, true}, want: log.TraceLevel},
		{name: "trace with more flags", verbose: []bool{true, true, true}, want: log.TraceLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := logLevelForVerbosity(tt.verbose); got != tt.want {
				t.Fatalf("logLevelForVerbosity() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestTrimTrailingNewline(t *testing.T) {
	tests := []struct {
		msg  string
		want string
	}{
		{msg: "", want: ""},
		{msg: "http: error", want: "http: error"},
		{msg: "http: error\n", want: "http: error"},
		{msg: "http: error\n\n", want: "http: error\n"},
	}

	for _, tt := range tests {
		if got := trimTrailingNewline(tt.msg); got != tt.want {
			t.Fatalf("trimTrailingNewline(%q) = %q, want %q", tt.msg, got, tt.want)
		}
	}
}

func TestIsHelpError(t *testing.T) {
	if !isHelpError(&flags.Error{Type: flags.ErrHelp}) {
		t.Fatal("isHelpError(help) = false, want true")
	}
	if isHelpError(errors.New("parse failed")) {
		t.Fatal("isHelpError(generic) = true, want false")
	}
	if isHelpError(nil) {
		t.Fatal("isHelpError(nil) = true, want false")
	}
}

func TestShutdownSignalHandling(t *testing.T) {
	if !isShutdownSignal(syscall.SIGINT) {
		t.Fatal("SIGINT was not treated as shutdown signal")
	}
	if !isShutdownSignal(syscall.SIGTERM) {
		t.Fatal("SIGTERM was not treated as shutdown signal")
	}
	if isShutdownSignal(syscall.SIGUSR1) {
		t.Fatal("SIGUSR1 was treated as shutdown signal")
	}
}

func TestWaitForShutdownSignal(t *testing.T) {
	signals := make(chan os.Signal, 2)
	done := make(chan struct{})

	signals <- syscall.SIGUSR1
	signals <- syscall.SIGTERM

	waitForShutdownSignal(signals, func() {
		close(done)
	})

	select {
	case <-done:
	default:
		t.Fatal("cancel was not called")
	}
}

func TestNewProxyServer(t *testing.T) {
	mux := http.NewServeMux()
	server := newProxyServer(":8090", mux, io.Discard)

	if server.Addr != ":8090" {
		t.Fatalf("Addr = %q, want :8090", server.Addr)
	}
	if server.Handler != mux {
		t.Fatal("Handler was not preserved")
	}
	if server.ReadTimeout != proxyReadTimeout {
		t.Fatalf("ReadTimeout = %s, want %s", server.ReadTimeout, proxyReadTimeout)
	}
	if server.ReadHeaderTimeout != proxyReadHeaderTimeout {
		t.Fatalf("ReadHeaderTimeout = %s, want %s", server.ReadHeaderTimeout, proxyReadHeaderTimeout)
	}
	if server.WriteTimeout != proxyWriteTimeout {
		t.Fatalf("WriteTimeout = %s, want %s", server.WriteTimeout, proxyWriteTimeout)
	}
	if server.IdleTimeout != proxyIdleTimeout {
		t.Fatalf("IdleTimeout = %s, want %s", server.IdleTimeout, proxyIdleTimeout)
	}
	if server.ErrorLog == nil {
		t.Fatal("ErrorLog is nil")
	}
}

func TestProxyAddress(t *testing.T) {
	if got := proxyAddress(8090); got != ":8090" {
		t.Fatalf("proxyAddress() = %q, want :8090", got)
	}
}

func TestConfigValidationErrorsAreSpecific(t *testing.T) {
	tests := []struct {
		cfg      Config
		contains string
	}{
		{
			cfg:      Config{DisableSpot: true, DisableFutures: true},
			contains: "both SPOT and FUTURES",
		},
		{
			cfg:      Config{SpotAddress: 0, FuturesAddress: 8091},
			contains: "invalid SPOT port",
		},
		{
			cfg:      Config{SpotAddress: 8090, FuturesAddress: 0},
			contains: "invalid FUTURES port",
		},
		{
			cfg:      Config{SpotAddress: 8090, FuturesAddress: 8090},
			contains: "must be different",
		},
	}

	for _, tt := range tests {
		err := validateConfig(tt.cfg)
		if err == nil {
			t.Fatalf("validateConfig(%#v) error = nil, want message containing %q", tt.cfg, tt.contains)
		}
		if !strings.Contains(err.Error(), tt.contains) {
			t.Fatalf("validateConfig(%#v) error = %q, want containing %q", tt.cfg, err.Error(), tt.contains)
		}
	}
}

func TestConfigureLogging(t *testing.T) {
	oldLevel := log.GetLevel()
	t.Cleanup(func() {
		log.SetLevel(oldLevel)
	})

	configureLogging([]bool{true})
	if got := log.GetLevel(); got != log.DebugLevel {
		t.Fatalf("log level = %s, want debug", got)
	}
}

func TestProxyTimeouts(t *testing.T) {
	got := []string{
		proxyReadTimeout.String(),
		proxyReadHeaderTimeout.String(),
		proxyWriteTimeout.String(),
		proxyIdleTimeout.String(),
		proxyShutdownTimeout.String(),
	}
	want := []string{"30s", "10s", "1m15s", "2m0s", "5s"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("timeouts = %#v, want %#v", got, want)
	}
}
