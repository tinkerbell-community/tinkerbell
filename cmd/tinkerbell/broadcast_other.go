//go:build !linux

package main

import (
	"errors"
	"runtime"

	"github.com/go-logr/logr"
	"github.com/tinkerbell/tinkerbell/cmd/tinkerbell/flag"
)

// errNotLinux is returned by the stubs below. The DHCP conduit and default route
// detection are implemented with netlink and network namespaces, both of which
// are Linux-only. The rest of the binary builds and runs on other platforms;
// only these DHCP interface features are unavailable.
var errNotLinux = errors.New("not supported on " + runtime.GOOS + ": requires Linux netlink and network namespaces")

func conduitIfaceName(bool) (string, error) { return "", errNotLinux }

func setupConduit(logr.Logger, flag.ConduitType, string, string) (func(), error) {
	return nil, errNotLinux
}

func defaultRouteInterface() (string, error) { return "", errNotLinux }
