build-cli-mac:
    GOOS=darwin GOARCH=amd64 go build -o bin/mitmpac-cli-macos cli/main.go

build-cli-linux:
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/mitmpac-cli-linux cli/main.go
