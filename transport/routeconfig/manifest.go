// Package routeconfig builds and decodes the route manifests exchanged by
// gRPC services and HTTP gateways.
package routeconfig

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const MaxManifestSize = 384 * 1024

type manifest struct {
	Services []string `json:"services"`
	Files    []byte   `json:"files"`
}

type Route struct {
	Method string
	Path   string
	Body   string
	RPC    string
	Stream bool
	Input  protoreflect.MessageDescriptor
	Output protoreflect.MessageDescriptor
	Types  *dynamicpb.Types
}

func Revision(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

// Encode includes descriptors only for services actually registered on the server.
func Encode(services []string, files *protoregistry.Files) ([]byte, error) {
	out := manifest{}
	descriptors := &descriptorpb.FileDescriptorSet{}
	seen := make(map[string]bool)
	var addFile func(protoreflect.FileDescriptor)
	addFile = func(file protoreflect.FileDescriptor) {
		if seen[file.Path()] {
			return
		}
		seen[file.Path()] = true
		for i := 0; i < file.Imports().Len(); i++ {
			addFile(file.Imports().Get(i).FileDescriptor)
		}
		descriptors.File = append(descriptors.File, protodesc.ToFileDescriptorProto(file))
	}
	services = slices.Clone(services)
	slices.Sort(services)
	for _, name := range services {
		desc, err := files.FindDescriptorByName(protoreflect.FullName(name))
		if err != nil {
			return nil, err
		}
		service, ok := desc.(protoreflect.ServiceDescriptor)
		if !ok {
			return nil, fmt.Errorf("%s is not a service", name)
		}
		for i := 0; i < service.Methods().Len(); i++ {
			if httpRule(service.Methods().Get(i)) != nil {
				out.Services = append(out.Services, name)
				addFile(service.ParentFile())
				break
			}
		}
	}
	var err error
	out.Files, err = proto.MarshalOptions{Deterministic: true}.Marshal(descriptors)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	if _, err := Decode(data); err != nil {
		return nil, err
	}
	return data, nil
}

func Decode(data []byte) ([]Route, error) {
	if len(data) > MaxManifestSize {
		return nil, fmt.Errorf("route manifest exceeds %d bytes", MaxManifestSize)
	}
	var spec manifest
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, err
	}
	var descriptors descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(spec.Files, &descriptors); err != nil {
		return nil, err
	}
	files, err := protodesc.NewFiles(&descriptors)
	if err != nil {
		return nil, err
	}
	types := dynamicpb.NewTypes(files)
	var routes []Route
	for _, name := range spec.Services {
		desc, err := files.FindDescriptorByName(protoreflect.FullName(name))
		if err != nil {
			return nil, err
		}
		service, ok := desc.(protoreflect.ServiceDescriptor)
		if !ok {
			return nil, fmt.Errorf("%s is not a service", name)
		}
		for i := 0; i < service.Methods().Len(); i++ {
			method := service.Methods().Get(i)
			rule := httpRule(method)
			if rule == nil {
				continue
			}
			if method.IsStreamingClient() != method.IsStreamingServer() {
				return nil, fmt.Errorf("%s: dynamic routes support unary or bidirectional RPCs", method.FullName())
			}
			bindings := append([]*annotations.HttpRule{rule}, rule.GetAdditionalBindings()...)
			for _, binding := range bindings {
				route, err := decodeRule(service, method, binding)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", method.FullName(), err)
				}
				route.Types = types
				routes = append(routes, route)
			}
		}
	}
	return routes, nil
}

func httpRule(method protoreflect.MethodDescriptor) *annotations.HttpRule {
	rule, _ := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
	return rule
}

func decodeRule(service protoreflect.ServiceDescriptor, method protoreflect.MethodDescriptor, rule *annotations.HttpRule) (Route, error) {
	route := Route{
		RPC:  "/" + string(service.FullName()) + "/" + string(method.Name()),
		Body: rule.GetBody(), Input: method.Input(), Output: method.Output(),
	}
	switch pattern := rule.GetPattern().(type) {
	case *annotations.HttpRule_Get:
		route.Method, route.Path = "GET", pattern.Get
	case *annotations.HttpRule_Post:
		route.Method, route.Path = "POST", pattern.Post
	case *annotations.HttpRule_Put:
		route.Method, route.Path = "PUT", pattern.Put
	case *annotations.HttpRule_Patch:
		route.Method, route.Path = "PATCH", pattern.Patch
	case *annotations.HttpRule_Delete:
		route.Method, route.Path = "DELETE", pattern.Delete
	}
	if route.Method == "" || !strings.HasPrefix(route.Path, "/") || rule.GetResponseBody() != "" {
		return Route{}, fmt.Errorf("unsupported HTTP annotation")
	}
	route.Stream = method.IsStreamingClient() && method.IsStreamingServer()
	if route.Stream && (route.Method != "GET" || route.Body != "" || strings.Contains(route.Path, "{")) {
		return Route{}, fmt.Errorf("a WebSocket RPC requires a literal GET mapping without a body")
	}
	if route.Body != "" && route.Body != "*" {
		return Route{}, fmt.Errorf("HTTP body must be omitted or mapped to *")
	}
	return route, nil
}
