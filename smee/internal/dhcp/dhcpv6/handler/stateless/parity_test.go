package stateless

import (
	"bytes"
	"context"
	"net"
	"testing"

	"github.com/insomniacslk/dhcp/dhcpv6"
	"github.com/insomniacslk/dhcp/iana"
	"github.com/tinkerbell/tinkerbell/api/v1alpha1/tinkerbell"
	v6 "github.com/tinkerbell/tinkerbell/smee/internal/dhcp/dhcpv6"
	"github.com/tinkerbell/tinkerbell/smee/internal/paritytest"
)

// TestReplyParity answers Information-Requests for every v1alpha1 fixture
// through the handler, which reads the v1alpha2 form, and compares the reply
// with the one built from the data Smee read from the v1alpha1 form before.
func TestReplyParity(t *testing.T) {
	peer := &net.UDPAddr{IP: net.ParseIP("fe80::1"), Port: dhcpv6.DefaultClientPort}
	for _, f := range paritytest.Fixtures() {
		for _, mac := range f.MACs() {
			for name, userClass := range map[string][]byte{"PXE": nil, "iPXE": []byte("iPXE")} {
				t.Run(f.Name+"/"+mac.String()+"/"+name, func(t *testing.T) {
					h := newHandler(t, map[string]*tinkerbell.Hardware{mac.String(): f.Hardware})
					msg := messageWithMAC(t, mac, dhcpv6.MessageTypeInformationRequest, userClass, iana.EFI_X86_64)
					requestBootURL(msg)
					requestStatelessConfig(msg)

					conn := &recordingPacketConn{}
					h.Handle(context.Background(), conn, peer, msg)

					want := v1alpha1Reply(t, h, peer, msg, mac, f.Hardware)
					if want == nil {
						if len(conn.writes) != 0 {
							t.Fatalf("got %d replies, want none", len(conn.writes))
						}
						return
					}
					got := requireSingleMessageReply(t, conn)
					if !bytes.Equal(got.ToBytes(), want.ToBytes()) {
						t.Errorf("reply differs:\nv1alpha1: %s\nv1alpha2: %s", want.Summary(), got.Summary())
					}
				})
			}
		}
	}
}

// v1alpha1Reply is the reply Handle built from the v1alpha1 data, or nil when
// it did not answer.
func v1alpha1Reply(t *testing.T, h *Handler, peer net.Addr, msg *dhcpv6.Message, mac net.HardwareAddr, hw *tinkerbell.Hardware) *dhcpv6.Message {
	t.Helper()
	old, err := paritytest.ConvertByMac(mac, hw)
	if err != nil {
		t.Fatalf("v1alpha1 conversion: %v", err)
	}
	if old.DHCP.Disabled {
		return nil
	}
	i, err := v6.NewInfo(peer, msg, h.infoOptions...)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := dhcpv6.NewReplyFromMessage(i.Msg, dhcpv6.WithServerID(h.serverID), dhcpv6.WithInformationRefreshTime(h.informationRefreshTime))
	if err != nil {
		t.Fatal(err)
	}
	bootURL, err := h.bootURLSource.BootURL(i, old.Netboot, "")
	if err != nil {
		return nil
	}
	v6.ApplyBootOptions(reply, i, bootURL)
	v6.ApplyRequestedStatelessOptions(reply, i, old.DHCP, h.dnsDefaults)
	return reply
}
