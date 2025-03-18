module github.com/tdewolff/dex_exporter

go 1.24.1

replace github.com/tdewolff/argp => ../argp

require (
	github.com/coreos/go-systemd/v22 v22.5.0
	github.com/gomodule/redigo v1.9.2
	github.com/grobie/gomemcache v0.0.0-20230213081705-239240bbc445
	github.com/prometheus/client_golang v1.21.1
	github.com/prometheus/procfs v0.16.0
	github.com/tdewolff/argp v0.0.0-20250318104246-7815969061ef
	github.com/tomasen/fcgi_client v0.0.0-20180423082037-2bb3d819fd19
	golang.org/x/sys v0.31.0
	gopkg.in/yaml.v2 v2.4.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/godbus/dbus/v5 v5.1.0 // indirect
	github.com/jmoiron/sqlx v1.4.0 // indirect
	github.com/klauspost/compress v1.18.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pelletier/go-toml v1.9.5 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/common v0.63.0 // indirect
	google.golang.org/protobuf v1.36.5 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
