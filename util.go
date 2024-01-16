package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func ListenAndServe(host string) error {
	var listener net.Listener
	var err error
	if strings.HasPrefix(host, "unix://") {
		path := host[7:]
		if _, err := os.Stat(path); err == nil {
			if err := os.Remove(path); err != nil {
				return err
			}
		}
		listener, err = net.Listen("unix", host[7:])
		if err != nil {
			return err
		} else if os.Chmod(path, 0770); err != nil {
			return err
		}
	} else {
		listener, err = net.Listen("tcp", host)
		if err != nil {
			return err
		}
	}
	return (&http.Server{Addr: host, Handler: nil}).Serve(listener)

}

type Client struct {
	client *http.Client
	uri    string
}

func newClient(dst string) (*Client, error) {
	u, err := url.Parse(dst)
	if err != nil {
		return nil, err
	}
	if u.Port() == "" {
		if u.Scheme == "http" {
			u.Host += ":80"
		} else if u.Scheme == "https" {
			u.Host += ":443"
		} else if u.Scheme != "unix" {
			return nil, fmt.Errorf("unsupported protocol: %v", u.Scheme)
		}
	}

	d := net.Dialer{
		Timeout:   1 * time.Second,  // timeout in establishing connection only
		KeepAlive: 30 * time.Second, // time between keep-alive probes
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if u.Scheme == "unix" {
				return d.DialContext(ctx, "unix", u.Path)
			}
			return d.DialContext(ctx, "tcp", u.Host)
		},
	}
	return &Client{
		client: &http.Client{
			Transport: tr,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse // don't follow redirects
			},
		},
		uri: u.Path,
	}, nil
}

func (c *Client) Get(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://localhost"+c.uri, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}
