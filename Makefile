.PHONY: test build packages clean
TAGS = desktop,production,webkit2_41
test:
	cd gui && go vet ./... && go test ./...
build:
	cd gui && CGO_ENABLED=1 go build -buildmode=pie -tags $(TAGS) -trimpath -ldflags "-s -w" -o fstab-manager .
packages:
	packaging/build.sh
clean:
	rm -rf dist gui/fstab-manager
