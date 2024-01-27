package main

import (
	"strconv"
	"time"

	"github.com/grobie/gomemcache/memcache"
	"github.com/prometheus/client_golang/prometheus"
)

type MemcacheOptions struct {
	URI []string `desc:"A URI or unix socket path for connecting to the Memcache server."`
}

type Memcache struct {
	uris  URIGlobs
	stats map[string]memcacheStats

	mem *prometheus.GaugeVec
	key *prometheus.CounterVec
}

func NewMemcache(opts MemcacheOptions) (*Memcache, error) {
	uris, err := ParseURIGlobs(opts.URI)
	if err != nil {
		return nil, err
	}
	e := &Memcache{
		uris:  uris,
		stats: map[string]memcacheStats{},

		mem: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "memcache_mem_bytes",
			Help: "Memory size in bytes.",
		}, []string{"type", "server"}),
		key: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "memcache_key_total",
			Help: "Key hits or misses.",
		}, []string{"type", "server"}),
	}
	e.updateStats()
	return e, nil
}

func (e *Memcache) Close() error {
	return nil
}

func (e *Memcache) Describe(ch chan<- *prometheus.Desc) {
	e.mem.Describe(ch)
	e.key.Describe(ch)
}

func (e *Memcache) Collect(ch chan<- prometheus.Metric) {
	t := time.Now()
	stats, err := e.updateStats()
	if err != nil {
		Error.Println(err)
	} else {
		for server, stat := range stats {
			e.mem.WithLabelValues("used", server).Set(float64(stat.MemoryUsed))
			e.mem.WithLabelValues("total", server).Set(float64(stat.MemoryTotal))
			e.key.WithLabelValues("hits", server).Add(float64(stat.KeyHits))
			e.key.WithLabelValues("misses", server).Add(float64(stat.KeyMisses))
		}
		e.mem.Collect(ch)
		e.key.Collect(ch)
	}
	Debug.Println("collect duration for memcache:", time.Since(t))
}

type memcacheStats struct {
	MemoryUsed  uint64
	MemoryTotal uint64
	KeyHits     uint64
	KeyMisses   uint64
}

func (e *Memcache) updateStats() (map[string]memcacheStats, error) {
	client, err := memcache.New(e.uris.Get()...)
	if err != nil {
		return nil, err
	}
	stats, err := client.Stats()
	if err != nil {
		//client.Close() // TODO
		return nil, err
		//} else if err := client.Close(); err != nil {
		//	return nil, err
	}

	diffs := map[string]memcacheStats{}
	for addr, stat := range stats {
		name := addr.String()

		cur := memcacheStats{}
		cur.MemoryUsed = memcacheGetUint64(stat.Stats, "bytes")
		cur.MemoryTotal = memcacheGetUint64(stat.Stats, "limit_maxbytes")
		cur.KeyHits = memcacheSumUint64(stat.Stats, []string{"get_hits", "delete_hits", "incr_hits", "decr_hits", "cas_hits", "touch_hits"})
		cur.KeyMisses = memcacheSumUint64(stat.Stats, []string{"get_misses", "delete_misses", "incr_misses", "decr_misses", "cas_misses", "touch_misses"})

		prev, ok := e.stats[name]
		e.stats[name] = cur
		if !ok {
			continue
		}

		diff := cur
		diff.KeyHits -= prev.KeyHits
		diff.KeyMisses -= prev.KeyMisses
		diffs[name] = diff
	}
	return diffs, nil
}

func memcacheGetUint64(stats map[string]string, key string) uint64 {
	val := stats[key]
	n, err := strconv.ParseUint(val, 10, 64)
	if err != nil {
		Warning.Printf("memcache: key %v: %v is not an integer", key, val)
	}
	return n
}

func memcacheSumUint64(stats map[string]string, keys []string) uint64 {
	var sum uint64
	for _, key := range keys {
		sum += memcacheGetUint64(stats, key)
	}
	return sum
}
