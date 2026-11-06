// Package nocloud serves the cloud-init NoCloud datasource over HTTP: the meta-data, user-data,
// vendor-data and network-config files cloud-init reads from a seedfrom URL.
//
//	https://cloudinit.readthedocs.io/en/latest/reference/datasources/nocloud.html
//
// The files are served at /nocloud/ for the Hardware with the request's source address and, when
// enabled, at /nocloud/<lookupID>/ for the Hardware with that spec.instance.lookupID:
//
//	ds=nocloud;s=http://tootles:8080/nocloud/
//	ds=nocloud;s=http://tootles:8080/nocloud/<lookupID>/
package nocloud

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/tinkerbell/tinkerbell/tootles/internal/http/request"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// ErrInstanceNotFound indicates no Hardware matches a request.
var ErrInstanceNotFound = errors.New("instance not found")

// Instance is the NoCloud data of a Hardware.
type Instance struct {
	MetaData      string
	UserData      string
	VendorData    string
	NetworkConfig string
}

// Client is a backend for retrieving NoCloud data. Both methods return an error wrapping
// ErrInstanceNotFound, or a Kubernetes not found error, when no Hardware matches.
type Client interface {
	// GetNoCloudInstance returns the NoCloud data of the Hardware with the address ip.
	GetNoCloudInstance(ctx context.Context, ip string) (Instance, error)
	// GetNoCloudInstanceByLookupID returns the NoCloud data of the Hardware with the
	// spec.instance.lookupID lookupID.
	GetNoCloudInstanceByLookupID(ctx context.Context, lookupID string) (Instance, error)
}

// prefix is the path every NoCloud route starts with.
const prefix = "/nocloud/"

// files are the files of a NoCloud seed. cloud-init fails the datasource when meta-data or
// user-data cannot be read, and retries the others before going without them, so every file is
// served, empty when the Hardware has none.
var files = []struct {
	name string
	get  func(Instance) string
}{
	{name: "meta-data", get: func(i Instance) string { return i.MetaData }},
	{name: "user-data", get: func(i Instance) string { return i.UserData }},
	{name: "vendor-data", get: func(i Instance) string { return i.VendorData }},
	{name: "network-config", get: func(i Instance) string { return i.NetworkConfig }},
}

// Configure adds the NoCloud routes to router. The routes for a lookup ID are added only with
// lookupIDEndpoint: like the EC2 instance ID endpoint, they serve any Hardware's data to whoever
// knows its lookup ID.
func Configure(router gin.IRouter, client Client, lookupIDEndpoint bool) {
	for _, f := range files {
		router.GET(prefix+f.name, func(ctx *gin.Context) {
			ip, err := request.RemoteAddrIP(ctx.Request)
			if err != nil {
				_ = ctx.AbortWithError(http.StatusBadRequest, errors.New("invalid remote address"))
				return
			}
			i, err := client.GetNoCloudInstance(ctx, ip)
			write(ctx, i, err, f.get, "source ip: "+ip)
		})

		if lookupIDEndpoint {
			router.GET(prefix+":lookupID/"+f.name, func(ctx *gin.Context) {
				lookupID := ctx.Param("lookupID")
				if strings.TrimSpace(lookupID) == "" {
					_ = ctx.AbortWithError(http.StatusNotFound, errors.New("lookup ID is empty"))
					return
				}
				i, err := client.GetNoCloudInstanceByLookupID(ctx, lookupID)
				write(ctx, i, err, f.get, fmt.Sprintf("lookup ID: '%s'", lookupID))
			})
		}
	}
}

// write writes the file get returns from i, or the error looking i up for the Hardware lookup
// describes.
func write(ctx *gin.Context, i Instance, err error, get func(Instance) string, lookup string) {
	switch {
	case errors.Is(err, ErrInstanceNotFound) || apierrors.IsNotFound(err):
		_ = ctx.AbortWithError(http.StatusNotFound, fmt.Errorf("no hardware found for %s", lookup))
	case err != nil:
		_ = ctx.AbortWithError(http.StatusInternalServerError, err)
	default:
		ctx.String(http.StatusOK, get(i))
	}
}
