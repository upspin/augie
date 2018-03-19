#!/bin/bash -e

# vendor-update.sh updates the vendored copy of the upspin.io repository and
# stages the result, ready for "git commit".

# The dep command can be obtained with "go get github.com/golang/dep/cmd/dep".

dep ensure -update upspin.io
git add vendor Gopkg.lock
git gofmt
