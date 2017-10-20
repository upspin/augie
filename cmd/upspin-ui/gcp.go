// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// TODO: tell the user to remove/deactivate the Owners service account once
// we're done with it. (Or maybe we can do this mechanically?)

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"upspin.io/flags"
	"upspin.io/subcmd"
	"upspin.io/upspin"

	"golang.org/x/oauth2/google"
	"golang.org/x/oauth2/jwt"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	iam "google.golang.org/api/iam/v1"
	servicemanagement "google.golang.org/api/servicemanagement/v1"
	storage "google.golang.org/api/storage/v1"
)

// gcpState represents the state of a GCP deployment. As the process proceeds,
// the fields are populated with nonzero values from top to bottom.
type gcpState struct {
	JWTConfig *jwt.Config
	ProjectID string

	APIsEnabled bool

	Region string
	Zone   string

	Storage struct {
		ServiceAccount string
		PrivateKeyData string
		Bucket         string
	}

	Server struct {
		IPAddr string

		Created bool

		KeyDir   string
		UserName upspin.UserName

		HostName string

		Configured bool
	}
}

// serverConfig returns a *subcmd.ServerConfig that can be used to configure
// the running upspinserver.
func (s *gcpState) serverConfig() *subcmd.ServerConfig {
	return &subcmd.ServerConfig{
		Addr:        upspin.NetAddr(s.Server.HostName),
		User:        s.Server.UserName,
		StoreConfig: s.storeConfig(),
	}
}

// storeConfig returns the StoreServer configuration for the upspinserver.
func (s *gcpState) storeConfig() []string {
	return []string{
		"backend=GCS",
		"defaultACL=publicRead",
		"gcpBucketName=" + s.Storage.Bucket,
		"privateKeyData=" + s.Storage.PrivateKeyData,
	}
}

// gcpStateFromFile loads the JSON-encoded GCP deployment state file from
// flags.Config+".gcpstate".
func gcpStateFromFile() (*gcpState, error) {
	name := flags.Config + ".gcpState"
	b, err := ioutil.ReadFile(name)
	if err != nil {
		return nil, err
	}
	var s gcpState
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// save writes the JSON-encoded GCP deployment state to
// flags.Config+".gcpstate".
func (s *gcpState) save() error {
	name := flags.Config + ".gcpState"
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(name, b, 0644)
}

// gcpStateFromPrivateKeyJSON instantiates a new gcpState from the given
// Google Cloud Platform Service Account JSON Private Key file.
func gcpStateFromPrivateKeyJSON(b []byte) (*gcpState, error) {
	cfg, err := google.JWTConfigFromJSON(b, compute.CloudPlatformScope)
	if err != nil {
		return nil, err
	}
	projectID, err := serviceAccountEmailToProjectID(cfg.Email)
	if err != nil {
		return nil, err
	}
	s := &gcpState{
		JWTConfig: cfg,
		ProjectID: projectID,
	}
	if !s.APIsEnabled {
		if err := s.enableAPIs(); err != nil {
			return nil, err
		}
		s.APIsEnabled = true
	}
	if err := s.save(); err != nil {
		return nil, err
	}
	return s, nil
}

// serviceAccountEmailToProjectID takes a service account email address and
// extracts the project ID component from its domain part.
func serviceAccountEmailToProjectID(email string) (string, error) {
	i := strings.Index(email, "@")
	if i < 0 {
		return "", fmt.Errorf("service account email %q has no @ sign", email)
	}
	const domain = ".iam.gserviceaccount.com"
	if !strings.HasSuffix(email, domain) {
		return "", fmt.Errorf("service account email %q does not have expected form", email)
	}
	return email[i+1 : len(email)-len(domain)], nil
}

// enableAPIs enables the Compute, Storage, and IAM APIs required to deploy
// upspinserver to GCP.
func (s *gcpState) enableAPIs() error {
	client := s.JWTConfig.Client(context.Background())
	svc, err := servicemanagement.New(client)
	if err != nil {
		return err
	}
	apis := []string{
		"compute_component",  // For the virtual machine.
		"storage_api",        // For storage bucket.
		"iam.googleapis.com", // For creating a service account.
	}
	for _, api := range apis {
		if err := s.enableAPI(api, svc); err != nil {
			return err
		}
	}
	return nil
}

// enableAPI enables the named GCP API using the provided service.
func (s *gcpState) enableAPI(name string, svc *servicemanagement.APIService) error {
	op, err := svc.Services.Enable(name, &servicemanagement.EnableServiceRequest{ConsumerId: "project:" + s.ProjectID}).Do()
	if err != nil {
		return err
	}
	for !op.Done {
		time.Sleep(250 * time.Millisecond)
		op, err = svc.Operations.Get(op.Name).Do()
		if err != nil {
			return err
		}
	}
	if op.Error != nil {
		return errors.New(op.Error.Message)
	}
	return err
}

func (s *gcpState) listZones() ([]string, error) {
	client := s.JWTConfig.Client(context.Background())
	svc, err := compute.New(client)
	if err != nil {
		return nil, err
	}

	list, err := svc.Regions.List(s.ProjectID).Do()
	if err != nil {
		return nil, err
	}
	var zones []string
	for _, region := range list.Items {
		if region.Status == "DOWN" {
			continue
		}
		for _, z := range region.Zones {
			i := strings.LastIndex(z, "/")
			if i < 0 {
				continue
			}
			zones = append(zones, region.Name+z[i:])
		}
	}
	sort.Strings(zones)
	return zones, nil
}

func (s *gcpState) listStorageLocations() ([]string, error) {
	// There's no API for this. Scraped from:
	// https://cloud.google.com/storage/docs/bucket-locations
	return []string{
		// Multi-regional locations.
		"asia",
		"eu",
		"us",
		// Regional locations.
		"asia-east1",
		"asia-northeast1",
		"asia-southeast1",
		"australia-southeast1",
		"europe-west1",
		"europe-west2",
		"europe-west3",
		"us-central1",
		"us-east1",
		"us-east4",
		"us-west1",
	}, nil
}

// create creates a Storage bucket with the given name, a service account to
// access the bucket, and a Compute instance running on a static IP address.
func (s *gcpState) create(region, zone, bucketName, bucketLoc string) error {
	s.Region = region
	s.Zone = zone

	if s.Storage.ServiceAccount == "" {
		email, key, err := s.createServiceAccount()
		if err != nil {
			return err
		}
		s.Storage.ServiceAccount = email
		s.Storage.PrivateKeyData = key
		if err := s.save(); err != nil {
			return err
		}
	}
	if s.Storage.Bucket == "" {
		err := s.createBucket(bucketName, bucketLoc)
		if err != nil {
			return err
		}
		s.Storage.Bucket = bucketName
		if err := s.save(); err != nil {
			return err
		}
	}
	if s.Server.IPAddr == "" {
		ip, err := s.createAddress()
		if err != nil {
			return err
		}
		s.Server.IPAddr = ip
		if err := s.save(); err != nil {
			return err
		}
	}
	if !s.Server.Created {
		err := s.createInstance()
		if err != nil {
			return err
		}
		s.Server.Created = true
		if err := s.save(); err != nil {
			return err
		}
	}
	return nil
}

// createAddress reserves a static IP address with the name "upspinserver".
func (s *gcpState) createAddress() (ip string, err error) {
	client := s.JWTConfig.Client(context.Background())
	svc, err := compute.New(client)
	if err != nil {
		return "", err
	}

	const addressName = "upspinserver"
	addr := &compute.Address{
		Description: "Public IP address for upspinserver",
		Name:        addressName,
	}
	op, err := svc.Addresses.Insert(s.ProjectID, s.Region, addr).Do()
	if err = okReason("alreadyExists", s.waitOp(svc, op, err)); err != nil {
		return "", err
	}
	addr, err = svc.Addresses.Get(s.ProjectID, s.Region, addressName).Do()
	if err != nil {
		return "", err
	}
	return addr.Address, nil
}

// createInstance creates a Compute instance named "upspinserver" running the
// upspinserver Docker image and a firewall rule to allow HTTPS connections to
// that instance. If a firewall rule of the name "allow-https" exists it is
// re-used.
func (s *gcpState) createInstance() error {
	client := s.JWTConfig.Client(context.Background())
	svc, err := compute.New(client)
	if err != nil {
		return err
	}

	const (
		firewallName = "allow-https"
		firewallTag  = firewallName

		instanceName = "upspinserver"
	)

	// Create a firewall to permit HTTPS connections.
	firewall := &compute.Firewall{
		Allowed: []*compute.FirewallAllowed{{
			IPProtocol: "tcp",
			Ports:      []string{"443"},
		}},
		Description:  "Allow HTTPS",
		Name:         firewallName,
		SourceRanges: []string{"0.0.0.0/0"},
		TargetTags:   []string{firewallTag},
	}
	op, err := svc.Firewalls.Insert(s.ProjectID, firewall).Do()
	if err = okReason("alreadyExists", s.waitOp(svc, op, err)); err != nil {
		return err
	}

	// Create a firewall to permit HTTPS connections.
	// Create the instance.
	userData := cloudInitYAML
	instance := &compute.Instance{
		Description: "upspinserver instance",
		Disks: []*compute.AttachedDisk{{
			AutoDelete: true,
			Boot:       true,
			DeviceName: "upspinserver",
			InitializeParams: &compute.AttachedDiskInitializeParams{
				SourceImage: "projects/cos-cloud/global/images/family/cos-stable",
			},
		}},
		MachineType: "zones/" + s.Zone + "/machineTypes/g1-small",
		Name:        instanceName,
		Tags:        &compute.Tags{Items: []string{firewallTag}},
		Metadata: &compute.Metadata{
			Items: []*compute.MetadataItems{{
				Key:   "user-data",
				Value: &userData,
			}},
		},
		NetworkInterfaces: []*compute.NetworkInterface{{
			AccessConfigs: []*compute.AccessConfig{{
				NatIP: s.Server.IPAddr,
			}},
		}},
	}
	op, err = svc.Instances.Insert(s.ProjectID, s.Zone, instance).Do()
	return s.waitOp(svc, op, err)
}

// createServiceAccount creates a service account named "upspinstorage" and
// generates a JSON Private Key for authenticating as that account.
func (s *gcpState) createServiceAccount() (email, privateKeyData string, err error) {
	client := s.JWTConfig.Client(context.Background())
	svc, err := iam.New(client)
	if err != nil {
		return "", "", err
	}

	name := "projects/" + s.ProjectID
	req := &iam.CreateServiceAccountRequest{
		AccountId: "upspinstorage",
		ServiceAccount: &iam.ServiceAccount{
			DisplayName: "Upspin Storage",
		},
	}
	acct, err := svc.Projects.ServiceAccounts.Create(name, req).Do()
	if isExists(err) {
		// This should be the name we need to get.
		// TODO(adg): make this more robust by listing instead.
		guess := name + "/serviceAccounts/upspinstorage@" + s.ProjectID + ".iam.gserviceaccount.com"
		acct, err = svc.Projects.ServiceAccounts.Get(guess).Do()
	}
	if err != nil {
		return "", "", err
	}

	name += "/serviceAccounts/" + acct.Email
	req2 := &iam.CreateServiceAccountKeyRequest{}
	key, err := svc.Projects.ServiceAccounts.Keys.Create(name, req2).Do()
	if err != nil {
		return "", "", err
	}
	return acct.Email, key.PrivateKeyData, nil
}

// createBucket creates the named Storage bucket, giving "owner" access to
// Storage.ServiceAccount in gcpState.
func (s *gcpState) createBucket(name, loc string) error {
	client := s.JWTConfig.Client(context.Background())
	svc, err := storage.New(client)
	if err != nil {
		return err
	}

	acl := &storage.BucketAccessControl{
		Bucket: name,
		Entity: "user-" + s.Storage.ServiceAccount,
		Email:  s.Storage.ServiceAccount,
		Role:   "OWNER",
	}
	_, err = svc.Buckets.Insert(s.ProjectID, &storage.Bucket{
		Acl:      []*storage.BucketAccessControl{acl},
		Name:     name,
		Location: loc,
	}).Do()
	if !isExists(err) {
		return err // May be nil.
	}
	// Bucket already exists. Check bucket ownership and ACL to make sure
	// the service account has access.
	bkt, err := svc.Buckets.Get(name).Do()
	if err != nil {
		return err
	}
	for _, a := range bkt.Acl {
		if a.Email == s.Storage.ServiceAccount && a.Role == "OWNER" {
			// The service account has OWNER privileges; we're ok.
			return nil
		}
	}
	// The service account doesn't have OWNER privileges; try to add them.
	bkt.Acl = append(bkt.Acl, acl)
	_, err = svc.Buckets.Update(name, bkt).Do()
	return err
}

// waitOp waits for the given compute operation to complete and returns the
// first error that occurred, if any.
func (s *gcpState) waitOp(svc *compute.Service, op *compute.Operation, err error) error {
	for err == nil && (op.Status == "PENDING" || op.Status == "RUNNING") {
		time.Sleep(250 * time.Millisecond)
		switch {
		case op.Zone != "":
			op, err = svc.ZoneOperations.Get(s.ProjectID, s.Zone, op.Name).Do()
		case op.Region != "":
			op, err = svc.RegionOperations.Get(s.ProjectID, s.Region, op.Name).Do()
		default:
			op, err = svc.GlobalOperations.Get(s.ProjectID, op.Name).Do()
		}
	}
	return opError(op, err)
}

// opError returns err or the first error in the given Operation, if any.
func opError(op *compute.Operation, err error) error {
	if err != nil {
		return err
	}
	if op == nil || op.Error == nil || len(op.Error.Errors) == 0 {
		return nil
	}
	return errors.New(op.Error.Errors[0].Message)
}

// isExists reports whether err is an "already exists" Google API error.
func isExists(err error) bool {
	return err != nil && (okReason("alreadyExists", err) == nil || okReason("conflict", err) == nil)
}

// okReason checks whether err is a Google API error with the given reason and
// returns nil if so. Otherwise, it returns the given error.
func okReason(reason string, err error) error {
	if ge, ok := err.(*googleapi.Error); ok && len(ge.Errors) > 0 {
		for _, e := range ge.Errors {
			if e.Reason != reason {
				return err
			}
		}
		return nil
	}
	return err
}

// configureServer configures an unconfigured upspinserver instance using
// the state from gcpState and the given set of writers.
// It is analagous to running "upspin setupserver".
//
// TODO(adg): this needn't be a method on gcpState as it's not at all to do
// with GCP. Move it elsewhere if/when we decide to generalize this to support
// other cloud service providers.
func (s *gcpState) configureServer(writers []upspin.UserName) error {
	files := map[string][]byte{}

	var buf bytes.Buffer
	for _, u := range writers {
		fmt.Fprintln(&buf, u)
	}
	files["Writers"] = buf.Bytes()

	for _, name := range []string{"public.upspinkey", "secret.upspinkey"} {
		b, err := ioutil.ReadFile(filepath.Join(s.Server.KeyDir, name))
		if err != nil {
			return err
		}
		files[name] = b
	}

	scfg := s.serverConfig()
	b, err := json.Marshal(scfg)
	if err != nil {
		return err
	}
	files["serverconfig.json"] = b

	b, err = json.Marshal(files)
	if err != nil {
		return err
	}
	u := "https://" + string(scfg.Addr) + "/setupserver"
	resp, err := http.Post(u, "application/octet-stream", bytes.NewReader(b))
	if err != nil {
		return err
	}
	b, _ = ioutil.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upspinserver returned status %v:\n%s", resp.Status, b)
	}
	return nil
}

// cloudInitYAML is the cloud-init configuration file for the virtual machine
// running Google's Container-Optimized OS. It instructs the machine to accept
// incoming TCP connections on port 443 and to run the
// gcr.io/upspin-containers-upspinserver Docker image, exposing the
// upspinserver service on port 443.
const cloudInitYAML = `#cloud-config

users:
- name: upspin
  uid: 2000

runcmd:
- iptables -w -A INPUT -p tcp --dport 443 -j ACCEPT

write_files:
- path: /etc/systemd/system/upspinserver.service
  permissions: 0644
  owner: root
  content: |
    [Unit]
    Description=An upspinserver container instance
    Wants=gcr-online.target
    After=gcr-online.target
    [Service]
    Environment="HOME=/home/upspin"
    ExecStartPre=/usr/bin/docker-credential-gcr configure-docker
    ExecStartPre=/usr/bin/docker pull gcr.io/upspin-containers/upspinserver:latest
    ExecStart=/usr/bin/docker run --rm -u=2000 --volume=/home/upspin:/upspin -p=443:8443 --name=upspinserver gcr.io/upspin-containers/upspinserver:latest
    ExecStop=/usr/bin/docker stop upspinserver
    ExecStopPost=/usr/bin/docker rm upspinserver
    Restart=on-failure

runcmd:
- systemctl daemon-reload
- systemctl start upspinserver.service

`
