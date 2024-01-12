all: build

build:
	CGO_ENABLED=0 go build -ldflags "-s -w" -trimpath

.PHONY: build
.SILENT: build
