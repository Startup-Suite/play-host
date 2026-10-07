# Build and verification

The supported binary is pure Go (`CGO_ENABLED=0`). Run the native checks, both target vets, and both release builds before shipping lifecycle changes:

```sh
gofmt -w ./cmd ./internal
go vet ./...
go test ./...
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go vet ./...
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -o /tmp/play-host-darwin-amd64 ./cmd/play-host
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go vet ./...
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go test -c -o /tmp/launch-windows.test.exe ./internal/launch
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -o /tmp/play-host-windows-amd64.exe ./cmd/play-host
```

`GOOS=windows go test ./...` cannot execute Windows binaries on Unix; compile the launch test binary above and run it on Windows to exercise the Job Object tests. The Darwin GitHub Actions workflow also runs native macOS vet/tests plus an explicit Darwin amd64 vet/build.

See [macos-setup.md](macos-setup.md) for packaging and LaunchAgent installation.
