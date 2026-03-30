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
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/haproxytech/client-native/v6/models"
	haproxyfilters "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/filters"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/metadata"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/templates"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"
	"github.com/imdario/mergo"

	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
	gatewayv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
)

type BackendOwnerType string

const (
	BackendOwnerTypeHTTPRoute BackendOwnerType = "HTTPRoute"
	BackendOwnerTypeTLSRoute  BackendOwnerType = "TLSRoute"
	cookieKey                                  = "ohph7OoGhong"
)

type BackendReferencedBy struct {
	owners map[string]map[BackendOwnerType]map[client.ObjectKey]int64 // map[backendName] -> map[ownerType] -> map[owner RouteKey] -> generation
}

type BackendImpactedInCycle struct {
	HTTPRouteKey client.ObjectKey
	Name         string
	// MatchPrefix is the first PathPrefix value found in the rule's matches,
	// used for URLRewrite/RequestRedirect ReplacePrefixMatch path modifiers.
	MatchPrefix string
	BackendRef  gatewayv1.HTTPBackendRef
	// RuleFilters holds the rule-level HTTPRoute filters that apply to this backend.
	RuleFilters       []gatewayv1.HTTPRouteFilter
	ResourceCandidate ResourceCandidate
	// IsRedirect indicates this is a redirect-only pseudo-backend with no servers.
	IsRedirect bool
}

type BackendsImpactedInCycle struct {
	Upserted map[string]map[client.ObjectKey]BackendImpactedInCycle // map[backendName] -> map [routeKey]
	Deleted  map[string]struct{}                                    // map[backendName]
	// For unreferenced backends, just update the metadata
	Unreferenced map[string]struct{} // map[backendName]
}

func NewBackendOwners() BackendReferencedBy {
	return BackendReferencedBy{owners: make(map[string]map[BackendOwnerType]map[client.ObjectKey]int64)}
}

func (b *HaproxyConfMgrImpl) getBackendName(svcKey k8stypes.NamespacedName, svcPort int32, filterHash string) (string, error) {
	tmpl, err := template.New("backend").Parse(b.params.BackendNameTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse backend name template: %w", err)
	}

	data := templates.TemplateData{
		SERVICE_NAMESPACE: svcKey.Namespace,
		SERVICE_NAME:      svcKey.Name,
		SERVICE_PORT:      svcPort,
		FILTER_HASH:       filterHash,
		LINK_ID:           b.params.LinkID,
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (b *HaproxyConfMgrImpl) processHTTPRoutes() error {
	var errs utils.Errors
	// Managed HTTPRoutes => Create / update/ delete backends
	for routeKey, route := range b.controllerStore.GateTree.HTTPRoutes {
		switch route.TreeStatus.Status {
		case store.StatusUnchanged:
			continue
		case store.StatusUpserted:
			err := b.onUpsertedHTTPRoute(routeKey, route)
			errs.Add(err)
		case store.StatusDeleted:
			err := b.onDeletedHTTPRoute(routeKey, route)
			errs.Add(err)
		}
	}

	// Cleanup Backends that are not referenced anymore
	if err := b.cleanupUnreferencedBackends(); err != nil {
		b.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to cleanup unreferenced backends",
			logging.LogAttrError(err),
		)
		errs.Add(err)
	}

	// Now we have the list of upserted + delete BE with the correct list of routes pointing to them
	if err := b.processBackendsModifiedInCycle(); err != nil {
		b.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to process backends modified in cycle",
			logging.LogAttrError(err),
		)
		errs.Add(err)
	}

	return errs.Result()
}

func (b *HaproxyConfMgrImpl) onUpsertedHTTPRoute(routeKey k8stypes.NamespacedName, route *tree.HTTPRoute) error {
	if route.Valid {
		return b.onValidHTTPRouteUpserted(routeKey, route)
	}
	return b.onInvalidHTTPRouteUpserted(routeKey, route)
}

func (b *HaproxyConfMgrImpl) onValidHTTPRouteUpserted(routeKey k8stypes.NamespacedName, route *tree.HTTPRoute) error {
	b.logHTTPRouteUpdate("upserted", routeKey)
	err := b.upsertHTTPRouteBackends(routeKey, route)
	return err
}

func (b *HaproxyConfMgrImpl) onInvalidHTTPRouteUpserted(routeKey k8stypes.NamespacedName, _ *tree.HTTPRoute) error {
	b.logHTTPRouteUpdate("upserted-invalid", routeKey)
	impactedBackends := b.backendOwners.getBackendsReferencedByHTTPRoute(routeKey)
	for beName := range impactedBackends {
		b.backendOwners.removeHTTPRoute(beName, routeKey)
		b.addImpactedBackendDeleted(beName)
	}
	return nil
}

func (b *HaproxyConfMgrImpl) onDeletedHTTPRoute(routeKey k8stypes.NamespacedName, _ *tree.HTTPRoute) error {
	b.logHTTPRouteUpdate("deleted", routeKey)
	impactedBackends := b.backendOwners.getBackendsReferencedByHTTPRoute(routeKey)
	for beName := range impactedBackends {
		b.backendOwners.removeHTTPRoute(beName, routeKey)
		b.addImpactedBackendDeleted(beName)
	}
	return nil
}

func (b *HaproxyConfMgrImpl) onDeletedTLSRoute(routeKey k8stypes.NamespacedName, _ *tree.TLSRoute) error {
	b.logTLSRouteUpdate("deleted", routeKey)
	impactedBackends := b.backendOwners.getBackendsReferencedByTLSRoute(routeKey)
	for beName := range impactedBackends {
		b.backendOwners.removeTLSRoute(beName, routeKey)
		b.addImpactedBackendDeleted(beName)
	}
	return nil
}

func (b *HaproxyConfMgrImpl) logHTTPRouteUpdate(action string, key k8stypes.NamespacedName) {
	b.logger.LogAttrs(context.Background(), slog.LevelDebug, "Processing HTTPRoute ["+action+"]",
		logging.LogAttrKey(key),
	)
}

func (b *HaproxyConfMgrImpl) logTLSRouteUpdate(action string, key k8stypes.NamespacedName) {
	b.logger.LogAttrs(context.Background(), slog.LevelDebug, "Processing TLSRoute ["+action+"]",
		logging.LogAttrKey(key),
	)
}

func (b *HaproxyConfMgrImpl) upsertHTTPRouteBackends(routeKey k8stypes.NamespacedName, route *tree.HTTPRoute) error {
	var errs utils.Errors
	upsertedBackendsReferencedByRoute := make(map[string]struct{})
	for ruleIndex, rule := range route.Rules {
		k8sRule := rule.K8sResource

		// Rules with a RequestRedirect filter are redirect-only: no real backend
		// proxying takes place.  We create a redirect pseudo-backend instead.
		// Handle this before the rule.Valid check since such rules may have no
		// backendRefs (which would cause rule.Valid to be false).
		if hasRedirectFilter(k8sRule.Filters) {
			beName := b.getRedirectBackendName(k8sRule.Filters)
			if err := b.backendOwners.addHTTPRoute(beName, routeKey, route.K8sResource); err != nil {
				errs.Add(err)
				continue
			}
			b.addImpactedHTTPRedirectBackendUpserted(beName, routeKey, k8sRule.Filters,
				extractFirstPathPrefix(k8sRule.Matches))
			upsertedBackendsReferencedByRoute[beName] = struct{}{}
			continue
		}

		if !rule.Valid {
			continue
		}

		// Extract the first PathPrefix match value for URLRewrite prefix rewriting.
		matchPrefix := extractFirstPathPrefix(k8sRule.Matches)

		// Iterate now on each referenced Backend.
		for backendRefIndex, backendRef := range k8sRule.BackendRefs {
			filterHash := getFilterHash(k8sRule.Filters, backendRef.Filters)
			var svcPort int32
			svcNsName := tree.ServiceNsNameKey(route.K8sResource.Namespace, backendRef.BackendObjectReference)
			if backendRef.Port != nil {
				svcPort = int32(*backendRef.Port)
			}
			beName, err := b.getBackendName(svcNsName, svcPort, filterHash)
			if err != nil {
				b.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to compute backendName",
					logging.LogAttrError(err))
				errs.Add(err)
				continue
			}

			// Check if backendRef is valid
			checkResult, ok := rule.CheckBackendRef.Get(backendRef.BackendObjectReference)
			if !ok {
				b.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to check backendRef")
				continue
			}
			// If the specific backendCheck result is ok, add the BE
			if !checkResult.Valid {
				continue
			}
			err = b.backendOwners.addHTTPRoute(beName, routeKey, route.K8sResource)
			if err != nil {
				errs.Add(err)
				continue
			}
			persistenceCandidate := ResourceCandidate{
				CreationTimestamp:  route.K8sResource.CreationTimestamp.Time,
				Namespace:          route.K8sResource.Namespace,
				RouteName:          route.K8sResource.Name,
				RuleIndex:          ruleIndex,
				BackendIndex:       backendRefIndex,
				SessionPersistence: k8sRule.SessionPersistence,
				HTTPTimeouts:       k8sRule.Timeouts,
			}
			b.addImpactedHTTPBackendUpserted(beName, routeKey, backendRef, k8sRule.Filters, matchPrefix, persistenceCandidate)
			upsertedBackendsReferencedByRoute[beName] = struct{}{}
		}
	}

	// Now cleanup the referenced Backends
	// For example:
	// Scenario:
	// Step1: Route1 references BE1, BE2, BE3
	// Step2: Update Route1 references BE1, BE2
	// Action: We have to remove BE3 from Backends referenced by Route1
	backendsReferencedByRoute := b.backendOwners.getBackendsReferencedByHTTPRoute(routeKey)
	unreferenced := utils.SetDifference(backendsReferencedByRoute, upsertedBackendsReferencedByRoute)
	for unreferencedBeName := range unreferenced {
		b.backendOwners.removeHTTPRoute(unreferencedBeName, routeKey)
		b.backendsImpactedInCycle.Unreferenced[unreferencedBeName] = struct{}{}
	}
	return errs.Result()
}

func (b *HaproxyConfMgrImpl) upsertTLSRouteBackends(routeKey k8stypes.NamespacedName, route *tree.TLSRoute) error {
	var errs utils.Errors
	upsertedBackendsReferencedByRoute := make(map[string]struct{})
	for _, rule := range route.Rules {
		if rule.Valid {
			k8sRule := rule.K8sResource
			// Iterate now on each referenced Backend
			for _, backendRef := range k8sRule.BackendRefs {
				var svcPort int32
				svcNsName := tree.ServiceNsNameKey(route.K8sResource.Namespace, backendRef.BackendObjectReference)
				if backendRef.Port != nil {
					svcPort = int32(*backendRef.Port)
				}
				beName, err := b.getBackendName(svcNsName, svcPort, "")
				if err != nil {
					b.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to compute backendName",
						logging.LogAttrError(err))
					errs.Add(err)
					continue
				}

				// Check if backendRef is valid
				checkResult, ok := rule.CheckBackendRef.Get(backendRef.BackendObjectReference)
				if !ok {
					b.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to check backendRef")
					continue
				}
				// If the specific backendCheck result is ok, add the BE
				if checkResult.Valid {
					err := b.backendOwners.addTLSRoute(beName, routeKey, route.K8sResource)
					if err != nil {
						errs.Add(err)
						continue
					}
					b.addImpactedLTSBackendUpserted(beName, routeKey, backendRef)
					upsertedBackendsReferencedByRoute[beName] = struct{}{}
				}
			}
		}
	}

	// Now cleanup the referenced Backends
	// For example:
	// Scenario:
	// Step1: Route1 references BE1, BE2, BE3
	// Step2: Update Route1 references BE1, BE2
	// Action: We have to remove BE3 from Backends referenced by Route1
	backendsReferencedByRoute := b.backendOwners.getBackendsReferencedByTLSRoute(routeKey)
	unreferenced := utils.SetDifference(backendsReferencedByRoute, upsertedBackendsReferencedByRoute)
	for unreferencedBeName := range unreferenced {
		b.backendOwners.removeTLSRoute(unreferencedBeName, routeKey)
		b.backendsImpactedInCycle.Unreferenced[unreferencedBeName] = struct{}{}
	}
	return errs.Result()
}

func (b *HaproxyConfMgrImpl) processTLSRoutes() error {
	var errs utils.Errors
	// Managed HTTPRoutes => Create / update/ delete backends
	for routeKey, tlsRoute := range b.controllerStore.GateTree.TLSRoutes {
		switch tlsRoute.TreeStatus.Status {
		case store.StatusUnchanged:
			continue
		case store.StatusUpserted:
			err := b.onUpsertedTLSRoute(routeKey, tlsRoute)
			errs.Add(err)
		case store.StatusDeleted:
			err := b.onDeletedTLSRoute(routeKey, tlsRoute)
			errs.Add(err)
		}
	}

	// Cleanup Backends that are not referenced anymore
	if err := b.cleanupUnreferencedBackends(); err != nil {
		b.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to cleanup unreferenced backends",
			logging.LogAttrError(err),
		)
		errs.Add(err)
	}

	// Now we have the list of upserted + delete BE with the correct list of routes pointing to them
	if err := b.processBackendsModifiedInCycle(); err != nil {
		b.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to process backends modified in cycle",
			logging.LogAttrError(err),
		)
		errs.Add(err)
	}

	return errs.Result()
}

func (b *HaproxyConfMgrImpl) onUpsertedTLSRoute(routeKey k8stypes.NamespacedName, tlsRoute *tree.TLSRoute) error {
	switch tlsRoute.Valid {
	case true:
		err := b.onValidTLSRouteUpserted(routeKey, tlsRoute)
		if err != nil {
			return err
		}
	case false:
		err := b.onInvalidTLSRouteUpserted(routeKey, tlsRoute)
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *HaproxyConfMgrImpl) onValidTLSRouteUpserted(routeKey k8stypes.NamespacedName, route *tree.TLSRoute) error {
	b.logTLSRouteUpdate("upserted", routeKey)
	err := b.upsertTLSRouteBackends(routeKey, route)
	return err
}

func (b *HaproxyConfMgrImpl) onInvalidTLSRouteUpserted(routeKey k8stypes.NamespacedName, _ *tree.TLSRoute) error {
	b.logTLSRouteUpdate("upserted-invalid", routeKey)
	impactedBackends := b.backendOwners.getBackendsReferencedByTLSRoute(routeKey)
	for beName := range impactedBackends {
		b.backendOwners.removeTLSRoute(beName, routeKey)
		b.addImpactedBackendDeleted(beName)
	}
	return nil
}

func (b *HaproxyConfMgrImpl) addImpactedHTTPBackendUpserted(backendName string, routeKey client.ObjectKey,
	httpBackendRef gatewayv1.HTTPBackendRef, ruleFilters []gatewayv1.HTTPRouteFilter,
	matchPrefix string, resourceCandidate ResourceCandidate,
) {
	impactedBe := BackendImpactedInCycle{
		Name:              backendName,
		HTTPRouteKey:      routeKey,
		BackendRef:        httpBackendRef,
		RuleFilters:       ruleFilters,
		MatchPrefix:       matchPrefix,
		ResourceCandidate: resourceCandidate,
	}

	if _, ok := b.backendsImpactedInCycle.Upserted[backendName]; !ok {
		b.backendsImpactedInCycle.Upserted[backendName] = make(map[client.ObjectKey]BackendImpactedInCycle)
	}
	b.backendsImpactedInCycle.Upserted[backendName][routeKey] = impactedBe
}

func (b *HaproxyConfMgrImpl) addImpactedHTTPRedirectBackendUpserted(backendName string, routeKey client.ObjectKey,
	ruleFilters []gatewayv1.HTTPRouteFilter, matchPrefix string,
) {
	impactedBe := BackendImpactedInCycle{
		Name:         backendName,
		HTTPRouteKey: routeKey,
		RuleFilters:  ruleFilters,
		MatchPrefix:  matchPrefix,
		IsRedirect:   true,
	}
	if _, ok := b.backendsImpactedInCycle.Upserted[backendName]; !ok {
		b.backendsImpactedInCycle.Upserted[backendName] = make(map[client.ObjectKey]BackendImpactedInCycle)
	}
	b.backendsImpactedInCycle.Upserted[backendName][routeKey] = impactedBe
}

func (b *HaproxyConfMgrImpl) getRedirectBackendName(filters []gatewayv1.HTTPRouteFilter) string {
	return fmt.Sprintf("%s_redirect_%s", b.params.LinkID, getRedirectFilterHash(filters))
}

func (b *HaproxyConfMgrImpl) addImpactedLTSBackendUpserted(backendName string, routeKey client.ObjectKey,
	_ gatewayv1alpha2.BackendRef,
) {
	impactedBe := BackendImpactedInCycle{
		Name:         backendName,
		HTTPRouteKey: routeKey,
		// BackendRef:   tlsBackendRef,
	}

	if _, ok := b.backendsImpactedInCycle.Upserted[backendName]; !ok {
		b.backendsImpactedInCycle.Upserted[backendName] = make(map[client.ObjectKey]BackendImpactedInCycle)
	}
	b.backendsImpactedInCycle.Upserted[backendName][routeKey] = impactedBe
}

func (b *HaproxyConfMgrImpl) addImpactedBackendDeleted(backendName string) {
	b.backendsImpactedInCycle.Deleted[backendName] = struct{}{}
}

func (bo *BackendReferencedBy) addHTTPRoute(backendName string, routeKey k8stypes.NamespacedName, route *gatewayv1.HTTPRoute) error {
	if route == nil {
		return errors.New("nil route")
	}
	bo.add(backendName, BackendOwnerTypeHTTPRoute, routeKey, route.Generation)
	return nil
}

func (bo *BackendReferencedBy) addTLSRoute(backendName string, routeKey k8stypes.NamespacedName, route *gatewayv1alpha2.TLSRoute) error {
	if route == nil {
		return errors.New("nil route")
	}
	bo.add(backendName, BackendOwnerTypeTLSRoute, routeKey, route.Generation)
	return nil
}

func (bo *BackendReferencedBy) add(backendName string, ownerType BackendOwnerType, ownerKey client.ObjectKey, generation int64) {
	if _, ok := bo.owners[backendName]; !ok {
		bo.owners[backendName] = make(map[BackendOwnerType]map[client.ObjectKey]int64)
	}
	if _, ok := bo.owners[backendName][ownerType]; !ok {
		bo.owners[backendName][ownerType] = make(map[client.ObjectKey]int64)
	}
	bo.owners[backendName][ownerType][ownerKey] = generation
}

func (bo *BackendReferencedBy) removeHTTPRoute(backendName string, routeKey k8stypes.NamespacedName) {
	bo.remove(backendName, BackendOwnerTypeHTTPRoute, routeKey)
}

func (bo *BackendReferencedBy) removeTLSRoute(backendName string, routeKey k8stypes.NamespacedName) {
	bo.remove(backendName, BackendOwnerTypeTLSRoute, routeKey)
}

func (bo *BackendReferencedBy) remove(backendName string, ownerType BackendOwnerType, ownerKey client.ObjectKey) {
	ownersForBackend, ok := bo.owners[backendName]
	if !ok {
		return
	}
	ownersForType, ok := ownersForBackend[ownerType]
	if !ok {
		return
	}
	delete(ownersForType, ownerKey)
}

func (bo *BackendReferencedBy) getBackendsReferencedByHTTPRoute(ownerKey client.ObjectKey) map[string]struct{} { // map[beName] -> struct{}
	return bo.getBackendsReferencedBy(BackendOwnerTypeHTTPRoute, ownerKey)
}

func (bo *BackendReferencedBy) getBackendsReferencedByTLSRoute(ownerKey client.ObjectKey) map[string]struct{} { // map[beName] -> struct{}
	return bo.getBackendsReferencedBy(BackendOwnerTypeTLSRoute, ownerKey)
}

func (bo *BackendReferencedBy) getBackendsReferencedBy(ownerType BackendOwnerType, ownerKey client.ObjectKey) map[string]struct{} { // map[beName] -> struct{}
	backends := make(map[string]struct{})
	for backendName, ownersForBackend := range bo.owners {
		ownersForType, ok := ownersForBackend[ownerType]
		if !ok {
			continue
		}
		if _, ok := ownersForType[ownerKey]; ok {
			backends[backendName] = struct{}{}
		}
	}
	return backends
}

func (b *HaproxyConfMgrImpl) cleanupUnreferencedBackends() error {
	var err utils.Errors
	err.Add(b.cleanupUnreferencedBackendsForHTTPRoutes(BackendOwnerTypeHTTPRoute),
		b.cleanupUnreferencedBackendsForHTTPRoutes(BackendOwnerTypeTLSRoute))
	return err.Result()
}

func (b *HaproxyConfMgrImpl) cleanupUnreferencedBackendsForHTTPRoutes(ownerType BackendOwnerType) error {
	for beName, ownersForBackend := range b.backendOwners.owners {
		ownersForType, ok := ownersForBackend[ownerType]
		if !ok {
			continue
		}
		if len(ownersForType) == 0 {
			delete(ownersForBackend, ownerType)
		}
		if len(ownersForBackend) == 0 {
			delete(b.backendOwners.owners, beName)
			b.addImpactedBackendDeleted(beName)
		}
	}
	return nil
}

//revive:disable:flag-parameter
func (b *HaproxyConfMgrImpl) newBackend(backendName string, md metadata.MetaData,
	backendRef gatewayv1.HTTPBackendRef, ruleFilters []gatewayv1.HTTPRouteFilter, matchPrefix string,
	sessionPersistence *gatewayv1.SessionPersistence,
	httpTimeouts *gatewayv1.HTTPRouteTimeouts, namespace string, isHTTPBackend bool,
) (*models.Backend, error) {
	// First, we merge the Backend CRDs from filters, if there are some
	// Backend CRDs are defined in the Filters of type: ExtensionRef
	// We gather all those filters, merge them and apply them
	var optionForwardFor *models.Forwardfor
	if isHTTPBackend {
		optionForwardFor = &models.Forwardfor{
			Enabled: new("enabled"),
		}
	}

	newBackend := &models.Backend{
		BackendBase: models.BackendBase{
			Metadata: md,
			Name:     backendName,
			Mode: func() string {
				if isHTTPBackend {
					return "http"
				}
				return "tcp"
			}(),
			From:          b.params.DefaultsSectionName,
			Balance:       &models.Balance{Algorithm: new("roundrobin")},
			Abortonclose:  "disabled",
			ServerTimeout: new(int64(50000)),
			Forwardfor:    optionForwardFor,
			DefaultServer: &models.DefaultServer{
				ServerParams: models.ServerParams{Check: "enabled"},
			},
		},
	}

	// Now Merge with the Backend CRs
	errs := b.mergeWithBackendCRs(backendRef, newBackend, namespace)
	// Handling session persistence with cookie
	if sessionPersistence != nil &&
		utils.PointerDefaultValueIfNil(sessionPersistence.Type) == gatewayv1.CookieBasedSessionPersistence {
		sessionName := utils.PointerDefaultValueIfNil(sessionPersistence.SessionName)
		if sessionName == "" {
			sessionName = fmt.Sprintf("gwapi-%s", strings.ToLower(backendName))
		}

		// We need to create a cookie for the backend
		cookie := &models.Cookie{
			Name:     &sessionName,
			Type:     "insert",
			Nocache:  true,
			Indirect: true,
			Dynamic:  true,
			Domains:  []*models.Domain{},
		}

		if sessionPersistence.AbsoluteTimeout != nil {
			absoluteTimeout, err := time.ParseDuration(string(*sessionPersistence.AbsoluteTimeout))
			if err != nil {
				errs.Add(err)
			} else {
				cookie.Maxlife = int64(absoluteTimeout.Seconds())
			}
		}

		if sessionPersistence.IdleTimeout != nil {
			idleTimeout, err := time.ParseDuration(string(*sessionPersistence.IdleTimeout))
			if err != nil {
				errs.Add(err)
			} else {
				cookie.Maxidle = int64(idleTimeout.Seconds())
			}
		}

		newBackend.Cookie = cookie
		newBackend.BackendBase.DynamicCookieKey = cookieKey
	}

	if httpTimeouts != nil {
		// At this time, HAProxy does not provide a way to configure the timeout for the request
		// So we will only configure the timeout for the backend request
		if httpTimeouts.BackendRequest != nil {
			backendRequestTimeout, err := time.ParseDuration(string(*httpTimeouts.BackendRequest))
			if err == nil {
				v := int64(backendRequestTimeout.Seconds() * 1000)
				newBackend.ServerTimeout = &v
			}
		}
	}

	// Apply HTTP rules derived from rule-level and backendRef-level filters.
	// Rule-level filters are applied first so that backendRef filters can override them.
	allFilters := make([]gatewayv1.HTTPRouteFilter, 0, len(ruleFilters)+len(backendRef.Filters))
	allFilters = append(allFilters, ruleFilters...)
	allFilters = append(allFilters, backendRef.Filters...)
	filterResult := haproxyfilters.ToHAProxyRules(allFilters, matchPrefix)
	newBackend.HTTPRequestRuleList = append(newBackend.HTTPRequestRuleList, filterResult.HTTPRequestRules...)
	newBackend.HTTPResponseRuleList = append(newBackend.HTTPResponseRuleList, filterResult.HTTPResponseRules...)

	return newBackend, errs.Result()
}

// newRedirectBackend creates a backend that only issues an HTTP redirect with
// no real servers.  HAProxy executes the http-request redirect rule before any
// server selection, so an empty server list is valid here.
func (b *HaproxyConfMgrImpl) newRedirectBackend(backendName string, md metadata.MetaData,
	ruleFilters []gatewayv1.HTTPRouteFilter, matchPrefix string,
) (*models.Backend, error) {
	filterResult := haproxyfilters.ToHAProxyRules(ruleFilters, matchPrefix)
	if !filterResult.IsRedirect || len(filterResult.RedirectRules) == 0 {
		return nil, fmt.Errorf("redirect backend %q has no redirect rule", backendName)
	}
	be := &models.Backend{
		BackendBase: models.BackendBase{
			Metadata: md,
			Name:     backendName,
			Mode:     "http",
			From:     b.params.DefaultsSectionName,
		},
		HTTPRequestRuleList: filterResult.RedirectRules,
	}
	return be, nil
}

func (b *HaproxyConfMgrImpl) mergeWithBackendCRs(backendRef gatewayv1.HTTPBackendRef, newBackend *models.Backend, namespace string) utils.Errors {
	var errs utils.Errors

	// Now Merge with the Backend CRs
	// Default Merge options is : Append
	opts := []func(*mergo.Config){mergo.WithOverride, mergo.WithAppendSlice}

	for _, filter := range backendRef.Filters {
		if filter.Type != gatewayv1.HTTPRouteFilterExtensionRef {
			continue
		}
		// We only accept v3.Backend or MergeType
		// Note that MergeType is not a real CRD, it's only a way to configure how the merge behaves.
		// There is no:
		// - Group: gate.v3.haproxy.org
		// - Kind: MergeType CRDs
		// Name can only have 2 values:
		// - Name: Override || Append
		isFilterExtensionRefKindSupported := utilsk8s.IsFilterExtensionRefKindSupported(filter.ExtensionRef, b.params.extractGVK)
		isFilterExtensionRefKindMergeType := utilsk8s.IsFilterExtensionRefKindMergeType(filter.ExtensionRef, b.params.extractGVK)

		if !(isFilterExtensionRefKindSupported || isFilterExtensionRefKindMergeType) {
			continue
		}

		// MergeType is set if present
		if isFilterExtensionRefKindMergeType {
			// If it's a MergeType, then we just set the correct merge type for next Merges
			switch filter.ExtensionRef.Name {
			case "Override":
				opts = []func(*mergo.Config){mergo.WithOverride, mergo.WithOverrideEmptySlice}
				continue
			case "Append":
				opts = []func(*mergo.Config){mergo.WithOverride, mergo.WithAppendSlice}
				continue
			}
		}

		nsName := k8stypes.NamespacedName{
			Namespace: namespace,
			Name:      string(filter.ExtensionRef.Name),
		}
		_ = nsName
		beCR, ok := b.controllerStore.ClusterStore.BackendCRs[nsName]
		if !ok {
			continue
		}
		beCR.Spec.BackendBase.Name = ""
		// At this point we have or a Backend custom ExtensionRef or a MergType

		err := mergo.Merge(newBackend, &beCR.Spec.Backend, opts...)
		if err != nil {
			errs.Add(err)
			continue
		}
	}
	return errs
}

// getRedirectFilterHash produces a hash identifying a redirect-only backend.
func getRedirectFilterHash(filters []gatewayv1.HTTPRouteFilter) string {
	jsonData, err := json.Marshal(filters)
	if err != nil {
		return "unknown"
	}
	hash := md5.Sum(jsonData)
	return hex.EncodeToString(hash[:])
}

// hasRedirectFilter reports whether any filter in the slice is a RequestRedirect.
func hasRedirectFilter(filters []gatewayv1.HTTPRouteFilter) bool {
	for _, f := range filters {
		if f.Type == gatewayv1.HTTPRouteFilterRequestRedirect {
			return true
		}
	}
	return false
}

// getFilterHash produces a hash that uniquely identifies the backend
// configuration derived from rule-level and backendRef-level filters.
// Rule-level RequestHeaderModifier filters are combined with backendRef filters;
// RequestRedirect and RequestMirror are excluded because they are handled separately.
func getFilterHash(ruleFilters, backendRefFilters []gatewayv1.HTTPRouteFilter) string {
	var combined []gatewayv1.HTTPRouteFilter
	for _, f := range ruleFilters {
		switch f.Type {
		case gatewayv1.HTTPRouteFilterRequestHeaderModifier,
			gatewayv1.HTTPRouteFilterResponseHeaderModifier,
			gatewayv1.HTTPRouteFilterURLRewrite,
			gatewayv1.HTTPRouteFilterCORS:
			combined = append(combined, f)
		}
	}
	combined = append(combined, backendRefFilters...)

	if len(combined) == 0 {
		return "_"
	}
	jsonData, err := json.Marshal(combined)
	if err != nil {
		return "_"
	}
	hash := md5.Sum(jsonData)
	return hex.EncodeToString(hash[:])
}

// extractFirstPathPrefix returns the Value of the first PathPrefix match found,
// or an empty string if none exists.  This is used by URLRewrite and
// RequestRedirect filters that operate on the matched prefix.
func extractFirstPathPrefix(matches []gatewayv1.HTTPRouteMatch) string {
	for _, m := range matches {
		if m.Path == nil || m.Path.Type == nil || m.Path.Value == nil {
			continue
		}
		if *m.Path.Type == gatewayv1.PathMatchPathPrefix {
			return *m.Path.Value
		}
	}
	return ""
}

func DeepCopyBackend(original *models.Backend) (*models.Backend, error) {
	if original == nil {
		return nil, nil
	}
	var copied models.Backend
	data, err := json.Marshal(original) // Serialize to JSON
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(data, &copied) // Deserialize to a new struct
	return &copied, nil
}

//revive:disable:function-length
func (b *HaproxyConfMgrImpl) processBackendsModifiedInCycle() error {
	var errs utils.Errors
	// UPSERTED
	errsUpserted := b.processBackendsUpsertedInCycle()
	errs.AddErrors(errsUpserted)

	// UNREFERENCED
	errsUnreferenced := b.processBackendsUnreferencedInCycle()
	errs.AddErrors(errsUnreferenced)

	// DELETED
	errsDel := b.processBackendsDeletedInCycle()
	errs.AddErrors(errsDel)

	return errs.Result()
}

func (b *HaproxyConfMgrImpl) processBackendsUpsertedInCycle() utils.Errors {
	var errs utils.Errors

	for backendName, mapImpactedBEs := range b.backendsImpactedInCycle.Upserted {
		routesInfo := make(map[string]metadata.RouteMetadaInfo)
		owners, ok := b.backendOwners.owners[backendName]
		if !ok {
			err := fmt.Errorf("could not find owner for backend %s", backendName)
			errs.Add(err)
			continue
		}
		isHTTPBackend := true
		ownerType := BackendOwnerTypeHTTPRoute
		ownersForRoute, ok := owners[BackendOwnerTypeHTTPRoute]
		if !ok {
			ownersForRoute, ok = owners[BackendOwnerTypeTLSRoute]
			if ok {
				isHTTPBackend = false
				ownerType = BackendOwnerTypeTLSRoute
			}
		}
		if !ok {
			err := fmt.Errorf("could not find owners for type %s/%s", BackendOwnerTypeHTTPRoute, BackendOwnerTypeTLSRoute)
			errs.Add(err)
			continue
		}
		for routeKey, generation := range ownersForRoute {
			routesInfo[routeKey.String()] = metadata.RouteMetadaInfo{
				OwnerType:  string(ownerType),
				Generation: generation,
			}
		}
		beMd := b.metadataManager.BackendMetaData(routesInfo)

		// All entries for a given backend name share the same filters and match prefix
		// (because the name is derived from their hash).
		// All entries for a given backend name share the same filters and match prefix
		// (because the name is derived from their hash).
		var backendRef gatewayv1.HTTPBackendRef
		var ruleFilters []gatewayv1.HTTPRouteFilter
		var matchPrefix string
		var isRedirect bool
		for _, impactedBE := range mapImpactedBEs {
			backendRef = impactedBE.BackendRef
			ruleFilters = impactedBE.RuleFilters
			matchPrefix = impactedBE.MatchPrefix
			isRedirect = impactedBE.IsRedirect
		}

		if isRedirect {
			be, err := b.newRedirectBackend(backendName, beMd, ruleFilters, matchPrefix)
			if err != nil {
				errs.Add(err)
				continue
			}
			if err := b.configuration.upsertBackend(b.logger, be); err != nil {
				errs.Add(err)
				continue
			}
			if b.firstSync.flag {
				b.firstSync.backends[be.Name] = struct{}{}
			}
			continue
		}

		var persistenceCandidates []ResourceCandidate
		for _, impactedBE := range mapImpactedBEs {
			persistenceCandidates = append(persistenceCandidates, impactedBE.ResourceCandidate)
		}
		var sessionPersistence *gatewayv1.SessionPersistence
		var httpTimeouts *gatewayv1.HTTPRouteTimeouts
		if len(persistenceCandidates) > 0 {
			slices.SortFunc(persistenceCandidates, persistenceCandidateSort)
			for _, persistenceCandidate := range persistenceCandidates {
				if sessionPersistence == nil && persistenceCandidate.SessionPersistence != nil {
					sessionPersistence = persistenceCandidate.SessionPersistence
				}
				if httpTimeouts == nil && persistenceCandidate.HTTPTimeouts != nil {
					httpTimeouts = persistenceCandidate.HTTPTimeouts
				}
				if httpTimeouts != nil && sessionPersistence != nil {
					break
				}
			}
		}
		// Same for Namespace, it should be the same for all
		var namespace string
		for owner := range ownersForRoute {
			namespace = owner.Namespace
			break
		}

		be, err := b.newBackend(backendName, beMd, backendRef, ruleFilters, matchPrefix, sessionPersistence, httpTimeouts, namespace, isHTTPBackend)
		if err != nil {
			errs.Add(err)
			continue
		}

		if err := b.configuration.upsertBackend(b.logger, be); err != nil {
			errs.Add(err)
			continue
		}
		if b.firstSync.flag {
			b.firstSync.backends[be.Name] = struct{}{}
		}
	}
	return errs
}

func (b *HaproxyConfMgrImpl) processBackendsUnreferencedInCycle() utils.Errors {
	var errs utils.Errors

	for backendName := range b.backendsImpactedInCycle.Unreferenced {
		// Recompute Metadata and update BE
		routesInfo := make(map[string]metadata.RouteMetadaInfo)
		owners, ok := b.backendOwners.owners[backendName]
		if !ok {
			// This is not an error, this can happen, especially if unreferenced
			// If there are still some routes that reference this backend, it would lead to
			// metadata updates only just below
			// if no route references it anymore, it would lead to backend deletion done previously in cleanupUnreferencedBackendsForHTTPRoutes
			//
			continue
		}
		ownerType := BackendOwnerTypeHTTPRoute
		ownersForRoute, ok := owners[BackendOwnerTypeHTTPRoute]
		if !ok {
			ownersForRoute, ok = owners[BackendOwnerTypeTLSRoute]
			ownerType = BackendOwnerTypeTLSRoute
		}
		if !ok {
			// No more owners, delete it
			if err := b.configuration.deleteBackend(b.logger, backendName); err != nil {
				errs.Add(err)
				continue
			}
			if b.firstSync.flag {
				delete(b.firstSync.backends, backendName)
			}
			continue
		}

		for routeKey, generation := range ownersForRoute {
			routesInfo[routeKey.String()] = metadata.RouteMetadaInfo{
				OwnerType:  string(ownerType),
				Generation: generation,
			}
		}
		beMd := b.metadataManager.BackendMetaData(routesInfo)
		if err := b.configuration.upsertBackendMetadata(b.logger, backendName, beMd); err != nil {
			errs.Add(err)
			continue
		}
		if b.firstSync.flag {
			b.firstSync.backends[backendName] = struct{}{}
		}
	}
	return errs
}

func (b *HaproxyConfMgrImpl) processBackendsDeletedInCycle() utils.Errors {
	var errs utils.Errors

	for backendName := range b.backendsImpactedInCycle.Deleted {
		if err := b.configuration.deleteBackend(b.logger, backendName); err != nil {
			errs.Add(err)
			continue
		}
		if b.firstSync.flag {
			delete(b.firstSync.backends, backendName)
		}
	}
	return errs
}

type ResourceCandidate struct {
	CreationTimestamp  time.Time
	SessionPersistence *gatewayv1.SessionPersistence
	HTTPTimeouts       *gatewayv1.HTTPRouteTimeouts
	Namespace          string
	RouteName          string
	RuleIndex          int
	BackendIndex       int
}

func persistenceCandidateSort(a, b ResourceCandidate) int {
	if !a.CreationTimestamp.Equal(b.CreationTimestamp) {
		if a.CreationTimestamp.Before(b.CreationTimestamp) {
			return -1
		}
		return 1
	}

	if a.Namespace != b.Namespace {
		if a.Namespace < b.Namespace {
			return -1
		}
		return 1
	}

	if a.RouteName != b.RouteName {
		if a.RouteName < b.RouteName {
			return -1
		}
		return 1
	}

	if d := a.RuleIndex - b.RuleIndex; d != 0 {
		return d
	}

	return a.BackendIndex - b.BackendIndex
}
