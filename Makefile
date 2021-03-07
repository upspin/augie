# Makefile to allow XCode to build go packages using its "external
# build system" feature. Use make instead of invoking go directly in
# order to provide the clean target as well.

.PHONY: build
build:
	go build -tags carchive -buildmode=c-archive -o lib/libwarden.a exp.upspin.io/cmd/upspin-warden

.PHONY: clean
clean:
	rm -f lib/*.h lib/*.a
