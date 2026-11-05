package flag

import (
	"net/netip"
	"testing"

	"github.com/tinkerbell/tinkerbell/tink/controller"
)

func TestTinkControllerConvertMetadataURL(t *testing.T) {
	publicIP := netip.MustParseAddr("192.0.2.10")
	plainHTTP := MetadataEndpoint{Enabled: true, PublicIP: publicIP, Scheme: SchemeHTTP, Port: 7080}

	tests := []struct {
		name    string
		flag    string
		md      MetadataEndpoint
		want    string
		wantErr bool
	}{
		{
			name: "derived from the public IP and HTTP port",
			md:   plainHTTP,
			want: "http://192.0.2.10:7080",
		},
		{
			name: "derived from the HTTPS port when HTTP redirects",
			md:   MetadataEndpoint{Enabled: true, PublicIP: publicIP, Scheme: SchemeHTTPS, Port: 7443},
			want: "https://192.0.2.10:7443",
		},
		{
			name: "IPv6 public IP is bracketed",
			md:   MetadataEndpoint{Enabled: true, PublicIP: netip.MustParseAddr("2001:db8::10"), Scheme: SchemeHTTP, Port: 7080},
			want: "http://[2001:db8::10]:7080",
		},
		{
			name: "nothing derived with Tootles disabled",
			md:   MetadataEndpoint{PublicIP: publicIP, Scheme: SchemeHTTP, Port: 7080},
		},
		{
			name: "nothing derived without a public IP",
			md:   MetadataEndpoint{Enabled: true, Scheme: SchemeHTTP, Port: 7080},
		},
		{
			name: "nothing derived from an unspecified public IP",
			md:   MetadataEndpoint{Enabled: true, PublicIP: netip.IPv4Unspecified(), Scheme: SchemeHTTP, Port: 7080},
		},
		{
			name: "flag wins and loses its trailing slash",
			flag: "http://tootles.example.com/",
			md:   plainHTTP,
			want: "http://tootles.example.com",
		},
		{
			name: "flag is used even with Tootles disabled",
			flag: "https://metadata.example.com:8443",
			want: "https://metadata.example.com:8443",
		},
		{
			name:    "flag without a scheme is rejected",
			flag:    "192.0.2.10:7080",
			md:      plainHTTP,
			wantErr: true,
		},
		{
			name:    "flag with another scheme is rejected",
			flag:    "ftp://192.0.2.10",
			md:      plainHTTP,
			wantErr: true,
		},
		{
			name:    "flag without a host is rejected",
			flag:    "http:///2009-04-04",
			md:      plainHTTP,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := &TinkControllerConfig{Config: controller.NewConfig(controller.WithMetadataURL(tt.flag))}
			err := tc.Convert(tt.md)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Convert() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if tc.Config.MetadataURL != tt.want {
				t.Fatalf("MetadataURL = %q, want %q", tc.Config.MetadataURL, tt.want)
			}
		})
	}
}
