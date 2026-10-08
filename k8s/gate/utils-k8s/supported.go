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

package utilsk8s

import (
	"strings"

	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
	objtypes "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/object-types"

	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// IsFilterExtensionRefKindSupported checks if the provided filter ExtensionRef has a supported Group and Kind.
// It only supports `v3.Backend` resources.
func IsFilterExtensionRefKindSupported(extensionRef *gatewayv1.LocalObjectReference, extractGVK ExtractGVK) bool {
	backendGVK := extractGVK(objtypes.ObjectTypeBackend)

	if extensionRef == nil {
		return false
	}
	if !strings.EqualFold(backendGVK.Kind, string(extensionRef.Kind)) {
		return false
	}
	if backendGVK.Group != string(extensionRef.Group) {
		return false
	}
	return true
}

func IsFilterExtensionRefKindMergeType(extensionRef *gatewayv1.LocalObjectReference, extractGVK ExtractGVK) bool {
	backendGVK := extractGVK(objtypes.ObjectTypeBackend)
	if extensionRef == nil {
		return false
	}
	if !strings.EqualFold("MergeType", string(extensionRef.Kind)) {
		return false
	}
	if backendGVK.Group != string(extensionRef.Group) {
		return false
	}
	if !(strings.EqualFold("Override", string(extensionRef.Name)) || strings.EqualFold("Append", string(extensionRef.Name))) {
		return false
	}
	return true
}

// IsBackendRefGroupKindSupported checks if the provided HTTPRoute parent reference has a supported Group and Kind.
// It only supports `corev1.Service` resources.
func IsBackendRefGroupKindSupported(backendRef gatewayv1.BackendObjectReference, extractGVK ExtractGVK) bool {
	serviceGVK := extractGVK(objtypes.ObjectTypeService)

	if backendRef.Kind != nil && *backendRef.Kind != gatewayv1.Kind(serviceGVK.Kind) {
		return false
	}
	if backendRef.Group != nil && *backendRef.Group != gatewayv1.Group(serviceGVK.Group) {
		return false
	}
	return true
}

// ParentRefKind identifies which of the supported parent kinds a ParentReference
// designates. The zero value is the unsupported case, so a ParentRefKind that was never
// resolved cannot be mistaken for a Gateway or a ListenerSet.
type ParentRefKind int

const (
	ParentRefKindUnsupported ParentRefKind = iota
	ParentRefKindGateway
	ParentRefKindListenerSet
)

// Unsupported reports whether the parent reference designates no kind we serve.
func (k ParentRefKind) Unsupported() bool {
	return k == ParentRefKindUnsupported
}

// ResolveParentRefKind returns the parent kind a ParentReference designates, among the
// ones we support: gatewayv1.Gateway and gatewayv1.ListenerSet.
//
// The kind and group are optional on a ParentReference: when absent they default to the
// Gateway ones, and a ListenerSet parentRef therefore only needs a group when its own
// group differs from the Gateway's.
func ResolveParentRefKind(parentRef gatewayv1.ParentReference, extractGVK ExtractGVK) ParentRefKind {
	gvkGw := extractGVK(objtypes.ObjectTypeGateway)
	gvkLS := extractGVK(objtypes.ObjectTypeListenerSet)

	parentRefKind := gatewayv1.Kind(gvkGw.Kind)
	if parentRef.Kind != nil {
		parentRefKind = *parentRef.Kind
	}
	parentRefGroup := gatewayv1.Group(gvkGw.Group)
	if parentRef.Group != nil {
		parentRefGroup = *parentRef.Group
	}

	if parentRefGroup == gatewayv1.Group(gvkGw.Group) && parentRefKind == gatewayv1.Kind(gvkGw.Kind) {
		return ParentRefKindGateway
	}
	if parentRefGroup == gatewayv1.Group(gvkLS.Group) && parentRefKind == gatewayv1.Kind(gvkLS.Kind) {
		return ParentRefKindListenerSet
	}
	return ParentRefKindUnsupported
}

// IsSecretGroupKindSupported checks if the provided certificate reference has a supported Group and Kind.
// It only supports core `v1.Secret` resources.
func IsSecretGroupKindSupported(certRef gatewayv1.SecretObjectReference) bool {
	if certRef.Kind != nil && *certRef.Kind != "Secret" {
		return false
	}
	if certRef.Group != nil && *certRef.Group != "" {
		return false
	}
	return true
}

// IsGlobalRefGroupKindSupported checks if the provided Global reference has a supported Group and Kind.
// It only supports `v3.Global` resources.
func IsGlobalRefGroupKindSupported(globalRef v3.CRReference, extractGVK ExtractGVK) bool {
	globalGVK := extractGVK(objtypes.ObjectTypeGlobal)

	if globalRef.Kind != nil && *globalRef.Kind != gatewayv1.Kind(globalGVK.Kind) {
		return false
	}
	if globalRef.Group != nil && *globalRef.Group != gatewayv1.Group(globalGVK.Group) {
		return false
	}
	return true
}

// IsDefaultsRefGroupKindSupported checks if the provided Defaults reference has a supported Group and Kind.
// It only supports `v3.Defaults` resources.
func IsDefaultsRefGroupKindSupported(defaultsRef v3.CRReference, extractGVK ExtractGVK) bool {
	defaultsGVK := extractGVK(objtypes.ObjectTypeDefaults)

	if defaultsRef.Kind != nil && *defaultsRef.Kind != gatewayv1.Kind(defaultsGVK.Kind) {
		return false
	}
	if defaultsRef.Group != nil && *defaultsRef.Group != gatewayv1.Group(defaultsGVK.Group) {
		return false
	}
	return true
}
