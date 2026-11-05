package flag

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/peterbourgon/ff/v4/ffval"
	"github.com/tinkerbell/tinkerbell/pkg/flag/delimitedlist"
	"github.com/tinkerbell/tinkerbell/tink/controller"
)

type TinkControllerConfig struct {
	Config   *controller.Config
	LogLevel int
}

const (
	SchemeHTTP  = "http"
	SchemeHTTPS = "https"
)

type MetadataEndpoint struct {
	Enabled  bool
	PublicIP netip.Addr
	Scheme   string
	Port     int
}

func (t *TinkControllerConfig) Convert(md MetadataEndpoint) error {
	if t.Config.MetadataURL != "" {
		u, err := url.Parse(t.Config.MetadataURL)
		if err != nil {
			return fmt.Errorf("invalid %s: %w", TinkControllerMetadataURL.Name, err)
		}
		if (u.Scheme != SchemeHTTP && u.Scheme != SchemeHTTPS) || u.Host == "" {
			return fmt.Errorf("invalid %s %q: must be an absolute http or https URL", TinkControllerMetadataURL.Name, t.Config.MetadataURL)
		}
		t.Config.MetadataURL = strings.TrimSuffix(t.Config.MetadataURL, "/")
		return nil
	}

	if !md.Enabled || !md.PublicIP.IsValid() || md.PublicIP.IsUnspecified() {
		return nil
	}
	t.Config.MetadataURL = (&url.URL{
		Scheme: md.Scheme,
		Host:   net.JoinHostPort(md.PublicIP.String(), strconv.Itoa(md.Port)),
	}).String()

	return nil
}

func RegisterTinkControllerFlags(fs *Set, t *TinkControllerConfig) {
	fs.Register(TinkControllerEnableLeaderElection, ffval.NewValueDefault(&t.Config.EnableLeaderElection, t.Config.EnableLeaderElection))
	fs.Register(TinkControllerLeaderElectionNamespace, ffval.NewValueDefault(&t.Config.LeaderElectionNamespace, t.Config.LeaderElectionNamespace))
	fs.Register(TinkControllerMaxConcurrentReconciles, ffval.NewValueDefault(&t.Config.MaxConcurrentReconciles, t.Config.MaxConcurrentReconciles))
	fs.Register(TinkControllerLogLevel, ffval.NewValueDefault(&t.LogLevel, t.LogLevel))
	fs.Register(TinkControllerReferenceAllowListRules, delimitedlist.New(&t.Config.ReferenceAllowListRules, '|'))
	fs.Register(TinkControllerReferenceDenyListRules, delimitedlist.New(&t.Config.ReferenceDenyListRules, '|'))
	fs.Register(TinkControllerMetadataURL, ffval.NewValueDefault(&t.Config.MetadataURL, t.Config.MetadataURL))
}

var TinkControllerMetadataURL = Config{
	Name:  "tink-controller-metadata-url",
	Usage: "base URL of the Tootles metadata service, available to Templates as {{ .metadata_url }}; derived from the public IP and the HTTP(S) port when unset and Tootles is enabled",
}

var TinkControllerEnableLeaderElection = Config{
	Name:  "tink-controller-enable-leader-election",
	Usage: "enable leader election for controller manager",
}

var TinkControllerLeaderElectionNamespace = Config{
	Name:  "tink-controller-leader-election-namespace",
	Usage: "namespace in which the leader election lease will be created",
}

var TinkControllerLogLevel = Config{
	Name:  "tink-controller-log-level",
	Usage: logLevelUsage,
}

var TinkControllerReferenceAllowListRules = Config{
	Name:  "tink-controller-reference-allow-list-rules",
	Usage: "rules for which Hardware Reference objects are accessible to Templates",
}

var TinkControllerReferenceDenyListRules = Config{
	Name:  "tink-controller-reference-deny-list-rules",
	Usage: "rules for which Hardware Reference objects are not accessible to Templates, defaults to deny all",
}

var TinkControllerMaxConcurrentReconciles = Config{
	Name:  "tink-controller-max-concurrent-reconciles",
	Usage: "maximum number of concurrent reconciles for tink controller",
}
