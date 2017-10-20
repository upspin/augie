// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "upspin.io/upspin"

// rm recursively removes the given path.
func (s *server) rm(name upspin.PathName) error {
	de, err := s.cli.Lookup(name, false)
	if err != nil {
		return err
	}
	return s.rmEntry(de)
}

// rmEntry removes the given entry. If the entry is a directory it removes its
// contents before removing the directory itself.
func (s *server) rmEntry(de *upspin.DirEntry) error {
	if de.IsDir() {
		dir, err := s.cli.DirServer(de.Name)
		if err != nil {
			return err
		}
		des, err := dir.Glob(upspin.AllFilesGlob(de.Name))
		if err != nil {
			return err
		}
		for _, de := range des {
			if err := s.rmEntry(de); err != nil {
				return err
			}
		}
	}
	return s.cli.Delete(de.Name)
}
