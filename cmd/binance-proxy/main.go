package main

import (
	"binance-proxy/internal/handler"
	"binance-proxy/internal/logcache"
	"binance-proxy/internal/service"
	"context"
	"fmt"
	stdlog "log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "net/http/pprof"

	"github.com/jessevdk/go-flags"
	log "github.com/sirupsen/logrus"
)

func startProxy(ctx context.Context, port int, class service.Class, disablefakekline bool, alwaysshowforwards bool) {
	mux := http.NewServeMux()
	address := fmt.Sprintf(":%d", port)
	mux.HandleFunc("/", handler.NewHandler(ctx, class, !disablefakekline, alwaysshowforwards))

	// Create an HTTP server with a custom ErrorLog that suppresses repeated lines
	srv := &http.Server{
		Addr:              address,
		Handler:           mux,
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      75 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog: stdlog.New(
			logcache.NewSuppressingWriter(os.Stderr),
			"", stdlog.LstdFlags,
		),
	}

	log.Infof("%s websocket proxy starting on port %d.", class, port)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("%s websocket proxy start failed (error: %s).", class, err)
	}
}

func handleSignal() {
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	for s := range signalChan {
		switch s {
		case syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT:
			cancel()
		}
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
	Version     string = "1.0.4"
	Buildtime   string = "2025-08-11"
	ctx, cancel        = context.WithCancel(context.Background())
)

func main() {
	log.SetFormatter(&log.TextFormatter{
		DisableColors: true,
		FullTimestamp: true,
	})

	// Route logcache output through logrus for consistent formatting/levels
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
		// net/http ErrorLog messages typically include trailing newlines
		if len(msg) > 0 && msg[len(msg)-1] == '\n' {
			msg = msg[:len(msg)-1]
		}
		log.Warnf("http: %s", msg)
	})

	log.Infof("Binance proxy version %s, build time %s", Version, Buildtime)

	if _, err := parser.Parse(); err != nil {
		if flagsErr, ok := err.(*flags.Error); ok && flagsErr.Type == flags.ErrHelp {
			os.Exit(0)
		} else {
			log.Fatalf("%s - %s", err, flagsErr.Type)
		}
	}

	if len(config.Verbose) >= 2 {
		log.SetLevel(log.TraceLevel)
	} else if len(config.Verbose) == 1 {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	if log.GetLevel() > log.InfoLevel {
		log.Infof("Set level to %s", log.GetLevel())
	}

	if config.DisableSpot && config.DisableFutures {
		log.Fatal("can't start if both SPOT and FUTURES are disabled!")
	}

	if !config.DisableFakeKline {
		log.Infof("Fake candles are enabled for faster processing, the feature can be disabled with --disable-fake-candles or -c")
	}

	if config.AlwaysShowForwards {
		log.Infof("Always show forwards is enabled, all API requests, that can't be served from websockets cached will be logged.")
	}

	go handleSignal()

	if !config.DisableSpot {
		go startProxy(ctx, config.SpotAddress, service.SPOT, config.DisableFakeKline, config.AlwaysShowForwards)
	}
	if !config.DisableFutures {
		go startProxy(ctx, config.FuturesAddress, service.FUTURES, config.DisableFakeKline, config.AlwaysShowForwards)
	}
	<-ctx.Done()

	log.Info("SIGINT received, aborting ...")
}
