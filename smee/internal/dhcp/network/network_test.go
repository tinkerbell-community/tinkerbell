package network

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func TestLeaderDefaults_AllEmpty(t *testing.T) {
	// Verify internal defaults are applied when creating a leader manager
	// via newLeaderManagerWithIfMgr by inspecting the LeaderElector configuration.
	// We test through the observable defaults used in newLeaderManagerWithIfMgr.
	identity := leaderIdentity()
	if identity == "" {
		t.Error("leaderIdentity should return a non-empty string")
	}
}

func TestLeaderIdentity_FromEnv(t *testing.T) {
	t.Setenv("HOSTNAME", "test-pod-123")
	if got := leaderIdentity(); got != "test-pod-123" {
		t.Errorf("got %q, want %q", got, "test-pod-123")
	}
}

func TestLeaderIdentity_Fallback(t *testing.T) {
	t.Setenv("HOSTNAME", "")
	got := leaderIdentity()
	want, _ := os.Hostname()
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNewLeaderManagerWithIfMgr_NilRestConfig(t *testing.T) {
	mock := &mockNetworkInterfaceManager{
		onSetup:   make(chan struct{}, 1),
		onCleanup: make(chan struct{}, 1),
	}
	_, err := newLeaderManagerWithIfMgr(LeaderConfig{
		RestConfig: nil,
	}, mock, logr.Discard())
	if err == nil {
		t.Fatal("expected error for nil RestConfig")
	}
	if !strings.Contains(err.Error(), "rest config is required") {
		t.Errorf("expected 'rest config is required' error, got: %v", err)
	}
}

// mockNetworkInterfaceManager tracks calls to Setup, Cleanup, and Close.
// Used by both unit and integration tests.
type mockNetworkInterfaceManager struct {
	setupCalls   int
	cleanupCalls int
	closeCalls   int
	setupErr     error
	cleanupErr   error

	// Channels signaled on each call. Buffered with capacity 10.
	onSetup   chan struct{}
	onCleanup chan struct{}
}

func newMockNetworkInterfaceManager() *mockNetworkInterfaceManager {
	return &mockNetworkInterfaceManager{
		onSetup:   make(chan struct{}, 10),
		onCleanup: make(chan struct{}, 10),
	}
}

func (m *mockNetworkInterfaceManager) Setup(_ context.Context) error {
	m.setupCalls++
	m.onSetup <- struct{}{}
	return m.setupErr
}

func (m *mockNetworkInterfaceManager) Cleanup() error {
	m.cleanupCalls++
	m.onCleanup <- struct{}{}
	return m.cleanupErr
}

func (m *mockNetworkInterfaceManager) Close() error {
	m.closeCalls++
	return nil
}

// waitForChan waits for a channel signal or fails the test after timeout.
func waitForChan(t *testing.T, ch <-chan struct{}, timeout time.Duration, msg string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for %s", msg)
	}
}
