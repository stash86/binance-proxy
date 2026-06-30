package main

import (
	"binance-proxy/internal/handler"
	"binance-proxy/internal/logcache"
	"binance-proxy/internal/service"
	"context"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	_ "net/http/pprof"

	"github.com/jessevdk/go-flags"
	log "github.com/sirupsen/logrus"
)

const (
	proxyReadTimeout       = 30 * time.Second
	proxyReadHeaderTimeout = 10 * time.Second
	proxyWriteTimeout      = 75 * time.Second
	proxyIdleTimeout       = 120 * time.Second
	proxyShutdownTimeout   = 5 * time.Second
	maxTCPPort             = 65535
)

func startProxy(ctx context.Context, port int, class service.Class, disableFakeKline bool, alwaysShowForwards bool) {
	if err := serveProxy(ctx, port, class, disableFakeKline, alwaysShowForwards, os.Stderr); err != nil {
		log.Fatalf("%s websocket proxy stopped with error: %s.", class, err)
	}
}

func serveProxy(ctx context.Context, port int, class service.Class, disableFakeKline bool, alwaysShowForwards bool, errorWriter io.Writer) error {
	srv := newProxyServer(proxyAddress(port), newProxyMux(ctx, class, disableFakeKline, alwaysShowForwards), errorWriter)

	log.Infof("%s websocket proxy starting on port %d.", class, port)
	errC := make(chan error, 1)
	go func() {
		errC <- srv.ListenAndServe()
	}()

	select {
	case err := <-errC:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), proxyShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}

	if err := <-errC; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func newProxyMux(ctx context.Context, class service.Class, disableFakeKline bool, alwaysShowForwards bool) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", handler.NewHandler(ctx, class, !disableFakeKline, alwaysShowForwards))
	return mux
}

func proxyAddress(port int) string {
	return fmt.Sprintf(":%d", port)
}

func newProxyServer(address string, handler http.Handler, errorWriter io.Writer) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadTimeout:       proxyReadTimeout,
		ReadHeaderTimeout: proxyReadHeaderTimeout,
		WriteTimeout:      proxyWriteTimeout,
		IdleTimeout:       proxyIdleTimeout,
		ErrorLog: stdlog.New(
			logcache.NewSuppressingWriter(errorWriter),
			"", stdlog.LstdFlags,
		),
	}
}

func handleSignal(cancel context.CancelFunc) {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer signal.Stop(signalChan)

	waitForShutdownSignal(signalChan, cancel)
}

func waitForShutdownSignal(signalChan <-chan os.Signal, cancel context.CancelFunc) {
	for sig := range signalChan {
		if isShutdownSignal(sig) {
			if cancel != nil {
				cancel()
			}
			return
		}
	}
}

func isShutdownSignal(sig os.Signal) bool {
	switch sig {
	case syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT:
		return true
	default:
		return false
	}
}

type Config struct {
	Verbose            []bool `short:"v" long:"verbose" env:"BPX_VERBOSE" description:"Verbose output (increase with -vv)"`
	SpotAddress        int    `short:"p" long:"port-spot" env:"BPX_PORT_SPOT" description:"Port to which to bind for SPOT markets" default:"8090"`
	FuturesAddress     int    `short:"t" long:"port-futures" env:"BPX_PORT_FUTURES" description:"Port to which to bind for FUTURES markets" default:"8091"`
	DisableFakeKline   bool   `short:"c" long:"disable-fake-candles" env:"BPX_DISABLE_FAKE_CANDLES" description:"Disable generation of fake candles (ohlcv) when sockets have not delivered data yet"`
	DisableSpot        bool   `short:"s" long:"disable-spot" env:"BPX_DISABLE_SPOT" description:"Disable proxying spot markets"`
	DisableFutures     bool   `short:"f" long:"disable-futures" env:"BPX_DISABLE_FUTURES" description:"Disable proxying futures markets"`
	AlwaysShowForwards bool   `short:"a" long:"always-show-forwards" env:"BPX_ALWAYS_SHOW_FORWARDS" description:"Always show requests forwarded via REST even if verbose is disabled"`
}

var (
	config      Config
	parser             = flags.NewParser(&config, flags.Default)
	Version     string = "1.0.5"
	Buildtime   string = "2026-06-30"
	ctx, cancel        = context.WithCancel(context.Background())
)

func main() {
	log.SetFormatter(&log.TextFormatter{
		DisableColors: true,
		FullTimestamp: true,
	})

	installLogcacheHooks()

	log.Infof("Binance proxy version %s, build time %s", Version, Buildtime)

	if _, err := parser.Parse(); err != nil {
		if isHelpError(err) {
			os.Exit(0)
		}
		log.Fatal(err)
	}

	configureLogging(config.Verbose)

	if log.GetLevel() > log.InfoLevel {
		log.Infof("Set level to %s", log.GetLevel())
	}

	if err := validateConfig(config); err != nil {
		log.Fatal(err)
	}

	if !config.DisableFakeKline {
		log.Infof("Fake candles are enabled for faster processing, the feature can be disabled with --disable-fake-candles or -c")
	}

	if config.AlwaysShowForwards {
		log.Infof("Always show forwards is enabled, all API requests, that can't be served from websockets cached will be logged.")
	}

	go handleSignal(cancel)

	var wg sync.WaitGroup
	if !config.DisableSpot {
		startProxyAsync(&wg, ctx, config.SpotAddress, service.SPOT, config.DisableFakeKline, config.AlwaysShowForwards)
	}
	if !config.DisableFutures {
		startProxyAsync(&wg, ctx, config.FuturesAddress, service.FUTURES, config.DisableFakeKline, config.AlwaysShowForwards)
	}
	<-ctx.Done()
	log.Info("shutdown signal received, aborting ...")
	wg.Wait()
}

func startProxyAsync(wg *sync.WaitGroup, ctx context.Context, port int, class service.Class, disableFakeKline bool, alwaysShowForwards bool) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		startProxy(ctx, port, class, disableFakeKline, alwaysShowForwards)
	}()
}

func installLogcacheHooks() {
	// Route logcache output through logrus for consistent formatting/levels.
	logcache.SetLoggerHook(func(level, msg string) {
		switch level {
		case "warn":
			log.Warn(msg)
		case "error":
			log.Error(msg)
		case "info":
			log.Info(msg)
		default:
			log.Print(msg)
		}
	})
	logcache.SetWriterHook(func(msg string) {
		log.Warnf("http: %s", trimTrailingNewline(msg))
	})
}

func trimTrailingNewline(msg string) string {
	if len(msg) > 0 && msg[len(msg)-1] == '\n' {
		return msg[:len(msg)-1]
	}
	return msg
}

func isHelpError(err error) bool {
	var flagsErr *flags.Error
	return errors.As(err, &flagsErr) && flagsErr.Type == flags.ErrHelp
}

func configureLogging(verbose []bool) {
	log.SetLevel(logLevelForVerbosity(verbose))
}

func logLevelForVerbosity(verbose []bool) log.Level {
	switch {
	case len(verbose) >= 2:
		return log.TraceLevel
	case len(verbose) == 1:
		return log.DebugLevel
	default:
		return log.InfoLevel
	}
}

func validateConfig(cfg Config) error {
	if cfg.DisableSpot && cfg.DisableFutures {
		return errors.New("can't start if both SPOT and FUTURES are disabled")
	}
	if !cfg.DisableSpot && !validTCPPort(cfg.SpotAddress) {
		return fmt.Errorf("invalid SPOT port %d", cfg.SpotAddress)
	}
	if !cfg.DisableFutures && !validTCPPort(cfg.FuturesAddress) {
		return fmt.Errorf("invalid FUTURES port %d", cfg.FuturesAddress)
	}
	if !cfg.DisableSpot && !cfg.DisableFutures && cfg.SpotAddress == cfg.FuturesAddress {
		return fmt.Errorf("SPOT and FUTURES ports must be different: %d", cfg.SpotAddress)
	}

	return nil
}

func validTCPPort(port int) bool {
	return port > 0 && port <= maxTCPPort
}
