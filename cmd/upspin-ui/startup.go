// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO: cache gcp zone/location lists

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"upspin.io/bind"
	"upspin.io/client"
	"upspin.io/cmd/cacheserver/cacheutil"
	"upspin.io/config"
	"upspin.io/errors"
	"upspin.io/flags"
	"upspin.io/key/keygen"
	"upspin.io/key/usercache"
	"upspin.io/serverutil/signup"
	"upspin.io/upspin"
	"upspin.io/user"
	"upspin.io/valid"
)

var defaultKeyServer = string(config.New().KeyEndpoint().NetAddr)

var (
	keyServerAddr = flag.String("keyserver", defaultKeyServer, "keyserver `address` (for signup; overriden by config)")
	tlsCertDir    = flag.String("tlscerts", "", "TLS certificate `directory` (for signup; overridden by config)")
)

// noneEndpoint is a sentinel Endpoint value that should be passed to
// writeConfig when we wish to set the dirserver and/or storeserver
// specifically to 'unassigned', to distinguish from the zero value.
// The NetAddr "none" is not written to the config file.
var noneEndpoint = upspin.Endpoint{
	Transport: upspin.Unassigned,
	NetAddr:   "none",
}

// startupResponse is sent to the client in response to startup requests.
type startupResponse struct {
	// Step is the modal dialog that should be displayed to the user at
	// this stage of the startup process.
	Step string

	// Step: "secretSeed" and "serverSecretSeed"
	KeyDir     string `json:",omitempty"`
	SecretSeed string `json:",omitempty"`

	// Step: "verify"
	UserName upspin.UserName `json:",omitempty"`

	// Step: "gcpDetails"
	BucketName string   `json:",omitempty"`
	Zones      []string `json:",omitempty"`
	Locations  []string `json:",omitempty"`

	// Step: "serverUserName"
	UserNamePrefix string `json:",omitempty"` // Includes trailing "+".
	UserNameSuffix string `json:",omitempty"` // Suggested default.
	UserNameDomain string `json:",omitempty"` // Includes leading "@".

	// Step: "serverHostName" and "waitServerHostName"
	IPAddr string `json:",omitempty"`

	// Step: "waitServerHostName"
	HostName string `json:",omitempty"`

	// Step: "serverWriters"
	Writers []upspin.UserName `json:",omitempty"`
}

// startup populates s.cfg and s.cli by either loading the config file
// nominated by flags.Config, or by taking the user through the signup process.
//
// The signup process works by checking for various conditions, and instructing
// the JS/HTML front end to present various Steps to the user.
//  - The config file exists at flags.Config. If not:
//    - Prompt the user for a user name and server endpoints (Step: "signup").
//    - Write a new config and generate keys (action "signup").
//    - Register the user and keys with the key server (action "register").
//  - Check that the config's user exists on the Key Server. If not:
//    - Prompt the user to click the verification link in the email (Step: "verify").
//  - Check that the user has endpoints defined in the config file. If not:
//    - Prompt the user to choose dir/store endpoints, deploy to GCP, or none.
//      (Step: "serverSelect")
//    - If the user selects explicit dir/store endpoints, or none:
//      - Update the user's endpoints in the keyserver record.
//      - Rewrite the config file with the chosen endpoints.
//    - If the user selects GCP deployment, do all of this:
//      - Prompt user to create a GCP Project and service account ("serverGCP").
//      - Use the provided service account key to authenticate with GCP and
//        enable the relevant APIs.
//      - Prompt user for GCP details such as bucket name and region ("gcpDetails").
//      - Create the GCP Storage Bucket, Address, and Compute Instance.
//      - Prompt user for a user name for the server ("serverUserName").
//      - Register the server user name with the key server.
//      - Display server user proquint, ask user to write it down ("serverSecretSeed").
//      - Prompt user for a host name for the server ("serverHostName").
//      - If they elect for a default, create a host name through host@upspin.io.
//      - Check that the host name resolves to the server IP.
//      - Update the key server records for both the user and server user so that
//        the directory endpoint is the new server.
//      - Prompt the user to specify server Writers ("serverWriters").
//      - Configure the running upspinserver by sending a configuration request.
//
// Only one of startup's return values should be non-nil. If a user is to be
// presented with a given step, startup returns a non-nil startupResponse. If
// all the conditions are met, startup returns a non-nil Config. If an error
// occurs startup returns that error.
func (s *server) startup(req *http.Request) (resp *startupResponse, cfg upspin.Config, err error) {
	s.mu.Lock()
	cfg = s.cfg
	s.mu.Unlock()
	if cfg != nil {
		return nil, cfg, nil
	}

	if err := req.ParseForm(); err != nil {
		return nil, nil, err
	}
	logf("startup: request: %v", formatRequest(req.Form))
	defer func() {
		if err != nil {
			logf("startup: error: %v", err)
		} else if resp != nil {
			logf("startup: response: %v", formatResponse(resp))
		} else if cfg != nil {
			logf("startup: returned with config")
		}
	}()

	action := req.FormValue("action")

	var secretSeed, keyDir string
	if action == "signup" {
		// The user clicked the "Sign up" button on the signup dialog.
		userName := upspin.UserName(req.FormValue("username"))

		if err := valid.UserName(userName); err != nil {
			return nil, nil, err
		}
		_, suffix, _, err := user.Parse(userName)
		if err != nil {
			return nil, nil, err
		}
		if suffix != "" {
			return nil, nil, errors.Errorf("Your primary user name must not contain a + symbol.")
		}

		// Check whether userName already exists on the KeyServer.
		if ok, err := isRegistered(userName); err != nil {
			return nil, nil, err
		} else if ok {
			return nil, nil, errors.Errorf("%q is already registered.", userName)
		}

		// Write config file.
		err = writeConfig(flags.Config, userName, upspin.Endpoint{}, upspin.Endpoint{}, false)
		if err != nil {
			return nil, nil, err
		}

		// Generate keys.
		secretSeed, keyDir, err = genkey(userName)
		if err != nil {
			// Don't leave the config lying around.
			os.Remove(flags.Config)
			return nil, nil, err
		}

		// Move on to the "register" action,
		// to send the signup request to the key server.
		action = "register"
	}

	// Look for a config file.
	if !exists(flags.Config) {
		// Config doesn't exist; need to sign up.
		return &startupResponse{
			Step: "signup",
		}, nil, nil
	}

	// Load existing config file.
	cfg, err = config.FromFile(flags.Config)
	if err != nil {
		return nil, nil, err
	}

	// Check for and load GCP setup state file.
	st, err := gcpStateFromFile()
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}

	var response string
	switch action {
	case "register":
		if err := signup.MakeRequest(cfg); err != nil {
			if keyDir != "" {
				// We have just generated the keys, so we
				// should remove both the keys and the config,
				// since they are bad.
				os.RemoveAll(keyDir)
				os.Remove(flags.Config)
			}
			return nil, nil, err
		}
		next := "verify"
		if secretSeed != "" {
			// Show the secret seed if we have just generated the key.
			next = "secretSeed"
		}
		return &startupResponse{
			Step:       next,
			KeyDir:     keyDir,
			SecretSeed: secretSeed,
			UserName:   cfg.UserName(),
		}, nil, nil

	case "specifyEndpoints":
		dirHost := req.FormValue("dirServer")
		dirEndpoint, err := hostnameToEndpoint(dirHost)
		if err != nil {
			return nil, nil, errors.Errorf("invalid hostname %q: %v", dirHost, err)
		}
		cfg = config.SetDirEndpoint(cfg, dirEndpoint)
		storeHost := req.FormValue("storeServer")
		storeEndpoint, err := hostnameToEndpoint(storeHost)
		if err != nil {
			return nil, nil, errors.Errorf("invalid hostname %q: %v", storeHost, err)
		}
		cfg = config.SetStoreEndpoint(cfg, storeEndpoint)

		// Check that the StoreServer is up.
		store, err := bind.StoreServer(cfg, storeEndpoint)
		if err != nil {
			return nil, nil, errors.Errorf("could not find %q:\n%v", storeHost, err)
		}
		_, _, _, err = store.Get("Upspin:notexist")
		if err != nil && !errors.Match(errors.E(errors.NotExist), err) {
			return nil, nil, errors.Errorf("error communicating with %q:\n%v", storeHost, err)
		}

		// Check that the DirServer is up, and create the user root.
		if err := makeRoot(cfg); err != nil {
			return nil, nil, err
		}

		// Put the updated user record to the key server.
		if err := putUser(cfg, nil); err != nil {
			return nil, nil, errors.Errorf("error updating key server:\n%v", err)
		}

		// Write config file with updated endpoints.
		err = writeConfig(flags.Config, cfg.UserName(), dirEndpoint, storeEndpoint, true)
		if err != nil {
			return nil, nil, err
		}

	case "specifyNoEndpoints":
		cfg = config.SetDirEndpoint(cfg, noneEndpoint)
		cfg = config.SetStoreEndpoint(cfg, noneEndpoint)

		// Write config file with updated "none" endpoints.
		err = writeConfig(flags.Config, cfg.UserName(), noneEndpoint, noneEndpoint, true)
		if err != nil {
			return nil, nil, err
		}

	case "specifyGCP":
		privateKeyData := req.FormValue("privateKeyData")

		st, err = gcpStateFromPrivateKeyJSON([]byte(privateKeyData))
		if err != nil {
			return nil, nil, err
		}

		response = "gcpDetails"

	case "createGCP":
		bucketName := req.FormValue("bucketName")
		bucketLoc := req.FormValue("bucketLoc")
		regionZone := req.FormValue("regionZone")
		p := strings.SplitN(regionZone, "/", 2)
		if len(p) != 2 {
			return nil, nil, errors.Errorf("invalid region/zone %q", regionZone)
		}
		region, zone := p[0], p[1]

		// Create the bucket, VM instance, and other associated bits.
		if err := st.create(region, zone, bucketName, bucketLoc); err != nil {
			return nil, nil, err
		}

		response = "serverUserName"

	case "configureServerUserName":
		suffix := req.FormValue("userNameSuffix")

		username, _, domain, err := user.Parse(cfg.UserName())
		if err != nil {
			return nil, nil, err
		}
		serverUser, err := user.Clean(upspin.UserName(username + "+" + suffix + "@" + domain))
		if err != nil {
			return nil, nil, err
		}

		// Generate key.
		seed, keyDir, err := genkey(serverUser)
		if err != nil {
			return nil, nil, err
		}
		// Write config file.
		serverCfgFile := flags.Config + "." + suffix
		err = writeConfig(serverCfgFile, serverUser, upspin.Endpoint{}, upspin.Endpoint{}, false)
		if err != nil {
			os.RemoveAll(keyDir)
			return nil, nil, err
		}
		// Read config file back.
		serverCfg, err := config.FromFile(serverCfgFile)
		if err != nil {
			os.RemoveAll(keyDir)
			os.Remove(serverCfgFile)
			return nil, nil, err
		}
		// Put the server user to the key server.
		if err := putUser(cfg, serverCfg); err != nil {
			os.RemoveAll(keyDir)
			os.Remove(serverCfgFile)
			return nil, nil, err
		}

		// Save the state.
		st.Server.KeyDir = keyDir
		st.Server.UserName = serverUser
		if err := st.save(); err != nil {
			return nil, nil, err
		}

		return &startupResponse{
			Step:       "serverSecretSeed",
			SecretSeed: seed,
			KeyDir:     keyDir,
		}, nil, nil

	case "configureServerHostName":
		hostName := req.FormValue("hostName")

		// Set up a default host name if none provided.
		if hostName == "" {
			serverCfg, _, err := serverConfig(st.Server.UserName)
			if err != nil {
				return nil, nil, err
			}
			hostName, err = serviceHostName(serverCfg, st.Server.IPAddr)
			if err != nil {
				return nil, nil, err
			}
		}

		// Save the state.
		st.Server.HostName = hostName
		if err := st.save(); err != nil {
			return nil, nil, err
		}

		response = "waitServerHostName"

	case "checkServerHostName":
		if req.FormValue("reset") == "true" {
			// User clicked the "Choose another host name" button.
			// Zero out the host name field to give
			// the user a second chance to select one.
			st.Server.HostName = ""
			if err := st.save(); err != nil {
				return nil, nil, err
			}
			break
		}

		// Check that the host name resolves to what we expect.
		if err := hostResolvesTo(st.Server.HostName, st.Server.IPAddr); err != nil {
			return nil, nil, err
		}
		ep, err := hostnameToEndpoint(st.Server.HostName)
		if err != nil {
			return nil, nil, err
		}

		// Update the user config file and key server record.
		cfg = config.SetDirEndpoint(cfg, ep)
		cfg = config.SetStoreEndpoint(cfg, ep)
		if err := writeConfig(flags.Config, cfg.UserName(), ep, ep, true); err != nil {
			return nil, nil, err
		}
		if err := putUser(cfg, nil); err != nil {
			return nil, nil, err
		}

		// Update the server user config and key server record.
		serverCfg, serverCfgFile, err := serverConfig(st.Server.UserName)
		if err != nil {
			return nil, nil, err
		}
		serverCfg = config.SetDirEndpoint(serverCfg, ep)
		serverCfg = config.SetStoreEndpoint(serverCfg, ep)
		if err := writeConfig(serverCfgFile, st.Server.UserName, ep, ep, true); err != nil {
			return nil, nil, err
		}
		if err := putUser(cfg, serverCfg); err != nil {
			return nil, nil, err
		}

		response = "serverWriters"

	case "configureServer":
		writersList := req.FormValue("writers")

		// Gather list of writers.
		// Always include the user and the server user.
		w := map[upspin.UserName]bool{
			st.Server.UserName: true,
			cfg.UserName():     true,
		}
		for _, s := range strings.Fields(writersList) {
			name := upspin.UserName(s)
			if name == "" {
				continue
			}
			var err error
			name, err = user.Clean(name)
			if err != nil {
				return nil, nil, err
			}
			w[name] = true
		}
		var writers []upspin.UserName
		for name := range w {
			writers = append(writers, name)
		}
		sort.Slice(writers, func(i, j int) bool { return writers[i] < writers[j] })

		// Configure the upspinserver.
		if err := st.configureServer(writers); err != nil {
			return nil, nil, err
		}

		// Save the state.
		st.Server.Configured = true
		if err := st.save(); err != nil {
			return nil, nil, err
		}
		// TODO: delete state file instead of saving?
	}

	// readOnly is set if the user explicitly decided to not nominate a
	// directory or store server, meaning they are read-only.
	readOnly := false

	// If we're in the middle of setting up a GCP instance, prompt the user
	// with the correct step of the process. Otherwise, if the user not
	// registered with the KeyServer, prompt them to click the verification
	// link. If they have a registered user, but not specified an endpoint
	// (including 'unassigned') in the config file, prompt them to select
	// Upspin servers.
	if st != nil && response == "" {
		// Deploying to GCP...
		if st.APIsEnabled {
			response = "gcpDetails"
		}
		if st.Server.Created {
			response = "serverUserName"
		}
		if st.Server.UserName != "" {
			response = "serverHostName"
		}
		if st.Server.HostName != "" {
			response = "waitServerHostName"
		}
		if st.Server.Configured {
			response = ""
		}
	} else if ok, err := isRegistered(cfg.UserName()); err != nil {
		return nil, nil, err
	} else if !ok {
		// TODO: Read seed from secret.upspinkey
		// and display proquint again?
		return &startupResponse{
			Step:     "verify",
			UserName: cfg.UserName(),
		}, nil, nil
	} else if ep := cfg.DirEndpoint(); response == "" && (ep == upspin.Endpoint{} || ep == noneEndpoint) {
		ok, err := hasEndpoints(flags.Config)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return &startupResponse{
				Step: "serverSelect",
			}, nil, nil
		}
		readOnly = true
	}

	switch response {
	case "gcpDetails":
		// Prompt for GCP Details such as bucket name and eventually
		// GCP zone/region, instance size, etc.

		// Assume a resonable default bucket name dervied from the project ID.
		bucketName := st.ProjectID + "-upspin"
		// TODO: check bucketName is available

		zones, err := st.listZones()
		if err != nil {
			return nil, nil, err
		}
		locs, err := st.listStorageLocations()
		if err != nil {
			return nil, nil, err
		}

		return &startupResponse{
			Step:       "gcpDetails",
			BucketName: bucketName,
			Zones:      zones,
			Locations:  locs,
		}, nil, nil

	case "serverUserName":
		// Prompt for a user name suffix for the server.
		// Provide a reasonable default suffix, 'upspinserver'.
		// If the user name already contains 'upspin' then suggest just
		// 'server' to avoid stutter.

		// Split the user name into user and domain components, to
		// display to the user as they choose the suffix.
		user, suffix, domain, err := user.Parse(cfg.UserName())
		if err != nil {
			return nil, nil, err
		}
		if suffix != "" {
			// Sanity check only; we shouldn't get here.
			return nil, nil, errors.Errorf("user name %q should not contain a + symbol", user)
		}

		// Choose default suffix.
		suffix = "upspinserver"
		if strings.Contains(user, "upspin") {
			suffix = "server"
		}

		return &startupResponse{
			Step:           "serverUserName",
			UserNamePrefix: user + "+",
			UserNameSuffix: suffix,
			UserNameDomain: "@" + domain,
		}, nil, nil

	case "serverHostName":
		// Prompt for a host name.
		// The default is an assigned name under upspin.services.
		return &startupResponse{
			Step:   "serverHostName",
			IPAddr: st.Server.IPAddr,
		}, nil, nil

	case "waitServerHostName":
		// Ask the user to wait a few minutes for the host name to
		// propagate.
		return &startupResponse{
			Step:     "waitServerHostName",
			IPAddr:   st.Server.IPAddr,
			HostName: st.Server.HostName,
		}, nil, nil

	case "serverWriters":
		// Prompt for a list of server Writers.
		// Pre-populate the list with the server user name
		// and the active user, so they get the idea.
		// Those users will *always* be added to the list, though.
		return &startupResponse{
			Step: "serverWriters",
			Writers: []upspin.UserName{
				st.Server.UserName,
				cfg.UserName(),
			},
		}, nil, nil
	}

	// Start cache if necessary.
	cacheutil.Start(cfg)

	if !readOnly {
		if err := makeRoot(cfg); err != nil {
			return nil, nil, err
		}
	}

	// We have a valid config. Set it in the server struct so that the
	// other methods can use it.
	s.mu.Lock()
	s.cfg = cfg
	s.cli = client.New(cfg)
	s.mu.Unlock()

	return nil, cfg, nil
}

// genkey generates an upspin key pair, placing it in the default directory for
// the given user. It returns the secret seed for the keys and the key
// directory. If the key directory already exists, genkey return an error.
func genkey(user upspin.UserName) (seed, keyDir string, err error) {
	keyDir, err = config.DefaultSecretsDir(user)
	if err != nil {
		return "", "", err
	}
	if exists(keyDir) {
		return "", "", errors.Errorf("cannot generate keys in %s: directory already exists", keyDir)
	}
	if err := os.MkdirAll(keyDir, 0700); err != nil {
		return "", "", err
	}
	pub, priv, seed, err := keygen.Generate("p256")
	if err != nil {
		os.Remove(keyDir)
		return "", "", err
	}
	err = keygen.SaveKeys(keyDir, false, pub, priv, seed)
	if err != nil {
		// SaveKeys may have left files behind, so RemoveAll.
		os.RemoveAll(keyDir)
		return "", "", err
	}
	return seed, keyDir, nil
}

// writeConfig writes an Upspin config to the nominated file containing the
// provided user name and endpoints.
// It will fail if file exists and allowOverwrite is false.
func writeConfig(file string, user upspin.UserName, dir, store upspin.Endpoint, allowOverwrite bool) error {
	if exists(file) && !allowOverwrite {
		return errors.Errorf("cannot write %s: file already exists", file)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		return err
	}
	cfg := fmt.Sprintf("username: %s\n", user)
	if *keyServerAddr != defaultKeyServer {
		cfg += fmt.Sprintf("keyserver: remote,%s\n", *keyServerAddr)
	}
	if dir != (upspin.Endpoint{}) {
		cfg += fmt.Sprintf("dirserver: %s\n", dir)
	}
	if store != (upspin.Endpoint{}) {
		cfg += fmt.Sprintf("storeserver: %s\n", store)
	}
	cfg += "packing: ee\n"
	if *tlsCertDir != "" {
		cfg += fmt.Sprintf("tlscerts: %s\n", *tlsCertDir)
	}
	// Deactivated cache for now, as it seems to interact poorly with
	// host@upspin.io. TODO(adg): turn it back on after more testing.
	//cfg += "cache: yes\n" // TODO(adg): make this configurable?
	return ioutil.WriteFile(file, []byte(cfg), 0644)
}

// isRegistered reports whether the given user is present on the KeyServer.
func isRegistered(user upspin.UserName) (bool, error) {
	// Do the lookup request as the user "nobody@upspin.io" instead of the
	// user we're looking for, so that bind doesn't cache the dialed
	// KeyServer for the actual user with a nil factotum. Otherwise this
	// will come to bite us later if/when we eventually try to perform a
	// Put of a server user. In any case, it doesn't matter who the calling
	// user is because the KeyServer.Lookup requests are not authenticated.
	cfg := config.SetUserName(config.New(), "nobody@upspin.io")

	if *tlsCertDir != "" {
		cfg = config.SetValue(cfg, "tlscerts", *tlsCertDir)
	}

	key, err := bind.KeyServer(cfg, upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr(*keyServerAddr),
	})
	if err != nil {
		return false, err
	}
	usercache.ResetGlobal() // Avoid hitting the local user cache.
	_, err = key.Lookup(user)
	if errors.Match(errors.E(errors.NotExist), err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// makeRoot checks whether the given config's user's root exists and creates it
// if not, returning any unexpected errors that occur during the process.
func makeRoot(cfg upspin.Config) error {
	make := false
	addr := cfg.DirEndpoint().NetAddr
	root := upspin.PathName(cfg.UserName())
	dir, err := bind.DirServer(cfg, cfg.DirEndpoint())
	if err != nil {
		return errors.Errorf("could not find %q:\n%v", addr, err)
	}
	_, err = dir.Lookup(root)
	if errors.Match(errors.E(errors.NotExist), err) {
		make = true
	} else if err != nil {
		return errors.Errorf("error communicating with %q:\n%v", addr, err)
	}
	if !make {
		return nil
	}
	_, err = client.New(cfg).MakeDirectory(root)
	if err != nil {
		return errors.Errorf("error creating Upspin root:\n%v", err)
	}
	return nil
}

// putUser updates the key server as the user in cfg with the user name,
// endpoints, and public key in the userCfg.
// If userCfg is nil then cfg is used in its place.
func putUser(cfg, userCfg upspin.Config) error {
	if userCfg == nil {
		userCfg = cfg
	}

	f := userCfg.Factotum()
	if f == nil {
		return errors.E(userCfg.UserName(), errors.Str("user has no keys"))
	}
	newU := upspin.User{
		Name:      userCfg.UserName(),
		Dirs:      []upspin.Endpoint{userCfg.DirEndpoint()},
		Stores:    []upspin.Endpoint{userCfg.StoreEndpoint()},
		PublicKey: f.PublicKey(),
	}

	key, err := bind.KeyServer(cfg, cfg.KeyEndpoint())
	if err != nil {
		return err
	}
	usercache.ResetGlobal() // Avoid hitting the local user cache.
	oldU, err := key.Lookup(userCfg.UserName())
	if err != nil && !errors.Match(errors.E(errors.NotExist), err) {
		return err
	}
	if reflect.DeepEqual(oldU, &newU) {
		// Don't do anything if we're not changing anything.
		return nil
	}
	return key.Put(&newU)
}

// serviceHostName registers an upspin.services host name with host@upspin.io
// for the given config's user and configures it to resolve to the given IP
// address.
func serviceHostName(cfg upspin.Config, ip string) (string, error) {
	cli := client.New(cfg)
	base := upspin.PathName("host@upspin.io/" + cfg.UserName())
	_, err := cli.MakeDirectory(base + "/" + upspin.PathName(ip))
	if err != nil {
		return "", err
	}
	b, err := cli.Get(base)
	if err != nil {
		return "", err
	}
	p := bytes.SplitN(b, []byte("\n"), 2)
	if len(p) == 2 {
		host := string(bytes.TrimSpace(p[1]))
		if strings.HasSuffix(host, "upspin.services") {
			return host, nil
		}
	}
	return "", errors.Errorf("unexpected response from host@upspin.io:\n%s", b)
}

// hostResolvesTo checks whether the given host name resolves to the given IP
// address. If not, it returns a descriptive error.
func hostResolvesTo(host, ip string) error {
	// TODO(adg): provide different error messages when upspin.services is
	// in the host name. In those cases, maybe the DNS cache is stale or
	// they might be misusing the service.
	ips, err := net.LookupIP(host)
	if err != nil {
		return errors.Errorf("Could not resolve %q:\n%s", host, err)
	}
	if len(ips) == 0 {
		return errors.Errorf("The host %q does not resolve to any IP.\nIt should resolve to %q. Check your DNS settings.", host, ip)
	}
	for _, ipp := range ips {
		if ipp.String() != ip {
			return errors.Errorf("The host %q resolves to %q.\nIt should resolve to %q. Check your DNS settings.", host, ipp, ip)
		}
	}
	return nil
}

// hasEndpoints reports whether the given config file contains a dirserver
// endpoint. It is only a rough test (it doesn't actually parse the YAML) and
// should be used in concert with a check against the parsed config.
func hasEndpoints(configFile string) (bool, error) {
	b, err := ioutil.ReadFile(configFile)
	if err != nil {
		return false, err
	}
	return bytes.Contains(b, []byte("\ndirserver:")), nil
}

// exists reports whether the given path is accessible.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// hostnameToEndpoint returns the remote endpoint for the given host name,
// appending :443 if no port is provided.
func hostnameToEndpoint(hostname string) (upspin.Endpoint, error) {
	if !strings.Contains(hostname, ":") {
		hostname += ":443"
	}
	host, port, err := net.SplitHostPort(hostname)
	if err != nil {
		return upspin.Endpoint{}, err
	}
	return upspin.Endpoint{
		Transport: upspin.Remote,
		NetAddr:   upspin.NetAddr(host + ":" + port),
	}, nil
}

// serverConfig returns the config for the given server user
// (obtained by concatenating flags.Config and the user name suffix)
// and the name of the config file.
func serverConfig(name upspin.UserName) (cfg upspin.Config, filename string, err error) {
	_, suffix, _, _ := user.Parse(name)
	filename = flags.Config + "." + suffix
	cfg, err = config.FromFile(filename)
	return
}

// formatRequest returns a human-readable string representation of the given
// request values, redacting any private key information.
func formatRequest(vals url.Values) string {
	var buf bytes.Buffer
	fmt.Fprint(&buf, "{")
	first := true
	for k, vs := range vals {
		// Redact private key data from the log file, so users don't
		// inadvertently leak their cloud project credentials to the
		// world when reporting bugs.
		if k == "privateKeyData" {
			vs = []string{"REDACTED"}
		}
		// This field is always "startup" so omit it.
		if k == "method" {
			continue
		}
		// Don't redundantly log the session key.
		if k == "key" {
			continue
		}
		if first {
			fmt.Fprint(&buf, "\n")
			first = false
		} else {
			fmt.Fprint(&buf, ",\n")
		}
		fmt.Fprintf(&buf, "\t%q: %q", k, vs)
	}
	if first {
		fmt.Fprint(&buf, "}")
	} else {
		fmt.Fprint(&buf, "\n}")
	}
	return buf.String()
}

// formatResponse returns a human-readable string representation of the given
// response, redacting any prviate key information.
func formatResponse(resp *startupResponse) string {
	if resp == nil {
		return "<nil>"
	}
	r := *resp
	if r.SecretSeed != "" {
		// Redact secret seeds from the log file, so users don't
		// inadvertently leak their Upspin keys to the world when
		// reporting bugs.
		r.SecretSeed = "REDACTED"
	}
	b, _ := json.MarshalIndent(&r, "", "\t")
	return string(b)
}
