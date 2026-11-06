package templates

//go:generate go tool templ generate

import (
	"strconv"
	"strings"
)

// PageConfig holds common page configuration.
type PageConfig struct {
	BaseURL    string   // URL prefix for all routes (e.g., "/ui")
	Namespaces []string // Available namespaces
}

// NavItem represents a navigation menu item.
type NavItem struct {
	Name string
	Icon string
	Href string
}

// Hardware represents a hardware resource in the list view.
type Hardware struct {
	Name        string
	Namespace   string
	Description string
	MAC         string
	// IPv4Address and IPv6Address are the addresses of the Hardware's first
	// network interface, empty when it has none of that family.
	IPv4Address string
	IPv6Address string
	Status      string
	CreatedAt   string
}

// Workflow represents a workflow resource in the list view.
type Workflow struct {
	Name      string
	Namespace string
	// TemplateRef is the deprecated v1alpha1 Template of a Workflow created
	// through v1alpha1. Tasks are the Tasks the Workflow runs.
	TemplateRef string
	Tasks       []TaskRef
	State       string
	Disabled    bool
	// CanUpdate reports whether the current user has permission to enable
	// this Workflow. Checked once per list render, not per row.
	CanUpdate bool
	Task      string
	Action    string
	Agent     string
	CreatedAt string
}

// WorkflowRuleSet represents a workflowruleset resource in the list view.
type WorkflowRuleSet struct {
	Name        string
	Namespace   string
	Rules       string
	TemplateRef string
	CreatedAt   string
}

// Template represents a template resource in the list view.
type Template struct {
	Name      string
	Namespace string
	State     string
	Data      string
	CreatedAt string
}

// BMCMachine represents a BMC machine resource in the list view.
type BMCMachine struct {
	Name        string
	Namespace   string
	PowerState  string
	Contactable string
	Endpoint    string
	CreatedAt   string
}

// BMCJob represents a BMC job resource in the list view.
type BMCJob struct {
	Name        string
	Namespace   string
	MachineRef  string
	Status      string
	CompletedAt string
	CreatedAt   string
}

// BMCTask represents a BMC task resource in the list view.
type BMCTask struct {
	Name        string
	Namespace   string
	JobRef      string
	TaskType    string
	Status      string
	CompletedAt string
	CreatedAt   string
}

// Task represents a v1alpha2 Task in the list view.
type Task struct {
	Name      string
	Namespace string
	Actions   int
	// Template is the deprecated v1alpha1 Template the Task was translated
	// from, empty for other Tasks.
	Template  string
	CreatedAt string
}

// TaskPageData is the data for the Task list page.
type TaskPageData struct {
	Tasks      []Task
	Pagination PaginationData
}

// TaskAction is an Action of a Task, as shown on the Task detail page.
type TaskAction struct {
	Name    string
	Image   string
	Command string // the command followed by its arguments
	Timeout string
	// Details lists the Action's other settings (environment, volumes, ...), one per line.
	Details []string
}

// TaskDetail is the data for the Task detail page.
type TaskDetail struct {
	Name        string
	Namespace   string
	CreatedAt   string
	Labels      map[string]string
	Annotations map[string]string
	// Details describes the Task as a whole, including the Template it was translated from.
	Details    []InfoRow
	Actions    []TaskAction
	Env        []InfoRow
	Vars       []InfoRow
	References []InfoRow
	SpecYAML   string
	YAML       string
}

// Policy represents a v1alpha2 Policy in the list view.
type Policy struct {
	Name      string
	Namespace string
	// WorkflowRules is the number of workflowAutoCreation rules.
	WorkflowRules   int
	ReferenceAccess bool
	// RuleSet is the deprecated WorkflowRuleSet the Policy was translated
	// from, empty for other Policies.
	RuleSet   string
	CreatedAt string
}

// PolicyPageData is the data for the Policy list page.
type PolicyPageData struct {
	Policies   []Policy
	Pagination PaginationData
}

// TaskRef references a Task, as listed in a Workflow or Policy.
type TaskRef struct {
	Name      string
	Namespace string
	AgentID   string
	Timeout   string
	// Hardware is the Hardware the Task runs against, as "namespace/name".
	Hardware string
	// BootOptions describes the Task's boot options.
	BootOptions string
}

// PolicyWorkflowRule is a workflowAutoCreation rule of a Policy.
type PolicyWorkflowRule struct {
	// Patterns are the Agent attribute patterns, as JSON, any of which matches.
	Patterns []string
	// Config describes the Workflow the rule creates.
	Config []InfoRow
	Tasks  []TaskRef
}

// PolicyDetail is the data for the Policy detail page.
type PolicyDetail struct {
	Name        string
	Namespace   string
	CreatedAt   string
	Labels      map[string]string
	Annotations map[string]string
	Details     []InfoRow
	Rules       []PolicyWorkflowRule
	// ReferenceAccessYAML is spec.rules.referenceAccess as YAML.
	ReferenceAccessYAML string
	SpecYAML            string
	YAML                string
}

// Permission represents a Kubernetes RBAC permission for a Tinkerbell resource.
type Permission struct {
	Resource  string
	APIGroup  string
	Namespace string   // Namespace scope (empty means cluster-wide)
	Verbs     []string // Allowed verbs for this resource
}

// ResourceInfo represents a Tinkerbell resource for the permissions loading page.
type ResourceInfo struct {
	Resource string
	APIGroup string
}

// PaginationData holds pagination state for list views.
type PaginationData struct {
	CurrentPage  int
	TotalPages   int
	TotalItems   int
	ItemsPerPage int
	StartItem    int
	EndItem      int
	ResourcePath string // e.g., "/hardware", "/workflows", "/templates"
	TargetID     string // e.g., "#hardware-content", "#workflow-content"
}

// HardwarePageData is the data for the hardware list page.
type HardwarePageData struct {
	Hardware   []Hardware
	Pagination PaginationData
}

// WorkflowPageData is the data for the workflow list page.
type WorkflowPageData struct {
	Workflows  []Workflow
	Pagination PaginationData
}

// WorkflowRuleSetPageData is the data for the workflowruleset list page.
type WorkflowRuleSetPageData struct {
	RuleSets   []WorkflowRuleSet
	Pagination PaginationData
}

// TemplatePageData is the data for the template list page.
type TemplatePageData struct {
	Templates  []Template
	Pagination PaginationData
}

// BMCMachinePageData is the data for the BMC machine list page.
type BMCMachinePageData struct {
	Machines   []BMCMachine
	Pagination PaginationData
}

// BMCJobPageData is the data for the BMC job list page.
type BMCJobPageData struct {
	Jobs       []BMCJob
	Pagination PaginationData
}

// BMCTaskPageData is the data for the BMC task list page.
type BMCTaskPageData struct {
	Tasks      []BMCTask
	Pagination PaginationData
}

// HardwareInterface is a network interface of a Hardware: its MAC and the
// network configuration Tinkerbell serves for it.
type HardwareInterface struct {
	MAC string
	// IPv4Address and IPv6Address are the interface's IPAM addresses, empty
	// when it has none of that family.
	IPv4Address string
	IPv6Address string
	// IPv4 and IPv6 describe ipam.ipv4 and ipam.ipv6, DHCP the DHCP options and
	// Netboot the network boot options. Rows with no value are not shown.
	IPv4    []InfoRow
	IPv6    []InfoRow
	DHCP    []InfoRow
	Netboot []InfoRow
}

// Condition is a status condition of a resource.
type Condition struct {
	Type    string
	Status  string
	Reason  string
	Message string
	Time    string
}

// NamedText is a named block of text, such as cloud-init user data.
type NamedText struct {
	Name string
	Text string
}

// AgentProcessor represents a processor from agent attributes.
type AgentProcessor struct {
	ID           int      `json:"id"`
	Cores        int      `json:"cores"`
	Threads      int      `json:"threads"`
	Vendor       string   `json:"vendor"`
	Model        string   `json:"model"`
	Capabilities []string `json:"capabilities"`
}

// AgentCPU represents CPU information from agent attributes.
type AgentCPU struct {
	TotalCores   int              `json:"totalCores"`
	TotalThreads int              `json:"totalThreads"`
	Processors   []AgentProcessor `json:"processors"`
}

// AgentMemory represents memory information from agent attributes.
type AgentMemory struct {
	Total  string `json:"total"`
	Usable string `json:"usable"`
}

// AgentBlockDevice represents a block device from agent attributes.
type AgentBlockDevice struct {
	Name              string `json:"name"`
	Size              string `json:"size"`
	ControllerType    string `json:"controllerType"`
	DriveType         string `json:"driveType"`
	PhysicalBlockSize string `json:"physicalBlockSize"`
	Vendor            string `json:"vendor"`
	Model             string `json:"model"`
	WWN               string `json:"wwn"`
	SerialNumber      string `json:"serialNumber"`
	Path              string `json:"path"`
}

// AgentNetworkInterface represents a network interface from agent attributes.
type AgentNetworkInterface struct {
	Name                string              `json:"name"`
	MAC                 string              `json:"mac"`
	Speed               string              `json:"speed"`
	EnabledCapabilities []string            `json:"enabledCapabilities"`
	LLDPNeighbors       []AgentLLDPNeighbor `json:"lldpNeighbors"`
}

// AgentLLDPNeighbor represents a switch (or other LLDP-speaking device)
// discovered via LLDP on a network port.
type AgentLLDPNeighbor struct {
	ChassisID       string `json:"chassisID"`
	SystemName      string `json:"systemName"`
	PortID          string `json:"portID"`
	PortDescription string `json:"portDescription"`
}

// AgentPCIDevice represents a PCI device from agent attributes.
type AgentPCIDevice struct {
	Vendor  string `json:"vendor"`
	Product string `json:"product"`
	Class   string `json:"class"`
	Driver  string `json:"driver"`
}

// AgentGPUDevice represents a GPU device from agent attributes.
type AgentGPUDevice struct {
	Vendor  string `json:"vendor"`
	Product string `json:"product"`
	Class   string `json:"class"`
	Driver  string `json:"driver"`
}

// AgentChassis represents chassis information from agent attributes.
type AgentChassis struct {
	Serial string `json:"serial"`
	Vendor string `json:"vendor"`
}

// AgentBIOS represents BIOS information from agent attributes.
type AgentBIOS struct {
	Vendor      string `json:"vendor"`
	Version     string `json:"version"`
	ReleaseDate string `json:"releaseDate"`
}

// AgentBaseboard represents baseboard information from agent attributes.
type AgentBaseboard struct {
	Vendor       string `json:"vendor"`
	Product      string `json:"product"`
	Version      string `json:"version"`
	SerialNumber string `json:"serialNumber"`
}

// AgentProduct represents product information from agent attributes.
type AgentProduct struct {
	Name         string `json:"name"`
	Vendor       string `json:"vendor"`
	SerialNumber string `json:"serialNumber"`
}

// AgentAttributes is the shape the "In-Band Attributes" section of the
// Hardware detail page renders, built from Hardware.status.attributes.inBand
// by AgentAttributesFromInBand.
type AgentAttributes struct {
	CPU               AgentCPU                `json:"cpu"`
	Memory            AgentMemory             `json:"memory"`
	BlockDevices      []AgentBlockDevice      `json:"blockDevices"`
	NetworkInterfaces []AgentNetworkInterface `json:"networkInterfaces"`
	PCIDevices        []AgentPCIDevice        `json:"pciDevices"`
	GPUDevices        []AgentGPUDevice        `json:"gpuDevices"`
	Chassis           AgentChassis            `json:"chassis"`
	BIOS              AgentBIOS               `json:"bios"`
	Baseboard         AgentBaseboard          `json:"baseboard"`
	Product           AgentProduct            `json:"product"`
}

// HardwareDetail is the data for the hardware detail page.
type HardwareDetail struct {
	Name        string
	Namespace   string
	Interfaces  []HardwareInterface
	Status      string
	CreatedAt   string
	Labels      map[string]string
	Annotations map[string]string
	// Spec describes the Hardware as a whole: architecture, Agent, enrollment
	// and storage devices. References lists spec.references, one row each.
	Spec       []InfoRow
	References []InfoRow
	// Instance describes spec.instance; InstanceData holds its cloud-init
	// documents (user data, vendor data, meta-data, network config).
	Instance     []InfoRow
	InstanceData []NamedText
	// BMC describes spec.bmc, and the deprecated bmc Machine of Hardware created
	// through v1alpha1. BMCProviderOptions is the connection's provider options as YAML.
	BMC                []InfoRow
	BMCProviderOptions string
	Conditions         []Condition
	AgentAttributes    *AgentAttributes
	OutOfBand          *OutOfBandAttributes
	SpecYAML           string
	StatusYAML         string
	YAML               string
}

// OOBStatus is health/state info for a component, as reported out-of-band.
// Mirrors api/v1alpha2/tinkerbell/inventory.ComponentStatus, flattened to a value type
// for template convenience.
type OOBStatus struct {
	Health string
	State  string
	// PostCode is a pointer because 0 is a meaningful POST code (success).
	// Only meaningful on BIOS.
	PostCode       *int32
	PostCodeStatus string
}

// OOBComponent is a basic hardware component without specialized fields, e.g.
// a baseboard, chassis, or storage controller.
type OOBComponent struct {
	Vendor          string
	Model           string
	SerialNumber    string
	Description     string
	FirmwareVersion string
	Status          OOBStatus
}

// OOBProduct describes the overall system identity as reported out-of-band —
// the machine's own asset details, distinct from any individual component.
type OOBProduct struct {
	Name         string
	Vendor       string
	Model        string
	SerialNumber string
	Status       OOBStatus
}

// OOBBIOS describes the system BIOS firmware as reported out-of-band.
type OOBBIOS struct {
	Vendor          string
	Model           string
	SerialNumber    string
	FirmwareVersion string
	ReleaseDate     string
	Status          OOBStatus
}

// OOBBMC describes the BMC's own firmware and management NIC.
type OOBBMC struct {
	Vendor          string
	Model           string
	SerialNumber    string
	FirmwareVersion string
	NIC             *OOBNetworkInterface
	Status          OOBStatus
}

// OOBCPUSocket describes a single physical CPU as reported out-of-band.
type OOBCPUSocket struct {
	Slot            string
	Vendor          string
	Model           string
	Cores           uint32
	Threads         uint32
	ClockSpeedMHz   uint32
	SerialNumber    string
	FirmwareVersion string
}

// OOBMemoryModule describes a single memory module as reported out-of-band.
type OOBMemoryModule struct {
	Slot            string
	Vendor          string
	Model           string
	SerialNumber    string
	PartNumber      string
	SizeBytes       int64
	SpeedMHz        uint32
	FormFactor      string
	FirmwareVersion string
}

// OOBNetworkInterface describes a network adapter as reported out-of-band.
type OOBNetworkInterface struct {
	Vendor          string
	Model           string
	SerialNumber    string
	FirmwareVersion string
	Ports           []OOBNetworkPort
}

// OOBNetworkPort describes a single physical port on a network adapter.
type OOBNetworkPort struct {
	PortID     string
	MAC        string
	LinkStatus string
	SpeedMbps  uint32
	MTU        uint32
}

// MACAddresses joins every port's MAC address for display in a single table
// cell, since a NIC can have multiple physical ports.
func (n OOBNetworkInterface) MACAddresses() string {
	var macs []string
	for _, p := range n.Ports {
		if p.MAC != "" {
			macs = append(macs, p.MAC)
		}
	}
	return strings.Join(macs, ", ")
}

// PortSpeeds joins every port's speed (e.g. "25000 Mbps") for display in a
// single table cell, since a NIC can have multiple physical ports.
func (n OOBNetworkInterface) PortSpeeds() string {
	var speeds []string
	for _, p := range n.Ports {
		if p.SpeedMbps > 0 {
			speeds = append(speeds, strconv.Itoa(int(p.SpeedMbps))+" Mbps")
		}
	}
	return strings.Join(speeds, ", ")
}

// OOBBlockDevice describes a storage drive as reported out-of-band.
type OOBBlockDevice struct {
	ControllerType  string
	DriveType       string
	Vendor          string
	Model           string
	SerialNumber    string
	WWN             string
	SizeBytes       int64
	SmartStatus     string
	FirmwareVersion string
	Status          OOBStatus
}

// OOBPSU describes a power supply unit as reported out-of-band.
type OOBPSU struct {
	Vendor             string
	Model              string
	SerialNumber       string
	Description        string
	FirmwareVersion    string
	PowerCapacityWatts int64
	Status             OOBStatus
}

// OOBTPM describes a trusted platform module as reported out-of-band.
type OOBTPM struct {
	Vendor          string
	Model           string
	SerialNumber    string
	InterfaceType   string
	FirmwareVersion string
	Status          OOBStatus
}

// OOBGPU describes a GPU or accelerator device as reported out-of-band.
type OOBGPU struct {
	Vendor          string
	Model           string
	SerialNumber    string
	Description     string
	FirmwareVersion string
	Status          OOBStatus
}

// OutOfBandAttributes is hardware data collected out-of-band via the BMC,
// mirroring api/v1alpha2/tinkerbell/inventory.Attributes (the
// status.attributes.outOfBand subtree). Every field is optional: BMC
// vendors/protocols vary widely in what they report, so an empty field here
// reflects what the BMC/collection driver reports, not an error.
type OutOfBandAttributes struct {
	LastUpdated        string // pre-formatted, same convention as HardwareDetail.CreatedAt
	CollectionMethod   string
	TotalCores         uint32
	TotalThreads       uint32
	CPUs               []OOBCPUSocket
	TotalMemoryBytes   int64
	MemoryModules      []OOBMemoryModule
	Drives             []OOBBlockDevice
	NICs               []OOBNetworkInterface
	GPUs               []OOBGPU
	Chassis            OOBComponent
	Baseboard          OOBComponent
	BIOS               OOBBIOS
	Product            OOBProduct
	BMC                OOBBMC
	PSUs               []OOBPSU
	TPMs               []OOBTPM
	StorageControllers []OOBComponent
}

// WorkflowDetail is the data for the workflow detail page.
type WorkflowDetail struct {
	Name      string
	Namespace string
	// TemplateRef is the deprecated v1alpha1 Template of a Workflow created
	// through v1alpha1. CompatibilityMode reports that the Workflow runs it in
	// v1alpha1 compatibility mode rather than running Tasks.
	TemplateRef       string
	CompatibilityMode bool
	// Hardware lists the Hardware the Workflow's Tasks run against, as "namespace/name".
	Hardware []string
	State    string
	Disabled bool
	// CanUpdate reports whether the current user has permission to enable
	// this Workflow.
	CanUpdate         bool
	Task              string
	Action            string
	Agent             string
	TemplateRendering string
	CreatedAt         string
	Labels            map[string]string
	Annotations       map[string]string
	// Details describes the rest of the spec: timeout, template map, vars and references.
	Details       []InfoRow
	Tasks         []TaskRef
	RenderedTasks []RenderedTask
	Conditions    []Condition
	SpecYAML      string
	StatusYAML    string
	YAML          string
}

// RenderedTask is a Task as a Workflow runs it, from the Workflow's status.
type RenderedTask struct {
	Name    string
	AgentID string
	State   string
	Actions []RenderedAction
}

// RenderedAction is an Action of a RenderedTask, with its progress.
type RenderedAction struct {
	Name     string
	Image    string
	State    string
	Started  string
	Duration string
	Message  string
}

// TemplateDetail is the data for the template detail page.
type TemplateDetail struct {
	Name        string
	Namespace   string
	State       string
	Data        string
	CreatedAt   string
	Labels      map[string]string
	Annotations map[string]string
	SpecYAML    string
	StatusYAML  string
	YAML        string
}

// WorkflowRuleSetDetail is the data for the workflowruleset detail page.
type WorkflowRuleSetDetail struct {
	Name              string
	Namespace         string
	YAMLData          string
	CreatedAt         string
	Labels            map[string]string
	Annotations       map[string]string
	Rules             []string
	TemplateRef       string
	WorkflowNamespace string
	WorkflowDisabled  bool
	AddAttributes     bool
	AgentValue        string
}

// BMCMachineDetail is the data for the BMC machine detail page.
type BMCMachineDetail struct {
	Name        string
	Namespace   string
	PowerState  string
	Contactable string
	Endpoint    string
	CreatedAt   string
	Labels      map[string]string
	Annotations map[string]string
	SpecYAML    string
	StatusYAML  string
	YAML        string
}

// BMCJobDetail is the data for the BMC job detail page.
type BMCJobDetail struct {
	Name        string
	Namespace   string
	MachineRef  string
	Status      string
	CompletedAt string
	CreatedAt   string
	Labels      map[string]string
	Annotations map[string]string
	SpecYAML    string
	StatusYAML  string
	YAML        string
}

// BMCTaskDetail is the data for the BMC task detail page.
type BMCTaskDetail struct {
	Name        string
	Namespace   string
	JobRef      string
	TaskType    string
	Status      string
	CompletedAt string
	CreatedAt   string
	Labels      map[string]string
	Annotations map[string]string
	SpecYAML    string
	StatusYAML  string
	YAML        string
}

// InfoRow represents a single row in a NameValueTable.
type InfoRow struct {
	Name  string
	Value string
	Link  string // If set, Value is rendered as a hyperlink to this URL.
	Hide  bool
}

// HasValues reports whether NameValueTable shows any of rows.
func HasValues(rows []InfoRow) bool {
	for _, r := range rows {
		if !r.Hide && r.Value != "" {
			return true
		}
	}
	return false
}

// SchemaField represents a field in a CRD schema for display.
type SchemaField struct {
	Name        string        // Field name
	Type        string        // Field type (string, integer, boolean, object, array)
	Description string        // Field description from CRD
	Required    bool          // Whether the field is required
	Deprecated  bool          // Whether the field is deprecated
	Children    []SchemaField // Nested fields for objects/arrays
	Pattern     string        // Regex pattern for validation (if any)
	Enum        []string      // Allowed enum values (if any)
	Default     string        // Default value (if any)
	Format      string        // Format hint (date-time, int64, uri, etc.)
}

// CRDInfo represents a Custom Resource Definition for display on the dashboard.
type CRDInfo struct {
	Kind         string        // Resource kind (e.g., "Hardware")
	Plural       string        // Plural name (e.g., "hardware")
	Group        string        // API group (e.g., "tinkerbell.org")
	Version      string        // API version (e.g., "v1alpha1")
	Description  string        // CRD description
	Route        string        // Web UI route (empty if no UI exists)
	SpecFields   []SchemaField // Top-level spec fields
	StatusFields []SchemaField // Top-level status fields
}

// CRDGroup represents a group of CRDs for display.
type CRDGroup struct {
	Name string    // Group name (e.g., "tinkerbell.org")
	CRDs []CRDInfo // CRDs in this group
}

// DashboardData is the data for the landing page/dashboard.
type DashboardData struct {
	Groups            []CRDGroup // CRDs grouped by API group
	SelectedVersion   string     // Currently selected API version (e.g., "v1alpha1")
	AvailableVersions []string   // All available API versions (e.g., ["v1alpha1", "v1alpha2"])
	BaseURL           string     // URL prefix for HTMX requests
}
