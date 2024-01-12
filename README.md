# Dex exporter
Various exporters for Prometheus scraping used in a setup with: Linux, Nginx, MariaDB, Redis, Memcached, PHP-FPM, Postfix, Dovecot. It's meant for servers managed by Dex, a web server management software.

By bundling the various exporters we save (tremendously) on memory consumption since each binary is about 8 MiB and needs to reside in memory while running. Additionally, we limit processing and traffic by cutting down (severely) on exporter metrics.

Exporters support reading from Unix sockets and reading from multiple pools (e.g. for Memcached).

It also supports listening on a Unix socket so that we can use Nginx as a proxy server while clamping down on file permissions and access rights. This will tighten down security since we can restrict local access (which is easier with a Unix socket than listening on a TCP port) and use the Nginx proxy for adding Basic Auth and TLS encryption.

## Metrics

```
node_cpu_seconds_total{mode}: Total CPU time in seconds.
node_mem_bytes{type}: Memory size in bytes.
node_swap_bytes{type}: Swap size in bytes.
node_network_bytes_total{interface,type}: Network traffic in bytes.
node_disk_kilobytes{device,type}: Hard disk size in kilobytes.
node_diskio_seconds_total{device,type}: Hard disk time in seconds.
node_service_active{service}: Systemd service active.
nginx_requests_total: Total number of requests.
```
