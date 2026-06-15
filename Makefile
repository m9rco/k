# Game Asset Studio — build targets.
#
# The frontend is a Vite + React app under web/ whose production build is emitted
# into web/static, which the Go binary embeds (web/web.go). So a full build is two
# steps: build the frontend, then build the Go binary.

BINARY = bin/game-asset-studio
TARGET_HOST = 9.135.12.71
TARGET_PATH = /data/home/user00/lab/k
TARGET = $(TARGET_HOST):$(TARGET_PATH)

.PHONY: web server build run kill-port test clean

# Port the server listens on; kill-port frees it before run.
PORT ?= 8080

# Build the React frontend into web/static (embedded by the Go binary).
web:
	cd web && npm install && npm run build

# Build the single binary (assumes web/static is already built).
server:
	go build -o bin/server ./cmd/server

# Full build: frontend then binary.
build: web server

# Free the listen port: kill whatever holds it (no-op when free).
kill-port:
	@pids=$$(lsof -ti tcp:$(PORT) -s tcp:LISTEN 2>/dev/null); \
	if [ -n "$$pids" ]; then \
		echo "killing process(es) on port $(PORT): $$pids"; \
		kill $$pids 2>/dev/null || true; \
		sleep 1; \
	else \
		echo "port $(PORT) is free"; \
	fi

# Run the server (build frontend + binary first, freeing the port if taken).
run: build kill-port
	./bin/server

deploy:
	GOOS=linux GOARCH=amd64 go build -o $(BINARY) ./cmd/server
	rsync -av $(BINARY) $(TARGET)
# 	ssh $(TARGET_HOST) "cd $(TARGET_PATH) && supervisorctl restart checkersvrd"

test:
	go test ./...

clean:
	rm -rf bin web/static/assets web/static/index.html
