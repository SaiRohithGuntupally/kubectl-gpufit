BINARY := kubectl-gpufit
INSTALL_DIR ?= $(HOME)/.local/bin

.PHONY: build test vet install clean release

build:
	go build -o $(BINARY) .

test:
	go test ./...

vet:
	go vet ./...

install: build
	mkdir -p $(INSTALL_DIR)
	cp $(BINARY) $(INSTALL_DIR)/$(BINARY)
	@echo "Installed to $(INSTALL_DIR)/$(BINARY)"
	@echo "Ensure $(INSTALL_DIR) is on your PATH, then: kubectl gpufit --help"

# Cross-compile release tarballs + checksums into dist/.
PLATFORMS := darwin/arm64 darwin/amd64 linux/amd64 linux/arm64
release:
	rm -rf dist && mkdir -p dist
	@for p in $(PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; stage="dist/stage_$${os}_$${arch}"; \
	  mkdir -p "$$stage"; \
	  echo "building $$os/$$arch"; \
	  CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "-s -w" -o "$$stage/$(BINARY)" . ; \
	  cp LICENSE README.md "$$stage/"; \
	  tar -C "$$stage" -czf "dist/kubectl-gpufit_$${os}_$${arch}.tar.gz" $(BINARY) LICENSE README.md; \
	  rm -rf "$$stage"; [ -n "$$CLEAN_CACHE" ] && go clean -cache || true; \
	done
	cd dist && shasum -a 256 *.tar.gz > checksums.txt && cat checksums.txt

clean:
	rm -rf $(BINARY) dist
