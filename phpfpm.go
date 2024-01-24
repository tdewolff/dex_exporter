package main

import (
	"bufio"
	"bytes"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	fcgiclient "github.com/tomasen/fcgi_client"
)

type PHPFPMOptions struct {
	URI      string `desc:"A URI or unix socket path for connecting to the PHP-FPM server."`
	Filepath string `desc:"Filepath that exposes the PHP-FPM metrics."`
}

type PHPFPM struct {
	scheme, host string
	filepath     string
	stats        phpfpmStats

	mem        *prometheus.GaugeVec
	stringsMem *prometheus.GaugeVec
	key        *prometheus.CounterVec
}

func NewPHPFPM(opts PHPFPMOptions) (*PHPFPM, error) {
	scheme, host, err := ParseURI(opts.URI)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(opts.Filepath); err != nil {
		return nil, err
	}
	e := &PHPFPM{
		scheme:   scheme,
		host:     host,
		filepath: opts.Filepath,
		stats:    phpfpmStats{},

		mem: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "phpfpm_mem_bytes",
			Help: "Memory size in bytes.",
		}, []string{"type"}),
		stringsMem: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "phpfpm_strings_mem_bytes",
			Help: "Interned strings memory size in bytes.",
		}, []string{"type"}),
		key: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "phpfpm_key_total",
			Help: "Key hits or misses.",
		}, []string{"type"}),
	}
	e.updateStats()
	return e, nil
}

func (e *PHPFPM) Close() error {
	return nil
}

func (e *PHPFPM) Describe(ch chan<- *prometheus.Desc) {
	e.mem.Describe(ch)
	e.stringsMem.Describe(ch)
	e.key.Describe(ch)
}

func (e *PHPFPM) Collect(ch chan<- prometheus.Metric) {
	t := time.Now()
	stats, err := e.updateStats()
	if err != nil {
		Error.Println(err)
	} else {
		e.mem.WithLabelValues("used").Set(float64(stats.MemoryUsed))
		e.mem.WithLabelValues("total").Set(float64(stats.MemoryTotal))
		e.mem.Collect(ch)

		e.stringsMem.WithLabelValues("used").Set(float64(stats.InternedStringsMemoryUsed))
		e.stringsMem.WithLabelValues("total").Set(float64(stats.InternedStringsMemoryTotal))
		e.stringsMem.Collect(ch)

		e.key.WithLabelValues("hits").Add(float64(stats.KeyHits))
		e.key.WithLabelValues("misses").Add(float64(stats.KeyMisses))
		e.key.Collect(ch)
	}
	Debug.Println("collect duration for phpfpm:", time.Since(t))
}

type phpfpmStats struct {
	MemoryUsed                 uint64
	MemoryTotal                uint64
	InternedStringsMemoryUsed  uint64
	InternedStringsMemoryTotal uint64
	KeyHits                    uint64
	KeyMisses                  uint64
}

func (e *PHPFPM) updateStats() (phpfpmStats, error) {
	client, err := fcgiclient.Dial(e.scheme, e.host)
	if err != nil {
		return phpfpmStats{}, err
	}
	defer client.Close()

	env := map[string]string{}
	env["SCRIPT_FILENAME"] = e.filepath
	env["SCRIPT_NAME"] = e.filepath

	resp, err := client.Get(env)
	if err != nil {
		return phpfpmStats{}, err
	}

	content, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return phpfpmStats{}, err
	}

	cur := phpfpmStats{}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}

		switch fields[0] {
		case "opcache_status_memory_usage_used_memory":
			cur.MemoryUsed = phpfpmGetUint64(fields[0], fields[1])
		case "opcache_status_memory_usage_free_memory":
			cur.MemoryTotal = phpfpmGetUint64(fields[0], fields[1])
		case "opcache_status_interned_strings_usage_used_memory":
			cur.InternedStringsMemoryUsed = phpfpmGetUint64(fields[0], fields[1])
		case "opcache_status_interned_strings_usage_free_memory":
			cur.InternedStringsMemoryTotal = phpfpmGetUint64(fields[0], fields[1])
		case "opcache_status_opcache_statistics_hits":
			cur.KeyHits = phpfpmGetUint64(fields[0], fields[1])
		case "opcache_status_opcache_statistics_misses":
			cur.KeyMisses = phpfpmGetUint64(fields[0], fields[1])
		}
	}
	cur.MemoryTotal += cur.MemoryUsed
	cur.InternedStringsMemoryTotal += cur.InternedStringsMemoryUsed

	diff := cur
	diff.KeyHits -= e.stats.KeyHits
	diff.KeyMisses -= e.stats.KeyMisses
	e.stats = cur
	return diff, nil
}

func phpfpmGetUint64(key, val string) uint64 {
	n, err := strconv.ParseUint(val, 10, 64)
	if err != nil {
		Warning.Printf("phpfpm: key %v: %v is not an integer", key, val)
	}
	return n
}
