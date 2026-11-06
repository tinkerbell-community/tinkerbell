package nocloud_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/tinkerbell/tinkerbell/tootles/internal/frontend/nocloud"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func init() {
	gin.SetMode(gin.ReleaseMode)
}

type fakeClient struct {
	instance nocloud.Instance
	err      error
	// ip and lookupID are those of the last lookup.
	ip, lookupID string
}

func (f *fakeClient) GetNoCloudInstance(_ context.Context, ip string) (nocloud.Instance, error) {
	f.ip = ip
	return f.instance, f.err
}

func (f *fakeClient) GetNoCloudInstanceByLookupID(_ context.Context, lookupID string) (nocloud.Instance, error) {
	f.lookupID = lookupID
	return f.instance, f.err
}

var instance = nocloud.Instance{
	MetaData:      "instance-id: i-1\n",
	UserData:      "#cloud-config\n",
	VendorData:    "#cloud-config\npackages: [vim]\n",
	NetworkConfig: "version: 2\n",
}

func serve(client nocloud.Client, lookupIDEndpoint bool, remoteAddr, path string) *httptest.ResponseRecorder {
	router := gin.New()
	nocloud.Configure(router, client, lookupIDEndpoint)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = remoteAddr
	router.ServeHTTP(w, r)
	return w
}

func TestFiles(t *testing.T) {
	files := map[string]string{
		"meta-data":      instance.MetaData,
		"user-data":      instance.UserData,
		"vendor-data":    instance.VendorData,
		"network-config": instance.NetworkConfig,
	}
	for file, want := range files {
		t.Run(file, func(t *testing.T) {
			client := &fakeClient{instance: instance}
			w := serve(client, false, "10.0.0.1:4242", "/nocloud/"+file)
			if w.Code != http.StatusOK || w.Body.String() != want {
				t.Fatalf("got %d %q, want 200 %q", w.Code, w.Body.String(), want)
			}
			if client.ip != "10.0.0.1" {
				t.Fatalf("looked up IP %q, want the source address", client.ip)
			}

			client = &fakeClient{instance: instance}
			w = serve(client, true, "10.0.0.1:4242", "/nocloud/lookup-1/"+file)
			if w.Code != http.StatusOK || w.Body.String() != want {
				t.Fatalf("by lookup ID got %d %q, want 200 %q", w.Code, w.Body.String(), want)
			}
			if client.lookupID != "lookup-1" || client.ip != "" {
				t.Fatalf("looked up %q by lookup ID and %q by IP, want only lookup-1 by lookup ID", client.lookupID, client.ip)
			}
		})
	}
}

// TestEmptyFiles serves files the Hardware has no data for: cloud-init needs meta-data and
// user-data to exist and waits on the others when they do not.
func TestEmptyFiles(t *testing.T) {
	for _, file := range []string{"meta-data", "user-data", "vendor-data", "network-config"} {
		w := serve(&fakeClient{}, false, "10.0.0.1:4242", "/nocloud/"+file)
		if w.Code != http.StatusOK || w.Body.Len() != 0 {
			t.Errorf("%s: got %d %q, want 200 and no content", file, w.Code, w.Body.String())
		}
	}
}

func TestLookupIDEndpointDisabled(t *testing.T) {
	client := &fakeClient{instance: instance}
	w := serve(client, false, "10.0.0.1:4242", "/nocloud/lookup-1/meta-data")
	if w.Code != http.StatusNotFound || client.lookupID != "" {
		t.Fatalf("got %d after looking up %q, want 404 without a lookup", w.Code, client.lookupID)
	}
}

// TestLookupIDNamedAsAFile serves the Hardware whose lookup ID is the name of a file.
func TestLookupIDNamedAsAFile(t *testing.T) {
	client := &fakeClient{instance: instance}
	w := serve(client, true, "10.0.0.1:4242", "/nocloud/meta-data/user-data")
	if w.Code != http.StatusOK || w.Body.String() != instance.UserData || client.lookupID != "meta-data" {
		t.Fatalf("got %d %q for lookup ID %q, want the user-data of lookup ID meta-data", w.Code, w.Body.String(), client.lookupID)
	}
}

func TestErrors(t *testing.T) {
	tests := map[string]struct {
		err        error
		remoteAddr string
		want       int
	}{
		"not found":              {err: fmt.Errorf("%w: gone", nocloud.ErrInstanceNotFound), remoteAddr: "10.0.0.1:4242", want: http.StatusNotFound},
		"kubernetes not found":   {err: apierrors.NewNotFound(schema.GroupResource{Resource: "hardware"}, "x"), remoteAddr: "10.0.0.1:4242", want: http.StatusNotFound},
		"backend error":          {err: errors.New("connection refused"), remoteAddr: "10.0.0.1:4242", want: http.StatusInternalServerError},
		"invalid remote address": {remoteAddr: "garbage", want: http.StatusBadRequest},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			client := &fakeClient{instance: instance, err: tt.err}
			if w := serve(client, true, tt.remoteAddr, "/nocloud/meta-data"); w.Code != tt.want {
				t.Fatalf("by IP got %d, want %d", w.Code, tt.want)
			}
			if tt.err == nil {
				return
			}
			if w := serve(client, true, tt.remoteAddr, "/nocloud/lookup-1/meta-data"); w.Code != tt.want {
				t.Fatalf("by lookup ID got %d, want %d", w.Code, tt.want)
			}
		})
	}
}
