package grpctranscode

import (
	"context"
	"fmt"
	"io"
	"strings"

	"google.golang.org/grpc"
	rpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

// discoveredMethod holds information about a single gRPC method discovered via reflection.
type discoveredMethod struct {
	fullMethod string // "pkg.Service/Method"
	inputDesc  protoreflect.MessageDescriptor
	outputDesc protoreflect.MessageDescriptor
	methodDesc protoreflect.MethodDescriptor
}

// discoverServices uses gRPC server reflection to list all services on a connection
// and return their method descriptors.
func discoverServices(ctx context.Context, conn *grpc.ClientConn) ([]discoveredMethod, error) {
	client := rpb.NewServerReflectionClient(conn)
	stream, err := client.ServerReflectionInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("open reflection stream: %w", err)
	}
	defer func() { _ = stream.CloseSend() }()

	// List services.
	if err := stream.Send(&rpb.ServerReflectionRequest{
		MessageRequest: &rpb.ServerReflectionRequest_ListServices{ListServices: ""},
	}); err != nil {
		return nil, fmt.Errorf("send list services: %w", err)
	}
	resp, err := stream.Recv()
	if err != nil {
		return nil, fmt.Errorf("recv list services: %w", err)
	}
	listResp := resp.GetListServicesResponse()
	if listResp == nil {
		return nil, fmt.Errorf("unexpected reflection response: %v", resp)
	}

	// Collect all file descriptors.
	fileDescMap := map[string]*descriptorpb.FileDescriptorProto{}
	for _, svc := range listResp.Service {
		if svc.Name == "grpc.reflection.v1alpha.ServerReflection" ||
			svc.Name == "grpc.reflection.v1.ServerReflection" ||
			svc.Name == "grpc.health.v1.Health" {
			continue
		}
		if err := fetchFileDescriptors(stream, svc.Name, fileDescMap); err != nil {
			return nil, fmt.Errorf("fetch descriptors for %s: %w", svc.Name, err)
		}
	}

	// Build a FileDescriptorSet and resolve it.
	fdSet := &descriptorpb.FileDescriptorSet{}
	for _, fd := range fileDescMap {
		fdSet.File = append(fdSet.File, fd)
	}
	files, err := protodesc.NewFiles(fdSet)
	if err != nil {
		return nil, fmt.Errorf("resolve descriptors: %w", err)
	}

	// Extract methods from non-infrastructure services.
	var methods []discoveredMethod
	files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		for i := 0; i < fd.Services().Len(); i++ {
			sd := fd.Services().Get(i)
			svcName := string(sd.FullName())
			if strings.HasPrefix(svcName, "grpc.reflection.") || svcName == "grpc.health.v1.Health" {
				continue
			}
			for j := 0; j < sd.Methods().Len(); j++ {
				md := sd.Methods().Get(j)
				// Skip streaming methods — v1 is unary only.
				if md.IsStreamingClient() || md.IsStreamingServer() {
					continue
				}
				methods = append(methods, discoveredMethod{
					fullMethod: svcName + "/" + string(md.Name()),
					inputDesc:  md.Input(),
					outputDesc: md.Output(),
					methodDesc: md,
				})
			}
		}
		return true
	})

	return methods, nil
}

// fetchFileDescriptors recursively fetches file descriptors for a service symbol.
func fetchFileDescriptors(stream rpb.ServerReflection_ServerReflectionInfoClient, symbol string, collected map[string]*descriptorpb.FileDescriptorProto) error {
	if err := stream.Send(&rpb.ServerReflectionRequest{
		MessageRequest: &rpb.ServerReflectionRequest_FileContainingSymbol{
			FileContainingSymbol: symbol,
		},
	}); err != nil {
		return err
	}
	resp, err := stream.Recv()
	if err != nil {
		return err
	}
	fdResp := resp.GetFileDescriptorResponse()
	if fdResp == nil {
		if errResp := resp.GetErrorResponse(); errResp != nil {
			return fmt.Errorf("reflection error for %s: %s", symbol, errResp.ErrorMessage)
		}
		return fmt.Errorf("unexpected response for %s", symbol)
	}
	for _, raw := range fdResp.FileDescriptorProto {
		fd := &descriptorpb.FileDescriptorProto{}
		if err := proto.Unmarshal(raw, fd); err != nil {
			return fmt.Errorf("unmarshal file descriptor: %w", err)
		}
		name := fd.GetName()
		if _, exists := collected[name]; exists {
			continue
		}
		collected[name] = fd
		// Recursively fetch dependencies.
		for _, dep := range fd.Dependency {
			if _, exists := collected[dep]; !exists {
				if err := fetchFileDependency(stream, dep, collected); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// fetchFileDependency fetches a file descriptor by filename.
func fetchFileDependency(stream rpb.ServerReflection_ServerReflectionInfoClient, filename string, collected map[string]*descriptorpb.FileDescriptorProto) error {
	if err := stream.Send(&rpb.ServerReflectionRequest{
		MessageRequest: &rpb.ServerReflectionRequest_FileByFilename{
			FileByFilename: filename,
		},
	}); err != nil {
		return err
	}
	resp, err := stream.Recv()
	if err != nil {
		if err == io.EOF {
			return nil
		}
		return err
	}
	fdResp := resp.GetFileDescriptorResponse()
	if fdResp == nil {
		// Dependency might not be resolvable (e.g. well-known types).
		return nil
	}
	for _, raw := range fdResp.FileDescriptorProto {
		fd := &descriptorpb.FileDescriptorProto{}
		if err := proto.Unmarshal(raw, fd); err != nil {
			return fmt.Errorf("unmarshal file descriptor: %w", err)
		}
		name := fd.GetName()
		if _, exists := collected[name]; exists {
			continue
		}
		collected[name] = fd
		for _, dep := range fd.Dependency {
			if _, exists := collected[dep]; !exists {
				if err := fetchFileDependency(stream, dep, collected); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
