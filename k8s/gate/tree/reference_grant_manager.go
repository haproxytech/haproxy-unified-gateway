package tree

import (
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	"k8s.io/apimachinery/pkg/types"
)

// To identifies a target resource as "namespace/group/kind/name".
// An empty name component means the grant applies to any resource name.
type To string

// From identifies a source resource type as "namespace/group/kind".
type From string

// ReferenceGrantNamespacedName is the namespace/name key of a ReferenceGrant.
type ReferenceGrantNamespacedName types.NamespacedName

// ReferenceGrantManager tracks which cross-namespace references are permitted
// by ReferenceGrant resources. It maintains three maps:
//
//   - toReferenceGrantFrom: for each target (To), which ReferenceGrants cover it
//     and from which source types (From). Used to incrementally update toFrom.
//
//   - referenceGrantsTo: inverse index — for each ReferenceGrant, which targets
//     (To) it covers. Required to efficiently remove all entries for a deleted grant.
//
//   - toFrom: the resolved flat map consumed by IsAccessGranted. Rebuilt from
//     toReferenceGrantFrom by ComputeToFrom after each reconcile cycle.
type ReferenceGrantManager struct {
	toReferenceGrantFrom map[To]map[ReferenceGrantNamespacedName]map[From]struct{}
	referenceGrantsTo    map[ReferenceGrantNamespacedName]map[To]struct{}
	toFrom               map[To]map[From]struct{}
}

// ConvertTo builds a To key from the target resource's namespace, API group,
// kind, and name. An empty name represents a wildcard (any resource name).
func ConvertTo(namespace string, group string, kind, name string) To {
	return To(namespace + "/" + group + "/" + kind + "/" + name)
}

// ConvertFrom builds a From key from the source resource's namespace, API group,
// and kind.
func ConvertFrom(namespace, group, kind string) From {
	return From(namespace + "/" + group + "/" + kind)
}

// ConvertReferenceGrantNamespacedName extracts the namespace/name identity of a
// ReferenceGrant from its K8sResource. K8sResource must not be nil.
func ConvertReferenceGrantNamespacedName(referenceGrant ReferenceGrant) ReferenceGrantNamespacedName {
	return ReferenceGrantNamespacedName{
		Namespace: referenceGrant.K8sResource.Namespace,
		Name:      referenceGrant.K8sResource.Name,
	}
}

// NewReferenceGrantManager returns an initialised ReferenceGrantManager with all
// internal maps allocated.
func NewReferenceGrantManager() *ReferenceGrantManager {
	return &ReferenceGrantManager{
		toReferenceGrantFrom: map[To]map[ReferenceGrantNamespacedName]map[From]struct{}{},
		referenceGrantsTo:    map[ReferenceGrantNamespacedName]map[To]struct{}{},
		toFrom:               map[To]map[From]struct{}{},
	}
}

// IsAccessGranted reports whether a resource of type (fromGroup, fromKind) in
// fromNamespace may reference a resource of type (toGroup, toKind) named toName
// in toNamespace. Same-namespace references are always permitted. Cross-namespace
// access requires a matching entry in toFrom, covering both named grants and
// wildcard grants (empty name).
func (mgr *ReferenceGrantManager) IsAccessGranted(fromGroup, fromKind, fromNamespace,
	toGroup, toKind, toNamespace, toName string,
) bool {
	// Same namespace access is always granted
	if toNamespace == fromNamespace {
		return true
	}
	convertedTo := ConvertTo(toNamespace, toGroup, toKind, toName)
	convertedToWithoutName := ConvertTo(toNamespace, toGroup, toKind, "")
	convertedFrom := ConvertFrom(fromNamespace, fromGroup, fromKind)
	froms := mgr.toFrom[convertedTo]
	fromsWithoutName := mgr.toFrom[convertedToWithoutName]
	if froms == nil && fromsWithoutName == nil {
		// No grants for this 'To'
		return false
	}
	_, granted := froms[convertedFrom]
	if !granted {
		_, granted = fromsWithoutName[convertedFrom]
	}
	return granted
}

// UpsertReferenceGrant registers or updates the access grants defined by the given
// ReferenceGrant. It clears any previous entries for the grant before inserting the
// current Spec, so that updates are handled correctly without diffing old vs new rules.
// toReferenceGrantFrom and referenceGrantsTo are updated; call ComputeToFrom afterwards
// to reflect the change in IsAccessGranted.
func (mgr *ReferenceGrantManager) UpsertReferenceGrant(referenceGrant ReferenceGrant) {
	if referenceGrant.K8sResource == nil ||
		referenceGrant.TreeStatus.Status != store.StatusUpserted {
		return
	}

	referenceGrantNamespacedName := ConvertReferenceGrantNamespacedName(referenceGrant)
	// We remove any previous association
	mgr.RemoveReferenceGrantWithCheck(referenceGrant, false)
	// For each 'To' of the ReferenceGrant
	for _, to := range referenceGrant.K8sResource.Spec.To {
		convertedTo := ConvertTo(referenceGrant.K8sResource.Namespace,
			string(to.Group),
			string(to.Kind),
			string(utils.PointerDefaultValueIfNil(to.Name)))
		// ___________________________
		// Update toReferenceGrantFrom
		referenceGrantFrom := mgr.toReferenceGrantFrom[convertedTo]
		for _, from := range referenceGrant.K8sResource.Spec.From {
			convertedFrom := ConvertFrom(string(from.Namespace),
				string(from.Group),
				string(from.Kind))
			// We associate the 'To' with the couple 'ReferenceGrant' and 'From'
			if referenceGrantFrom == nil {
				// First association so we create the map
				referenceGrantFrom = map[ReferenceGrantNamespacedName]map[From]struct{}{
					referenceGrantNamespacedName: {
						convertedFrom: {},
					},
				}
				mgr.toReferenceGrantFrom[convertedTo] = referenceGrantFrom
			} else {
				// Subsequent association so update the association
				froms := referenceGrantFrom[referenceGrantNamespacedName]
				if froms == nil {
					froms = map[From]struct{}{}
					referenceGrantFrom[referenceGrantNamespacedName] = froms
				}
				referenceGrantFrom[referenceGrantNamespacedName][convertedFrom] = struct{}{}
			}
		}
		// ________________________
		// Update referenceGrantsTo
		referenceGrantsTo := mgr.referenceGrantsTo[referenceGrantNamespacedName]
		if referenceGrantsTo == nil {
			referenceGrantsTo = map[To]struct{}{
				convertedTo: {},
			}
			mgr.referenceGrantsTo[referenceGrantNamespacedName] = referenceGrantsTo
		} else {
			referenceGrantsTo[convertedTo] = struct{}{}
		}
	}
}

// RemoveReferenceGrantWithCheck removes all entries for a ReferenceGrant.
// When check is true, the function validates that the grant's status is StatusDeleted
// and operates on OldTreeResource (the pre-deletion snapshot). When check is false,
// it operates on the grant as given; this is used by UpsertReferenceGrant to clear
// the previous footprint before reinserting updated rules.
func (mgr *ReferenceGrantManager) RemoveReferenceGrantWithCheck(referenceGrant ReferenceGrant, check bool) {
	if check && referenceGrant.TreeStatus.Status != store.StatusDeleted {
		return
	}
	if check {
		removedReferenceGrant := referenceGrant.TreeStatus.OldTreeResource
		if removedReferenceGrant == nil {
			return
		}
		referenceGrant = *removedReferenceGrant
	}
	referenceGrantNamespacedName := ConvertReferenceGrantNamespacedName(referenceGrant)
	// Get all the To from ReferenceGrant with inverse index
	tos := mgr.referenceGrantsTo[referenceGrantNamespacedName]
	// For each To
	for to := range tos {
		referenceGrantFrom, exists := mgr.toReferenceGrantFrom[to]
		// If no association for this 'To'
		if referenceGrantFrom == nil && !exists {
			// Delete the 'To' key in inverse map referenceGrantsTo
			delete(tos, to)
			continue
		}
		// If empty association for this 'To'
		if exists && len(referenceGrantFrom) == 0 {
			// Delete the 'To' key in full map toReferenceGrantFrom
			delete(mgr.toReferenceGrantFrom, to)
			// Delete the 'To' key in inverse map referenceGrantsTo
			delete(tos, to)
			// And continue
			continue
		}

		delete(referenceGrantFrom, referenceGrantNamespacedName)
		// Cleanup if empty
		if len(referenceGrantFrom) == 0 {
			delete(mgr.toReferenceGrantFrom, to)
		}
		delete(tos, to)
	}
	if len(tos) == 0 {
		delete(mgr.referenceGrantsTo, referenceGrantNamespacedName)
	}
}

// RemoveReferenceGrant removes all entries for a deleted ReferenceGrant.
// It is the public counterpart of RemoveReferenceGrantWithCheck with check=true.
func (mgr *ReferenceGrantManager) RemoveReferenceGrant(referenceGrant ReferenceGrant) {
	mgr.RemoveReferenceGrantWithCheck(referenceGrant, true)
}

// ComputeToFrom rebuilds the toFrom map from toReferenceGrantFrom. It must be called
// after all UpsertReferenceGrant and RemoveReferenceGrant calls for a reconcile cycle
// have completed, and before any IsAccessGranted call consults the result.
func (mgr *ReferenceGrantManager) ComputeToFrom() {
	mgr.toFrom = map[To]map[From]struct{}{}
	for to, referenceGrantFrom := range mgr.toReferenceGrantFrom {
		for _, froms := range referenceGrantFrom {
			for from := range froms {
				existingFroms := mgr.toFrom[to]
				if existingFroms == nil {
					existingFroms = map[From]struct{}{}
					mgr.toFrom[to] = existingFroms
				}
				existingFroms[from] = struct{}{}
			}
		}
	}
}
