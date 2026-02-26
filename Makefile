.PHONY: test tag-patch tag-minor

default:
	$(error pick a target)

test:
	go tool staticcheck ./...
	go test -v ./...

tag-patch:
	git tag $(shell go tool svu patch)

tag-minor:
	git tag $(shell go tool svu minor)
