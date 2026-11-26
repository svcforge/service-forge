// Package gatewaypolicy embeds the framework's protobuf policy schema.
package gatewaypolicy

import _ "embed"

const ProjectPath = "api/proto/svcforge/gateway/v1/policy.proto"

//go:embed policy.proto
var schema string

// Schema returns the policy definition shared by the CLI and generated code.
func Schema() string { return schema }
