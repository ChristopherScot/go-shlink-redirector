BINARY_NAME=app
DOCKER_IMAGE=ghcr.io/$(shell echo ${GITHUB_REPOSITORY} | tr '[:upper:]' '[:lower:]'):latest

build:
	export CGO_ENABLED=0; go build -o bin/$(BINARY_NAME) .

run: build
	./bin/$(BINARY_NAME)

test:
	go test ./...

clean:
	rm -f bin/$(BINARY_NAME)

docker-build: build
	docker build -t $(DOCKER_IMAGE) .

docker-push: docker-build
	docker push $(DOCKER_IMAGE)

all: build

.PHONY: build run test clean docker-build docker-push all
