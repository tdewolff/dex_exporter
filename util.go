package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

func ParseURI(uri string) (string, string, error) {
	if strings.HasPrefix(uri, "unix:") {
		uri = uri[5:]
		if strings.HasPrefix(uri, "//") {
			uri = uri[2:]
		}
		if !path.IsAbs(uri) {
			return "", "", fmt.Errorf("Unix socket path is not an absolute path")
		}
		return "unix", uri, nil
	}

	if strings.HasPrefix(uri, "tcp://") {
		uri = uri[6:]
	}
	return "tcp", uri, nil
}

type URIGlobs struct {
	literals []string
	globs    []string
}

func ParseURIGlobs(uris []string) (URIGlobs, error) {
	var literals, globs []string
	for i := range uris {
		uri := uris[i]
		scheme, _, err := ParseURI(uri)
		if err != nil {
			return URIGlobs{}, err
		}
		if scheme == "unix" {
			if strings.ContainsRune(uri, '*') {
				globs = append(globs, uri)
			} else if info, err := os.Stat(uri); err != nil {
				return URIGlobs{}, err
			} else if info.IsDir() {
				globs = append(globs, path.Join(uri, "*"))
			} else {
				literals = append(literals, uri)
			}
		} else {
			literals = append(literals, uris[i])
		}
	}
	for _, uriGlob := range globs {
		if _, err := filepath.Glob(uriGlob); err != nil {
			return URIGlobs{}, err
		}
	}
	fmt.Println(literals, globs)
	return URIGlobs{literals, globs}, nil
}

func (z URIGlobs) Get() []string {
	uris := z.literals
	for _, uriGlob := range z.globs {
		matches, _ := filepath.Glob(uriGlob)
		fmt.Println(uriGlob, "=>", matches)
		uris = append(uris, matches...)
	}
	return uris
}

func ListenAndServe(uri, tlsCert, tlsKey string) error {
	scheme, host, err := ParseURI(uri)
	if err != nil {
		return err
	}

	var listener net.Listener
	if scheme == "unix" {
		if _, err := os.Stat(host); err == nil {
			Info.Println("removing existing file", host)
			if err := os.Remove(host); err != nil {
				return err
			}
		}
		listener, err = net.Listen("unix", host)
		if err != nil {
			return err
		}
		Info.Println("setting file permissions to 0770 on", host)
		if os.Chmod(host, 0770); err != nil {
			return err
		}
		Info.Println("listening on Unix socket", host)
		return (&http.Server{Addr: host, Handler: nil}).Serve(listener)
	}

	if tlsCert != "" && tlsKey != "" {
		Info.Println("listening on", host, "with TLS")
		return http.ListenAndServeTLS(host, tlsCert, tlsKey, nil)
	}
	Info.Println("listening on", host)
	return http.ListenAndServe(host, nil)
}

func BasicAuth(next http.Handler, users map[string]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if ok {
			for authUsername, authPassword := range users {
				authUsernameHash := sha256.Sum256([]byte(authUsername))
				authPasswordHash := sha256.Sum256([]byte(authPassword))
				usernameHash := sha256.Sum256([]byte(username))
				passwordHash := sha256.Sum256([]byte(password))
				usernameCompare := subtle.ConstantTimeCompare(usernameHash[:], authUsernameHash[:])
				passwordCompare := subtle.ConstantTimeCompare(passwordHash[:], authPasswordHash[:])
				if usernameCompare == 1 && passwordCompare == 1 {
					next.ServeHTTP(w, r)
					return
				}
			}
		}

		w.Header().Set("WWW-Authenticate", `Basic realm="restricted", charset="UTF-8"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})
}

type Client struct {
	client *http.Client
	uri    string
}

func newClient(uri string) (*Client, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	if u.Port() == "" {
		if u.Scheme == "http" {
			u.Host += ":80"
		} else if u.Scheme == "https" {
			u.Host += ":443"
		} else if u.Scheme == "unix" {
			uri = "http://localhost" + u.Path
		} else {
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
		uri: uri,
	}, nil
}

func (c *Client) Get(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.uri, nil)
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
