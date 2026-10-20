package tree

import (
	"slices"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	"k8s.io/apimachinery/pkg/types"
	gatewayv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
)

// GrantFrom identifies the source side of a ReferenceGrant check.
type GrantFrom struct {
	Group     string
	Kind      string
	Namespace string
}

// GrantTo identifies the target side of a ReferenceGrant check.
// An empty Name means the grant applies to any resource name (wildcard).
type GrantTo struct {
	Group     string
	Kind      string
	Namespace string
	Name      string
}

// ReferenceGrantNamespacedName is the namespace/name key of a ReferenceGrant.
type ReferenceGrantNamespacedName types.NamespacedName

// ReferenceGrantManager tracks which cross-namespace references are permitted
// by ReferenceGrant resources. It maintains three maps:
//
//   - toReferenceGrantFrom: for each target (GrantTo), which ReferenceGrants
//     cover it and from which source types (GrantFrom). Used to incrementally
//     update toFrom.
//
//   - referenceGrantsTo: inverse index — for each ReferenceGrant, which targets
//     (GrantTo) it covers. Required to efficiently remove all entries for a
//     deleted grant.
//
//   - toFrom: the resolved flat map consumed by IsAccessGranted. Rebuilt from
//     toReferenceGrantFrom by ComputeToFrom after each reconcile cycle.
type ReferenceGrantManager struct {
	toReferenceGrantFrom map[GrantTo]map[ReferenceGrantNamespacedName]map[GrantFrom]struct{}
	referenceGrantsTo    map[ReferenceGrantNamespacedName]map[GrantTo]struct{}
	toFrom               map[GrantTo]map[GrantFrom]struct{}
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
		toReferenceGrantFrom: map[GrantTo]map[ReferenceGrantNamespacedName]map[GrantFrom]struct{}{},
		referenceGrantsTo:    map[ReferenceGrantNamespacedName]map[GrantTo]struct{}{},
		toFrom:               map[GrantTo]map[GrantFrom]struct{}{},
	}
}

// IsAccessGranted reports whether the resource described by from may reference
// the resource described by to. Same-namespace references are always permitted.
// Cross-namespace access requires a matching entry in toFrom, covering both
// named grants and wildcard grants (empty Name).
func (mgr *ReferenceGrantManager) IsAccessGranted(from GrantFrom, to GrantTo) bool {
	// Same namespace access is always granted
	if to.Namespace == from.Namespace {
		return true
	}
	wildcard := GrantTo{Group: to.Group, Kind: to.Kind, Namespace: to.Namespace}
	froms := mgr.toFrom[to]
	fromsWildcard := mgr.toFrom[wildcard]
	if froms == nil && fromsWildcard == nil {
		// No grants for this 'To'
		return false
	}
	_, granted := froms[from]
	if !granted {
		_, granted = fromsWildcard[from]
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
	mgr.removeReferenceGrantWithCheck(referenceGrant, false)
	// For each 'To' of the ReferenceGrant
	for _, grantTo := range referenceGrantTos(referenceGrant.K8sResource) {
		// ___________________________
		// Update toReferenceGrantFrom
		referenceGrantFrom := mgr.toReferenceGrantFrom[grantTo]
		for _, grantFrom := range referenceGrantFroms(referenceGrant.K8sResource) {
			// We associate the 'To' with the couple 'ReferenceGrant' and 'From'
			if referenceGrantFrom == nil {
				// First association so we create the map
				referenceGrantFrom = map[ReferenceGrantNamespacedName]map[GrantFrom]struct{}{
					referenceGrantNamespacedName: {
						grantFrom: {},
					},
				}
				mgr.toReferenceGrantFrom[grantTo] = referenceGrantFrom
			} else {
				// Subsequent association so update the association
				froms := referenceGrantFrom[referenceGrantNamespacedName]
				if froms == nil {
					froms = map[GrantFrom]struct{}{}
					referenceGrantFrom[referenceGrantNamespacedName] = froms
				}
				referenceGrantFrom[referenceGrantNamespacedName][grantFrom] = struct{}{}
			}
		}
		// ________________________
		// Update referenceGrantsTo
		referenceGrantsTo := mgr.referenceGrantsTo[referenceGrantNamespacedName]
		if referenceGrantsTo == nil {
			referenceGrantsTo = map[GrantTo]struct{}{
				grantTo: {},
			}
			mgr.referenceGrantsTo[referenceGrantNamespacedName] = referenceGrantsTo
		} else {
			referenceGrantsTo[grantTo] = struct{}{}
		}
	}
}

// referenceGrantTos lists the To side a ReferenceGrant declares, in the manager's
// vocabulary: the grant's own namespace, the declared group and kind, and an absent name
// spelled as the empty wildcard GrantTo uses.
func referenceGrantTos(referenceGrant *gatewayv1beta1.ReferenceGrant) []GrantTo {
	if referenceGrant == nil {
		return nil
	}
	tos := make([]GrantTo, 0, len(referenceGrant.Spec.To))
	for _, to := range referenceGrant.Spec.To {
		tos = append(tos, GrantTo{
			Namespace: referenceGrant.Namespace,
			Group:     string(to.Group),
			Kind:      string(to.Kind),
			Name:      string(utils.PointerDefaultValueIfNil(to.Name)),
		})
	}
	return tos
}

// referenceGrantFroms lists the From side a ReferenceGrant declares, in the manager's
// vocabulary.
func referenceGrantFroms(referenceGrant *gatewayv1beta1.ReferenceGrant) []GrantFrom {
	if referenceGrant == nil {
		return nil
	}
	froms := make([]GrantFrom, 0, len(referenceGrant.Spec.From))
	for _, from := range referenceGrant.Spec.From {
		froms = append(froms, GrantFrom{
			Namespace: string(from.Namespace),
			Group:     string(from.Group),
			Kind:      string(from.Kind),
		})
	}
	return froms
}

// ReferenceGrantCovers reports whether a single ReferenceGrant, from its spec alone,
// permits the given reference. It is the stateless half of IsAccessGranted: it reads no
// resolved map, so an enqueue edge can filter on exactly the shape the manager is built
// from -- referenceGrantTos/referenceGrantFroms are shared with UpsertReferenceGrant --
// rather than keeping a second, drift-prone copy of it.
//
// A same-namespace reference is not "covered": it needs no grant at all, so a grant
// appearing or disappearing cannot be its cause.
func ReferenceGrantCovers(referenceGrant *gatewayv1beta1.ReferenceGrant, from GrantFrom, to GrantTo) bool {
	if to.Namespace == from.Namespace {
		// Same namespace: no grant is needed, so none can be the one permitting it.
		return false
	}
	for _, grantTo := range referenceGrantTos(referenceGrant) {
		if !grantToMatches(grantTo, to) {
			continue
		}
		if slices.Contains(referenceGrantFroms(referenceGrant), from) {
			return true
		}
	}
	return false
}

// grantToMatches reports whether a grant's To side covers a wanted target. An empty
// granted name is the wildcard IsAccessGranted resolves through its wildcard lookup.
func grantToMatches(granted, wanted GrantTo) bool {
	if granted.Group != wanted.Group || granted.Kind != wanted.Kind || granted.Namespace != wanted.Namespace {
		return false
	}
	return granted.Name == "" || granted.Name == wanted.Name
}

// removeReferenceGrantWithCheck removes all entries for a ReferenceGrant.
// When check is true, the function validates that the grant's status is StatusDeleted
// and operates on OldTreeResource (the pre-deletion snapshot). When check is false,
// it operates on the grant as given; this is used by UpsertReferenceGrant to clear
// the previous footprint before reinserting updated rules.
func (mgr *ReferenceGrantManager) removeReferenceGrantWithCheck(referenceGrant ReferenceGrant, check bool) {
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
func (mgr *ReferenceGrantManager) RemoveReferenceGrant(referenceGrant ReferenceGrant) {
	mgr.removeReferenceGrantWithCheck(referenceGrant, true)
}

// ComputeToFrom rebuilds the toFrom map from toReferenceGrantFrom. It must be called
// after all UpsertReferenceGrant and RemoveReferenceGrant calls for a reconcile cycle
// have completed, and before any IsAccessGranted call consults the result.
func (mgr *ReferenceGrantManager) ComputeToFrom() {
	mgr.toFrom = map[GrantTo]map[GrantFrom]struct{}{}
	for to, referenceGrantFrom := range mgr.toReferenceGrantFrom {
		for _, froms := range referenceGrantFrom {
			for from := range froms {
				existingFroms := mgr.toFrom[to]
				if existingFroms == nil {
					existingFroms = map[GrantFrom]struct{}{}
					mgr.toFrom[to] = existingFroms
				}
				existingFroms[from] = struct{}{}
			}
		}
	}
}
