package consul

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"

	consulapi "github.com/hashicorp/consul/api"
)

const routePrefix = "svcforge/routes/v1/"

var routeServiceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func routeKey(service string) (string, error) {
	if !routeServiceName.MatchString(service) {
		return "", fmt.Errorf("invalid route catalog service name %q", service)
	}
	return routePrefix + service, nil
}

func (r *Registry) PublishRoutes(ctx context.Context, service string, manifest []byte) error {
	key, err := routeKey(service)
	if err != nil {
		return err
	}
	pair, _, err := r.client.KV().Get(key, (&consulapi.QueryOptions{}).WithContext(ctx))
	if err != nil {
		return err
	}
	if pair != nil && bytes.Equal(pair.Value, manifest) {
		return nil
	}
	update := &consulapi.KVPair{Key: key, Value: manifest}
	if pair != nil {
		update.ModifyIndex = pair.ModifyIndex
	}
	ok, _, err := r.client.KV().CAS(update, (&consulapi.WriteOptions{}).WithContext(ctx))
	if err != nil || ok {
		return err
	}
	current, err := r.LoadRoutes(ctx, service)
	if err != nil || bytes.Equal(current, manifest) {
		return err
	}
	return fmt.Errorf("route manifest for %s changed during publication", service)
}

func (r *Registry) LoadRoutes(ctx context.Context, service string) ([]byte, error) {
	key, err := routeKey(service)
	if err != nil {
		return nil, err
	}
	pair, _, err := r.client.KV().Get(key, (&consulapi.QueryOptions{}).WithContext(ctx))
	if err != nil || pair == nil {
		return nil, err
	}
	return pair.Value, nil
}

func (r *Registry) ListRouteServices(ctx context.Context) ([]string, error) {
	keys, _, err := r.client.KV().Keys(routePrefix, "", (&consulapi.QueryOptions{}).WithContext(ctx))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, key := range keys {
		name := strings.TrimPrefix(key, routePrefix)
		if routeServiceName.MatchString(name) {
			names = append(names, name)
		}
	}
	return names, nil
}
