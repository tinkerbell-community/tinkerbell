// package tootles is the code for serving metadata (ec2 style, etc).
// Useful for Cloud-init integration.
package tootles

import (
	"context"
	"fmt"
	"net/http"

	"dario.cat/mergo"
	"github.com/gin-gonic/gin"
	v1alpha2 "github.com/tinkerbell/tinkerbell/api/v1alpha2/tinkerbell"
	"github.com/tinkerbell/tinkerbell/pkg/data"
	"github.com/tinkerbell/tinkerbell/tootles/internal/backend"
	"github.com/tinkerbell/tinkerbell/tootles/internal/frontend/ec2"
	"github.com/tinkerbell/tinkerbell/tootles/internal/frontend/hack"
	"github.com/tinkerbell/tinkerbell/tootles/internal/frontend/nocloud"
)

type Config struct {
	BackendEc2     ec2.Client
	BackendHack    hack.Client
	BackendNoCloud nocloud.Client
	DebugMode      bool
	// InstanceEndpoint enables the endpoints that find Hardware by an identifier in the request
	// path rather than by the request's source address: /tootles/instanceID/<instanceID>/ for
	// EC2 metadata and /nocloud/<lookupID>/ for NoCloud.
	InstanceEndpoint bool
}

// HardwareFilterer is the interface required to filter Hardware objects.
// It is implemented by the kube backend's rendered reader and the noop backend.
type HardwareFilterer interface {
	FilterHardware(ctx context.Context, opts data.HardwareFilter) (*v1alpha2.Hardware, error)
}

// SetBackendFromFilterer configures BackendEc2, BackendHack and BackendNoCloud from a
// HardwareFilterer. This allows callers to wire a backend without importing tootles internal
// packages.
func (c *Config) SetBackendFromFilterer(filterer HardwareFilterer) {
	b := backend.New(filterer)
	c.BackendEc2 = b
	c.BackendHack = b
	c.BackendNoCloud = b
}

func NewConfig(c Config) *Config {
	defaults := &Config{
		DebugMode: false,
	}

	if err := mergo.Merge(defaults, &c); err != nil {
		panic(fmt.Sprintf("failed to merge config: %v", err))
	}

	return defaults
}

// EC2MetadataHandler returns an http.Handler that serves EC2-style metadata at
// /2009-04-04/... and optionally /tootles/instanceID/:instanceID/2009-04-04/...
func (c *Config) EC2MetadataHandler() http.Handler {
	if !c.DebugMode {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()

	fe := ec2.New(c.BackendEc2, c.InstanceEndpoint)
	fe.Configure(router)

	return router
}

// HackMetadataHandler returns an http.Handler that serves the /metadata endpoint
// for the rootio hub action.
func (c *Config) HackMetadataHandler() http.Handler {
	if !c.DebugMode {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()

	hack.Configure(router, c.BackendHack)

	return router
}

// NoCloudHandler returns an http.Handler that serves the cloud-init NoCloud datasource at
// /nocloud/ and, with InstanceEndpoint, /nocloud/<lookupID>/.
func (c *Config) NoCloudHandler() http.Handler {
	if !c.DebugMode {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()

	nocloud.Configure(router, c.BackendNoCloud, c.InstanceEndpoint)

	return router
}
