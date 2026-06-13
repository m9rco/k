# Game Asset Studio — build targets.
#
# The frontend is a Vite + React app under web/ whose production build is emitted
# into web/static, which the Go binary embeds (web/web.go). So a full build is two
# steps: build the frontend, then build the Go binary.

.PHONY: web server build run test clean

# Build the React frontend into web/static (embedded by the Go binary).
web:
	cd web && npm install && npm run build

# Build the single binary (assumes web/static is already built).
server:
	go build -o bin/server ./cmd/server

# Full build: frontend then binary.
build: web server

# Run the server (build frontend + binary first).
run: build
	./bin/server

test:
	go test ./...

clean:
	rm -rf bin web/static/assets web/static/index.html
