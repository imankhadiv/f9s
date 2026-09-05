.PHONY: build test lint run snapshot playground chaos playground-down

build:
	go build -o f9s ./cmd/f9s

test:
	go test -race ./...

lint:
	go vet ./...

run: build
	./f9s --address http://localhost:8081

snapshot: build
	./f9s --address http://localhost:8081 --snapshot

playground:
	docker compose -f playground/docker-compose.yml up -d --wait
	docker compose -f playground/docker-compose.yml exec -u root jobmanager \
		chown flink:flink /archive /tmp/sp
	docker compose -f playground/docker-compose.yml exec -u root taskmanager \
		chown flink:flink /tmp/sp
	docker compose -f playground/docker-compose.yml exec jobmanager \
		bin/flink run -d examples/streaming/TopSpeedWindowing.jar
	@echo "Flink UI: http://localhost:8081 — now run: make run"

chaos: playground
	python3 playground/chaos.py
	@echo "every detector should fire within ~90s — run: make run"

playground-down:
	docker compose -f playground/docker-compose.yml down -v
