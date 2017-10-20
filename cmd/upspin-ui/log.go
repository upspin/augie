// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"log"
	"os"
	"path/filepath"
	"sync"

	"upspin.io/config"
	upLog "upspin.io/log"
	"upspin.io/shutdown"
)

// logf logs a formatted log message to $HOME/upspin/log/browser.log,
// or to standard error if that file cannot be opened.
func logf(format string, args ...interface{}) {
	logger.Lock()
	defer logger.Unlock()
	logger.Printf(format, args...)
}

// logger is the log.Logger used by logf.
var logger struct {
	sync.Mutex
	*log.Logger
}

func init() {
	l, err := newLogger()
	if err != nil {
		// Fall back to standard error if we can't log to a file.
		l = log.New(os.Stderr, "browser: ", log.LstdFlags)
		l.Print(err)
	}
	logger.Logger = l
}

// newLogger initializes a log.Logger that writes to
// $HOME/upspin/log/browser.log and redirects the Upspin logger and the
// standard logger to that file.
func newLogger() (*log.Logger, error) {
	home, err := config.Homedir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, "upspin", "log")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	file := filepath.Join(dir, "browser.log")
	const flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	f, err := os.OpenFile(file, flags, 0600)
	if err != nil {
		return nil, err
	}
	shutdown.Handle(func() {
		f.Close()
	})
	upLog.SetOutput(f)
	log.SetOutput(f)
	return log.New(f, "", log.LstdFlags), nil
}
