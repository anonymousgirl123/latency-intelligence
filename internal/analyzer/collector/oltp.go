package collector

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/kamini/latency-intelligence/internal/store"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// OTLPReceiver listens on a gRPC port and receives traces from the
// OpenTelemetry Collector or any OTLP-compatible exporter.
type OTLPReceiver struct {
	collectortrace.UnimplementedTraceServiceServer

	store  *store.ClickHouseStore
	port   string
	server *grpc.Server
}

func NewOTLPReceiver(st *store.ClickHouseStore, port string) *OTLPReceiver {
	return &OTLPReceiver{
		store: st,
		port:  port,
	}
}

// Start begins listening for OTLP trace exports.
func (r *OTLPReceiver) Start() error {
	lis, err := net.Listen("tcp", ":"+r.port)
	if err != nil {
		return fmt.Errorf("otlp listen on %s: %w", r.port, err)
	}

	r.server = grpc.NewServer()

	collectortrace.RegisterTraceServiceServer(r.server, r)
	reflection.Register(r.server)

	log.Printf("[otlp] Receiver listening on :%s (gRPC)", r.port)

	go func() {
		if err := r.server.Serve(lis); err != nil {
			log.Printf("[otlp] Server stopped: %v", err)
		}
	}()

	return nil
}

func (r *OTLPReceiver) Stop() {
	if r.server != nil {
		r.server.GracefulStop()
	}
}

// Export implements the OTLP TraceService — called for every batch of spans.
func (r *OTLPReceiver) Export(
	ctx context.Context,
	req *collectortrace.ExportTraceServiceRequest,
) (*collectortrace.ExportTraceServiceResponse, error) {

	var records []store.SpanRecord

	for _, resourceSpan := range req.ResourceSpans {

		serviceName := attrString(
			resourceSpan.Resource.GetAttributes(),
			"service.name",
		)

		environment := attrString(
			resourceSpan.Resource.GetAttributes(),
			"deployment.environment",
		)

		commitHash := attrString(
			resourceSpan.Resource.GetAttributes(),
			"git.commit.sha",
		)

		if environment == "" {
			environment = "unknown"
		}

		for _, scopeSpan := range resourceSpan.ScopeSpans {

			for _, span := range scopeSpan.Spans {

				method := resolveMethod(span)
				filePath := attrString(span.Attributes, "code.filepath")
				callType := resolveCallType(span)

				if method == "" {
					// skip spans we can't map to a method
					continue
				}

				durationMs := float64(
					span.EndTimeUnixNano-span.StartTimeUnixNano,
				) / 1e6

				records = append(records, store.SpanRecord{
					TraceID:     fmt.Sprintf("%x", span.TraceId),
					SpanID:      fmt.Sprintf("%x", span.SpanId),
					Method:      method,
					FilePath:    filePath,
					CallType:    callType,
					DurationMs:  durationMs,
					Environment: environment,
					CommitHash:  commitHash,
					ServiceName: serviceName,
					Timestamp: time.Unix(
						0,
						int64(span.StartTimeUnixNano),
					),
				})
			}
		}
	}

	if len(records) > 0 {
		if err := r.store.InsertSpans(ctx, records); err != nil {
			log.Printf("[otlp] Insert error: %v", err)

			// Don't return error — we don't want to block the OTel Collector
		} else {
			log.Printf("[otlp] Stored %d spans", len(records))
		}
	}

	return &collectortrace.ExportTraceServiceResponse{}, nil
}

// resolveMethod extracts a meaningful method name from a span.
// Priority: code.function attribute → span name → empty (skip)
func resolveMethod(span *tracepb.Span) string {

	if fn := attrString(span.Attributes, "code.function"); fn != "" {

		ns := attrString(span.Attributes, "code.namespace")

		if ns != "" {
			return ns + "." + fn
		}

		return fn
	}

	// Fall back to span name if it looks like a method
	if strings.Contains(span.Name, ".") {
		return span.Name
	}

	return ""
}

// resolveCallType infers the call type from span attributes
func resolveCallType(span *tracepb.Span) string {

	name := strings.ToLower(span.Name)

	switch {

	case span.Kind == tracepb.Span_SPAN_KIND_CLIENT &&
		attrString(span.Attributes, "db.system") != "":
		return "DB"

	case attrString(span.Attributes, "messaging.system") == "kafka":
		return "KAFKA"

	case attrString(span.Attributes, "db.system") == "redis":
		return "REDIS"

	case span.Kind == tracepb.Span_SPAN_KIND_CLIENT &&
		(attrString(span.Attributes, "http.method") != "" ||
			attrString(span.Attributes, "http.request.method") != ""):
		return "HTTP"

	case strings.Contains(name, "sleep"):
		return "SLEEP"

	default:
		return "INTERNAL"
	}
}

// attrString finds a string attribute value by key.
func attrString(attrs []*commonpb.KeyValue, key string) string {

	for _, a := range attrs {

		if a.Key == key {

			if sv := a.Value.GetStringValue(); sv != "" {
				return sv
			}
		}
	}

	return ""
}
