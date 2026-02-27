package hook

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/tinkerbell/tinkerbell/pkg/tftp/handler"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/file"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
)

// rpiPrefixPattern matches Raspberry Pi netboot path prefixes: <serial-or-mac>/
// Raspberry Pi firmware prepends the serial number (8-12 hex chars) or MAC address (dash-separated)
// to all TFTP file requests during netboot.
var rpiPrefixPattern = regexp.MustCompile(`^([0-9a-fA-F]{8,12}|[0-9a-fA-F]{2}-[0-9a-fA-F]{2}-[0-9a-fA-F]{2}-[0-9a-fA-F]{2}-[0-9a-fA-F]{2}-[0-9a-fA-F]{2})/{1,2}(.+)$`)

const (
	// DefaultURLPrefix is the default URI path prefix for all hook file requests.
	DefaultURLPrefix = "/images/"
)

// Config holds the configuration for the hook service
type Config struct {
	DebugMode bool
	URLPrefix string
	// ImagePath is the directory where hook images are stored
	ImagePath string
	// OCIRegistry is the OCI registry URL (e.g., "ghcr.io")
	OCIRegistry string
	// OCIRepository is the repository path (e.g., "tinkerbell/hook")
	OCIRepository string
	// OCIReference is the image tag or digest (e.g., "latest", "v1.2.3", "sha256:...")
	OCIReference string
	// OCIUsername is the optional username for OCI registry authentication
	OCIUsername string
	// OCIPassword is the optional password for OCI registry authentication
	OCIPassword string
	// PullTimeout for pulling OCI images
	PullTimeout time.Duration
}

// Option functions for configuring the hook service
type Option func(*Config)

func WithURLPrefix(prefix string) Option {
	return func(c *Config) {
		c.URLPrefix = prefix
	}
}

func WithImagePath(path string) Option {
	return func(c *Config) {
		c.ImagePath = path
	}
}

func WithOCIRegistry(registry string) Option {
	return func(c *Config) {
		c.OCIRegistry = registry
	}
}

func WithOCIRepository(repository string) Option {
	return func(c *Config) {
		c.OCIRepository = repository
	}
}

func WithOCIReference(reference string) Option {
	return func(c *Config) {
		c.OCIReference = reference
	}
}

func WithPullTimeout(timeout time.Duration) Option {
	return func(c *Config) {
		c.PullTimeout = timeout
	}
}

func WithOCIUsername(username string) Option {
	return func(c *Config) {
		c.OCIUsername = username
	}
}

func WithOCIPassword(password string) Option {
	return func(c *Config) {
		c.OCIPassword = password
	}
}

// NewConfig creates a new hook service configuration with defaults
func NewConfig(opts ...Option) *Config {
	defaults := &Config{
		DebugMode: false,
		URLPrefix: DefaultURLPrefix,

		ImagePath:     "/var/lib/hook",
		OCIRegistry:   "ghcr.io",
		OCIRepository: "tinkerbell/hook",
		OCIReference:  "latest",
		PullTimeout:   10 * time.Minute,
	}

	for _, opt := range opts {
		opt(defaults)
	}

	return defaults
}

// service manages hook image downloads and serving
type service struct {
	config     *Config
	log        logr.Logger
	pullOnce   sync.Once
	mutex      sync.RWMutex
	ready      bool
	httpServer *http.Server
}

// Handler returns an http.Handler that serves Hook OS files from the filesystem.
// It strips the URLPrefix from incoming request paths so the FileServer resolves
// files relative to ImagePath (e.g. /images/vmlinuz-x86_64 → <ImagePath>/vmlinuz-x86_64).
func (c *Config) Handler(log logr.Logger) (http.Handler, error) {
	return http.StripPrefix(c.URLPrefix, http.FileServerFS(os.DirFS(c.ImagePath))), nil
}

// TFTPHandler returns a TFTP handler that serves Hook OS files from the filesystem.
func (c *Config) TFTPHandler(log logr.Logger) handler.Handler {
	return &tftpHandler{
		log:      log,
		cacheDir: c.ImagePath,
	}
}

// tftpHandler serves hook files over TFTP.
type tftpHandler struct {
	log      logr.Logger
	cacheDir string
}

func (h *tftpHandler) ServeTFTP(filename string, rf io.ReaderFrom) error {
	log := h.log.WithValues("event", "hook_file", "filename", filename)
	log.Info("handling hook file request")

	if h.cacheDir == "" {
		return fmt.Errorf("cache directory not configured")
	}

	tracer := otel.Tracer("TFTP-Hook")
	_, span := tracer.Start(context.Background(), "TFTP hook file serve",
		trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()

	// Strip Raspberry Pi netboot prefix (serial or MAC) from the filename.
	// RPi firmware prepends its serial/MAC to all TFTP requests during netboot,
	// e.g. "dc-a6-32-01-02-03/vmlinuz-aarch64" or "abcdef01/initramfs-aarch64".
	cleanName := filename
	if matches := rpiPrefixPattern.FindStringSubmatch(filename); matches != nil {
		cleanName = matches[2]
		log.Info("stripped RPi netboot prefix from filename", "original", filename, "cleaned", cleanName)
	}

	filePath := filepath.Join(h.cacheDir, cleanName)

	// Security check - ensure the file is within the configured directory
	if !strings.HasPrefix(filepath.Clean(filePath), filepath.Clean(h.cacheDir)) {
		err := fmt.Errorf("invalid file path: %s", filename)
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	f, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			err := fmt.Errorf("hook file not found: %s", filename)
			span.SetStatus(codes.Error, err.Error())
			return err
		}
		log.Error(err, "failed to open hook file from filesystem")
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to open hook file from filesystem: %w", err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		log.Error(err, "failed to stat hook file")
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to stat hook file: %w", err)
	}

	if transfer, ok := rf.(interface{ SetSize(int64) }); ok {
		transfer.SetSize(fi.Size())
	}

	n, err := rf.ReadFrom(f)
	if err != nil {
		log.Error(err, "failed to serve hook file content")
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("failed to serve hook file content: %w", err)
	}

	log.Info("successfully served hook file", "filename", filename, "bytes_transferred", n)
	span.SetStatus(codes.Ok, "file served successfully")
	return nil
}

// Start initializes and starts the hook service
func (c *Config) Start(ctx context.Context, log logr.Logger) error {
	log.Info("starting hook service",
		"ociRegistry", c.OCIRegistry,
		"ociRepository", c.OCIRepository,
		"ociReference", c.OCIReference,
		"imagePath", c.ImagePath)

	svc := &service{
		config: c,
		log:    log,
	}

	// Create image directory
	if err := os.MkdirAll(c.ImagePath, 0o755); err != nil {
		return fmt.Errorf("failed to create image directory: %w", err)
	}

	// Check if ImagePath has any files
	if svc.imagePathHasFiles() {
		log.Info("image path contains files, skipping OCI pull")
		svc.ready = true
	} else {
		log.Info("image path is empty, will pull OCI image in background")
	}

	// Start background pull if needed
	go func() {
		if !svc.ready {
			if err := svc.pullOCIImage(ctx); err != nil {
				log.Error(err, "failed to pull OCI image")
			} else {
				svc.mutex.Lock()
				svc.ready = true
				svc.mutex.Unlock()
				log.Info("OCI image pulled and ready")
			}
		}
	}()

	// If HTTP server is not enabled, just wait for context cancellation
	<-ctx.Done()
	return nil
}

// imagePathHasFiles checks if the ImagePath directory contains any files
func (s *service) imagePathHasFiles() bool {
	entries, err := os.ReadDir(s.config.ImagePath)
	if err != nil {
		s.log.Info("unable to read image path", "error", err)
		return false
	}

	// Check if there are any files (not just directories)
	for _, entry := range entries {
		if !entry.IsDir() {
			return true
		}
	}

	return false
}

// pullOCIImage pulls the OCI image from the registry and extracts it to ImagePath
func (s *service) pullOCIImage(ctx context.Context) error {
	var err error
	s.pullOnce.Do(func() {
		err = s.doPullOCIImage(ctx)
		if err != nil {
			s.log.Error(err, "failed to pull OCI image")
		}
	})
	return err
}

// doPullOCIImage performs the actual OCI image pull
func (s *service) doPullOCIImage(ctx context.Context) error {
	log := s.log.WithValues("event", "pull_oci_image",
		"registry", s.config.OCIRegistry,
		"repository", s.config.OCIRepository,
		"reference", s.config.OCIReference,
		"imagePath", s.config.ImagePath)

	log.Info("pulling OCI image")

	// Create a timeout context for the pull operation
	pullCtx, cancel := context.WithTimeout(ctx, s.config.PullTimeout)
	defer cancel()

	// Create a file store for the extracted files
	fileStore, err := file.New(s.config.ImagePath)
	if err != nil {
		return fmt.Errorf("failed to create file store: %w", err)
	}
	defer fileStore.Close()

	// Create a remote repository
	repo, err := remote.NewRepository(fmt.Sprintf("%s/%s", s.config.OCIRegistry, s.config.OCIRepository))
	if err != nil {
		return fmt.Errorf("failed to create repository: %w", err)
	}

	log.Info("using authenticated registry access")
	authClient := auth.Client{
		Client: &http.Client{
			Timeout: s.config.PullTimeout,
		},
		Cache: auth.NewCache(),
		Credential: auth.StaticCredential(s.config.OCIRegistry, auth.Credential{
			Username: s.config.OCIUsername,
			Password: s.config.OCIPassword,
		}),
	}

	// Configure authentication only if credentials are provided
	if s.config.OCIUsername != "" || s.config.OCIPassword != "" {
		authClient.Credential = auth.StaticCredential(s.config.OCIRegistry, auth.Credential{
			Username: s.config.OCIUsername,
			Password: s.config.OCIPassword,
		})
	}

	repo.Client = &authClient

	// Copy from remote repository to local file store
	reference := s.config.OCIReference
	log.Info("copying OCI image to local file store", "reference", reference)

	desc, err := oras.Copy(pullCtx, repo, reference, fileStore, reference, oras.DefaultCopyOptions)
	if err != nil {
		return fmt.Errorf("failed to pull OCI image: %w", err)
	}

	log.Info("OCI image pulled successfully",
		"digest", desc.Digest.String(),
		"size", desc.Size,
		"mediaType", desc.MediaType)

	return nil
}
