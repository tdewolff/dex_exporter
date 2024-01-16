package main

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Nginx struct {
	client  *Client
	reqStat uint64

	req prometheus.Counter
}

func NewNginx(url string) (*Nginx, error) {
	client, err := newClient(url)
	if err != nil {
		return nil, err
	}
	return &Nginx{
		client: client,

		req: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "nginx_requests_total",
			Help: "Total number of requests.",
		}),
	}, nil
}

func (e *Nginx) Describe(ch chan<- *prometheus.Desc) {
	e.req.Describe(ch)
}

func (e *Nginx) Collect(ch chan<- prometheus.Metric) {
	t := time.Now()
	req, err := e.updateReq()
	if err != nil {
		Error.Println(err)
	} else {
		e.req.Add(math.Max(0.0, float64(req)))
		e.req.Collect(ch)
	}
	Info.Println("collect duration for nginx:", time.Since(t))
}

const templateMetrics string = `Active connections: %d
server accepts handled requests
%d %d %d
Reading: %d Writing: %d Waiting: %d
`

type StubStats struct {
	Connections StubConnections
	Requests    uint64
}

type StubConnections struct {
	Active   uint64
	Accepted uint64
	Handled  uint64
	Reading  uint64
	Writing  uint64
	Waiting  uint64
}

func (e *Nginx) updateReq() (uint64, error) {
	b, err := e.client.Get(context.TODO())
	if err != nil {
		return 0, err
	}

	s := StubStats{}
	if _, err := fmt.Fscanf(bytes.NewReader(b), templateMetrics,
		&s.Connections.Active,
		&s.Connections.Accepted,
		&s.Connections.Handled,
		&s.Requests,
		&s.Connections.Reading,
		&s.Connections.Writing,
		&s.Connections.Waiting); err != nil {
		return 0, fmt.Errorf("failed to scan template metrics: %w", err)
	}

	diff := s.Requests - e.reqStat
	e.reqStat = s.Requests
	return diff, nil
}
