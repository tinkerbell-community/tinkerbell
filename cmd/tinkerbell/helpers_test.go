package main

import (
	"maps"
	"net/netip"
	"slices"
	"testing"

	"github.com/tinkerbell/tinkerbell/cmd/tinkerbell/flag"
	"github.com/tinkerbell/tinkerbell/pkg/backend/kube"
)

func TestEnabledIndexes(t *testing.T) {
	smeeIndexes := len(flag.KubeIndexesSmee)
	tests := map[string]struct {
		smee, tootles, tinkServer, secondStar bool
		want                                  []kube.IndexType
	}{
		"none": {},
		"smee": {smee: true, want: []kube.IndexType{
			kube.IndexTypeMACAddrV1Alpha2, kube.IndexTypeIPAddrV1Alpha2,
		}},
		"smee and tootles": {smee: true, tootles: true, want: []kube.IndexType{
			kube.IndexTypeMACAddrV1Alpha2, kube.IndexTypeIPAddrV1Alpha2, kube.IndexTypeLookupIDV1Alpha2,
		}},
		"tink server": {tinkServer: true, want: []kube.IndexType{
			kube.IndexTypeWorkflowAgentIDV1Alpha2, kube.IndexTypeWorkflowTaskAgentIDV1Alpha2, kube.IndexTypeHardwareAgentIDV1Alpha2,
		}},
		"secondstar": {secondStar: true, want: []kube.IndexType{
			kube.IndexTypeHardwareNameV1Alpha2, kube.IndexTypeMachineName,
		}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := enabledIndexes(tt.smee, tt.tootles, tt.tinkServer, tt.secondStar)
			gotTypes := slices.Sorted(maps.Keys(got))
			slices.Sort(tt.want)
			if !slices.Equal(gotTypes, tt.want) {
				t.Fatalf("enabled %v, want %v", gotTypes, tt.want)
			}
			for indexType, index := range got {
				if index.Obj == nil || index.ExtractValue == nil {
					t.Errorf("index %q is not defined", indexType)
				}
			}
			if len(flag.KubeIndexesSmee) != smeeIndexes {
				t.Fatal("enabling indexes must not change a component's index set")
			}
		})
	}
}

func TestMetadataEndpoint(t *testing.T) {
	base := flag.GlobalConfig{
		EnableTootles: true,
		PublicIP:      netip.MustParseAddr("192.0.2.10"),
		HTTPPort:      7080,
		HTTPSPort:     7443,
	}

	tests := []struct {
		name            string
		tlsEnabled      bool
		disableRedirect bool
		wantScheme      string
		wantPort        int
	}{
		{name: "plain HTTP without TLS", wantScheme: flag.SchemeHTTP, wantPort: 7080},
		{name: "HTTPS when HTTP redirects to it", tlsEnabled: true, wantScheme: flag.SchemeHTTPS, wantPort: 7443},
		{name: "plain HTTP when the redirect is disabled", tlsEnabled: true, disableRedirect: true, wantScheme: flag.SchemeHTTP, wantPort: 7080},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			globals := base
			globals.TLS.DisableHTTPToHTTPSRedirect = tt.disableRedirect

			got := metadataEndpoint(&globals, tt.tlsEnabled)
			if got.Scheme != tt.wantScheme || got.Port != tt.wantPort {
				t.Fatalf("metadataEndpoint() = %s:%d, want %s:%d", got.Scheme, got.Port, tt.wantScheme, tt.wantPort)
			}
			if !got.Enabled || got.PublicIP != base.PublicIP {
				t.Fatalf("metadataEndpoint() = %+v, want Tootles enabled on %s", got, base.PublicIP)
			}
		})
	}
}
