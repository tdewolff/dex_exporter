package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-systemd/v22/dbus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/tdewolff/argp"
	"gopkg.in/yaml.v2"
)

var Version = "built from source"

type WebOptions struct {
	ListenAddress string `desc:"Address to listen to (e.g. :9900 or 123.45.67.89:9900), can be Unix socket (e.g. unix:///var/run/dex_exporter/dex_exporter.sock)."`
	TelemetryPath string `desc:"Path under which to expose metrics."`
	TLSCert       string `desc:"Path to TLS certificate."`
	TLSKey        string `desc:"Path to TLS key."`
	BasicAuth     string `desc:"Basic authentication as username:password."`
	Config        struct {
		File string `desc:"Path to configuration file that can enable TLS or authentication. See: https://github.com/prometheus/exporter-toolkit/blob/master/docs/web-configuration.md"`
	}
}

type LogOptions struct {
	Level string `desc:"Only log messages with the given severity or above. One of: [debug, info, warn, error]"`
}

type WebConfig struct {
	TLSServerConfig struct {
		CertFile string `yaml:"cert_file"`
		KeyFile  string `yaml:"key_file"`
	} `yaml:"tls_server_config"`
	BasicAuthUsers map[string]string `yaml:"basic_auth_users"`
}

var (
	Error   *log.Logger
	Warning *log.Logger
	Info    *log.Logger
	Debug   *log.Logger
)

func main() {
	version := false
	webOptions := WebOptions{
		ListenAddress: ":9900",
		TelemetryPath: "/metrics",
	}
	logOptions := LogOptions{
		Level: "info",
	}
	nginxOptions := NginxOptions{}
	redisOptions := RedisOptions{}
	memcacheOptions := MemcacheOptions{}

	cmd := argp.New("Exporter for Prometheus by Taco de Wolff")
	cmd.AddOpt(&version, "", "version", "Show version")
	cmd.AddOpt(&webOptions, "", "web", "")
	cmd.AddOpt(&logOptions, "", "log", "")
	cmd.AddOpt(&nginxOptions, "", "nginx", "")
	cmd.AddOpt(&redisOptions, "", "redis", "")
	cmd.AddOpt(&memcacheOptions, "", "memcache", "")
	cmd.Parse()

	if version {
		fmt.Println("dex_exporter", Version)
		return
	}

	verbose := 0
	switch logOptions.Level {
	case "error":
		verbose = 1
	case "warn":
		verbose = 2
	case "info":
		verbose = 3
	case "debug":
		verbose = 4
	}
	if 1 <= verbose {
		Error = log.New(os.Stderr, "ERROR: ", 0)
	} else {
		Error = log.New(ioutil.Discard, "", 0)
	}
	if 2 <= verbose {
		Warning = log.New(os.Stderr, "WARNING: ", 0)
	} else {
		Warning = log.New(ioutil.Discard, "", 0)
	}
	if 3 <= verbose {
		Info = log.New(os.Stderr, "INFO: ", 0)
	} else {
		Info = log.New(ioutil.Discard, "", 0)
	}
	if 4 <= verbose {
		Debug = log.New(os.Stderr, "DEBUG: ", 0)
	} else {
		Debug = log.New(ioutil.Discard, "", 0)
	}

	// register all exporters
	ctx, cancel := context.WithCancel(context.Background())
	exporter, err := NewExporter(ctx)
	if err != nil {
		Error.Println(err)
		os.Exit(1)
	}
	defer exporter.Close()

	// node exporter
	node, err := NewNode()
	if err != nil {
		Error.Println(err)
		os.Exit(1)
	}
	defer node.Close()
	exporter.AddCollector(node)

	// nginx exporter
	if nginxOptions.URI != "" {
		nginx, err := NewNginx(nginxOptions)
		if err != nil {
			Error.Println(err)
			os.Exit(1)
		}
		defer nginx.Close()
		exporter.AddCollector(nginx, "nginx")
	}

	// redis exporter
	if redisOptions.URI != "" {
		redis, err := NewRedis(redisOptions)
		if err != nil {
			Error.Println(err)
			os.Exit(1)
		}
		defer redis.Close()
		exporter.AddCollector(redis, "redis")
	}

	// memcache exporter
	if memcacheOptions.URI != "" {
		memcache, err := NewMemcache(memcacheOptions)
		if err != nil {
			Error.Println(err)
			os.Exit(1)
		}
		defer memcache.Close()
		exporter.AddCollector(memcache, "memcache")
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(exporter)

	config := WebConfig{}
	tlsCert, tlsKey := "", ""
	basicAuthUsers := map[string]string{}
	if webOptions.Config.File != "" {
		b, err := os.ReadFile(webOptions.Config.File)
		if err != nil {
			Error.Println(err)
			os.Exit(1)
		} else if err := yaml.Unmarshal(b, &config); err != nil {
			Error.Println(err)
			os.Exit(1)
		}
		tlsCert = config.TLSServerConfig.CertFile
		tlsKey = config.TLSServerConfig.KeyFile
		basicAuthUsers = config.BasicAuthUsers
	} else {
		tlsCert = webOptions.TLSCert
		tlsKey = webOptions.TLSKey
		if webOptions.BasicAuth != "" {
			colon := strings.IndexByte(webOptions.BasicAuth, ':')
			if colon == -1 || colon == 0 || colon == len(webOptions.BasicAuth)-1 {
				Error.Println("invalid format for web.basic-auth")
				os.Exit(1)
			}
			username := webOptions.BasicAuth[:colon]
			password := webOptions.BasicAuth[colon+1:]
			basicAuthUsers[username] = password
		}
	}

	telemetryHandler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	if 0 < len(basicAuthUsers) {
		if tlsCert == "" || tlsKey == "" {
			Warning.Println("using basic authorization without TLS")
		}
		telemetryHandler = BasicAuth(telemetryHandler, basicAuthUsers)
	}
	http.Handle(webOptions.TelemetryPath, telemetryHandler)

	if err := ListenAndServe(webOptions.ListenAddress, tlsCert, tlsKey); err != nil && err != http.ErrServerClosed {
		Error.Println(err)
	}
	cancel()
}

type ServiceCollector struct {
	prometheus.Collector
	services uint64
}

type Exporter struct {
	mu         sync.RWMutex
	services   []string
	collectors []ServiceCollector

	conn    *dbus.Conn
	service *prometheus.GaugeVec
}

func NewExporter(ctx context.Context) (*Exporter, error) {
	conn, err := dbus.NewWithContext(ctx)
	if err != nil {
		return nil, err
	}
	return &Exporter{
		conn: conn,
		service: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "node_service_active",
			Help: "Systemd service active.",
		}, []string{"service"}),
	}, nil
}

func (e *Exporter) Close() error {
	e.conn.Close()
	return nil
}

func (e *Exporter) addServices(services ...string) uint64 {
	bits := uint64(0)
	for _, service := range services {
		has := false
		for i := range e.services {
			if e.services[i] == service {
				bits |= 1 << i
				has = true
				break
			}
		}
		if !has {
			if len(e.services) == 64 {
				panic("too many services added: maximum is up to 64 services")
			}
			bits |= 1 << len(e.services)
			e.services = append(e.services, service)
		}
	}
	return bits
}

func (e *Exporter) AddServices(services ...string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.addServices(services...)
}

func (e *Exporter) AddCollector(collector prometheus.Collector, services ...string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	bits := e.addServices(services...)
	e.collectors = append(e.collectors, ServiceCollector{
		Collector: collector,
		services:  bits,
	})
}

func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	e.service.Describe(ch)
	for _, collector := range e.collectors {
		collector.Describe(ch)
	}
}

func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	t0 := time.Now()
	defer func() {
		Info.Println("collect duration total:", time.Since(t0))
	}()

	t := time.Now()
	activeServices := uint64(0)
	services, err := e.conn.ListUnitsByNamesContext(context.Background(), e.services)
	if err != nil {
		Error.Println("retrieving systemd services over dbus:", err)
		return
	} else {
		for i, service := range services {
			active := 0.0
			if service.ActiveState == "active" || service.ActiveState == "reloading" {
				active = 1.0
				activeServices |= 1 << i
			}
			e.service.WithLabelValues(e.services[i]).Set(active)
		}
		e.service.Collect(ch)
	}
	Info.Println("collect duration for node_service:", time.Since(t))

	wg := sync.WaitGroup{}
	for i, collector := range e.collectors {
		fmt.Printf("%d %x %x\n", i, collector.services, activeServices)
		if collector.services&activeServices == activeServices {
			wg.Add(1)
			go func(collector prometheus.Collector) {
				defer wg.Done()
				collector.Collect(ch)
			}(collector.Collector)
		}
	}
	wg.Wait()
}
