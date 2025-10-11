package firmware

import (
	"context"
	"io"

	"github.com/go-logr/logr"
	"github.com/tinkerbell/tinkerbell/smee/internal/firmware"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// NewHandler creates a new firmware file handler.
func NewHandler(log logr.Logger) func(filename string, rf io.ReaderFrom) error {
	return func(filename string, rf io.ReaderFrom) error {
		return handleFirmware(filename, rf, log)
	}
}

func handleFirmware(filename string, rf io.ReaderFrom, log logr.Logger) error {
	log = log.WithValues("event", "firmware", "filename", filename)
	log.Info("handling firmware file request")

	// Create tracing context
	tracer := otel.Tracer("TFTP-Firmware")
	_, span := tracer.Start(context.Background(), "TFTP firmware serve",
		trace.WithSpanKind(trace.SpanKindServer))
	defer span.End()

	// Use firmware package's optimized HandleRead which streams directly
	err := firmware.HandleRead(filename, rf)
	if err != nil {
		log.Error(err, "failed to handle firmware read")
		span.SetStatus(codes.Error, err.Error())
		return err
	}

	log.Info("successfully served firmware file")
	span.SetStatus(codes.Ok, "firmware file served")
	return nil
}
