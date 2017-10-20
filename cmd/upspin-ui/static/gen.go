// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package static provides access to static assets, such as HTML, CSS,
// JavaScript, and image files.
package static // import "augie.upspin.io/cmd/upspin-ui/static"

import (
	"go/build"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"

	"upspin.io/errors"
)

//go:generate go run makestatic.go

var files map[string]string

var static struct {
	once sync.Once
	dir  string
}

// File returns the file rooted at "exp.upspin.io/cmd/browser/static" either
// from an in-memory map or, if no map was generated, the contents of the file
// from disk.
func File(name string) (string, error) {
	if files != nil {
		b, ok := files[name]
		if !ok {
			return "", errors.E(errors.NotExist, errors.Str("file not found"))
		}
		return b, nil

	}
	static.once.Do(func() {
		pkg, _ := build.Default.Import("exp.upspin.io/cmd/browser/static", "", build.FindOnly)
		if pkg == nil {
			return
		}
		static.dir = pkg.Dir
	})
	if static.dir == "" {
		return "", errors.E(errors.NotExist, errors.Str("could not find static assets"))
	}
	b, err := ioutil.ReadFile(filepath.Join(static.dir, name))
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.E(errors.NotExist, err)
		}
		return "", err
	}
	return string(b), nil
}
