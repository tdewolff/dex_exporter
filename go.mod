module github.com/tdewolff/dex_exporter

go 1.21

replace github.com/tdewolff/argp => ../argp

require (
	github.com/coreos/go-systemd/v22 v22.5.0
	github.com/gomodule/redigo v1.8.9
	github.com/grobie/gomemcache v0.0.0-20230213081705-239240bbc445
	github.com/prometheus/client_golang v1.17.0
	github.com/prometheus/procfs v0.11.1
	github.com/tdewolff/argp v0.0.0-20231229133132-ebbc03b216f1
	github.com/tomasen/fcgi_client v0.0.0-20180423082037-2bb3d819fd19
	golang.org/x/sys v0.15.0
	gopkg.in/yaml.v2 v2.4.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/go-sql-driver/mysql v1.7.1 // indirect
	github.com/godbus/dbus/v5 v5.0.4 // indirect
	github.com/jmoiron/sqlx v1.3.5 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/matttproud/golang_protobuf_extensions/v2 v2.0.0 // indirect
	github.com/pelletier/go-toml v1.9.5 // indirect
	github.com/prometheus/client_model v0.4.1-0.20230718164431-9a2bf3000d16 // indirect
	github.com/prometheus/common v0.45.0 // indirect
	github.com/stretchr/testify v1.8.4 // indirect
	google.golang.org/protobuf v1.31.0 // indirect
)
