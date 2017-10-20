// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO(adg): How to handle partially copied trees?
// TODO(adg): Do permissions checking up front?

package main

import (
	"upspin.io/errors"
	"upspin.io/path"
	"upspin.io/upspin"
)

// copy recursively copies the specified source paths to the given destination.
// It uses Client.PutDuplicate to copy files, so file content is not copied;
// the underlying DirBlocks do not change.
func (s *server) copy(dst upspin.PathName, srcs []upspin.PathName) error {
	// Check that the destination exists and is a directory.
	dstEntry, err := s.cli.Lookup(dst, true)
	if err != nil {
		return err
	}
	if !dstEntry.IsDir() {
		return errors.E(dst, errors.NotDir)
	}

	// Iterate through sources and copy them recursively.
	for _, src := range srcs {
		// Lookup src, but don't follow links.
		// We will make a copy of those links, not traverse them.
		srcEntry, err := s.cli.Lookup(src, false)
		if err != nil {
			return err
		}
		if err := s.copyEntry(dst, srcEntry); err != nil {
			return err
		}
	}
	return nil
}

// copyEntry copies the given entry to the given destination directory.
// If the entry is a directory then the directory is copied recursively.
// If the entry is a link then an equivalent link is created in dstDir.
// This function assuems that dstDir exists and is a directory.
func (s *server) copyEntry(dstDir upspin.PathName, srcEntry *upspin.DirEntry) error {
	srcPath, err := path.Parse(srcEntry.Name)
	if err != nil {
		return err
	}
	if srcPath.NElem() == 0 {
		// The browser user interface doesn't allow you to select a
		// root for a copy, so this shouldn't come up in practice.
		return errors.E(srcEntry.Name, errors.Str("cannot copy a root"))
	}
	dstDirPath, _ := path.Parse(dstDir)
	if dstDirPath.HasPrefix(srcPath) {
		return errors.E(srcEntry.Name, errors.Str("cannot copy a directory into one of its sub-directories"))
	}

	dst := path.Join(dstDir, srcPath.Elem(srcPath.NElem()-1))

	switch {
	case srcEntry.IsDir():
		// Recur into directories.
		if _, err := s.cli.MakeDirectory(dst); err != nil {
			return err
		}
		dir, err := s.cli.DirServer(srcEntry.Name)
		if err != nil {
			return err
		}
		des, err := dir.Glob(upspin.AllFilesGlob(srcEntry.Name))
		if err != nil && err != upspin.ErrFollowLink {
			return err
		}
		for _, de := range des {
			if err := s.copyEntry(dst, de); err != nil {
				return err
			}
		}
	case srcEntry.IsLink():
		if _, err := s.cli.PutLink(srcEntry.Link, dst); err != nil {
			return err
		}
	default:
		if _, err := s.cli.PutDuplicate(srcEntry.Name, dst); err != nil {
			return err
		}
	}
	return nil
}
