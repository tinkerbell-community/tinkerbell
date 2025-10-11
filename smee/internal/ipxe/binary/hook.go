package binary

// PxeTemplate is the PXE extlinux.conf script template used to boot a machine via TFTP.
var PxeTemplate = `
default deploy

label deploy
kernel {{ .Kernel }}
append console=tty1 console=ttyAMA0,115200 loglevel=7 cgroup_enable=cpuset cgroup_memory=1 cgroup_enable=memory {{- if ne .VLANID "" }} vlan_id={{ .VLANID }} {{- end }} facility={{ .Facility }} syslog_host={{ .SyslogHost }} grpc_authority={{ .TinkGRPCAuthority }} tinkerbell_tls={{ .TinkerbellTLS }} tinkerbell_insecure_tls={{ .TinkerbellInsecureTLS }} worker_id={{ .WorkerID }} hw_addr={{ .HWAddr }} modules=loop,squashfs,sd-mod,usb-storage intel_iommu=on iommu=pt {{- range .ExtraKernelParams}} {{.}} {{- end}}
initrd {{ .Initrd }}
ipappend 2
`

// Hook holds the values used to generate the iPXE script that loads the Hook OS.
type Hook struct {
	Arch                  string   // example x86_64
	Console               string   // example ttyS1,115200
	DownloadURL           string   // example https://location:8080/to/kernel/and/initrd
	ExtraKernelParams     []string // example tink_worker_image=quay.io/tinkerbell/tink-worker:v0.8.0
	Facility              string
	HWAddr                string // example 3c:ec:ef:4c:4f:54
	SyslogHost            string
	TinkerbellTLS         bool
	TinkerbellInsecureTLS bool
	TinkGRPCAuthority     string // example 192.168.2.111:42113
	TraceID               string
	VLANID                string // string number between 1-4095
	WorkerID              string // example 3c:ec:ef:4c:4f:54 or worker1
	Retries               int    // number of retries to attempt when fetching kernel and initrd files
	RetryDelay            int    // number of seconds to wait between retries
	Kernel                string // name of the kernel file
	Initrd                string // name of the initrd file
}
