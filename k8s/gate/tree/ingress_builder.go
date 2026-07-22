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
package tree

import (
	"context"
	"log/slog"
	"path"
	"strconv"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/constants"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
)

// ingressBackendCRAnnotation, when set on an Ingress, points every synthetic
// backendRef at a Backend custom resource (api/gate/v3 Backend). Its value is
// "name" or "namespace/name". It is translated into an ExtensionRef filter, the
// same mechanism an HTTPRoute uses to reference a Backend CR.
const ingressBackendCRAnnotation = "cr-backend"

var _ Builder = &IngressBuilderImpl{}

const (
	CONTROLLER = "haproxy.org/ingress-controller"
)

type IngressBuilderImpl struct {
	*ControllerStore
}

func NewIngressBuilder(controllerStore *ControllerStore) Builder {
	return &IngressBuilderImpl{
		ControllerStore: controllerStore,
	}
}

// ingressToProcess is an Ingress scheduled for (re)evaluation this cycle, with
// the update status to carry into the synthetic HTTPRoute updates it produces.
type ingressToProcess struct {
	ingress *networkingv1.Ingress
	status  store.Status
}

// ComputeTreeUpdates translates the Ingress updates of this cycle into synthetic
// raw HTTPRoute updates and injects them into ClusterStore.Updates.HTTPRoutes.
// The HTTPRouteBuilder, running afterwards, consumes them like any other
// HTTPRoute update (raw -> tree conversion, checks, rules, backends, maps), so
// the whole pipeline is reused without duplication.
//
// Besides the directly-updated Ingresses, it also re-evaluates the Ingresses
// whose eligibility may have changed because a referenced IngressClass changed
// in this cycle (see addIngressesForChangedClasses). Nothing else consumes
// Updates.IngressClasses, so without this an IngressClass deletion would leave a
// now-ineligible Ingress (and its backends) in the configuration whenever the
// Ingress re-enqueue did not land in the same batch as the class deletion.
func (b *IngressBuilderImpl) ComputeTreeUpdates() {
	toProcess := make(map[types.NamespacedName]ingressToProcess)

	for key, updatedIngress := range b.ClusterStore.Updates.Ingresses {
		rawIngress := updatedIngress.NewObject
		if updatedIngress.Status == store.StatusDeleted {
			rawIngress = updatedIngress.OldObject
		}
		if rawIngress == nil {
			continue
		}
		toProcess[key] = ingressToProcess{ingress: rawIngress, status: updatedIngress.Status}
	}

	b.addIngressesForChangedClasses(toProcess)

	for key, item := range toProcess {
		b.processIngressUpdate(key, item.ingress, item.status)
	}
}

// addIngressesForChangedClasses schedules, for re-evaluation, the Ingresses
// affected by an IngressClass change in this cycle: those that reference a
// changed class by name, plus every unclassed Ingress when a default class was
// added or removed (an unclassed Ingress's eligibility depends on the default
// class). Ingresses already scheduled from Updates.Ingresses keep their own
// status and are not overwritten.
func (b *IngressBuilderImpl) addIngressesForChangedClasses(toProcess map[types.NamespacedName]ingressToProcess) {
	if len(b.ClusterStore.Updates.IngressClasses) == 0 {
		return
	}

	changedClasses := make(map[string]struct{}, len(b.ClusterStore.Updates.IngressClasses))
	defaultClassChanged := false
	for name, updated := range b.ClusterStore.Updates.IngressClasses {
		changedClasses[name.Name] = struct{}{}
		if isDefaultIngressClass(updated.NewObject) || isDefaultIngressClass(updated.OldObject) {
			defaultClassChanged = true
		}
	}

	for key, ingress := range b.ClusterStore.Ingresses {
		if ingress == nil {
			continue
		}
		if _, already := toProcess[key]; already {
			continue
		}
		className := utils.PointerDefaultValueIfNil(ingress.Spec.IngressClassName)
		_, matchesName := changedClasses[className]
		matchesDefault := className == "" && defaultClassChanged
		if matchesName || matchesDefault {
			toProcess[key] = ingressToProcess{ingress: ingress, status: store.StatusUpserted}
		}
	}
}

// processIngressUpdate re-checks a single Ingress's eligibility and either
// translates it into synthetic HTTPRoutes or deletes the synthetic routes it
// previously produced.
func (b *IngressBuilderImpl) processIngressUpdate(key types.NamespacedName, rawIngress *networkingv1.Ingress, status store.Status) {
	ingressClassName := utils.PointerDefaultValueIfNil(rawIngress.Spec.IngressClassName)
	eligible := b.isIngressClassSupported(ingressClassName, b.IngressClass, b.EmptyIngressClass)
	b.Logger.LogAttrs(
		context.Background(), slog.LevelDebug,
		"Processing Ingress update",
		slog.String("ingress", key.String()),
		slog.String("status", string(status)),
		slog.String("ingressClassName", ingressClassName),
		slog.Bool("eligible", eligible),
	)
	if !eligible {
		b.deleteRoutesForIngress(key)
		return
	}
	routes := b.convertIngressToHTTPRoutes(rawIngress)
	b.Logger.LogAttrs(
		context.Background(), slog.LevelDebug,
		"Translated Ingress into synthetic HTTPRoutes",
		slog.String("ingress", key.String()),
		slog.Int("syntheticRoutes", len(routes)),
	)
	for routeKey, route := range routes {
		b.ClusterStore.Updates.HTTPRoutes[routeKey] = store.Update[*gatewayv1.HTTPRoute]{
			NewObject: route,
			Status:    status,
		}
	}
}

// isDefaultIngressClass reports whether the IngressClass is annotated as the
// cluster default.
func isDefaultIngressClass(ic *networkingv1.IngressClass) bool {
	return ic != nil && ic.Annotations[constants.DefaultIngressClassAnnotation] == "true"
}

func (*IngressBuilderImpl) CleanTreeUpdates() {}

// convertIngressToHTTPRoutes builds one synthetic raw HTTPRoute per Ingress rule,
// keyed by its synthetic NamespacedName. One route per rule is required because
// an HTTPRoute carries a single set of hostnames that applies to all its rules,
// whereas each Ingress rule has its own host.
func (b *IngressBuilderImpl) convertIngressToHTTPRoutes(
	ingress *networkingv1.Ingress,
) map[types.NamespacedName]*gatewayv1.HTTPRoute {
	routes := make(map[types.NamespacedName]*gatewayv1.HTTPRoute)

	// The Backend CR annotation is set once on the Ingress and applies to every
	// synthetic backendRef.
	backendCRFilters := b.ingressBackendCRFilters(ingress)

	for i := range ingress.Spec.Rules {
		rule := ingress.Spec.Rules[i]
		if rule.HTTP == nil {
			continue
		}

		rules := b.ingressPathsToRules(ingress, rule.HTTP.Paths, backendCRFilters)
		if len(rules) == 0 {
			continue
		}

		name := utils.MangleIngressName(ingress, strconv.Itoa(i))
		key := types.NamespacedName{Namespace: ingress.Namespace, Name: name}
		routes[key] = newSyntheticHTTPRoute(ingress, name, rule.Host, rules)
	}

	// spec.defaultBackend serves requests matching no rule. Model it as a
	// hostless synthetic route with a "/" PathPrefix match (the lowest routing
	// precedence), so it is the fallback, mirroring an Ingress default backend.
	if db := ingress.Spec.DefaultBackend; db != nil {
		if backendRef, ok := b.ingressBackendToRef(ingress, db, backendCRFilters); ok {
			prefix := gatewayv1.PathMatchPathPrefix
			slash := "/"
			rule := gatewayv1.HTTPRouteRule{
				Matches: []gatewayv1.HTTPRouteMatch{{
					Path: &gatewayv1.HTTPPathMatch{Type: &prefix, Value: &slash},
				}},
				BackendRefs: []gatewayv1.HTTPBackendRef{backendRef},
			}
			name := utils.MangleIngressName(ingress, "default")
			key := types.NamespacedName{Namespace: ingress.Namespace, Name: name}
			routes[key] = newSyntheticHTTPRoute(ingress, name, "", []gatewayv1.HTTPRouteRule{rule})
		}
	}

	return routes
}

func (b *IngressBuilderImpl) ingressPathsToRules(
	ingress *networkingv1.Ingress, paths []networkingv1.HTTPIngressPath,
	backendCRFilters []gatewayv1.HTTPRouteFilter,
) []gatewayv1.HTTPRouteRule {
	rules := make([]gatewayv1.HTTPRouteRule, 0, len(paths))
	for i := range paths {
		httpPath := paths[i]
		backendRef, ok := b.ingressBackendToRef(ingress, &httpPath.Backend, backendCRFilters)
		if !ok {
			continue
		}
		rules = append(rules, gatewayv1.HTTPRouteRule{
			Matches:     []gatewayv1.HTTPRouteMatch{ingressPathToMatch(httpPath)},
			BackendRefs: []gatewayv1.HTTPBackendRef{backendRef},
		})
	}
	return rules
}

// ingressBackendToRef converts an Ingress backend to an HTTPBackendRef. Only
// Service backends are supported (Resource backends are skipped with a log). The
// Service port may be numeric or named; a named port is resolved against the
// referenced Service in the Ingress namespace. backendCRFilters (a Backend CR
// ExtensionRef, possibly nil) is attached to the produced backendRef.
func (b *IngressBuilderImpl) ingressBackendToRef(
	ingress *networkingv1.Ingress, backend *networkingv1.IngressBackend,
	backendCRFilters []gatewayv1.HTTPRouteFilter,
) (gatewayv1.HTTPBackendRef, bool) {
	if backend == nil || backend.Service == nil {
		b.logSkippedBackend(ingress, "only Service backends are supported")
		return gatewayv1.HTTPBackendRef{}, false
	}
	port, ok := b.resolveServicePort(ingress, backend.Service)
	if !ok {
		return gatewayv1.HTTPBackendRef{}, false
	}

	return gatewayv1.HTTPBackendRef{
		BackendRef: gatewayv1.BackendRef{
			BackendObjectReference: gatewayv1.BackendObjectReference{
				Name: gatewayv1.ObjectName(backend.Service.Name),
				Port: &port,
				// Namespace is intentionally nil: an Ingress can only reference a
				// Service in its own namespace, so the backend stays same-namespace
				// and never needs a ReferenceGrant.
			},
		},
		Filters: backendCRFilters,
	}, true
}

// resolveServicePort returns the numeric Service port for an Ingress backend. A
// numeric port is used as-is; a named port is looked up on the referenced Service
// (which lives in the Ingress namespace). Resolution fails (reported false, with a
// log) when the Service is not yet in the store or has no port with that name. A
// Service change re-enqueues the Ingress (enqueueIngressesForService), so a
// transient miss is retried on the next batch.
func (b *IngressBuilderImpl) resolveServicePort(
	ingress *networkingv1.Ingress, svc *networkingv1.IngressServiceBackend,
) (gatewayv1.PortNumber, bool) {
	if svc.Port.Number != 0 {
		return gatewayv1.PortNumber(svc.Port.Number), true
	}
	if svc.Port.Name == "" {
		b.logSkippedBackend(ingress, "service backend has neither a port number nor a port name")
		return 0, false
	}

	key := types.NamespacedName{Namespace: ingress.Namespace, Name: svc.Name}
	service := b.ClusterStore.Services[key]
	if service == nil {
		b.logSkippedBackend(
			ingress, "service not found to resolve named port",
			slog.String("service", key.String()),
			slog.String("portName", svc.Port.Name),
		)
		return 0, false
	}
	for _, p := range service.Spec.Ports {
		if p.Name == svc.Port.Name {
			return gatewayv1.PortNumber(p.Port), true
		}
	}
	b.logSkippedBackend(
		ingress, "named port not found on service",
		slog.String("service", key.String()),
		slog.String("portName", svc.Port.Name),
	)
	return 0, false
}

func (b *IngressBuilderImpl) logSkippedBackend(ingress *networkingv1.Ingress, reason string, attrs ...slog.Attr) {
	all := append([]slog.Attr{
		slog.String("ingress", client.ObjectKeyFromObject(ingress).String()),
		slog.String("reason", reason),
	}, attrs...)
	b.Logger.LogAttrs(context.Background(), slog.LevelWarn, "Skipping Ingress backend", all...)
}

// ingressBackendCRFilters reads the Backend CR annotation and, when present and
// pointing at a same-namespace Backend CR, returns the matching ExtensionRef
// filter. It returns nil when the annotation is absent or empty.
//
// The annotation value is "name" or "namespace/name". A namespace equal to the
// Ingress namespace (or omitted) is honoured via ExtensionRef, which is
// namespace-local. A different namespace cannot be expressed by ExtensionRef and
// is not supported yet.
//
// TODO(ingress): support cross-namespace Backend CR references. ExtensionRef
// carries no namespace and the resolver looks the CR up in the route's
// namespace (k8s/gate/haproxy/backends.go), so an out-of-namespace CR needs a
// dedicated mechanism.
func (b *IngressBuilderImpl) ingressBackendCRFilters(ingress *networkingv1.Ingress) []gatewayv1.HTTPRouteFilter {
	value, ok := utils.AnnotationValue(ingress.Annotations, ingressBackendCRAnnotation)
	if !ok || value == "" {
		return nil
	}

	namespace, name, hasNamespace := strings.Cut(value, "/")
	if !hasNamespace {
		name = namespace
		namespace = ingress.Namespace
	}

	if namespace != ingress.Namespace {
		b.Logger.LogAttrs(
			context.Background(), slog.LevelWarn,
			"Ignoring cross-namespace Backend CR annotation on Ingress (not supported yet)",
			slog.String("ingress", client.ObjectKeyFromObject(ingress).String()),
			slog.String(ingressBackendCRAnnotation, value),
		)
		return nil
	}

	return []gatewayv1.HTTPRouteFilter{{
		Type: gatewayv1.HTTPRouteFilterExtensionRef,
		ExtensionRef: &gatewayv1.LocalObjectReference{
			Group: gatewayv1.Group(v3.GroupName),
			Kind:  gatewayv1.Kind("Backend"),
			Name:  gatewayv1.ObjectName(name),
		},
	}}
}

// newSyntheticHTTPRoute assembles the synthetic raw *gatewayv1.HTTPRoute. The
// namespace is the real Ingress namespace (so same-namespace backend resolution
// holds); only the name is mangled to guarantee a collision-free identity.
//
// The parentRef targets the synthetic ingress gateway by its fixed key. Its
// empty namespace is reachable only because this route is built in memory (a
// real route could not set parentRef.Namespace to "", which the API rejects) —
// that is what lets ingress routes attach where real routes cannot. SectionName
// is omitted so the route attaches to every compatible listener (http + https).
func newSyntheticHTTPRoute(
	ingress *networkingv1.Ingress, name, host string, rules []gatewayv1.HTTPRouteRule,
) *gatewayv1.HTTPRoute {
	var hostnames []gatewayv1.Hostname
	if host != "" {
		hostnames = []gatewayv1.Hostname{gatewayv1.Hostname(host)}
	}

	group := gatewayv1.Group(gatewayv1.GroupName)
	kind := gatewayv1.Kind("Gateway")
	gatewayNamespace := gatewayv1.Namespace(syntheticGatewayNamespacedName.Namespace)

	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ingress.Namespace,
			Name:      name,
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{{
					Group:     &group,
					Kind:      &kind,
					Namespace: &gatewayNamespace,
					Name:      gatewayv1.ObjectName(syntheticGatewayNamespacedName.Name),
				}},
			},
			Hostnames: hostnames,
			Rules:     rules,
		},
	}
}

// ingressPathToMatch converts an Ingress path + pathType to an HTTPRouteMatch.
// The Ingress path types are mapped to their Gateway API equivalents:
//   - Exact  -> PathMatchExact
//   - Prefix -> PathMatchPathPrefix (both match element-by-element)
//
// TODO(ingress): decide how to handle PathTypeImplementationSpecific. The spec
// leaves its meaning "up to the IngressClass"; for now it falls back to the
// PathPrefix default like a nil pathType, but HAProxy could also interpret it as
// a regular expression (PathMatchRegularExpression). Revisit before GA.
func ingressPathToMatch(httpPath networkingv1.HTTPIngressPath) gatewayv1.HTTPRouteMatch {
	matchType := gatewayv1.PathMatchPathPrefix
	if httpPath.PathType != nil {
		switch *httpPath.PathType {
		case networkingv1.PathTypeExact:
			matchType = gatewayv1.PathMatchExact
		case networkingv1.PathTypePrefix:
			matchType = gatewayv1.PathMatchPathPrefix
		}
	}

	value := httpPath.Path
	if value == "" {
		value = "/"
	}

	return gatewayv1.HTTPRouteMatch{
		Path: &gatewayv1.HTTPPathMatch{Type: &matchType, Value: &value},
	}
}

// isIngressClassSupported reports whether an Ingress with the given
// ingressClassName is handled by this controller instance, following the
// IngressClass eligibility rules. It lives on ControllerStore so both the
// Ingress and synthetic-gateway builders can use it.
func (b *ControllerStore) isIngressClassSupported(ingressClassFromIngress, controllerIngressClassParameter string,
	allowEmptyClass bool,
) bool {
	var supported bool
	var ingressgClassControllerFromSpec string
	if ingressClassResource := b.ClusterStore.IngressClasses[types.NamespacedName{Name: ingressClassFromIngress}]; ingressClassResource != nil {
		ingressgClassControllerFromSpec = ingressClassResource.Spec.Controller
	}
	if ingressClassFromIngress == "" {
		for _, ingressClass := range b.ClusterStore.IngressClasses {
			if isDefaultIngressClass(ingressClass) {
				ingressgClassControllerFromSpec = ingressClass.Spec.Controller
				break
			}
		}
	}

	switch controllerIngressClassParameter {
	case "":
		supported = (ingressClassFromIngress == "" && ingressgClassControllerFromSpec == "") ||
			ingressgClassControllerFromSpec == CONTROLLER
	default:
		supported = ingressClassFromIngress == "" && allowEmptyClass ||
			ingressgClassControllerFromSpec == path.Join(CONTROLLER, controllerIngressClassParameter)
	}

	return supported
}

// IsIngressEligible reports whether this controller instance handles the Ingress,
// following the IngressClass eligibility rules. It is exported so consumers
// outside the tree package (e.g. the Ingress LoadBalancer status update) can
// decide which Ingresses to advertise controller addresses on.
func (b *ControllerStore) IsIngressEligible(ingress *networkingv1.Ingress) bool {
	if ingress == nil {
		return false
	}
	className := utils.PointerDefaultValueIfNil(ingress.Spec.IngressClassName)
	return b.isIngressClassSupported(className, b.IngressClass, b.EmptyIngressClass)
}

func (b *IngressBuilderImpl) deleteRoutesForIngress(ingKey types.NamespacedName) {
	for key, treeRoute := range b.GateTree.HTTPRoutes {
		if src, _, ok := utils.ParseSyntheticRoute(key.Namespace, key.Name); ok && src == ingKey {
			b.ClusterStore.Updates.HTTPRoutes[key] = store.Update[*gatewayv1.HTTPRoute]{
				OldObject: treeRoute.K8sResource,
				Status:    store.StatusDeleted,
			}
		}
	}
}
