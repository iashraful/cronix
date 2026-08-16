BINARY    := cronix
IMAGE     := cronix
CONTAINER := cronix-dev
TOKEN     ?= devtoken
PORT      ?= 7002
DATA_DIR  := .data
STORE     := $(DATA_DIR)/jobs.json

.PHONY: help build test vet lint run ui \
        docker-build docker-run docker-logs docker-stop \
        cli-list cli-add cli-run \
        clean

help:
	@echo "Targets:"
	@echo "  build          compile the binary to bin/$(BINARY)"
	@echo "  test           run all Go tests"
	@echo "  vet            run go vet"
	@echo "  lint           check gofmt"
	@echo "  ui              build the React UI into web/ (npm ci + vite build)"
	@echo "  run            run the server locally (token: $$CRONIX_API_TOKEN or $(TOKEN))"
	@echo "  docker-build   build the Docker image"
	@echo "  docker-run     start the container (volume $(DATA_DIR), port $(PORT))"
	@echo "  docker-logs    follow container logs"
	@echo "  docker-stop    stop and remove the container"
	@echo "  cli-list       list jobs via the running container"
	@echo "  cli-add        add a sample job via the running container"
	@echo "  cli-run ID     run a job now via the running container"
	@echo "  clean          remove build artifacts and local data"

build:
	go build -o bin/$(BINARY) .

test:
	go test ./...

vet:
	go vet ./...

lint:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)

ui:
	npm --prefix ui ci
	npm --prefix ui run build

run:
	mkdir -p $(DATA_DIR)
	CRONIX_API_TOKEN=$${CRONIX_API_TOKEN:-$(TOKEN)} \
	CRONIX_STORE_PATH=$(STORE) \
	go run .

docker-build:
	docker build -t $(IMAGE) .

docker-run:
	docker rm -f $(CONTAINER) 2>/dev/null || true
	mkdir -p $(DATA_DIR)
	docker run --rm -d --name $(CONTAINER) \
		-e CRONIX_API_TOKEN=$${CRONIX_API_TOKEN:-$(TOKEN)} \
		-v "$$(pwd)/$(DATA_DIR):/data" \
		-p $(PORT):8080 \
		$(IMAGE)

docker-logs:
	docker logs -f $(CONTAINER)

docker-stop:
	docker rm -f $(CONTAINER) 2>/dev/null || true

cli-list:
	docker exec $(CONTAINER) /cronix cli list --token $${CRONIX_API_TOKEN:-$(TOKEN)}

cli-add:
	docker exec $(CONTAINER) /cronix cli add \
		--name ping --schedule '*/5 * * * *' \
		--curl 'curl -s https://example.com' \
		--token $${CRONIX_API_TOKEN:-$(TOKEN)}

cli-run:
	@test -n "$(filter-out $@,$(MAKECMDGOALS))" || (echo "usage: make cli-run ID" && exit 1)
	docker exec $(CONTAINER) /cronix cli run --token $${CRONIX_API_TOKEN:-$(TOKEN)} $(filter-out $@,$(MAKECMDGOALS))

%:
	@:
