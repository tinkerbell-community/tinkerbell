package docker

import (
	"bytes"
	"encoding/binary"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/google/go-cmp/cmp"
	"github.com/moby/moby/api/pkg/stdcopy"
)

// recorder collects what a logr.Logger logs, as "prefix: message args".
type recorder struct {
	mu    sync.Mutex
	lines []string
}

func (r *recorder) logger() logr.Logger {
	return funcr.New(func(prefix, args string) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.lines = append(r.lines, strings.TrimSpace(prefix+" "+args))
	}, funcr.Options{})
}

func (r *recorder) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}

func TestLineLogger(t *testing.T) {
	var r recorder
	l := &lineLogger{log: r.logger(), keysAndValues: []any{"stream", "stdout"}}
	for _, w := range []string{"first line\nsec", "ond line\r\n\n", "unfinished"} {
		if n, err := l.Write([]byte(w)); err != nil || n != len(w) {
			t.Fatalf("Write(%q) = %d, %v", w, n, err)
		}
	}
	l.Flush()
	want := []string{
		`"level"=0 "msg"="first line" "stream"="stdout"`,
		`"level"=0 "msg"="second line" "stream"="stdout"`,
		`"level"=0 "msg"="unfinished" "stream"="stdout"`,
	}
	if d := cmp.Diff(want, r.get()); d != "" {
		t.Errorf("logged lines (-want +got):\n%s", d)
	}
}

func TestLineLoggerLongLine(t *testing.T) {
	var r recorder
	l := &lineLogger{log: r.logger()}
	if _, err := l.Write(bytes.Repeat([]byte("x"), maxLineLength+10)); err != nil {
		t.Fatal(err)
	}
	if got := len(r.get()); got != 1 {
		t.Fatalf("a line of %d bytes without a newline logged %d times, want once", maxLineLength+10, got)
	}
}

// TestLogOutput demultiplexes stdout and stderr the way Docker sends them for a container
// without a TTY.
func TestLogOutput(t *testing.T) {
	var stream bytes.Buffer
	stream.Write(frame(stdcopy.Stdout, "to stdout\n"))
	stream.Write(frame(stdcopy.Stderr, "to stderr"))
	var r recorder
	done := make(chan struct{})
	logOutput(r.logger(), &stream, "action", done)
	<-done
	want := []string{
		`"level"=0 "msg"="to stdout" "container_name"="action" "stream"="stdout"`,
		`"level"=0 "msg"="to stderr" "container_name"="action" "stream"="stderr"`,
	}
	if d := cmp.Diff(want, r.get()); d != "" {
		t.Errorf("logged lines (-want +got):\n%s", d)
	}
}

// frame returns payload as Docker frames it in a multiplexed stream: an 8-byte header holding
// the stream and the payload's length, then the payload.
func frame(stream stdcopy.StdType, payload string) []byte {
	b := make([]byte, 8+len(payload))
	b[0] = byte(stream)
	binary.BigEndian.PutUint32(b[4:8], uint32(len(payload)))
	copy(b[8:], payload)
	return b
}

// TestLogOutputClosed checks that a stream closed under the reader, as Execute closes it once
// it stops waiting, is not logged as an error.
func TestLogOutputClosed(t *testing.T) {
	l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	var r recorder
	done := make(chan struct{})
	go logOutput(r.logger(), conn, "action", done)
	_ = conn.Close()
	<-done
	if got := r.get(); len(got) != 0 {
		t.Errorf("logged %q, want nothing", got)
	}
}
