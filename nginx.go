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
	e.updateStats()
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
		Debug.Printf("data from stub_status:\n%v", string(b))
		return nginxStats{}, fmt.Errorf("failed to scan template metrics: %w", err)
	}
	fmt.Println("cur", cur)

	if cur.Accepted < e.stats.Accepted && cur.Handled < e.stats.Handled && cur.Requests < e.stats.Requests {
		// nginx was reset
		e.stats = cur
		return cur, nil
	}

	diff := cur
	diff.Accepted = intDiff(e.stats.Accepted, cur.Accepted)
	diff.Handled = intDiff(e.stats.Handled, cur.Handled)
	diff.Requests = intDiff(e.stats.Requests, cur.Requests)
	fmt.Println("diff", diff)
	e.stats = cur
	return diff, nil
}
