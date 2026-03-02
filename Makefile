.DEFAULT_GOAL := test
.PHONY: test release-patch release-minor tag-patch tag-minor

test:
	go tool staticcheck ./...
	go test -v ./...

release-patch:
	tag=$$(go tool svu patch); \
	git tag "$$tag"; \
	git push origin "$$tag"

release-minor:
	tag=$$(go tool svu minor); \
	git tag "$$tag"; \
	git push origin "$$tag"

tag-patch: release-patch

tag-minor: release-minor
