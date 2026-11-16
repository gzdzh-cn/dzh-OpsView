.PHONY: dev assets build release test check api
dev:
	@./scripts/dev.sh
api:
	python3 scripts/generate-openapi.py
	cd web && npm run api:types
assets:
	cd web && npm run build
	rm -rf server/public/dist
	cp -R web/dist server/public/dist
build:
	cd web && npm ci
	$(MAKE) assets
	mkdir -p dist/local
	cd server && go build -trimpath -o ../dist/local/opsview ./cmd/opsview
	cd server && go build -trimpath -o ../dist/local/opsview-executor ./cmd/executor
	cd server && go build -trimpath -o ../dist/local/opsview-plugin-worker ./cmd/plugin-worker
	cd server && go build -trimpath -o ../dist/local/opsview-pack ./cmd/pack
release: build
	./scripts/release.sh
test: assets
	cd server && go test -race ./...
	cd web && npm test
check: test
	cd server && go vet ./...
