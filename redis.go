package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gomodule/redigo/redis"
	"github.com/prometheus/client_golang/prometheus"
)

type RedisOptions struct {
	URI string `desc:"A URI or unix socket path for connecting to the Redis server."`
}

type Redis struct {
	client redis.Conn
	stats  redisStats

	mem *prometheus.GaugeVec
	key *prometheus.CounterVec
}

func NewRedis(opts RedisOptions) (*Redis, error) {
	scheme, host, err := ParseURI(opts.URI)
	if err != nil {
		return nil, err
	}
	client, err := redis.Dial(scheme, host)
	if err != nil {
		return nil, err
	}
	e := &Redis{
		client: client,

		mem: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "redis_mem_bytes",
			Help: "Memory size in bytes.",
		}, []string{"type"}),
		key: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "redis_key_total",
			Help: "Key hits or misses.",
		}, []string{"type"}),
	}
	e.updateStats()
	return e, nil
}

func (e *Redis) Close() error {
	return e.client.Close()
}

func (e *Redis) Describe(ch chan<- *prometheus.Desc) {
	e.mem.Describe(ch)
	e.key.Describe(ch)
}

func (e *Redis) Collect(ch chan<- prometheus.Metric) {
	t := time.Now()
	stats, err := e.updateStats()
	if err != nil {
		Error.Println(err)
	} else {
		e.mem.WithLabelValues("used").Set(float64(stats.MemoryUsed))
		e.mem.WithLabelValues("total").Set(float64(stats.MemoryTotal))
		e.mem.Collect(ch)

		e.key.WithLabelValues("hits").Add(float64(stats.KeyHits))
		e.key.WithLabelValues("misses").Add(float64(stats.KeyMisses))
		e.key.Collect(ch)
	}
	Debug.Println("collect duration for redis:", time.Since(t))
}

type redisStats struct {
	MemoryUsed  uint64
	MemoryTotal uint64
	KeyHits     uint64
	KeyMisses   uint64
}

func (e *Redis) updateStats() (redisStats, error) {
	reply, err := e.client.Do("INFO", "ALL")
	if err != nil {
		return redisStats{}, err
	}

	info, ok := reply.([]byte)
	if !ok {
		return redisStats{}, fmt.Errorf("redis: reply to INFO ALL is not a []byte")
	}

	cur := redisStats{}
	for _, line := range strings.Split(string(info), "\n") {
		line = strings.TrimSpace(line)
		split := strings.SplitN(line, ":", 2)
		if len(split) != 2 {
			continue
		}

		key, val := split[0], split[1]
		switch key {
		case "used_memory":
			cur.MemoryUsed = redisGetUint64(key, val)
		case "maxmemory":
			cur.MemoryTotal = redisGetUint64(key, val)
		case "keyspace_hits":
			cur.KeyHits = redisGetUint64(key, val)
		case "keyspace_misses":
			cur.KeyMisses = redisGetUint64(key, val)
		}
	}

	diff := cur
	diff.KeyHits -= e.stats.KeyHits
	diff.KeyMisses -= e.stats.KeyMisses
	e.stats = cur
	return diff, nil
}

func redisGetUint64(key, val string) uint64 {
	n, err := strconv.ParseUint(val, 10, 64)
	if err != nil {
		Warning.Printf("redis: key %v: %v is not an integer", key, val)
	}
	return n
}
