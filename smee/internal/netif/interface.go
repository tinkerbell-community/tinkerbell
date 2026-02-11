package netif

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// InterfaceType represents the type of network interface to create.
type InterfaceType string

const (
	// InterfaceTypeMacvlan creates a macvlan interface in bridge mode.
	InterfaceTypeMacvlan InterfaceType = "macvlan"
	// InterfaceTypeIPvlan creates an ipvlan interface in L2 mode.
	InterfaceTypeIPvlan InterfaceType = "ipvlan"

	// DefaultDHCPAddr is the default IP address assigned to the created interface.
	DefaultDHCPAddr = "127.1.1.1/32"
)

// Config holds the configuration for managing network interfaces for DHCP proxy mode.
type Config struct {
	// SrcInterface is the source/parent interface to attach to (e.g., "eth0").
	// If empty, the default gateway interface will be used.
	SrcInterface string
	// InterfaceType is the type of interface to create (macvlan or ipvlan).
	InterfaceType InterfaceType
	// Enabled determines whether interface management is enabled.
	Enabled bool
	// EnableLeaderElection determines if leader election is enabled.
	EnableLeaderElection bool
	// KubeConfig is the Kubernetes client configuration for leader election.
	KubeConfig *rest.Config
	// LeaderElectionNamespace is the namespace for the leader election lock.
	LeaderElectionNamespace string
	// LeaderElectionLockName is the name of the leader election lock.
	LeaderElectionLockName string
	// LeaderElectionIdentity is the unique identity of this instance.
	// If empty, the pod name from HOSTNAME env var is used.
	LeaderElectionIdentity string
	// LeaseDuration is the duration that non-leader candidates will wait to force acquire leadership.
	LeaseDuration time.Duration
	// RenewDeadline is the duration the leader will retry refreshing leadership before giving up.
	RenewDeadline time.Duration
	// RetryPeriod is the duration the LeaderElector clients should wait between tries of actions.
	RetryPeriod time.Duration
	// Logger for logging operations.
	Logger logr.Logger
}

// Manager handles the lifecycle of network interfaces for DHCP proxy mode.
type Manager struct {
	config       Config
	log          logr.Logger
	hostNs       netns.NsHandle
	currentLink  netlink.Link
	srcInterface string
	elector      *leaderelection.LeaderElector
}

// NewManager creates a new interface manager.
func NewManager(cfg Config) (*Manager, error) {
	log := cfg.Logger
	if log.GetSink() == nil {
		log = logr.Discard()
	}

	if !cfg.Enabled {
		return &Manager{config: cfg, log: log}, nil
	}

	if cfg.InterfaceType != InterfaceTypeMacvlan && cfg.InterfaceType != InterfaceTypeIPvlan {
		return nil, fmt.Errorf("invalid interface type %q, must be macvlan or ipvlan", cfg.InterfaceType)
	}

	// Get handle to host network namespace (always PID 1 in containers)
	hostNs, err := netns.GetFromPid(1)
	if err != nil {
		return nil, fmt.Errorf("failed to get host network namespace: %w", err)
	}

	m := &Manager{
		config: cfg,
		log:    log,
		hostNs: hostNs,
	}

	// Determine source interface if not explicitly set
	if cfg.SrcInterface == "" {
		srcIface, err := m.getDefaultGatewayInterface()
		if err != nil {
			_ = hostNs.Close()
			return nil, fmt.Errorf("failed to determine default gateway interface: %w", err)
		}
		m.srcInterface = srcIface
	} else {
		m.srcInterface = cfg.SrcInterface
	}

	// Set up leader election if enabled
	if cfg.EnableLeaderElection {
		elector, err := m.setupLeaderElection(cfg, log)
		if err != nil {
			_ = hostNs.Close()
			return nil, err
		}
		m.elector = elector
		m.config = cfg
	}

	return m, nil
}

// setupLeaderElection configures and creates a leader elector.
func (m *Manager) setupLeaderElection(cfg Config, log logr.Logger) (*leaderelection.LeaderElector, error) {
	if cfg.KubeConfig == nil {
		return nil, fmt.Errorf("kubernetes config required for leader election")
	}

	// Set defaults
	if cfg.LeaseDuration == 0 {
		cfg.LeaseDuration = 15 * time.Second
	}
	if cfg.RenewDeadline == 0 {
		cfg.RenewDeadline = 10 * time.Second
	}
	if cfg.RetryPeriod == 0 {
		cfg.RetryPeriod = 2 * time.Second
	}
	if cfg.LeaderElectionIdentity == "" {
		identity, err := getLeaderElectionIdentity()
		if err != nil {
			return nil, fmt.Errorf("failed to determine identity: %w", err)
		}
		cfg.LeaderElectionIdentity = identity
	}
	if cfg.LeaderElectionNamespace == "" {
		cfg.LeaderElectionNamespace = "default"
	}
	if cfg.LeaderElectionLockName == "" {
		cfg.LeaderElectionLockName = "smee-dhcp-proxy"
	}

	// Create Kubernetes client
	clientset, err := kubernetes.NewForConfig(cfg.KubeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Create resource lock
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      cfg.LeaderElectionLockName,
			Namespace: cfg.LeaderElectionNamespace,
		},
		Client: clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: cfg.LeaderElectionIdentity,
		},
	}

	// Create leader elector
	elector, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:          lock,
		LeaseDuration: cfg.LeaseDuration,
		RenewDeadline: cfg.RenewDeadline,
		RetryPeriod:   cfg.RetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				log.Info("elected as leader, setting up DHCP proxy interface")
				if err := m.setup(ctx); err != nil {
					log.Error(err, "failed to setup interface after becoming leader")
				}
			},
			OnStoppedLeading: func() {
				log.Info("lost leadership, cleaning up DHCP proxy interface")
				if err := m.cleanup(); err != nil {
					log.Error(err, "failed to cleanup interface after losing leadership")
				}
			},
			OnNewLeader: func(identity string) {
				if identity == cfg.LeaderElectionIdentity {
					return
				}
				log.Info("new leader elected", "leader", identity)
			},
		},
		ReleaseOnCancel: true,
		Name:            cfg.LeaderElectionLockName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create leader elector: %w", err)
	}

	return elector, nil
}

// getLeaderElectionIdentity determines the identity for leader election.
func getLeaderElectionIdentity() (string, error) {
	identity := os.Getenv("HOSTNAME")
	if identity != "" {
		return identity, nil
	}
	return os.Hostname()
}

// Start runs the manager, handling leader election if enabled.
// This blocks until the context is cancelled.
func (m *Manager) Start(ctx context.Context) error {
	if !m.config.Enabled {
		m.log.Info("interface management disabled")
		return nil
	}

	if !m.config.EnableLeaderElection {
		m.log.Info("leader election disabled, setting up interface immediately")
		if err := m.setup(ctx); err != nil {
			return err
		}
		<-ctx.Done()
		return m.cleanup()
	}

	// Run with leader election
	m.log.Info("starting leader election",
		"identity", m.config.LeaderElectionIdentity,
		"namespace", m.config.LeaderElectionNamespace,
		"lockName", m.config.LeaderElectionLockName)

	m.elector.Run(ctx)
	return nil
}

// setup configures the network interface. This should only be called when elected as leader.
func (m *Manager) setup(ctx context.Context) error {
	m.log.Info("setting up DHCP proxy interface",
		"type", m.config.InterfaceType,
		"srcInterface", m.srcInterface)

	// Cleanup any existing interfaces first
	if err := m.cleanup(); err != nil {
		m.log.Error(err, "failed to cleanup existing interfaces")
		// Continue anyway to try creating new interface
	}

	// Create the interface in the host network namespace
	if err := m.configureInterface(); err != nil {
		return fmt.Errorf("failed to configure interface: %w", err)
	}

	// For ipvlan, run the broadcast workaround
	if m.config.InterfaceType == InterfaceTypeIPvlan {
		if err := m.ipvlanBroadcastWorkaround(); err != nil {
			m.log.Error(err, "ipvlan broadcast workaround failed, broadcast packets may not work")
		}
	}

	m.log.Info("successfully set up DHCP proxy interface")
	return nil
}

// Cleanup removes the created network interface. Called when losing leadership.
func (m *Manager) Cleanup(ctx context.Context) error {
	if !m.config.Enabled {
		return nil
	}

	m.log.Info("cleaning up DHCP proxy interface")
	return m.cleanup()
}

// Close releases resources held by the manager.
func (m *Manager) Close() error {
	if m.hostNs != 0 {
		return m.hostNs.Close()
	}
	return nil
}

// cleanup removes interfaces in both container and host namespaces.
func (m *Manager) cleanup() error {
	var errs []error

	// Cleanup in container namespace
	containerLinks := []string{string(InterfaceTypeMacvlan) + "0", string(InterfaceTypeIPvlan) + "0", string(InterfaceTypeIPvlan) + "0-wa"}
	for _, name := range containerLinks {
		if link, err := netlink.LinkByName(name); err == nil {
			if err := netlink.LinkDel(link); err != nil {
				m.log.V(1).Info("failed to delete container interface", "name", name, "error", err)
				errs = append(errs, err)
			} else {
				m.log.V(1).Info("deleted container interface", "name", name)
			}
		}
	}

	// Cleanup in host namespace
	if m.hostNs != 0 {
		runtime, err := netns.Get()
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to get current namespace: %w", err))
			return errors.Join(errs...)
		}
		defer runtime.Close()

		if err := netns.Set(m.hostNs); err != nil {
			errs = append(errs, fmt.Errorf("failed to switch to host namespace: %w", err))
			return errors.Join(errs...)
		}
		defer netns.Set(runtime)

		for _, name := range containerLinks {
			if link, err := netlink.LinkByName(name); err == nil {
				if err := netlink.LinkDel(link); err != nil {
					m.log.V(1).Info("failed to delete host interface", "name", name, "error", err)
					errs = append(errs, err)
				} else {
					m.log.V(1).Info("deleted host interface", "name", name)
				}
			}
		}
	}

	return errors.Join(errs...)
}

// createInterfaceInHost creates the virtual interface in the host network namespace.
func (m *Manager) createInterfaceInHost() error {
	// Switch to host namespace
	runtime, err := netns.Get()
	if err != nil {
		return fmt.Errorf("failed to get current namespace: %w", err)
	}
	defer runtime.Close()

	if err := netns.Set(m.hostNs); err != nil {
		return fmt.Errorf("failed to switch to host namespace: %w", err)
	}
	defer netns.Set(runtime)

	// Get the parent interface
	parent, err := netlink.LinkByName(m.srcInterface)
	if err != nil {
		return fmt.Errorf("failed to find parent interface %s: %w", m.srcInterface, err)
	}

	ifName := string(m.config.InterfaceType) + "0"

	// Create the virtual interface
	var link netlink.Link
	switch m.config.InterfaceType {
	case InterfaceTypeMacvlan:
		link = &netlink.Macvlan{
			LinkAttrs: netlink.LinkAttrs{
				Name:        ifName,
				ParentIndex: parent.Attrs().Index,
			},
			Mode: netlink.MACVLAN_MODE_BRIDGE,
		}
	case InterfaceTypeIPvlan:
		link = &netlink.IPVlan{
			LinkAttrs: netlink.LinkAttrs{
				Name:        ifName,
				ParentIndex: parent.Attrs().Index,
			},
			Mode: netlink.IPVLAN_MODE_L2,
		}
	}

	if err := netlink.LinkAdd(link); err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("failed to create %s interface: %w", m.config.InterfaceType, err)
	}

	// Store the link for later operations
	m.currentLink, err = netlink.LinkByName(ifName)
	if err != nil {
		return fmt.Errorf("failed to get created interface: %w", err)
	}

	m.log.V(1).Info("created interface in host namespace",
		"interface", ifName,
		"parent", m.srcInterface)

	return nil
}

// moveInterfaceToContainer moves the interface from host to container namespace.
func (m *Manager) moveInterfaceToContainer() error {
	// Switch to host namespace to move the interface
	runtime, err := netns.Get()
	if err != nil {
		return fmt.Errorf("failed to get current namespace: %w", err)
	}
	defer runtime.Close()

	if err := netns.Set(m.hostNs); err != nil {
		return fmt.Errorf("failed to switch to host namespace: %w", err)
	}
	defer netns.Set(runtime)

	// Move the interface to the container's namespace
	if err := netlink.LinkSetNsFd(m.currentLink, int(runtime)); err != nil {
		return fmt.Errorf("failed to move interface to container namespace: %w", err)
	}

	m.log.V(1).Info("moved interface to container namespace", "interface", m.currentLink.Attrs().Name)
	return nil
}

// configureInterface brings up the interface and assigns an IP address.
func (m *Manager) configureInterface() error {
	// Re-fetch the link in the container namespace
	link, err := netlink.LinkByName(m.currentLink.Attrs().Name)
	if err != nil {
		return fmt.Errorf("failed to find interface in container namespace: %w", err)
	}
	m.currentLink = link

	// Bring the interface up
	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("failed to bring interface up: %w", err)
	}

	// Add IP address
	addr, err := netlink.ParseAddr(DefaultDHCPAddr)
	if err != nil {
		return fmt.Errorf("failed to parse IP address: %w", err)
	}
	addr.Scope = 254 // RT_SCOPE_HOST - noprefixroute equivalent

	if err := netlink.AddrAdd(link, addr); err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("failed to add IP address: %w", err)
	}

	m.log.V(1).Info("configured interface",
		"interface", link.Attrs().Name,
		"ip", DefaultDHCPAddr)

	return nil
}

// ipvlanBroadcastWorkaround implements a workaround for ipvlan interfaces not receiving broadcast packets.
// This creates a temporary bridge-mode ipvlan interface to trigger broadcast packet flow.
func (m *Manager) ipvlanBroadcastWorkaround() error {
	m.log.V(1).Info("applying ipvlan broadcast workaround")

	// Switch to host namespace
	runtime, err := netns.Get()
	if err != nil {
		return fmt.Errorf("failed to get current namespace: %w", err)
	}
	defer runtime.Close()

	if err := netns.Set(m.hostNs); err != nil {
		return fmt.Errorf("failed to switch to host namespace: %w", err)
	}
	defer netns.Set(runtime)

	// Get the parent interface
	parent, err := netlink.LinkByName(m.srcInterface)
	if err != nil {
		return fmt.Errorf("failed to find parent interface: %w", err)
	}

	// Create a temporary ipvlan interface with bridge flag
	waName := "ipvlan0-wa"
	waLink := &netlink.IPVlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:        waName,
			ParentIndex: parent.Attrs().Index,
		},
		Mode: netlink.IPVLAN_MODE_L2,
		Flag: netlink.IPVLAN_FLAG_BRIDGE,
	}

	if err := netlink.LinkAdd(waLink); err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("failed to create workaround interface: %w", err)
	}

	m.log.V(1).Info("created ipvlan workaround interface", "interface", waName)
	return nil
}

// getDefaultGatewayInterface returns the interface associated with the default route in the host namespace.
func (m *Manager) getDefaultGatewayInterface() (string, error) {
	// Switch to host namespace
	runtime, err := netns.Get()
	if err != nil {
		return "", fmt.Errorf("failed to get current namespace: %w", err)
	}
	defer runtime.Close()

	if err := netns.Set(m.hostNs); err != nil {
		return "", fmt.Errorf("failed to switch to host namespace: %w", err)
	}
	defer netns.Set(runtime)

	// Get default routes
	routes, err := netlink.RouteList(nil, unix.AF_INET)
	if err != nil {
		return "", fmt.Errorf("failed to list routes: %w", err)
	}

	// Find the default route
	for _, route := range routes {
		if route.Dst == nil || route.Dst.String() == "0.0.0.0/0" {
			if route.LinkIndex > 0 {
				link, err := netlink.LinkByIndex(route.LinkIndex)
				if err != nil {
					continue
				}
				return link.Attrs().Name, nil
			}
		}
	}

	return "", fmt.Errorf("no default gateway interface found")
}

// WaitForInterface waits for a network interface to be ready.
func WaitForInterface(ctx context.Context, ifName string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for interface %s: %w", ifName, ctx.Err())
		case <-ticker.C:
			// Check if interface exists and is up
			link, err := netlink.LinkByName(ifName)
			if err != nil {
				continue
			}
			if link.Attrs().Flags&net.FlagUp != 0 {
				return nil
			}
		}
	}
}
