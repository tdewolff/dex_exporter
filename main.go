package main

import (
	"io/ioutil"
	"log"
	"net/http"
	"os"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/tdewolff/argp"
)

var listenAddress = ":9900"
var telemetryPath = "/metrics"

var (
	Error   *log.Logger
	Warning *log.Logger
	Info    *log.Logger
)

func main() {
	var verbose int
	var quiet bool

	cmd := argp.New("Exporter for Prometheus by Taco de Wolff")
	cmd.AddOpt(argp.Count{&verbose}, "v", "verbose", nil, "Log verbosity, can specify multiple times to increase verbosity.")
	cmd.AddOpt(&quiet, "q", "quiet", false, "Quiet mode to suppress all output")
	cmd.AddOpt(&listenAddress, "", "listen-address", ":9900", "Path under which to expose metrics.")
	cmd.AddOpt(&telemetryPath, "", "telemetry-path", "/metrics", "Path under which to expose metrics.")
	cmd.Parse()

	Error = log.New(ioutil.Discard, "", 0)
	Warning = log.New(ioutil.Discard, "", 0)
	Info = log.New(ioutil.Discard, "", 0)
	if !quiet {
		Error = log.New(os.Stderr, "ERROR: ", log.Ldate|log.Ltime|log.Lmsgprefix)
		if 0 < verbose {
			Warning = log.New(os.Stderr, "WARNING: ", log.Ldate|log.Ltime|log.Lmsgprefix)
		}
		if 1 < verbose {
			Info = log.New(os.Stderr, "INFO: ", log.Ldate|log.Ltime|log.Lmsgprefix)
		}
	}

	// register all exporters
	registry := prometheus.NewRegistry()

	node, err := NewNode()
	if err != nil {
		Error.Println(err)
		os.Exit(1)
	}
	node.AddServices("ntpd", "bluetooth")
	registry.MustRegister(node)

	nginx, err := NewNginx("unix:///var/run/mysqld/d.sock")
	if err != nil {
		Error.Println(err)
		os.Exit(1)
	}
	registry.MustRegister(nginx)

	http.Handle(telemetryPath, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	if err := ListenAndServe(listenAddress); err != nil && err != http.ErrServerClosed {
		Error.Println(err)
	}
}