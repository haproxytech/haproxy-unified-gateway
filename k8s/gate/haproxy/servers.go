// Copyright 2025 HAProxy Technologies LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package haproxy

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"html/template"
	"log/slog"

	"github.com/haproxytech/client-native/v6/misc"
	"github.com/haproxytech/client-native/v6/models"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/templates"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/index"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"

	discoveryV1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SvcEndpoints describes endpoints of a service port
// map[discoveryV1.AddressType]map[port]map[address]
type SvcEndpoints map[discoveryV1.AddressType]Endpoints

// BackendPort is used to store both a backend name and the Service Port (not the target Port)
type BackendPort struct {
	backendName string
	svcPort     int32
}

type Endpoints struct {
	endpoints []discoveryV1.Endpoint
	ports     []discoveryV1.EndpointPort
}

// HashSHA1 returns the SHA-1 hash of a string in hexadecimal
func HashSHA1(data string) string {
	hash := sha1.Sum([]byte(data))
	return hex.EncodeToString(hash[:])
}

func (b *HaproxyConfMgrImpl) getServerName(address string, port int32) (string, error) {
	tmpl, err := template.New("server").Parse(b.params.ServerNameTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse server name template: %w", err)
	}

	cnServerAddress := fmt.Sprintf("%s:%d", misc.SanitizeIPv6Address(address), port)

	data := templates.TemplateData{
		POD_IP_PORT_HASH: HashSHA1(cnServerAddress),
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (b *HaproxyConfMgrImpl) processEndpointSlices(ctx context.Context) error {
	// If no update in EndpointSlices or no Created/Update Backend, we can skip the server update computation
	if len(b.controllerStore.ClusterStore.Updates.EndpointSlices) == 0 &&
		len(b.configuration.diffs.Created.Backends) == 0 && len(b.configuration.diffs.Updated.Backends) == 0 {
		return nil
	}

	// Build which services have any endpoint updates
	epsUpdatesByService := b.endpointSliceUpdatedByService()

	backendsByService := b.backendsByService()

	var errors utils.Errors
	for svcKey, backends := range backendsByService {
		// Find if there is any update in Endpoints for this service
		// or if any of the backends has been created/updated
		var beCreatedOrUpdated bool
		for backendPort := range backends {
			if _, ok := b.configuration.diffs.Created.Backends[backendPort.backendName]; ok {
				beCreatedOrUpdated = true
				break
			}
			if _, ok := b.configuration.diffs.Updated.Backends[backendPort.backendName]; ok {
				beCreatedOrUpdated = true
				break
			}
		}
		_, ok := epsUpdatesByService[svcKey]
		if !ok && !beCreatedOrUpdated {
			// If not, nothing to do
			continue
		}
		// The service has Endpoint updates, we now need to compute the list of servers
		// and apply it to the backends
		svcEndpoints, err := b.getSvcEndpoints(ctx, svcKey)
		if err != nil {
			errors.Add(err)
			continue
		}

		for backendPort := range backends {
			servers, err := b.getServersForBackend(svcKey, backendPort, svcEndpoints)
			if err != nil {
				errors.Add(err)
				continue
			}
			serverDiffs, err := b.configuration.upsertBackendWithServers(b.logger, backendPort.backendName, servers)
			if err != nil {
				errors.Add(err)
				continue
			}

			if err = b.runtimeDeleteServers(backendPort.backendName, serverDiffs); err != nil {
				errors.Add(err)
			}
			if err = b.runtimeCreateServers(backendPort.backendName, serverDiffs, servers); err != nil {
				errors.Add(err)
			}
		}
	}

	return errors.Result()
}

// getServersForBackend builds the list of models.Servers for a given backend
// - svcKey is the Service object key
// - be contains the backend name and the service port
// - svcEndpoints contains the list of endpoint for this service (already organized as a map)
// It will first match the service Port with the service TargetPort and use only the endpoints on this port (matching TargetRef)
// It also consider the haproxy configuration: DisableIPv4 or DisableIPv6 to discard IPv4/IPv6 as required.
func (b *HaproxyConfMgrImpl) getServersForBackend(svcKey client.ObjectKey, be BackendPort, svcEndpoints SvcEndpoints) (map[string]models.Server, error) {
	servers := make(map[string]models.Server)
	bePort := be.svcPort
	var errors utils.Errors
	// First match the Service Port with the TargetPort
	svc, ok := b.controllerStore.ClusterStore.Services[svcKey]
	if !ok {
		b.logger.LogAttrs(context.Background(), slog.LevelError, "could not retrieve the Service", logging.LogAttrKey(svcKey))
		return nil, fmt.Errorf("could not retrieve the Service %s", svcKey.String())
	}

	// Find the TargetPort in the Service
	var targetPort intstr.IntOrString
	for _, port := range svc.Spec.Ports {
		// Match the Service's published port number
		if port.Port == bePort {
			targetPort = port.TargetPort
			break
		}
	}

	if targetPort.String() == "" || targetPort.String() == "<nil>" {
		return nil, fmt.Errorf("no TargetPort found on Service %s matching Port %d", svcKey.String(), bePort)
	}

	for addressType, endpointsForAddressType := range svcEndpoints {
		switch addressType {
		case discoveryV1.AddressTypeIPv4:
			if b.params.HaproxyConfParams.DisableIPv4 {
				continue
			}
		case discoveryV1.AddressTypeIPv6:
			if b.params.HaproxyConfParams.DisableIPv6 {
				continue
			}
		}

		// Check if the targetPort is part of endpoint ports
		foundPort := false
		var serverPort int32
		for _, epPort := range endpointsForAddressType.ports {
			switch targetPort.Type {
			case intstr.Int:
				targetPortInt := targetPort.IntVal
				if epPort.Port != nil && targetPortInt == *epPort.Port {
					foundPort = true
					serverPort = *epPort.Port
				}
			case intstr.String:
				targetPortStr := targetPort.String()
				if epPort.Name != nil && targetPortStr == *epPort.Name {
					foundPort = true
					if epPort.Port != nil {
						serverPort = *epPort.Port
					}
				}
			}
			if foundPort {
				break
			}
		}

		if !foundPort {
			continue
		}

		for _, eps := range endpointsForAddressType.endpoints {
			for _, address := range eps.Addresses {
				// Compute the server Name
				serverName, err := b.getServerName(address, serverPort)
				if err != nil {
					errors.Add(err)
					continue
				}

				server := models.Server{
					ServerParams: models.ServerParams{
						Maintenance: "disabled",
					},
					Address: address,
					Port:    new(int64(serverPort)),
					Name:    serverName,
				}
				servers[serverName] = server
			}
		}
	}
	return servers, errors.Result()
}

func (b *HaproxyConfMgrImpl) getSvcEndpoints(ctx context.Context, svcKey client.ObjectKey) (SvcEndpoints, error) {
	var endpointSliceList discoveryV1.EndpointSliceList
	svcEndpoints := SvcEndpoints{}
	// First Get the EndpointSlice from the cache
	// This call is not targetting the k8s API, but only gets from the cache
	// Should be performant as we used an index
	err := b.k8sClient.List(
		ctx,
		&endpointSliceList,
		client.MatchingFields{index.EndpointSliceServiceNameIndexField: svcKey.Name},
		client.InNamespace(svcKey.Namespace),
	)
	if err != nil {
		b.logger.LogAttrs(context.Background(), slog.LevelError, "could not retrieve endpoints",
			logging.LogAttrKey(svcKey),
			logging.LogAttrError(err),
		)
		return nil, err
	}

	// Build Endpoints from EndpointSlice
	for _, eps := range endpointSliceList.Items {
		// if svcEndpoints[eps.AddressType] == nil {
		// 	svcEndpoints[eps.AddressType] = make(map[Endpoints])
		// }

		epsForAddressType, ok := svcEndpoints[eps.AddressType]
		if !ok {
			svcEndpoints[eps.AddressType] = Endpoints{
				endpoints: make([]discoveryV1.Endpoint, 0),
				ports:     make([]discoveryV1.EndpointPort, 0),
			}
		}

		// Ports
		p := epsForAddressType.ports
		p = append(p, eps.Ports...)

		// Endpoints
		e := epsForAddressType.endpoints
		// Filter the one that are ready only
		for _, ep := range eps.Endpoints {
			if ep.Conditions.Ready == nil || !*ep.Conditions.Ready {
				continue
			}
			e = append(e, ep)
		}
		epsForAddressType.endpoints = e
		epsForAddressType.ports = p
		svcEndpoints[eps.AddressType] = epsForAddressType
	}

	return svcEndpoints, err
}

func (b *HaproxyConfMgrImpl) backendsByServiceForHTTPRoute(routeOwners map[client.ObjectKey]int64,
	beName string,
	servicesByBackend map[client.ObjectKey]map[BackendPort]struct{},
) {
	for ownerRouteKey := range routeOwners {
		// Find the route in controllerStore GateTree
		treeHTTPRoute, ok := b.controllerStore.GateTree.HTTPRoutes[ownerRouteKey]
		if !ok {
			b.logger.LogAttrs(context.Background(), slog.LevelError, "could not find HTTPRoute in GateTree", logging.LogAttrKey(ownerRouteKey))
			continue
		}
		// Iterate over each rule, extract the service name
		for _, rule := range treeHTTPRoute.Rules {
			if !rule.Valid {
				continue
			}
			for _, hBackendRef := range rule.K8sResource.BackendRefs {
				backendRef := hBackendRef.BackendObjectReference
				nsName := utils.GetNamespacedName(backendRef.Name, backendRef.Namespace, treeHTTPRoute.K8sResource.Namespace)
				filterHash := getFilterHash(rule.K8sResource.Filters, hBackendRef.Filters)
				var svcPort int32
				if backendRef.Port != nil {
					svcPort = int32(*backendRef.Port)
				}
				ruleBeName, err := b.getBackendName(nsName, svcPort, filterHash)
				if err != nil {
					continue
				}
				if ruleBeName != beName {
					continue
				}
				if servicesByBackend[nsName] == nil {
					servicesByBackend[nsName] = make(map[BackendPort]struct{})
				}
				backendPort := BackendPort{
					backendName: beName,
					svcPort:     svcPort,
				}
				servicesByBackend[nsName][backendPort] = struct{}{}
			}
		}
	}
}

func (b *HaproxyConfMgrImpl) backendsByServiceForTLSRoute(routeOwners map[client.ObjectKey]int64,
	beName string,
	servicesByBackend map[client.ObjectKey]map[BackendPort]struct{},
) {
	for ownerRouteKey := range routeOwners {
		// Find the route in controllerStore GateTree
		treeTLSRoute, ok := b.controllerStore.GateTree.TLSRoutes[ownerRouteKey]
		if !ok {
			b.logger.LogAttrs(context.Background(), slog.LevelError, "could not find TLSRoute in GateTree", logging.LogAttrKey(ownerRouteKey))
			continue
		}
		// Iterate over each rule, extract the service name
		for _, rule := range treeTLSRoute.Rules {
			if !rule.Valid {
				continue
			}
			for _, hBackendRef := range rule.K8sResource.BackendRefs {
				backendRef := hBackendRef.BackendObjectReference
				nsName := utils.GetNamespacedName(backendRef.Name, backendRef.Namespace, treeTLSRoute.K8sResource.Namespace)
				var svcPort int32
				if backendRef.Port != nil {
					svcPort = int32(*backendRef.Port)
				}
				ruleBeName, err := b.getBackendName(nsName, svcPort, "")
				if err != nil {
					continue
				}
				if ruleBeName != beName {
					continue
				}
				if servicesByBackend[nsName] == nil {
					servicesByBackend[nsName] = make(map[BackendPort]struct{})
				}
				backendPort := BackendPort{
					backendName: beName,
					svcPort:     svcPort,
				}
				servicesByBackend[nsName][backendPort] = struct{}{}
			}
		}
	}
}

// backendsByService returns a:
// map[service key]map[backendName] struct{} for all referenced backend
func (b *HaproxyConfMgrImpl) backendsByService() map[client.ObjectKey]map[BackendPort]struct{} {
	servicesByBackend := make(map[client.ObjectKey]map[BackendPort]struct{})
	for beName := range b.backendOwners.owners {
		routeType := BackendOwnerTypeHTTPRoute
		routeOwners, ok := b.backendOwners.owners[beName][routeType]
		if !ok {
			routeOwners, ok = b.backendOwners.owners[beName][BackendOwnerTypeTLSRoute]
			routeType = BackendOwnerTypeTLSRoute
		}
		if !ok {
			continue
		}
		switch routeType {
		case BackendOwnerTypeHTTPRoute:
			b.backendsByServiceForHTTPRoute(routeOwners, beName, servicesByBackend)
		case BackendOwnerTypeTLSRoute:
			b.backendsByServiceForTLSRoute(routeOwners, beName, servicesByBackend)
		}
	}
	return servicesByBackend
}

// getServiceNameFromEndpointSlice returns the service name from an EndpointSlice
// Based on the label "kubernetes.io/service-name"
func getServiceNameFromEndpointSlice(eps *discoveryV1.EndpointSlice) string {
	return eps.Labels["kubernetes.io/service-name"]
}

// endpointSliceUpdatedByService returns a map of services with endpoint Updates
// map[service key] map[endpointslice key] struct{}
func (b *HaproxyConfMgrImpl) endpointSliceUpdatedByService() map[client.ObjectKey]map[client.ObjectKey]*discoveryV1.EndpointSlice {
	res := make(map[client.ObjectKey]map[client.ObjectKey]*discoveryV1.EndpointSlice)

	for epsKey, eps := range b.controllerStore.ClusterStore.Updates.EndpointSlices {
		var k8sEps *discoveryV1.EndpointSlice
		switch eps.Status {
		case store.StatusUpserted:
			k8sEps = eps.NewObject
		case store.StatusDeleted:
			k8sEps = eps.OldObject
		}
		svcName := getServiceNameFromEndpointSlice(k8sEps)
		svcNs := k8sEps.GetNamespace()
		updatedSvcKey := client.ObjectKey{Namespace: svcNs, Name: svcName}
		if res[updatedSvcKey] == nil {
			res[updatedSvcKey] = make(map[client.ObjectKey]*discoveryV1.EndpointSlice)
		}
		res[updatedSvcKey][epsKey] = k8sEps
	}
	return res
}
