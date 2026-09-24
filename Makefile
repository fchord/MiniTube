.PHONY: up down run worker test docker-images k8s-cutover deploy-api

up:
	mkdir -p api/data/srs-hls api/data/live-hls
	docker compose up -d --remove-orphans postgres srs

down:
	docker compose down

run: up
	set -a && [ -f .env ] && . ./.env; set +a && cd api && go run ./cmd/api

worker: up
	set -a && [ -f .env ] && . ./.env; set +a && cd api && go run ./cmd/worker

test: up
	set -a && [ -f .env ] && . ./.env; set +a && cd api && go test ./...

docker-images:
	./k8s/minitube/build-images.sh

k8s-cutover:
	./k8s/minitube/cutover.sh

deploy-api:
	./k8s/minitube/deploy-api.sh
