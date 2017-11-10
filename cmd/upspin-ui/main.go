// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main // import "augie.upspin.io/cmd/upspin-ui"

// TODO(adg): Flesh out the inspector (show blocks, etc).
// TODO(adg): Update the URL in the browser window to reflect the UI.
// TODO(adg): Facility to add/edit Access files in UI.
// TODO(adg): Awareness of Access files during copy and remove.
// TODO(adg): Show progress of removes/copies in the user interface.
// TODO(adg): Display links and handle their navigation properly.

import (
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/xsrftoken"

	"augie.upspin.io/cmd/upspin-ui/static"

	"upspin.io/errors"
	"upspin.io/flags"
	"upspin.io/upspin"
	"upspin.io/version"

	_ "upspin.io/transports"
)

const defaultPath = "augie@upspin.io/" // Show this tree on startup.

func main() {
	httpAddr := flag.String("http", "localhost:8000", "HTTP listen `address` (must be loopback)")
	versionFlag := flag.Bool("version", false, "print version string and exit")
	flags.Parse(flags.Client)

	if *versionFlag {
		fmt.Print(version.Version())
		return
	}

	// Disallow listening on non-loopback addresses until we have a better
	// security model. (Even this is not really secure enough.)
	if err := isLocal(*httpAddr); err != nil {
		exit(err)
	}

	s, err := newServer()
	if err != nil {
		exit(err)
	}
	http.Handle("/", s)

	l, err := net.Listen("tcp", *httpAddr)
	if err != nil {
		exit(err)
	}
	url := fmt.Sprintf("http://%s/#key=%s", *httpAddr, s.key)
	if !startBrowser(url) {
		fmt.Printf("Open %s in your web browser.\n", url)
	} else {
		fmt.Printf("Serving at %s\n", url)
	}
	exit(http.Serve(l, nil))
}

func exit(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

// server implements an http.Handler that performs various Upspin operations
// using a config. It is the back end for the JavaScript Upspin browser.
type server struct {
	// key to prevent request forgery; static for server's lifetime.
	key string

	mu  sync.Mutex
	cfg upspin.Config // Non-nil if signup flow has been completed.
	cli upspin.Client
}

func newServer() (*server, error) {
	key, err := generateKey()
	if err != nil {
		return nil, err
	}

	return &server{
		key: key,
	}, nil
}

func (s *server) hasConfig() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg != nil && s.cli != nil
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if p == "/_upspin" {
		s.serveAPI(w, r)
		return
	}
	if strings.Contains(p, "@") {
		s.serveContent(w, r)
		return
	}
	s.serveStatic(w, r)
}

func (s *server) serveStatic(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path[1:]
	if p == "" {
		p = "index.html"
	}
	b, err := static.File(p)
	if errors.Match(errors.E(errors.NotExist), err) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, path.Base(p), time.Now(), strings.NewReader(b))
}

func (s *server) serveContent(w http.ResponseWriter, r *http.Request) {
	if !s.hasConfig() {
		http.Error(w, "No configuration", http.StatusServiceUnavailable)
		return
	}

	p := r.URL.Path[1:]
	if !xsrftoken.Valid(r.FormValue("token"), s.key, string(s.cfg.UserName()), p) {
		http.Error(w, "Invalid XSRF token", http.StatusForbidden)
		return
	}

	name := upspin.PathName(p)
	de, err := s.cli.Lookup(name, true)
	if err != nil {
		httpError(w, err)
		return
	}
	f, err := s.cli.Open(name)
	if err != nil {
		httpError(w, err)
		return
	}
	http.ServeContent(w, r, path.Base(p), de.Time.Go(), f)
	f.Close()
}

func (s *server) serveAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Expected POST request", http.StatusMethodNotAllowed)
		return
	}

	// Require a valid key.
	if r.FormValue("key") != s.key {
		http.Error(w, "Invalid key", http.StatusForbidden)
		return
	}

	method := r.FormValue("method")

	// Don't permit accesses of non-startup methods if there is no config
	// nor client; those methods need them.
	if method != "startup" && !s.hasConfig() {
		http.Error(w, "No configuration", http.StatusServiceUnavailable)
		return
	}

	var resp interface{}
	switch method {
	case "startup":
		sResp, cfg, err := s.startup(r)
		var errString string
		if err != nil {
			errString = err.Error()
		}
		var (
			user        upspin.UserName
			left, right upspin.PathName
		)
		if cfg != nil {
			user = cfg.UserName()
			right = defaultPath
			// If the user has a directory endpoint then open to
			// their tree. Otherwise, open both panels to augie.
			if cfg.DirEndpoint().Transport == upspin.Remote {
				left = upspin.PathName(user + "/")
			} else {
				left = right
			}
		}
		resp = struct {
			Startup   *startupResponse
			UserName  upspin.UserName
			LeftPath  upspin.PathName
			RightPath upspin.PathName
			Error     string
		}{sResp, user, left, right, errString}
	case "list":
		path := upspin.PathName(r.FormValue("path"))
		des, err := s.cli.Glob(upspin.AllFilesGlob(path))
		var errString string
		if err != nil {
			errString = err.Error()
		}
		var entries []entryWithToken
		for _, de := range des {
			tok := xsrftoken.Generate(s.key, string(s.cfg.UserName()), string(de.Name))
			entries = append(entries, entryWithToken{
				DirEntry:  de,
				FileToken: tok,
			})
		}
		resp = struct {
			Entries []entryWithToken
			Error   string
		}{entries, errString}
	case "mkdir":
		_, err := s.cli.MakeDirectory(upspin.PathName(r.FormValue("path")))
		var errString string
		if err != nil {
			errString = err.Error()
		}
		resp = struct {
			Error string
		}{errString}
	case "rm":
		var errString string
		for _, p := range r.Form["paths[]"] {
			if err := s.rm(upspin.PathName(p)); err != nil {
				errString = err.Error()
				break
			}
		}
		resp = struct {
			Error string
		}{errString}
	case "copy":
		dst := upspin.PathName(r.FormValue("dest"))
		var paths []upspin.PathName
		for _, p := range r.Form["paths[]"] {
			paths = append(paths, upspin.PathName(p))
		}
		var errString string
		if err := s.copy(dst, paths); err != nil {
			errString = err.Error()
		}
		resp = struct {
			Error string
		}{errString}
	case "put":
		const maxMultipartSize = 500e6
		if err := r.ParseMultipartForm(maxMultipartSize); err != nil {
			http.Error(w, "Parse error: "+err.Error(), http.StatusBadRequest)
			return
		}
		if len(r.MultipartForm.File) == 0 {
			http.Error(w, "missing file", http.StatusBadRequest)
			return
		}
		var errString string
		for _, fhs := range r.MultipartForm.File {
			if len(fhs) == 0 {
				http.Error(w, "missing file handle", http.StatusBadRequest)
				return
			}
			err := s.put(upspin.PathName(r.FormValue("dir")), fhs[0])
			if err != nil {
				errString = err.Error()
				break
			}
		}
		resp = struct {
			Error string
		}{errString}
	}
	b, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write(b)
}

type entryWithToken struct {
	*upspin.DirEntry
	FileToken string
}

func generateKey() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

// isLocal returns an error if the given address is not a loopback address.
func isLocal(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return err
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return fmt.Errorf("cannot listen on non-loopback address %q", addr)
		}
	}
	return nil
}

// ifError checks if the error is the expected one, and if so writes back an
// HTTP error of the corresponding code.
func ifError(w http.ResponseWriter, got error, want errors.Kind, code int) bool {
	if !errors.Match(errors.E(want), got) {
		return false
	}
	http.Error(w, http.StatusText(code), code)
	return true
}

func httpError(w http.ResponseWriter, err error) {
	// This construction sets the HTTP error to the first type that matches.
	switch {
	case ifError(w, err, errors.Private, http.StatusForbidden):
	case ifError(w, err, errors.Permission, http.StatusForbidden):
	case ifError(w, err, errors.NotExist, http.StatusNotFound):
	case ifError(w, err, errors.BrokenLink, http.StatusNotFound):
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// startBrowser tries to open the URL in a web browser,
// and reports whether it succeed.
func startBrowser(url string) bool {
	var args []string
	switch runtime.GOOS {
	case "darwin":
		args = []string{"open"}
	case "windows":
		args = []string{"cmd", "/c", "start"}
	default:
		args = []string{"xdg-open"}
	}
	cmd := exec.Command(args[0], append(args[1:], url)...)
	return cmd.Start() == nil
}
