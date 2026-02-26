.PHONY: test release-patch release-minor

default:
	$(error pick a target)

test:
	go tool staticcheck ./...
	go test -v ./...

release-patch:
	git tag $(shell go tool svu patch)
	git push --tags

release-minor:
	git tag $(shell go tool svu minor)
	git push --tags
