binary = hey

# Note: Go names the 64-bit ARM architecture "arm64", not "aarch64".
# aarch64 is the GNU/uname name and is not a valid GOARCH value
# (go tool dist list has no aarch64 entry).
release:
	mkdir -p ./bin
	GOOS=windows GOARCH=amd64 go build -o ./bin/$(binary)_windows_amd64
	GOOS=windows GOARCH=arm64 go build -o ./bin/$(binary)_windows_arm64
	GOOS=linux   GOARCH=amd64 go build -o ./bin/$(binary)_linux_amd64
	GOOS=linux   GOARCH=arm64 go build -o ./bin/$(binary)_linux_arm64
	GOOS=darwin  GOARCH=amd64 go build -o ./bin/$(binary)_darwin_amd64
	GOOS=darwin  GOARCH=arm64 go build -o ./bin/$(binary)_darwin_arm64

upload:
	gsutil -m cp -r ./bin/* gs://hey-releases/

release-upload: release upload

.PHONY: release upload release-upload
