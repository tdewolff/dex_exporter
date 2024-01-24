package main

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type NginxOptions struct {
	URI string `desc:"A URI or unix socket path for scraping NGINX metrics. The stub_status page must be available through the URI."`
}

type Nginx struct {
	client *Client
	stats  nginxStats

	req prometheus.Counter
}

func NewNginx(opts NginxOptions) (*Nginx, error) {
	client, err := newClient(opts.URI)
	if err != nil {
		return nil, err
	}
	e := &Nginx{
		client: client,

		req: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nginx_requests_total",
			Help: "Total number of requests.",
		}),
	}
	if _, err = e.updateStats(); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *Nginx) Close() error {
	return nil
}

func (e *Nginx) Describe(ch chan<- *prometheus.Desc) {
	e.req.Describe(ch)
}

func (e *Nginx) Collect(ch chan<- prometheus.Metric) {
	t := time.Now()
	stats, err := e.updateStats()
	if err != nil {
		Error.Println(err)
	} else {
		e.req.Add(math.Max(0.0, float64(stats.Requests)))
		e.req.Collect(ch)
	}
	Debug.Println("collect duration for nginx:", time.Since(t))
}

const templateMetrics string = `Active connections: %d
server accepts handled requests
%d %d %d
Reading: %d Writing: %d Waiting: %d
`

type nginxStats struct {
	Active   uint64
	Accepted uint64
	Handled  uint64
	Requests uint64
	Reading  uint64
	Writing  uint64
	Waiting  uint64
}

func (e *Nginx) updateStats() (nginxStats, error) {
	b, err := e.client.Get(context.TODO())
	if err != nil {
		return nginxStats{}, err
	}

	cur := nginxStats{}
	if _, err := fmt.Fscanf(bytes.NewReader(b), templateMetrics,
		&cur.Active,
		&cur.Accepted,
		&cur.Handled,
		&cur.Requests,
		&cur.Reading,
		&cur.Writing,
		&cur.Waiting); err != nil {
		return nginxStats{}, fmt.Errorf("failed to scan template metrics: %w", err)
	}

	diff := cur
	diff.Handled -= e.stats.Handled
	diff.Requests -= e.stats.Requests
	e.stats = cur
	return diff, nil
}
