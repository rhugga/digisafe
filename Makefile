.DEFAULT_GOAL := help

BIN    := bin/digisafe
PREFIX ?= $(HOME)/bin

# version.sh holds the version (make bump-version changes it, and its copies); releases are
# tagged v<version>.
VERSION   := $(shell sh version.sh)
TAG       := v$(VERSION)
DIST      := dist
PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64
LDFLAGS   := -s -w
# releases are cut from this branch, which must be pushed
RELEASE_BRANCH ?= main
# release notes: NOTES=file, else this version's section of RELEASE_NOTES.md, else GitHub's
# notes generated from the commits since the last release
NOTES ?=
# where the releases go (gh must be logged in to it, not just to some host)
GH_HOST ?= github.com
SHA256    := $(shell command -v sha256sum >/dev/null 2>&1 && echo sha256sum || echo shasum -a 256)

##@ General

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Build

.PHONY: all
all: check build cross ## Everything before committing: check, then build bin/digisafe and every platform

.PHONY: build
build: ## Build bin/digisafe
	go build -trimpath -o $(BIN) .

.PHONY: cross
cross: ## Build every platform's binary into bin/ (digisafe-<os>-<arch>)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "  $$os/$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' \
			-o bin/digisafe-$$os-$$arch$$ext . || exit 1; \
	done

.PHONY: install
install: build ## Install to ~/bin (override with PREFIX=/some/dir)
	install -d $(PREFIX)
	install -m 0755 $(BIN) $(PREFIX)/digisafe

.PHONY: clean
clean: ## Remove build output
	rm -rf bin $(DIST) $(COVERAGE)

##@ Release

.PHONY: version
version: ## Print the version (from version.sh) and the tag a release would get
	@echo "$(VERSION) ($(TAG))"

# bump-version: the new version from TO (patch on a pre-release releases it: 1.0.0-rc.1 becomes
# 1.0.0), then replace() puts it where the version is copied: version.sh, the Version constant,
# and the release file names in the docs. Only those exact strings change (go.mod and go.sum
# have unrelated "v0.1.0"s), and RELEASE_NOTES.md keeps its old versions: its "## Unreleased"
# section becomes the new version's, under a fresh empty one.
TO ?= patch
SEMVER := ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$$
.PHONY: bump-version
bump-version: ## Set a new version everywhere: TO=patch (default), minor, major or X.Y.Z
	@set -eu; old='$(VERSION)'; \
	echo "$$old" | grep -Eq '$(SEMVER)' || { echo "version.sh: '$$old' isn't a version (X.Y.Z)" >&2; exit 1; }; \
	core=$${old%%-*}; major=$${core%%.*}; rest=$${core#*.}; minor=$${rest%%.*}; patch=$${rest#*.}; \
	case '$(TO)' in \
		major) new="$$((major + 1)).0.0" ;; \
		minor) new="$$major.$$((minor + 1)).0" ;; \
		patch) if [ "$$core" != "$$old" ]; then new=$$core; else new="$$major.$$minor.$$((patch + 1))"; fi ;; \
		*) new='$(TO)'; new=$${new#v} ;; \
	esac; \
	echo "$$new" | grep -Eq '$(SEMVER)' || { echo "'$$new' isn't a version: want TO=patch, minor, major, X.Y.Z or X.Y.Z-pre" >&2; exit 1; }; \
	[ "$$new" != "$$old" ] || { echo "the version is already $$old" >&2; exit 1; }; \
	replace() { \
		grep -qF -- "$$2" "$$1" || { echo "$$1: '$$2' not found" >&2; exit 1; }; \
		tmp=$$(mktemp); \
		awk -v from="$$2" -v to="$$3" '{ out = ""; while ((i = index($$0, from)) > 0) { out = out substr($$0, 1, i - 1) to; $$0 = substr($$0, i + length(from)) } print out $$0 }' "$$1" > "$$tmp"; \
		cat "$$tmp" > "$$1"; rm -f "$$tmp"; \
	}; \
	replace version.sh "VERSION=$$old" "VERSION=$$new"; \
	replace internal/app/app.go "const Version = \"$$old\"" "const Version = \"$$new\""; \
	for doc in *.md; do \
		[ "$$doc" != RELEASE_NOTES.md ] || continue; \
		! grep -qF -- "digisafe-$$old-" "$$doc" || replace "$$doc" "digisafe-$$old-" "digisafe-$$new-"; \
	done; \
	if grep -qx '## Unreleased' RELEASE_NOTES.md; then \
		tmp=$$(mktemp); \
		awk -v h="## $$new ($$(date +%Y-%m-%d))" '$$0 == "## Unreleased" && !done { print; print ""; print h; done = 1; next } { print }' \
			RELEASE_NOTES.md > "$$tmp"; \
		cat "$$tmp" > RELEASE_NOTES.md; rm -f "$$tmp"; \
	else echo "note: RELEASE_NOTES.md has no '## Unreleased' section to make $$new's" >&2; fi; \
	left=$$(grep -nF -e "VERSION=$$old" -e "const Version = \"$$old\"" version.sh internal/app/app.go || true); \
	[ -z "$$left" ] || { echo "still $$old:" >&2; echo "$$left" >&2; exit 1; }; \
	echo "version $$old -> $$new"; git --no-pager diff --stat 2>/dev/null || true; \
	echo "next: write $$new's notes in RELEASE_NOTES.md, make check, commit, merge to main, then make release"

.PHONY: dist
dist: check ## Build the release archives and SHA256SUMS into dist/ (runs check first)
	@test -n "$(VERSION)" || { echo "can't read the version from version.sh"; exit 1; }
	rm -rf $(DIST) && mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		name=digisafe-$(VERSION)-$$os-$$arch; dir=$(DIST)/$$name; \
		echo "  $$name"; mkdir -p $$dir; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' \
			-o $$dir/digisafe$$ext . || exit 1; \
		cp README.md LICENSE $$dir/; \
		if [ $$os = windows ]; then (cd $(DIST) && zip -qrX $$name.zip $$name) || exit 1; \
		else COPYFILE_DISABLE=1 tar -C $(DIST) -czf $(DIST)/$$name.tar.gz $$name || exit 1; fi; \
		rm -rf $$dir; \
	done
	cd $(DIST) && $(SHA256) digisafe-* > SHA256SUMS
	@echo "dist/ ready for $(TAG):"; ls -1 $(DIST)

.PHONY: release-check
release-check: ## Check a release could be cut: gh logged in, clean tree, branch pushed, tag unused
	@command -v gh >/dev/null || { echo "gh (the GitHub CLI) isn't installed"; exit 1; }
	@gh auth status -h $(GH_HOST) >/dev/null 2>&1 || { echo "gh isn't logged in to $(GH_HOST): gh auth login -h $(GH_HOST)"; exit 1; }
	@test -z "$$(git status --porcelain)" || { echo "the working tree has changes: commit them first"; git status --short; exit 1; }
	@test "$$(git rev-parse --abbrev-ref HEAD)" = "$(RELEASE_BRANCH)" || \
		{ echo "releases are cut from $(RELEASE_BRANCH); this is $$(git rev-parse --abbrev-ref HEAD)"; exit 1; }
	@git fetch -q origin $(RELEASE_BRANCH)
	@test "$$(git rev-parse HEAD)" = "$$(git rev-parse origin/$(RELEASE_BRANCH))" || \
		{ echo "$(RELEASE_BRANCH) isn't the same as origin/$(RELEASE_BRANCH): push (or pull) first"; exit 1; }
	@test -z "$$(git ls-remote --tags origin refs/tags/$(TAG))" || \
		{ echo "tag $(TAG) already exists on origin: make bump-version"; exit 1; }
	@# a local tag is fine only at HEAD: a release that stopped after tagging, picked up again
	@if git rev-parse -q --verify "refs/tags/$(TAG)" >/dev/null; then \
		test "$$(git rev-parse 'refs/tags/$(TAG)^{commit}')" = "$$(git rev-parse HEAD)" || \
			{ echo "tag $(TAG) exists here, on another commit, and isn't pushed: if that release never went out,"; \
			  echo "delete it (git tag -d $(TAG)) and run this again; else make bump-version"; exit 1; }; \
		echo "tag $(TAG) exists here at HEAD but isn't pushed: carrying on with it"; \
	fi
	@! git show-ref -q --verify "refs/heads/$(TAG)" || \
		echo "note: a branch is also called $(TAG); the tag is pushed as refs/tags/$(TAG)"
	@test -z "$(NOTES)" || test -f "$(NOTES)" || { echo "NOTES=$(NOTES): no such file"; exit 1; }
	@test -n "$(NOTES)" || test -n "$$($(MAKE) --no-print-directory -s release-notes)" || \
		echo "note: RELEASE_NOTES.md has no $(VERSION) section; the release will get GitHub's generated notes"
	@echo "ready to release $(TAG) from $$(git rev-parse --short HEAD)"

.PHONY: release
release: release-check dist ## Tag v<version>, push the tag, and publish a GitHub release with dist/
	git rev-parse -q --verify "refs/tags/$(TAG)" >/dev/null || git tag -a $(TAG) -m "digisafe $(VERSION)"
	git push origin "refs/tags/$(TAG)"
	@$(MAKE) --no-print-directory release-publish

.PHONY: release-publish
release-publish: ## Publish the GitHub release for v<version> from dist/ (the tag must be pushed already)
	@gh auth status -h $(GH_HOST) >/dev/null 2>&1 || { echo "gh isn't logged in to $(GH_HOST): gh auth login -h $(GH_HOST)"; exit 1; }
	@test -n "$$(git ls-remote --tags origin refs/tags/$(TAG))" || { echo "tag $(TAG) isn't on origin: make release"; exit 1; }
	@test "$$(git rev-parse 'refs/tags/$(TAG)^{commit}')" = "$$(git rev-parse HEAD)" || \
		{ echo "HEAD isn't $(TAG): check out the tag's commit, so dist/ is built from it"; exit 1; }
	@! gh release view $(TAG) >/dev/null 2>&1 || { echo "the $(TAG) release already exists"; exit 1; }
	@test -f $(DIST)/SHA256SUMS || $(MAKE) --no-print-directory dist
	@test -z "$(NOTES)" || test -f "$(NOTES)" || { echo "NOTES=$(NOTES): no such file"; exit 1; }
	@notes='$(NOTES)'; \
	if [ -z "$$notes" ]; then \
		notes=$$(mktemp); $(MAKE) --no-print-directory -s release-notes > "$$notes"; \
		[ -s "$$notes" ] || { rm -f "$$notes"; notes=; }; \
	fi; \
	if [ -n "$$notes" ]; then how="--notes-file $$notes"; else how=--generate-notes; \
		echo "RELEASE_NOTES.md has no $(VERSION) section: using GitHub's generated notes"; fi; \
	echo "gh release create $(TAG) $(DIST)/* --verify-tag --title \"digisafe $(VERSION)\" $$how"; \
	gh release create $(TAG) $(DIST)/* --verify-tag --title "digisafe $(VERSION)" $$how
	@echo "released: $$(gh release view $(TAG) --json url -q .url)"

.PHONY: release-notes
release-notes: ## Print this version's section of RELEASE_NOTES.md (what make release publishes)
	@awk -v v='$(VERSION)' '/^## / { p = ($$2 == v); next } p' RELEASE_NOTES.md | \
		awk 'NF { started = 1 } started' # without the blank lines at the top

##@ Check

.PHONY: test
test: ## Run the tests
	go test ./...

.PHONY: race
race: ## Run the tests with the race detector
	go test -race -count=1 ./...

COVERAGE := coverage.out

.PHONY: coverage
coverage: ## Test coverage across all packages: functions under 80% and the total (HTML=1: a browser report)
	go test -count=1 -coverpkg=./... -coverprofile=$(COVERAGE) ./...
	@go tool cover -func=$(COVERAGE) | awk '$$NF+0 < 80 { print }' | grep -v '^total:' | sed 's|github.com/rhugga/digisafe/||' || true
	@go tool cover -func=$(COVERAGE) | tail -1
	@if [ -n "$(HTML)" ]; then go tool cover -html=$(COVERAGE); fi

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Format the code
	gofmt -w .

.PHONY: check
check: ## Fail on unformatted code, vet problems or failing tests (run before committing)
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "not gofmt'd:"; echo "$$out"; exit 1; fi
	go vet ./...
	GOOS=windows go vet ./...
	GOOS=linux go vet ./...
	go test -race -count=1 ./...

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	go mod tidy
