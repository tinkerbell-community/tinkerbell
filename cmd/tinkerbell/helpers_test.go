package main

import (
	"net/netip"
	"testing"

	"github.com/tinkerbell/tinkerbell/cmd/tinkerbell/flag"
)

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
