.PHONY: build baseline-bin kind-up kind-down collect-baseline load-prometheus test vet smoke demo clean cover

COVERPKG := $(shell go list ./... | grep -vE '/(cmd/baseline|hack/loadgen)$$' | paste -sd,)

BIN          := bin/pgoctl
BASELINE_BIN := bin/baseline
PROM_NS      := monitoring
PROM_SVC     := prometheus-kube-prometheus-prometheus

build:
	go build -o $(BIN) ./cmd/pgoctl

baseline-bin:
	go build -o $(BASELINE_BIN) ./cmd/baseline

kind-up:
	./scripts/kind-prometheus.sh
	kubectl port-forward -n $(PROM_NS) svc/$(PROM_SVC) 9090:9090 &
	@echo "Prometheus available at http://localhost:9090"

kind-down:
	kind delete cluster --name pgoctl-dev

collect-baseline: baseline-bin
	mkdir -p testdata
	./$(BASELINE_BIN) \
		--url http://localhost:9090/debug/pprof/profile \
		--seconds 30
	@echo ""
	@ls -lh testdata/*.pprof 2>/dev/null || true

load-prometheus:
	@echo "Sending load to Prometheus query endpoint for 60s..."
	hey -z 60s -c 50 http://localhost:9090/api/v1/query?query=up

smoke: build
	PGOCTL=$(BIN) ./scripts/smoke.sh

demo: build
	PGOCTL=$(BIN) ./demo.sh

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -rf bin/

cover:
	go test -covermode=set -coverpkg=$(COVERPKG) \
	$(shell go list ./... | grep -vE '/(cmd/baseline|hack/loadgen)$$') \
	-coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -1
