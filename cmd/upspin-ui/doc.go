// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Command upspin-ui presents a web interface to the Upspin name space,
and also provides a facility to sign up an Upspin user and deploy
an upspinserver to Google Cloud Platform.
It operates as the user in the specified config.
If no config is available at the specified path,
the user is prompted to sign up an Upspin user.

Browser features

The Upspin browser presents two navigation panes.

Each browser pane lists the contents of an Upspin directory.
The directory is shown in a text box at the top of each pane.

You can navigate directly to a specific Upspin path by typing (or pasting) it
into the text box and pressing the enter key.
The button to the left of the text box navigates to the parent of the current
directory.

Clicking the name of an entry will attempt to download the entry with your web
browser or, if the entry is a directory, will navigate to that directory.

At startup, the left pane displays the current user's root and the right pane
displays the path augie@upspin.io.

The checkboxes beside each entry permit the (de-)selection of entries.
The checkbox at the top of each list of entries (de-)selects all entries in
that directory.

The "Delete" button recursively deletes the selected files and directories.

The "Copy" button recurisively copies the selected files and directories to
the directory displayed in the opposite pane.

The "Make directory" button creates a directory in the pane's current
directory.

The "Refresh" button reloads the contents of the directory and displays it.

The info buttons (a little "i" in a circle, to the right of each file) display
extended information for a given directory entry.

Files created by upspin-ui

The signup process creates a config file at the location provided by the
-config flag. The flag's default is $HOME/upspin/config.
Signup also generates key files and puts them in the directory
$HOME/.ssh/user, where user is the Upspin user name.

The upspinserver deplopment process records its state in a file with the same
name as the config file with the additional suffix ".gcpState".
The state file is used to resume the deployment process should the upspin-ui
program crash or be terminated by the user.
Once deployment is complete this file may be removed.
Deployment also generates key files which it puts in $HOME/.ssh/user,
where user is the Upspin user name of the server being deployed.

During the signup and deployment processes, upspin-ui logs debugging
information to $HOME/upspin/log/upspin-ui.log.
*/
package main
