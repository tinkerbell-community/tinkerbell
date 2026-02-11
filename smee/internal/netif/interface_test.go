package netif

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewManager(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "disabled manager",
			config: Config{
				Enabled: false,
				Logger:  logr.Discard(),
			},
			wantErr: false,
		},
		{
			name: "invalid interface type",
			config: Config{
				Enabled:       true,
				InterfaceType: "invalid",
				Logger:        logr.Discard(),
			},
			wantErr: true,
		},
		{
			name: "valid macvlan config",
			config: Config{
				Enabled:       true,
				InterfaceType: InterfaceTypeMacvlan,
				SrcInterface:  "lo", // loopback always exists
				Logger:        logr.Discard(),
			},
			wantErr: false,
		},
		{
			name: "valid ipvlan config",
			config: Config{
				Enabled:       true,
				InterfaceType: InterfaceTypeIPvlan,
				SrcInterface:  "lo",
				Logger:        logr.Discard(),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, err := NewManager(tt.config)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			if mgr != nil && mgr.hostNs != 0 {
				defer mgr.Close()
			}
		})
	}
}

func TestManager_Start_Disabled(t *testing.T) {
	mgr, err := NewManager(Config{
		Enabled: false,
		Logger:  logr.Discard(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start should return immediately when disabled
	err = mgr.Start(ctx)
	assert.NoError(t, err)
}

func TestInterfaceTypeConstants(t *testing.T) {
	assert.Equal(t, InterfaceType("macvlan"), InterfaceTypeMacvlan)
	assert.Equal(t, InterfaceType("ipvlan"), InterfaceTypeIPvlan)
}

func TestWaitForInterface(t *testing.T) {
	t.Run("timeout waiting for non-existent interface", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		err := WaitForInterface(ctx, "nonexistent-interface-12345", 200*time.Millisecond)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "timeout waiting for interface")
	})

	t.Run("loopback interface exists", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// loopback interface should always exist
		err := WaitForInterface(ctx, "lo", 1*time.Second)
		assert.NoError(t, err)
	})
}
