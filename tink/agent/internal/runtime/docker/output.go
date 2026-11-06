package docker

import (
	"bytes"
	"errors"
	"io"
	"net"
	"time"

	"github.com/go-logr/logr"
	"github.com/moby/moby/api/pkg/stdcopy"
)

// outputDrainTimeout bounds how long Execute waits, once the container has exited, for the
// rest of its output. Docker ends the stream right after the container's output is flushed.
const outputDrainTimeout = 5 * time.Second

// maxLineLength is the longest line lineLogger buffers before logging it unfinished, so output
// that never ends a line (a progress bar redrawn with carriage returns) still shows.
const maxLineLength = 64 << 10

// logOutput logs a container's stdout and stderr, which the attach stream multiplexes because
// the container has no TTY, one line at a time, until the stream ends. It closes done when
// it returns.
func logOutput(log logr.Logger, stream io.Reader, containerName string, done chan<- struct{}) {
	defer close(done)
	stdout := &lineLogger{log: log, keysAndValues: []any{"container_name", containerName, "stream", "stdout"}}
	stderr := &lineLogger{log: log, keysAndValues: []any{"container_name", containerName, "stream", "stderr"}}
	_, err := stdcopy.StdCopy(stdout, stderr, stream)
	stdout.Flush()
	stderr.Flush()
	// The stream is closed under the reader when Execute stops waiting for it.
	if err != nil && !errors.Is(err, net.ErrClosed) {
		log.Error(err, "error reading container output", "container_name", containerName)
	}
}

// waitOutput waits until done is closed or outputDrainTimeout has passed.
func waitOutput(done <-chan struct{}) {
	t := time.NewTimer(outputDrainTimeout)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	}
}

// lineLogger is an io.Writer that logs each line written to it.
type lineLogger struct {
	log           logr.Logger
	keysAndValues []any
	buf           []byte
}

func (l *lineLogger) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		l.emit(l.buf[:i])
		l.buf = l.buf[i+1:]
	}
	if len(l.buf) >= maxLineLength {
		l.emit(l.buf)
		l.buf = l.buf[:0]
	}
	return len(p), nil
}

// Flush logs what is left of an unfinished line.
func (l *lineLogger) Flush() {
	l.emit(l.buf)
	l.buf = l.buf[:0]
}

func (l *lineLogger) emit(line []byte) {
	line = bytes.TrimRight(line, "\r")
	if len(line) == 0 {
		return
	}
	l.log.Info(string(line), l.keysAndValues...)
}
