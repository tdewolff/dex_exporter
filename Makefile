ENVS=GO111MODULES=on CGO_ENABLED=0 GOOS=linux GOARCH=amd64
TAG=`git describe --tags 2>/dev/null || echo "v0"`
COMMIT=`git rev-parse --short HEAD`

all: build

build:
	${ENVS} go build -ldflags "-X main.Version=${TAG}-${COMMIT}"

release:
	if [ -z "${VERSION}" ]; then echo "Specify VERSION"; exit 1; fi
	echo "Releasing ${VERSION}"
	${ENVS} go build -ldflags "-s -w -X 'main.Version=${VERSION}'" -trimpath -o dex_exporter
	tar -czvf dex_exporter_linux_amd64.tar.gz dex_exporter
	gh release create "${VERSION}" dex_exporter_linux_amd64.tar.gz
	rm dex_exporter_linux_amd64.tar.gz
	git pull --tags

.PHONY: build release
.SILENT: build release
