// Command grpc-echo is a tiny, self-contained gRPC server used by the
// end-to-end test stack to exercise the gRPC-transcoding plugin against a
// real gRPC backend.
//
// It deliberately avoids any protoc / code-generation step: the service
// schema is constructed at runtime from a hand-built FileDescriptorProto and
// served with dynamic message handlers. This keeps the binary buildable with
// nothing but the modules already vendored by the proxy, and mirrors exactly
// what the transcoding plugin learns over the wire.
//
// The server exposes everything the plugin requires of a real backend:
//
//   - gRPC server reflection (grpc.reflection.v1 / v1alpha) — so routes and
//     message schemas can be discovered, and
//   - gRPC health checking (grpc.health.v1.Health) — which the plugin probes
//     at startup before trusting a backend.
//
// Service: greeter.v1.Greeter
//
//	rpc SayHello(HelloRequest{name})       returns (HelloReply{message})
//	rpc Echo(EchoRequest{message})         returns (EchoReply{message, user_id})
//
// SayHello encodes a couple of gRPC error cases so the transcoder's status
// mapping can be verified end-to-end:
//
//	name == "error"   -> NOT_FOUND          (HTTP 404)
//	name == "invalid" -> INVALID_ARGUMENT   (HTTP 400)
//
// Echo reflects any forwarded x-auth-user-id gRPC metadata back into the
// response's user_id field, so header→metadata forwarding can be asserted.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const serviceName = "greeter.v1.Greeter"

// options controls which gRPC infrastructure services the backend exposes.
// The "no" toggles exist to drive the proxy's negative paths: the transcoding
// plugin refuses to use a backend that lacks health checking or reflection.
type options struct {
	addr         string
	noHealth     bool // omit grpc.health.v1.Health (default: registered, SERVING)
	noReflection bool // omit server reflection (default: registered)
}

func main() {
	opts := options{}
	flag.StringVar(&opts.addr, "addr", envOr("GRPC_ECHO_ADDR", ":50051"), "listen address (host:port)")
	flag.BoolVar(&opts.noHealth, "no-health", envBool("GRPC_ECHO_NO_HEALTH"), "do not register the gRPC health service")
	flag.BoolVar(&opts.noReflection, "no-reflection", envBool("GRPC_ECHO_NO_REFLECTION"), "do not register gRPC server reflection")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if err := run(opts, logger); err != nil {
		logger.Error("grpc-echo failed", "error", err)
		os.Exit(1)
	}
}

func run(opts options, logger *slog.Logger) error {
	files, err := buildFiles()
	if err != nil {
		return fmt.Errorf("build descriptors: %w", err)
	}

	// Register the file in the global registry so server reflection can resolve
	// and serve the descriptors to clients (the transcoding plugin).
	files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if _, lookupErr := protoregistry.GlobalFiles.FindFileByPath(fd.Path()); lookupErr != nil {
			_ = protoregistry.GlobalFiles.RegisterFile(fd)
		}
		return true
	})

	svcDesc, err := buildServiceDesc(files, logger)
	if err != nil {
		return fmt.Errorf("build service desc: %w", err)
	}

	lis, err := net.Listen("tcp", opts.addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", opts.addr, err)
	}

	srv := grpc.NewServer()
	srv.RegisterService(svcDesc, struct{}{})

	// Reflection: lets the proxy discover services, methods and schemas.
	// Omitted with -no-reflection to drive the proxy's "reflection absent" path.
	if !opts.noReflection {
		reflection.Register(srv)
	}

	// Health: the proxy refuses to use a backend that has no health service.
	// Omitted with -no-health to drive the proxy's "health absent" path.
	if !opts.noHealth {
		healthSrv := health.NewServer()
		healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
		healthSrv.SetServingStatus(serviceName, healthpb.HealthCheckResponse_SERVING)
		healthpb.RegisterHealthServer(srv, healthSrv)
	}

	logger.Info("grpc-echo listening",
		"address", lis.Addr().String(),
		"service", serviceName,
		"methods", "SayHello, Echo",
		"health", !opts.noHealth,
		"reflection", !opts.noReflection,
	)
	return srv.Serve(lis)
}

// buildServiceDesc assembles the grpc.ServiceDesc for greeter.v1.Greeter using
// dynamic handlers — no generated stubs required.
func buildServiceDesc(files *protoregistry.Files, logger *slog.Logger) (*grpc.ServiceDesc, error) {
	d, err := files.FindDescriptorByName(protoreflect.FullName(serviceName))
	if err != nil {
		return nil, fmt.Errorf("find service %s: %w", serviceName, err)
	}
	sd, ok := d.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("%s is not a service", serviceName)
	}

	return &grpc.ServiceDesc{
		ServiceName: serviceName,
		HandlerType: (*interface{})(nil),
		Methods: []grpc.MethodDesc{
			{MethodName: "SayHello", Handler: dynamicHandler(sd, "SayHello", sayHello, logger)},
			{MethodName: "Echo", Handler: dynamicHandler(sd, "Echo", echo, logger)},
		},
	}, nil
}

// logicFunc is the business logic of a single method: it receives the decoded
// request fields (as strings) and the incoming metadata, and returns the
// response fields or a gRPC status error.
type logicFunc func(req map[string]string, md metadata.MD) (map[string]string, error)

// dynamicHandler builds a unary gRPC handler that decodes the request into a
// dynamic message, runs the supplied logic, and encodes the response — all
// driven by the method's descriptor rather than generated code.
func dynamicHandler(
	sd protoreflect.ServiceDescriptor,
	method string,
	logic logicFunc,
	logger *slog.Logger,
) func(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	md := sd.Methods().ByName(protoreflect.Name(method))
	inputDesc := md.Input()
	outputDesc := md.Output()

	return func(_ interface{}, ctx context.Context, dec func(interface{}) error, _ grpc.UnaryServerInterceptor) (interface{}, error) {
		reqMsg := dynamicpb.NewMessage(inputDesc)
		if err := dec(reqMsg); err != nil {
			return nil, status.Errorf(codes.Internal, "decode request: %v", err)
		}

		reqFields := map[string]string{}
		reqMsg.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
			reqFields[string(fd.Name())] = v.String()
			return true
		})

		inMD, _ := metadata.FromIncomingContext(ctx)
		respFields, err := logic(reqFields, inMD)
		if err != nil {
			logger.Info("call rejected", "method", method, "error", err)
			return nil, err
		}

		respMsg := dynamicpb.NewMessage(outputDesc)
		for name, val := range respFields {
			fd := outputDesc.Fields().ByName(protoreflect.Name(name))
			if fd != nil {
				respMsg.Set(fd, protoreflect.ValueOfString(val))
			}
		}
		logger.Info("call ok", "method", method)
		return respMsg, nil
	}
}

// --- business logic ---

func sayHello(req map[string]string, _ metadata.MD) (map[string]string, error) {
	name := req["name"]
	switch name {
	case "error":
		return nil, status.Error(codes.NotFound, "user not found")
	case "invalid":
		return nil, status.Error(codes.InvalidArgument, "invalid name")
	}
	return map[string]string{"message": "Hello, " + name + "!"}, nil
}

func echo(req map[string]string, md metadata.MD) (map[string]string, error) {
	resp := map[string]string{"message": req["message"]}
	if ids := md.Get("x-auth-user-id"); len(ids) > 0 {
		resp["user_id"] = ids[0]
	}
	return resp, nil
}

// --- schema (hand-built FileDescriptorProto) ---

// buildFiles constructs and resolves the greeter.v1 file descriptor set.
func buildFiles() (*protoregistry.Files, error) {
	str := descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
	opt := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
	field := func(name string, num int32, json string) *descriptorpb.FieldDescriptorProto {
		return &descriptorpb.FieldDescriptorProto{
			Name: proto.String(name), Number: proto.Int32(num),
			Type: str, Label: opt, JsonName: proto.String(json),
		}
	}

	fdp := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("greeter/v1/greeter.proto"),
		Package: proto.String("greeter.v1"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: proto.String("HelloRequest"), Field: []*descriptorpb.FieldDescriptorProto{field("name", 1, "name")}},
			{Name: proto.String("HelloReply"), Field: []*descriptorpb.FieldDescriptorProto{field("message", 1, "message")}},
			{Name: proto.String("EchoRequest"), Field: []*descriptorpb.FieldDescriptorProto{field("message", 1, "message")}},
			{Name: proto.String("EchoReply"), Field: []*descriptorpb.FieldDescriptorProto{
				field("message", 1, "message"),
				field("user_id", 2, "userId"),
			}},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{
			{
				Name: proto.String("Greeter"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{Name: proto.String("SayHello"), InputType: proto.String(".greeter.v1.HelloRequest"), OutputType: proto.String(".greeter.v1.HelloReply")},
					{Name: proto.String("Echo"), InputType: proto.String(".greeter.v1.EchoRequest"), OutputType: proto.String(".greeter.v1.EchoReply")},
				},
			},
		},
	}

	return protodesc.NewFiles(&descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{fdp},
	})
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envBool(key string) bool {
	switch os.Getenv(key) {
	case "1", "true", "TRUE", "True", "yes":
		return true
	default:
		return false
	}
}
