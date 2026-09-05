.PHONY: build web test vet sqlc run migrate
build: web
	go build ./...
web:
	cd web && pnpm install --frozen-lockfile && pnpm build
test:
	go test ./...
vet:
	go vet ./...
sqlc:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate
migrate:
	go run ./cmd/konnyaku migrate
run:
	go run ./cmd/konnyaku serve
