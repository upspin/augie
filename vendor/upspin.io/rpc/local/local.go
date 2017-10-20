// Copyright 2017 The Upspin Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package local provides interprocess communication on the local host.
package local // import "upspin.io/rpc/local"

import (
	"context"
	"fmt"
	"net"
	"strings"

	"upspin.io/upspin"
)

const localSuffix = ".localhost."

type Dialer net.Dialer

// LocalName constructs the host local name for a service.
func LocalName(config upspin.Config, service string) string {
	s := fmt.Sprintf("%s.%s%s", config.UserName(), service, localSuffix)
	return strings.Replace(s, "@", ".", 1)
}

// IsLocal returns true if the address is host local.
func IsLocal(address string) bool {
	h, _, err := net.SplitHostPort(address)
	if err != nil {
		h = address
	}
	if !strings.HasSuffix(h, localSuffix) {
		return false
	}
	return true
}

// DialContext dials a service. Use it instead of the standard net.DialContext
// to use a local IPC for host names ending in localSuffix.
func (d *Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if IsLocal(address) {
		return d.DialContextLocal(ctx, network, address)
	}
	nd := net.Dialer(*d)
	return nd.DialContext(ctx, network, address)
}

// Listen listens for calls to a service. Use it instead of the standard net.Listen
// to use a local IPC for host names ending in localSuffix.
func Listen(network, address string) (net.Listener, error) {
	if IsLocal(address) {
		return ListenLocal(address)
	}
	return net.Listen(network, address)
}
