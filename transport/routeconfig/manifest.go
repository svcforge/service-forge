// Package routeconfig builds and decodes the route manifests exchanged by
// gRPC services and HTTP gateways.
package routeconfig

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	gatewayv1 "github.com/svcforge/service-forge/api/gen/go/svcforge/gateway/v1"
	"github.com/svcforge/service-forge/core/config"

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
	Services  []string                     `json:"services"`
	Files     []byte                       `json:"files"`
	Policy    *config.PublishedRoutePolicy `json:"policy,omitempty"`
	Automatic bool                         `json:"automatic,omitempty"`
}

type Route struct {
	Method           string
	Path             string
	Body             string
	RPC              string
	Stream           bool
	AllowAnonymous   bool
	ForwardAccountID bool
	Input            protoreflect.MessageDescriptor
	Output           protoreflect.MessageDescriptor
	Types            *dynamicpb.Types
}

var jwtSecretEnvName = regexp.MustCompile(`^(?:[A-Z][A-Z0-9_]*_)?JWT_SECRET$`)

func validatePolicy(policy *config.PublishedRoutePolicy) error {
	if policy == nil {
		return nil
	}
	if policy.JWTSecretEnv != "" && !jwtSecretEnvName.MatchString(policy.JWTSecretEnv) {
		return fmt.Errorf("jwt_secret_env must name JWT_SECRET or *_JWT_SECRET")
	}
	for _, claim := range policy.ForwardClaims {
		if claim.Default != nil {
			return fmt.Errorf("published forward_claims require a verified claim and cannot set a default")
		}
	}
	return nil
}

func Revision(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

// Encode includes descriptors only for services actually registered on the server.
func Encode(services []string, files *protoregistry.Files) ([]byte, error) {
	return EncodeWithPolicy(services, files, nil)
}

func EncodeWithPolicy(services []string, files *protoregistry.Files, policy *config.PublishedRoutePolicy) ([]byte, error) {
	return encode(services, files, manifest{Policy: policy})
}

// EncodeAnnotated uses HTTP and gateway annotations as the complete route policy.
func EncodeAnnotated(services []string, files *protoregistry.Files) ([]byte, error) {
	return encode(services, files, manifest{Automatic: true})
}

func encode(services []string, files *protoregistry.Files, out manifest) ([]byte, error) {
	if err := validatePolicy(out.Policy); err != nil {
		return nil, err
	}
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
		if out.Automatic && errors.Is(err, protoregistry.NotFound) {
			continue
		}
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
	if out.Automatic && len(out.Services) == 0 {
		return nil, nil
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

type DecodedManifest struct {
	Routes    []Route
	Policy    *config.PublishedRoutePolicy
	Automatic bool
}

func Decode(data []byte) ([]Route, error) {
	spec, err := DecodeManifest(data)
	return spec.Routes, err
}

func DecodeManifest(data []byte) (DecodedManifest, error) {
	if len(data) > MaxManifestSize {
		return DecodedManifest{}, fmt.Errorf("route manifest exceeds %d bytes", MaxManifestSize)
	}
	var spec manifest
	if err := json.Unmarshal(data, &spec); err != nil {
		return DecodedManifest{}, err
	}
	if spec.Automatic && spec.Policy != nil {
		return DecodedManifest{}, fmt.Errorf("route manifest cannot combine automatic and explicit policy")
	}
	if err := validatePolicy(spec.Policy); err != nil {
		return DecodedManifest{}, err
	}
	routes, err := decodeRoutes(spec)
	if err != nil {
		return DecodedManifest{}, err
	}
	return DecodedManifest{Routes: routes, Policy: spec.Policy, Automatic: spec.Automatic}, nil
}

func decodeRoutes(spec manifest) ([]Route, error) {
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
	route.AllowAnonymous, _ = proto.GetExtension(method.Options(), gatewayv1.E_AllowAnonymous).(bool)
	route.ForwardAccountID, _ = proto.GetExtension(service.Options(), gatewayv1.E_ForwardAccountId).(bool)
	route.Stream = method.IsStreamingClient() && method.IsStreamingServer()
	if route.Stream && (route.Method != "GET" || route.Body != "" || strings.Contains(route.Path, "{")) {
		return Route{}, fmt.Errorf("a WebSocket RPC requires a literal GET mapping without a body")
	}
	if route.Body != "" && route.Body != "*" {
		return Route{}, fmt.Errorf("HTTP body must be omitted or mapped to *")
	}
	return route, nil
}
