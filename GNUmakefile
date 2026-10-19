default: build

BINARY_NAME=terraform-provider-rustfs
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

# Duration of each target's run in `make fuzz`; the weekly Fuzz workflow uses 60s.
FUZZTIME ?= 30s

build:
	go build -o bin/$(BINARY_NAME) -ldflags "-X main.version=$(VERSION)" .

test:
	go test -v -cover ./...

coverage:
	go test -count=1 -skip '^TestAcc' -covermode=atomic -coverprofile=coverage.out ./...
	@echo
	@./scripts/coverage-summary.sh coverage.out

coverage-html: coverage
	go tool cover -html=coverage.out

# Unit coverage (coverage.out) plus the acceptance profile written by
# acc_test/docker-compose.yml (coverage-acceptance.out) as one number.
coverage-merge:
	./scripts/merge-coverage.sh coverage-union.out coverage.out coverage-acceptance.out
	@echo
	@./scripts/coverage-summary.sh coverage-union.out

# Fail when coverage-union.out is below .github/coverage-baseline.txt. CI runs
# the same script on every pull request and on main.
coverage-gate:
	./scripts/coverage-gate.sh coverage-union.out

# Regenerate the coverage badge and block in README.md from the baseline;
# `make coverage-readme-check` is what CI runs to fail on drift.
coverage-readme:
	./scripts/coverage-readme.sh

coverage-readme-check:
	./scripts/coverage-readme.sh --check

# Short fuzz run of every target. `-fuzz` takes exactly one target, so each is
# listed explicitly; the weekly Fuzz workflow runs the same commands (60s each).
# `go test ./...` replays the committed seed corpus, so no target needs fuzzing
# to stay green — use this for real campaigns: `make fuzz FUZZTIME=10m`.
fuzz:
	go test -run '^$$' -fuzz '^FuzzPolicyDocument$$' -fuzztime $(FUZZTIME) ./pkg/rustfs
	go test -run '^$$' -fuzz '^FuzzAdminErrorBody$$' -fuzztime $(FUZZTIME) ./pkg/rustfs
	go test -run '^$$' -fuzz '^FuzzConfigKV$$' -fuzztime $(FUZZTIME) ./pkg/rustfs
	go test -run '^$$' -fuzz '^FuzzTargetConfig$$' -fuzztime $(FUZZTIME) ./provider

testacc:
	TF_ACC=1 go test -v -cover ./...

lint:
	golangci-lint run --config .github/golangci.yml ./...

lint-fix:
	golangci-lint run --config .github/golangci.yml --fix ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

install: build
	mkdir -p ~/.terraform.d/plugins/registry.terraform.io/caberdo/rustfs/$(VERSION)/$$(go env GOOS)_$$(go env GOARCH)
	cp bin/$(BINARY_NAME) ~/.terraform.d/plugins/registry.terraform.io/caberdo/rustfs/$(VERSION)/$$(go env GOOS)_$$(go env GOARCH)/$(BINARY_NAME)

testacc-docker:
	docker compose -f acc_test/docker-compose.yml --profile test run --rm test

testacc-live:
	podman-compose -f acc_test/docker-compose.yml up -d rustfs rustfs-peer
	RUSTFS_ENDPOINT=127.0.0.1:9001 RUSTFS_USER=rustfsadmin RUSTFS_SECRET=rustfsadmin \
	RUSTFS_NOTIFY_TEST_ENDPOINT=http://127.0.0.1:9001/health/ready \
	RUSTFS_PEER_ENDPOINT=rustfs-peer:9001 RUSTFS_PEER_ADMIN_ENDPOINT=127.0.0.1:9003 \
	RUSTFS_PEER_S3_ENDPOINT=127.0.0.1:9002 \
	RUSTFS_PEER_ACCESS_KEY=rustfsadmin RUSTFS_PEER_SECRET_KEY=rustfsadmin \
	TF_ACC=1 go test -v ./...

validate-examples:
	./scripts/validate-examples.sh

generate:
	go generate ./...

.PHONY: build test coverage coverage-html coverage-merge coverage-gate coverage-readme coverage-readme-check testacc lint lint-fix fmt vet install testacc-docker testacc-live validate-examples generate fuzz
