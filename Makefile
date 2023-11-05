#!/usr/bin/env gmake -f

BUILDOPTS=-ldflags="-s -w" -a -gcflags=all=-l -trimpath
FILELIST=collection.go commands.go event_parser.go globals.go lib.go main.go read_config.go redis.go types.go \
  settings-db-util.go

BINARY=aleesa-jabber-go


all: clean build


build:
ifeq ($(OS),Windows_NT)
# powershell
ifeq ($(SHELL),sh.exe)
	SET CGO_ENABLED=0
	go build ${BUILDOPTS} -o ${BINARY} ${FILELIST}
else
# jetbrains golang
	CGO_ENABLED=0
	go build ${BUILDOPTS} -o ${BINARY} ${FILELIST}
endif
# bash/git bash
else
	CGO_ENABLED=0 go build ${BUILDOPTS} -o ${BINARY} ${FILELIST}
endif


clean:
	go clean


upgrade:
ifeq ($(OS),Windows_NT)
# jetbrains golang, powershell
ifeq ($(SHELL),sh.exe)
	if exist vendor del /F /S /Q vendor >nul
# git bash case
else
	$(RM) -r vendor
endif
else
	$(RM) -r vendor
endif
	go get -d -u -t ./...
	go mod tidy
	go mod vendor

# vim: set ft=make noet ai ts=4 sw=4 sts=4:
