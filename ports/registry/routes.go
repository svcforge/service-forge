package registry

import "context"

// RouteRevisionMetadata identifies the route schema supported by an instance.
const RouteRevisionMetadata = "svcforge_route_revision"

const RouteCatalogKey = "route_catalog"

// RouteCatalog publishes and retrieves one protobuf route manifest per service.
type RouteCatalog interface {
	PublishRoutes(ctx context.Context, service string, manifest []byte) error
	LoadRoutes(ctx context.Context, service string) ([]byte, error)
}

// RouteCatalogLister lets gateways discover publisher-managed manifests.
type RouteCatalogLister interface {
	ListRouteServices(ctx context.Context) ([]string, error)
}
