.PHONY: up down run worker test docker-images k8s-cutover k8s-test deploy-api

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

k8s-test:
	./k8s/minitube-test/bootstrap.sh

deploy-api:
	@test "$(ENV)" = "prod" -o "$(ENV)" = "test" || (echo "ENV=prod|test required"; exit 1)
	ENV=$(ENV) ./k8s/minitube/deploy-api.sh
