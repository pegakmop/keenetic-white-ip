BINARY    := white-ip
BUILD_DIR := bin
LDFLAGS   := -s -w
GOFILES   := $(wildcard *.go) go.mod

.PHONY: all mipsel mips aarch64 clean

all: mipsel mips aarch64

mipsel: $(BUILD_DIR)/$(BINARY)_mipsel-3.4

mips: $(BUILD_DIR)/$(BINARY)_mips-3.4

aarch64: $(BUILD_DIR)/$(BINARY)_aarch64-3.10

$(BUILD_DIR)/$(BINARY)_mipsel-3.4: $(GOFILES)
	mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build -ldflags="$(LDFLAGS)" -o $@ .

$(BUILD_DIR)/$(BINARY)_mips-3.4: $(GOFILES)
	mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=mips GOMIPS=softfloat go build -ldflags="$(LDFLAGS)" -o $@ .

$(BUILD_DIR)/$(BINARY)_aarch64-3.10: $(GOFILES)
	mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=arm64 go build -ldflags="$(LDFLAGS)" -o $@ .

clean:
	rm -rf $(BUILD_DIR)
