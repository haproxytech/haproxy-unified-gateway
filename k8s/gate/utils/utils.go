//
// Copyright 2025 HAProxy Technologies LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package utils //revive:disable:package-naming

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/gateway-api/apis/v1alpha2"
)

// syntheticRouteNamePrefix prefixes the name of every synthetic HTTPRoute.
// It contains ':' on purpose: ':' is illegal in a Kubernetes object name
// (RFC 1123 subdomain), so the prefix doubles as a tamper-proof origin marker.
// A real HTTPRoute can never carry such a name (the API server rejects it), so
// a synthetic route can neither collide with a real one in the
// GateTree.HTTPRoutes map nor be impersonated by a user-controlled resource —
// no annotation or label is needed, and none is set so that real Ingress
// annotations can be carried through later without conflict.
const syntheticRouteNamePrefix = "ing:"

type ObjectWithTimestamp interface {
	GetCreationTimestamp() metav1.Time
	GetName() string
}

func SortByCreationTimestamp[T ObjectWithTimestamp](objects []T) {
	slices.SortFunc(objects, func(a, b T) int {
		// Handle nil cases - nil values sort before non-nil values
		aIsNil := isNilInterface(a)
		bIsNil := isNilInterface(b)

		if aIsNil && bIsNil {
			return 0 // both nil, considered equal
		}
		if aIsNil {
			return -1 // nil comes before non-nil
		}
		if bIsNil {
			return 1 // non-nil comes after nil
		}

		aTime := a.GetCreationTimestamp()
		bTime := b.GetCreationTimestamp()
		if c := aTime.Time.Compare(bTime.Time); c != 0 {
			return c
		}
		return cmp.Compare(a.GetName(), b.GetName())
	})
}

// isNilInterface checks if an interface value is nil
func isNilInterface(i any) bool {
	if i == nil {
		return true
	}
	// Use type assertion to check if the underlying value is nil
	switch v := i.(type) {
	case interface{ IsNil() bool }:
		return v.IsNil()
	default:
		// For pointer types, check if nil using reflection-free approach
		return false
	}
}

func MapToSortedListByCreationTimestamp[T ObjectWithTimestamp](objects map[types.NamespacedName]T) []T {
	list := make([]T, 0, len(objects))
	for _, obj := range objects {
		list = append(list, obj)
	}
	SortByCreationTimestamp(list)
	return list
}

// ParseNamespacedName parses a "namespace/name" string into a NamespacedName.
func ParseNamespacedName(s string) (types.NamespacedName, error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return types.NamespacedName{}, fmt.Errorf("invalid format: expected namespace/name, got %q", s)
	}
	return types.NamespacedName{Namespace: parts[0], Name: parts[1]}, nil
}

// Generic function to clear a map in place
func ClearMap[K comparable, V any](m map[K]V) {
	for k := range m {
		delete(m, k)
	}
}

func Keys[K comparable, V any](src map[K]V) map[K]struct{} {
	dst := make(map[K]struct{})
	for k := range src {
		dst[k] = struct{}{}
	}
	return dst
}

func NamespaceAsString(ns *gatewayv1.Namespace) string {
	if ns == nil {
		return ""
	}
	return string(*ns)
}

func ObjectKeyFromNamespacedName(s string) (client.ObjectKey, error) {
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		// Handle error: string is not in the correct format
		return client.ObjectKey{}, fmt.Errorf("invalid format: expected namespace/name, got %q", s)
	}
	return client.ObjectKey{
		Namespace: parts[0],
		Name:      parts[1],
	}, nil
}

func RouteGroupKindsToString(routesGK []gatewayv1.RouteGroupKind) string {
	kinds := make([]string, 0, len(routesGK))
	for _, kind := range routesGK {
		kinds = append(kinds, string(kind.Kind))
	}
	return fmt.Sprintf("[%s]", strings.Join(kinds, ", "))
}

// SetDifference finds the keys that are in mapA but not in mapB.
// It works for any map with a comparable key type K and any value type V.
func SetDifference[K comparable, V any](mapA, mapB map[K]V) map[K]struct{} {
	diff := make(map[K]struct{})
	for key := range mapA {
		if _, exists := mapB[key]; !exists {
			diff[key] = struct{}{}
		}
	}
	return diff
}

func SetIntersection[K comparable, V any](mapA, mapB map[K]V) map[K]struct{} {
	intersection := make(map[K]struct{})
	for key := range mapA {
		if _, exists := mapB[key]; exists {
			intersection[key] = struct{}{}
		}
	}
	return intersection
}

// ComparePointers compares two pointers to any ordered type.
// It establishes a consistent sort order where nil values come before non-nil values.
//
// It returns:
//   - -1 if a < b
//   - 0 if a == b (or both are nil)
//   - +1 if a > b
func ComparePointers[T cmp.Ordered](a, b *T) int {
	if a == nil && b != nil {
		return -1 // nil comes before non-nil
	}
	if a != nil && b == nil {
		return 1 // non-nil comes after nil
	}
	if a != nil && b != nil {
		// Both are non-nil, compare their values.
		return cmp.Compare(*a, *b)
	}
	// Both are nil, so they are equal.
	return 0
}

// PointerDefaultValueIfNil dereferences a pointer and returns its value.
// If the pointer is nil, the zero value of T is returned instead.
//
// Example:
//
//	x := 42
//	v := PointerDefaultValueIfNil(&x)  // returns 42
//	v := PointerDefaultValueIfNil(nil) // returns 0 (zero value of int)
func PointerDefaultValueIfNil[T any](arg *T) T {
	if arg == nil {
		var a T
		return a
	}
	return *arg
}

func GetNamespacedName(name gatewayv1.ObjectName, namespace *gatewayv1.Namespace, defaultNamespace string) types.NamespacedName {
	ns := defaultNamespace
	if namespace != nil {
		ns = string(*namespace)
	}
	return types.NamespacedName{Name: string(name), Namespace: ns}
}

// GetHostnamesForRouteWithListener returns the hostnames that match a listener and a route.
// The rules are based on the Gateway API specification.
// If the listener hostname is not set, it matches any route hostname.
// If the listener hostname is a wildcard, it checks if it matches any route hostname.
// If the route hostnames are empty, the listener hostname matches the route hostnames.
func GetHostnamesForRouteWithListener(listenerHostname *string, routeHostnames []string) []string {
	if len(routeHostnames) == 0 && PointerDefaultValueIfNil(listenerHostname) == "" {
		return []string{""}
	}

	if PointerDefaultValueIfNil(listenerHostname) == "" {
		// no restriction from listeners, all hostnames from route are allowed
		return routeHostnames
	}

	if len(routeHostnames) == 0 {
		// route hostnames are empty, the listener hostname becomes the restriction
		return []string{*listenerHostname}
	}

	// to avoid duplicates
	matched := map[string]struct{}{}
	for _, routeHostname := range routeHostnames {
		// If the listener hostname is a wildcard, check if it matches any route hostname.
		if hostnamesMatchingRouteAndListener(routeHostname, *listenerHostname) {
			matched[*listenerHostname] = struct{}{}
		} else if hostnamesMatchingRouteAndListener(*listenerHostname, routeHostname) {
			// If the route hostname is a wildcard, check if it matches any listener hostname.
			matched[routeHostname] = struct{}{}
		}
	}
	result := make([]string, len(matched))
	i := 0
	for k := range matched {
		result[i] = k
		i++
	}
	slices.Sort(result)
	return result
}

func hostnamesMatchingRouteAndListener(pattern, hostname string) bool {
	// Exact match if no wildcard
	if !strings.HasPrefix(pattern, "*.") {
		return pattern == hostname
	}

	// pattern is of the form "*.example.com"
	return strings.HasSuffix(hostname, pattern[1:])
}

func ConvertSliceWithFunc[U, V any](arg []U, f func(U) V) []V {
	result := make([]V, len(arg))
	for i, v := range arg {
		result[i] = f(v)
	}
	return result
}

func ConvertV1Alpha2HostnameToString(hostname v1alpha2.Hostname) string {
	return string(hostname)
}

// MangleIngressName builds the collision-free name of a synthetic HTTPRoute,
// e.g. "ing:my-app:0" for the first rule of Ingress "my-app".
func MangleIngressName(ingress *networkingv1.Ingress, suffix string) string {
	return fmt.Sprintf("%s%s:%s", syntheticRouteNamePrefix, ingress.Name, suffix)
}

// IsSyntheticName reports whether the name was produced by
// mangleIngressName. The ':' prefix is illegal in a Kubernetes object name, so a
// real Kubernetes resource can never carry it: this is a tamper-proof origin marker.
func IsSyntheticName(name string) bool {
	return strings.HasPrefix(name, syntheticRouteNamePrefix)
}

// ParseSyntheticRoute is the inverse of mangleIngressName. Given a synthetic
// route namespace and name, it returns the source Ingress key and the rule
// suffix. isSynthetic is false when name is not a synthetic route name.
//
// The prefix is stripped first, then the remaining "name:suffix" is cut at the
// first ':'. The Ingress name is a valid Kubernetes name (so it never contains
// ':'), making the cut unambiguous even if a suffix ever contained one.
func ParseSyntheticRoute(routeNamespace, name string) (ingress types.NamespacedName, suffix string, isSynthetic bool) {
	rest, ok := strings.CutPrefix(name, syntheticRouteNamePrefix)
	if !ok {
		return types.NamespacedName{}, "", false
	}
	ingressName, suffix, ok := strings.Cut(rest, ":")
	if !ok {
		return types.NamespacedName{}, "", false
	}
	return types.NamespacedName{Namespace: routeNamespace, Name: ingressName}, suffix, true
}

// AnnotationPrefixes are the prefixes under which HAProxy Ingress/Service
// annotations are recognised. An annotation may be used bare (no prefix) or with
// any of these; e.g. "cr-backend", "haproxy.org/cr-backend" and
// "haproxy.com/cr-backend" are equivalent.
var AnnotationPrefixes = []string{
	"ingress.kubernetes.io",
	"haproxy.org",
	"haproxy.com",
}

// AnnotationKeys returns the accepted keys for an annotation suffix: the bare
// suffix first, then each supported prefix joined with '/'.
func AnnotationKeys(suffix string) []string {
	keys := make([]string, 0, len(AnnotationPrefixes)+1)
	keys = append(keys, suffix)
	for _, prefix := range AnnotationPrefixes {
		keys = append(keys, prefix+"/"+suffix)
	}
	return keys
}

// AnnotationValue returns the value of the first present key among the accepted
// keys for suffix (bare then prefixed), and whether one was found.
func AnnotationValue(annotations map[string]string, suffix string) (string, bool) {
	for _, key := range AnnotationKeys(suffix) {
		if value, ok := annotations[key]; ok {
			return value, true
		}
	}
	return "", false
}
