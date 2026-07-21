# Build the Signal-bridge + worker binaries the systemd units and (disarmed)
# orders reference. `make install` deploys the Phase-4 systemd --user units.
BIN := bin
CMDS := temporal-signal maintenance-reconcile

.PHONY: build test clean install
build:
	@mkdir -p $(BIN)
	@for c in $(CMDS); do \
		echo "building $$c"; \
		go build -o $(BIN)/$$c ./cmd/$$c || exit 1; \
	done
	@echo "building maintenance-worker"
	@go build -o $(BIN)/maintenance-worker ./worker

test:
	go test -race ./...

install: build
	@bash deploy/install.sh

clean:
	rm -rf $(BIN)
