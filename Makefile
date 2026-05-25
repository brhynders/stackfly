.PHONY: build release run dev clean dev-up dev-down dev-rebuild

build:
	go build -o bin/stackfly ./cmd/stackfly

release:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/stackfly-linux-amd64 ./cmd/stackfly

run: build
	./bin/stackfly

dev:
	go run ./cmd/stackfly --password admin

dev-up:
	docker compose -f docker-compose.dev.yml up --build

dev-down:
	docker compose -f docker-compose.dev.yml down -v
	rm -rf /tmp/stackfly-dev

dev-rebuild:
	docker compose -f docker-compose.dev.yml up --build --force-recreate

clean:
	rm -rf bin/
