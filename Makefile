.PHONY: build test run generate clean

BINARY=bin/executor

generate:
	@npx -y esbuild ./web/src/index.jsx --bundle --external:react --external:react-dom --external:react-dom/client --loader:.jsx=jsx --jsx-factory=React.createElement --jsx-fragment=React.Fragment --target=es2017 --outfile=./internal/frontend/static/js/app.bundle.js

build: generate
	@mkdir -p bin
	go build -o $(BINARY) ./cmd/executor

test: generate
	go test ./... -v -race

run: build
	./$(BINARY) --listen-addr=:8080 --sandbox-dir=./scratch --auth-stub

clean:
	rm -rf bin scratch data internal/frontend/static/js/app.bundle.js
