.DEFAULT_GOAL := test
.PHONY: test release-patch release-minor tag-patch tag-minor gen-completion

gen-completion:
	claude --print "Regenerate the shell completion scripts in _comp/ (bash, zsh, fish) for the ghinst CLI. Read main.go and the existing scripts in _comp/ first to understand all flags, then rewrite the scripts to reflect the current flags."

test:
	go tool staticcheck ./...
	go tool govulncheck ./...
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
