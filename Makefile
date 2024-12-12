SOURCE_OS ?= $(shell uname -s | tr '[:upper:]' '[:lower:]')
SOURCE_ARCH ?= $(shell uname -m)
TARGET_OS ?= $(SOURCE_OS)
TARGET_ARCH ?= $(SOURCE_ARCH)
normalize_arch = $(if $(filter aarch64,$(1)),arm64,$(if $(filter x86_64,$(1)),amd64,$(1)))
SOURCE_ARCH := $(call normalize_arch,$(SOURCE_ARCH))
TARGET_ARCH := $(call normalize_arch,$(TARGET_ARCH))

BIN_OUTPUT_PATH = bin/$(TARGET_OS)-$(TARGET_ARCH)
TOOL_BIN = bin/gotools/$(shell uname -s)-$(shell uname -m)

FFMPEG_TAG ?= n6.1
FFMPEG_VERSION ?= $(shell pwd)/FFmpeg/$(FFMPEG_TAG)
FFMPEG_VERSION_PLATFORM ?= $(FFMPEG_VERSION)/$(TARGET_OS)-$(TARGET_ARCH)
FFMPEG_BUILD ?= $(FFMPEG_VERSION_PLATFORM)/build
FFMPEG_OPTS ?= --prefix=$(FFMPEG_BUILD) \
               --disable-shared \
               --disable-programs \
               --disable-doc \
               --disable-everything \
               --enable-static \
               --enable-libx264 \
               --enable-gpl \
               --enable-encoder=libx264 \
               --enable-muxer=segment \
               --enable-muxer=mp4 \
               --enable-demuxer=segment \
               --enable-demuxer=concat \
               --enable-demuxer=mov \
               --enable-demuxer=mp4 \
               --enable-parser=h264 \
               --enable-protocol=file \
               --enable-protocol=concat \
               --enable-protocol=crypto \
               --enable-bsf=h264_mp4toannexb

CGO_LDFLAGS := -L$(FFMPEG_BUILD)/lib -lavcodec -lavutil -lavformat -lswscale -lz
ifeq ($(SOURCE_OS),linux)
	CGO_LDFLAGS += -l:libjpeg.a -l:libx264.a
endif
ifeq ($(SOURCE_OS),darwin)
	CGO_LDFLAGS += $(HOMEBREW_PREFIX)/Cellar/x264/r3108/lib/libx264.a -liconv
endif

CGO_CFLAGS := -I$(FFMPEG_BUILD)/include
GOFLAGS := -buildvcs=false
export PKG_CONFIG_PATH=$(FFMPEG_BUILD)/lib/pkgconfig
export PATH := $(PATH):$(shell go env GOPATH)/bin

.PHONY: lint tool-install test clean module

$(BIN_OUTPUT_PATH)/video-store: *.go cam/*.go $(FFMPEG_BUILD)
	go mod tidy
	CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CFLAGS=$(CGO_CFLAGS) go build -o $(BIN_OUTPUT_PATH)/video-store main.go

$(FFMPEG_VERSION_PLATFORM):
	git clone https://github.com/FFmpeg/FFmpeg.git --depth 1 --branch $(FFMPEG_TAG) $(FFMPEG_VERSION_PLATFORM)

$(FFMPEG_BUILD): $(FFMPEG_VERSION_PLATFORM)
# Only need nasm to build assembly kernels for amd64 targets.
ifeq ($(SOURCE_OS),linux)
ifeq ($(shell dpkg -l | grep -w x264 > /dev/null; echo $$?), 1)
	sudo apt update && sudo apt install -y libx264-dev
endif
ifeq ($(SOURCE_ARCH),amd64)
	which nasm || (sudo apt update && sudo apt install -y nasm)
endif
endif
ifeq ($(SOURCE_OS),darwin)
ifeq ($(shell brew list | grep -w x264 > /dev/null; echo $$?), 1)
	brew update && brew install x264
endif
endif
	cd $(FFMPEG_VERSION_PLATFORM) && ./configure $(FFMPEG_OPTS) && $(MAKE) -j$(NPROC) && $(MAKE) install

tool-install:
	GOBIN=`pwd`/$(TOOL_BIN) go install \
		github.com/edaniels/golinters/cmd/combined \
		github.com/golangci/golangci-lint/cmd/golangci-lint \
		github.com/rhysd/actionlint/cmd/actionlint

lint: tool-install $(FFMPEG_BUILD)
	go mod tidy
	CGO_CFLAGS=$(CGO_CFLAGS) GOFLAGS=$(GOFLAGS) $(TOOL_BIN)/golangci-lint run -v --fix --config=./etc/.golangci.yaml --timeout=2m

test: $(BIN_OUTPUT_PATH)/video-store
ifeq ($(shell which ffmpeg > /dev/null 2>&1; echo $$?), 1)
ifeq ($(SOURCE_OS),linux)
	sudo apt update && sudo apt install -y ffmpeg
endif
ifeq ($(SOURCE_OS),darwin)
	brew update && brew install ffmpeg
endif
endif
ifeq ($(shell which artifact > /dev/null 2>&1; echo $$?), 1)
	go install go.viam.com/utils/artifact/cmd/artifact@latest
endif
	artifact pull
	cp $(BIN_OUTPUT_PATH)/video-store bin/video-store
	go test -v ./tests/
	rm bin/video-store

module: $(BIN_OUTPUT_PATH)/video-store
	cp $(BIN_OUTPUT_PATH)/video-store bin/video-store
	tar czf module.tar.gz bin/video-store
	rm bin/video-store

clean:
	rm -rf $(BIN_OUTPUT_PATH)
	rm -rf FFmpeg
	git clean -fxd
