package main

import (
	"bufio"
	"bytes"
	"io/ioutil"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	fcgiclient "github.com/tomasen/fcgi_client"
)

type PHPFPMOptions struct {
	StatusURI  []string `desc:"A URI or unix socket path for connecting to the PHP-FPM server."`
	StatusPath string   `desc:"Path of the PHP-FPM status page."`

	OPcacheURI  string `name:"opcache-uri" desc:"A URI or unix socket path for connecting to the PHP-FPM server."`
	OPcachePath string `name:"opcache-path" desc:"Path of the OPcache metrics page."`
}

type PHPFPM struct {
	statusURIs   URIGlobs
	statusPath   string
	opcacheURI   string
	opcachePath  string
	opcacheStats phpfpmOPcacheStats

	proc              *prometheus.GaugeVec
	opcacheMem        *prometheus.GaugeVec
	opcacheStringsMem *prometheus.GaugeVec
	opcacheKey        *prometheus.CounterVec
}

func NewPHPFPM(opts PHPFPMOptions) (*PHPFPM, error) {
	statusURIs, err := ParseURIGlobs(opts.StatusURI)
	if err != nil {
		return nil, err
	} else if _, _, err := ParseURI(opts.OPcacheURI); err != nil {
		return nil, err
	}
	e := &PHPFPM{
		statusURIs:  statusURIs,
		statusPath:  opts.StatusPath,
		opcacheURI:  opts.OPcacheURI,
		opcachePath: opts.OPcachePath,

		proc: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "phpfpm_proc_count",
			Help: "Number of processes.",
		}, []string{"type", "pool"}),
		opcacheMem: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "phpfpm_opcache_mem_bytes",
			Help: "Memory size in bytes.",
		}, []string{"type"}),
		opcacheStringsMem: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "phpfpm_opcache_strings_mem_bytes",
			Help: "Interned strings memory size in bytes.",
		}, []string{"type"}),
		opcacheKey: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "phpfpm_opcache_key_total",
			Help: "Key hits or misses.",
		}, []string{"type"}),
	}
	e.updateOPcacheStats()
	return e, nil
}

func (e *PHPFPM) Close() error {
	return nil
}

func (e *PHPFPM) Describe(ch chan<- *prometheus.Desc) {
	e.proc.Describe(ch)
	e.opcacheMem.Describe(ch)
	e.opcacheStringsMem.Describe(ch)
	e.opcacheKey.Describe(ch)
}

func (e *PHPFPM) Collect(ch chan<- prometheus.Metric) {
	t0 := time.Now()
	t := time.Now()
	stats, err := e.updateStats()
	if err != nil {
		Error.Println(err)
	} else {
		for pool, stat := range stats {
			e.proc.WithLabelValues("active", pool).Set(float64(stat.ActiveProcesses))
			e.proc.WithLabelValues("total", pool).Set(float64(stat.TotalProcesses))
		}
		e.proc.Collect(ch)
	}
	Debug.Println("collect duration for phpfpm proc:", time.Since(t))

	t = time.Now()
	opcacheStats, err := e.updateOPcacheStats()
	if err != nil {
		Error.Println(err)
	} else {
		e.opcacheMem.WithLabelValues("used").Set(float64(opcacheStats.MemoryUsed))
		e.opcacheMem.WithLabelValues("total").Set(float64(opcacheStats.MemoryTotal))
		e.opcacheMem.Collect(ch)

		e.opcacheStringsMem.WithLabelValues("used").Set(float64(opcacheStats.InternedStringsMemoryUsed))
		e.opcacheStringsMem.WithLabelValues("total").Set(float64(opcacheStats.InternedStringsMemoryTotal))
		e.opcacheStringsMem.Collect(ch)

		e.opcacheKey.WithLabelValues("hits").Add(float64(opcacheStats.KeyHits))
		e.opcacheKey.WithLabelValues("misses").Add(float64(opcacheStats.KeyMisses))
		e.opcacheKey.Collect(ch)
	}
	Debug.Println("collect duration for phpfpm opcache:", time.Since(t))
	Debug.Println("collect duration for phpfpm:", time.Since(t0))
}

type phpfpmStats struct {
	ActiveProcesses uint64
	TotalProcesses  uint64
}

func (e *PHPFPM) updateStats() (map[string]phpfpmStats, error) {
	stats := map[string]phpfpmStats{}
	for _, uri := range e.statusURIs.Get() {
		content, err := e.getURL(uri, e.statusPath)
		if err != nil {
			return nil, err
		}

		// pool:                 name
		// process manager:      static
		// start time:           24/Jan/2024:15:12:49 +0100
		// start since:          213812
		// accepted conn:        30102
		// listen queue:         0
		// max listen queue:     0
		// listen queue len:     0
		// idle processes:       31
		// active processes:     1
		// total processes:      32
		// max active processes: 15
		// max children reached: 0
		// slow requests:        0

		pool := ""
		cur := phpfpmStats{}
		scanner := bufio.NewScanner(bytes.NewReader(content))
		for scanner.Scan() {
			line := scanner.Text()
			if colon := strings.IndexByte(line, ':'); colon != -1 {
				key := line[:colon]
				val := strings.TrimSpace(line[colon+1:])
				switch key {
				case "pool":
					pool = val
				case "active processes":
					cur.ActiveProcesses = phpfpmGetUint64(key, val)
				case "total processes":
					cur.TotalProcesses = phpfpmGetUint64(key, val)
				}
			}
		}
		if pool == "" {
			Warning.Println("PHP-FPM status page pool name not found for %v")
		} else {
			stats[pool] = cur
		}
	}
	return stats, nil
}

type phpfpmOPcacheStats struct {
	MemoryUsed                 uint64
	MemoryTotal                uint64
	InternedStringsMemoryUsed  uint64
	InternedStringsMemoryTotal uint64
	KeyHits                    uint64
	KeyMisses                  uint64
}

func (e *PHPFPM) updateOPcacheStats() (phpfpmOPcacheStats, error) {
	content, err := e.getURL(e.opcacheURI, e.opcachePath)
	if err != nil {
		return phpfpmOPcacheStats{}, err
	}

	cur := phpfpmOPcacheStats{}
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
	diff.KeyHits -= e.opcacheStats.KeyHits
	diff.KeyMisses -= e.opcacheStats.KeyMisses
	e.opcacheStats = cur
	return diff, nil
}

func (e *PHPFPM) getURL(uri, path string) ([]byte, error) {
	scheme, host, _ := ParseURI(uri)
	client, err := fcgiclient.Dial(scheme, host)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	env := map[string]string{}
	env["SCRIPT_FILENAME"] = path
	env["SCRIPT_NAME"] = path
	resp, err := client.Get(env)
	if err != nil {
		return nil, err
	}
	content, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return content, nil
}

func phpfpmGetUint64(key, val string) uint64 {
	n, err := strconv.ParseUint(val, 10, 64)
	if err != nil {
		Warning.Printf("phpfpm: key %v: %v is not an integer", key, val)
	}
	return n
}
