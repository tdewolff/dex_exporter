# Dex exporter
Various exporters for Prometheus scraping used in a setup with: Linux, Nginx, MariaDB, Redis, Memcached, PHP-FPM, Postfix, Dovecot. It's meant for servers managed by Dex, a web server management software.

By bundling the various exporters we safe (tremendously) on memory consumption. Additionally, we limit traffic by cutting down (severely) on exporter metrics.

Exporters support reading from Unix sockets and reading from multiple pools (e.g. for Memcached).

It also supports listening on a Unix socket so that we can use Nginx as a proxy server while clamping down on file permissions and access rights. This will tighten down security since we can restrict local access (which is easier with a Unix socket than listening on a TCP port) and use the Nginx proxy for adding Basic Auth and TLS encryption.
