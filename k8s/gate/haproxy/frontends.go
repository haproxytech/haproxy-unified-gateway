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
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/templates"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type FrontendsOwnedbyGateway struct {
	current           map[client.ObjectKey]map[string]struct{} // map[gwKey] -> map[frontendName]struct{}
	byUpdatedGateways map[client.ObjectKey]map[string]struct{} // map[gwKey] -> map[frontendName]struct{}
}

func (b *HaproxyConfMgrImpl) getFrontendName(gwKey k8stypes.NamespacedName, listener gatewayv1.Listener) (string, error) {
	tmpl, err := template.New("frontend").Parse(b.params.FrontendNameTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse frontend name template: %w", err)
	}

	data := templates.TemplateData{
		GATEWAY_NAMESPACE: gwKey.Namespace,
		GATEWAY_NAME:      gwKey.Name,
		LISTENER_NAME:     string(listener.Name),
		LINK_ID:           b.params.LinkID,
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (b *HaproxyConfMgrImpl) processGateways() error {
	var errors utils.Errors
	// Managed Gateways => Create / update/ delete frontends
	for gwKey, gateway := range b.controllerStore.GateTree.Gateways {
		switch gateway.TreeStatus.Status {
		case store.StatusUnchanged:
			continue
		case store.StatusUpserted:
			err := b.onUpsertedGateway(gwKey, gateway)
			errors.Add(err)
		case store.StatusDeleted:
			err := b.onDeletedGateway(gwKey, gateway)
			errors.Add(err)
		}
	}

	// Unmanaged Gateways => Delete frontends
	for gwKey, gateway := range b.controllerStore.UnmanagedGateTree.Gateways {
		err := b.onUnmanagedGateway(gwKey, gateway)
		errors.Add(err)
	}

	// Cleanup frontends for gateways that were updated
	if err := b.cleanupFrontendsForGateways(); err != nil {
		b.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to cleanup frontends for gateways",
			logging.LogAttrError(err),
		)
		errors.Add(err)
	}

	// Finalize frontends by gateway
	b.finalizeFrontendsByGateway()
	return errors.Result()
}

func (b *HaproxyConfMgrImpl) onUpsertedGateway(gwKey k8stypes.NamespacedName, gw *tree.Gateway) error {
	switch gw.Valid {
	case true:
		err := b.onValidGatewayUpserted(gwKey, gw)
		if err != nil {
			return err
		}
	case false:
		err := b.onInvalidGatewayUpserted(gwKey, gw)
		if err != nil {
			return err
		}
	}
	return nil
}

func (b *HaproxyConfMgrImpl) onValidGatewayUpserted(gwKey k8stypes.NamespacedName, gw *tree.Gateway) error {
	b.logGatewayUpdate("upserted", gwKey)
	err := b.upsertFrontends(gwKey, gw)
	return err
}

func (b *HaproxyConfMgrImpl) onInvalidGatewayUpserted(gwKey k8stypes.NamespacedName, gw *tree.Gateway) error {
	b.logGatewayUpdate("upserted-invalid", gwKey)
	err := b.deleteFrontendForAllListeners(gwKey, gw)
	return err
}

func (b *HaproxyConfMgrImpl) onDeletedGateway(gwKey k8stypes.NamespacedName, gw *tree.Gateway) error {
	b.logGatewayUpdate("deleted", gwKey)
	err := b.deleteFrontendForAllListeners(gwKey, gw)
	return err
}

func (b *HaproxyConfMgrImpl) onUnmanagedGateway(gwKey k8stypes.NamespacedName, gw *tree.Gateway) error {
	b.logGatewayUpdate("unmanaged", gwKey)
	err := b.deleteFrontendForAllListeners(gwKey, gw)
	return err
}

func (b *HaproxyConfMgrImpl) upsertFrontends(gwKey k8stypes.NamespacedName, gw *tree.Gateway) error {
	for _, listener := range gw.Listeners {
		if !listener.Valid {
			if errDel := b.deleteFrontendForListener(gwKey, listener.K8sResource); errDel != nil {
				feName, errName := b.getFrontendName(gwKey, listener.K8sResource)
				if errName != nil {
					b.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to get frontend name",
						slog.String("frontendNameTemplate", b.params.FrontendNameTemplate),
						logging.LogAttrKey(gwKey))
				}
				b.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to delete frontend",
					logging.LogAttrFrontendName(feName),
					logging.LogAttrError(errDel))
			}
			// Proceed with next listeners
			continue
		}

		newFe, err := b.newFrontend(newFrontendParams{
			gwKey:        gwKey,
			treeGw:       gw,
			treeListener: listener,
		})
		if err != nil {
			return err
		}
		if b.firstSync.flag {
			b.firstSync.frontends[newFe.Name] = struct{}{}
		}
		if err := b.configuration.upsertFrontend(b.logger, newFe); err != nil {
			b.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to upsert frontend",
				logging.LogAttrFrontendName(newFe.Name),
				logging.LogAttrError(err))
			continue
		}
		b.frontendsOwnedbyGateway.AddToUpdated(gwKey, newFe.Name)
	}

	return nil
}

func (b *HaproxyConfMgrImpl) cleanupFrontendsForGateways() error {
	// For each updated gateway, check if the frontend is still present
	for gwKey := range b.frontendsOwnedbyGateway.byUpdatedGateways {
		for frontendName := range b.frontendsOwnedbyGateway.current[gwKey] {
			b.logger.LogAttrs(context.Background(), slog.LevelDebug, "Cleaning up frontend for gateway",
				logging.LogAttrKey(gwKey),
				slog.Any("current frontends", b.frontendsOwnedbyGateway.current[gwKey]))
			if _, ok := b.frontendsOwnedbyGateway.byUpdatedGateways[gwKey][frontendName]; !ok {
				if err := b.deleteFrontend(gwKey, frontendName); err != nil {
					b.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to deleted frontend",
						logging.LogAttrFrontendName(frontendName),
						logging.LogAttrError(err))
					continue
				}
			}
		}
	}
	return nil
}

func (b *HaproxyConfMgrImpl) finalizeFrontendsByGateway() {
	// For each updated gateway, check if the frontend is still present
	for gwKey := range b.frontendsOwnedbyGateway.byUpdatedGateways {
		b.frontendsOwnedbyGateway.current[gwKey] = b.frontendsOwnedbyGateway.byUpdatedGateways[gwKey]
		delete(b.frontendsOwnedbyGateway.byUpdatedGateways, gwKey)
	}
}

type newFrontendParams struct {
	treeGw       *tree.Gateway
	treeListener *tree.Listener
	gwKey        k8stypes.NamespacedName
}

func (b *HaproxyConfMgrImpl) newFrontend(params newFrontendParams) (*models.Frontend, error) { //revive:disable:function-length
	gwKey := params.gwKey
	treeGw := params.treeGw
	treeListener := params.treeListener

	listener := treeListener.K8sResource

	// Create a frontend for each listener
	frontendName, err := b.getFrontendName(gwKey, listener)
	if err != nil {
		b.logger.LogAttrs(context.Background(), slog.LevelError,
			"Failed to get frontend name",
			slog.String("frontendNameTemplate", b.params.FrontendNameTemplate),
			logging.LogAttrKey(gwKey))
		return nil, fmt.Errorf("failed to get frontend name: %w", err)
	}

	md := b.metadataManager.FrontendMetaData(treeGw)

	pathExactMap := b.params.mapsStorage.MapPath(frontendName, storage.PATH_EXACT_MAP)
	pathPrefixMap := b.params.mapsStorage.MapPath(frontendName, storage.PATH_PREFIX_MAP)
	pathDomainWPathExactMap := b.params.mapsStorage.MapPath(frontendName, storage.PATH_EXACT_DOMAIN_WILDCARD_MAP)
	pathRegexMap := b.params.mapsStorage.MapPath(frontendName, storage.PATH_REGEX_MAP)
	sniMap := b.params.mapsStorage.MapPath(frontendName, storage.SNI_MAP)
	sniDomainWildcardMap := b.params.mapsStorage.MapPath(frontendName, storage.SNI_DOMAIN_WILDCARD_MAP)

	b.params.mapsStorage.EnsureMapData(pathExactMap)
	b.params.mapsStorage.EnsureMapData(pathPrefixMap)
	b.params.mapsStorage.EnsureMapData(pathRegexMap)
	b.params.mapsStorage.EnsureMapData(pathDomainWPathExactMap)
	b.params.mapsStorage.EnsureMapData(sniMap)
	b.params.mapsStorage.EnsureMapData(sniDomainWildcardMap)

	var tcpRules []*models.TCPRequestRule
	var httpRules []*models.HTTPRequestRule
	var backendSwitchingRules []*models.BackendSwitchingRule
	var aclList []*models.ACL
	switch {
	case listener.TLS != nil && utils.PointerDefaultValueIfNil(listener.TLS.Mode) == gatewayv1.TLSModePassthrough:
		tcpRules = []*models.TCPRequestRule{
			{ // tcp-request content reject if !{ req.ssl_hello_type 1 }
				Type:     "content",
				Action:   "reject",
				Cond:     "if",
				CondTest: "!{ req.ssl_hello_type 1 }",
			},
			{
				// tcp-request inspect-delay 50000
				Type:    "inspect-delay",
				Timeout: utils.PtrInt64(50000),
			},
			{
				// tcp-request content set-var(sess.sni) req.ssl_sni
				Type:     "content",
				Action:   "set-var",
				VarName:  "sni",
				VarScope: "sess",
				Expr:     "req.ssl_sni",
			},
			{
				// tcp-request content set-var(txn.sni_match) req.ssl_sni,map(sni.map)
				Type:     "content",
				Action:   "set-var",
				VarName:  "sni_match",
				VarScope: "txn",
				Expr:     "req.ssl_sni,map(" + sniMap.FullPath() + ")",
			},
			{
				// tcp-request content set-var(txn.sni_match,ifnotexists) req.ssl_sni,map_end(sniDomainWildcardMap.map)
				Type:     "content",
				Action:   "set-var",
				VarName:  "sni_match,ifnotexists",
				VarScope: "txn",
				Expr:     "req.ssl_sni,map_end(" + sniDomainWildcardMap.FullPath() + ")",
			},
		}
		backendSwitchingRules = []*models.BackendSwitchingRule{
			{
				Name:     "%[var(txn.backend)]",
				Cond:     "if",
				CondTest: "route_is_json",
			},
			{
				Name: "%[var(txn.sni_match),field(1,.)]",
			},
		}
		aclList = []*models.ACL{
			{ // acl route_is_json var(txn.sni_match),bytes(0,1) -m str
				ACLName:   "route_is_json",
				Criterion: "var(txn.sni_match),bytes(0,1)",
				Value:     "-m str {",
				Metadata: map[string]any{
					"hug": "for lua routing",
				},
			},
		}

	default:
		httpRules = []*models.HTTPRequestRule{
			{ // http-request set-var(txn.path) path
				Type:     "set-var",
				VarName:  "path",
				VarScope: "txn",
				VarExpr:  "path",
			},
			{ // http-request set-var(txn.host) req.hdr(Host),host_only
				Type:     "set-var",
				VarName:  "host",
				VarScope: "txn",
				VarExpr:  "req.hdr(Host),host_only",
			},
			{ // http-request set-var(txn.base) var(txn.host),concat("",txn.path)
				Type:     "set-var",
				VarName:  "base",
				VarScope: "txn",
				VarExpr:  "var(txn.host),concat(\"\",txn.path)",
			},
			{
				// exact domain + exact path
				// http-request set-var(txn.route) base,map(route_exact_match.map)
				Type:     "set-var",
				VarName:  "route",
				VarScope: "txn",
				VarExpr:  "var(txn.base),map(" + pathExactMap.FullPath() + ")",
				Metadata: map[string]any{"hug": "exact domain + exact path"},
			},
			{
				// # any domain + exact path
				// http-request set-var(txn.route,ifnotexists) path,map(route_exact_match.map)
				Type:     "set-var",
				VarName:  "route,ifnotexists",
				VarScope: "txn",
				VarExpr:  "path,map(" + pathExactMap.FullPath() + ")",
				Metadata: map[string]any{"hug": "any domain + exact path"},
			},
			{
				// # exact domain + path prefix
				// http-request set-var(txn.route,ifnotexists) base,map_beg(route_prefix_match.map)
				Type:     "set-var",
				VarName:  "route,ifnotexists",
				VarScope: "txn",
				VarExpr:  "var(txn.base),map_beg(" + pathPrefixMap.FullPath() + ")",
				Metadata: map[string]any{"hug": "exact domain + path prefix"},
			},
			{
				//  # any domain + path prefix
				// http-request set-var(txn.route,ifnotexists) path,map_beg(route_prefix_match.map)
				Type:     "set-var",
				VarName:  "route,ifnotexists",
				VarScope: "txn",
				VarExpr:  "path,map_beg(" + pathPrefixMap.FullPath() + ")",
				Metadata: map[string]any{"hug": "any domain + path prefix"},
			},
			{
				//	# domain wildcard + exact path
				//	 http-request set-var(txn.route,ifnotexists) base,map_end(route_dw_ep.map)
				Type:     "set-var",
				VarName:  "route,ifnotexists",
				VarScope: "txn",
				VarExpr:  "var(txn.base),map_end(" + pathDomainWPathExactMap.FullPath() + ")",
				Metadata: map[string]any{"hug": "domain wildcard + exact path"},
			},
			{
				// # any domain + path regex
				// http-request set-var(txn.route,ifnotexists) path,map_reg(route_regex.map) # ^/(foo|bar)/.*
				Type:     "set-var",
				VarName:  "route,ifnotexists",
				VarScope: "txn",
				VarExpr:  "path,map_reg(" + pathRegexMap.FullPath() + ")",
				Metadata: map[string]any{"hug": "any domain + path regex"},
			},
			{
				// # domain wildcard + path prefix. Example: ^[^.]+\.domain\.com/v1/foo/.*   # or map_sub
				// # domain wildcard + path regex   Example: ^[^.]+\.domain\.com/v[1-3]/foo
				// # exact domain + path regex      Example: ^www\.domain\.com/v[1-3]/foo
				// http-request set-var(txn.route,ifnotexists) base,map_reg(route_regex.map)
				Type:     "set-var",
				VarName:  "route,ifnotexists",
				VarScope: "txn",
				VarExpr:  "var(txn.base),map_reg(" + pathRegexMap.FullPath() + ")",
				Metadata: map[string]any{"hug": "domain wildcard + path prefix or regex, exact domain + path regex"},
			},
			{
				// http-request lua.route if route_is_json
				Type:      "lua",
				LuaAction: "route",
				Cond:      "if",
				CondTest:  "route_is_json",
				Metadata: map[string]any{
					"hug": "lua routing",
				},
			},
		}
		backendSwitchingRules = []*models.BackendSwitchingRule{
			{
				Name:     "%[var(txn.backend)]",
				Cond:     "if",
				CondTest: "route_is_json",
			},
			{
				Name: "%[var(txn.route)]",
			},
		}
		aclList = []*models.ACL{
			{ // acl route_is_json var(txn.route),bytes(0,1) -m str {
				ACLName:   "route_is_json",
				Criterion: "var(txn.route),bytes(0,1)",
				Value:     "-m str {",
				Metadata: map[string]any{
					"hug": "for lua routing",
				},
			},
		}
	}

	fe := &models.Frontend{
		FrontendBase: models.FrontendBase{
			Name:           frontendName,
			From:           b.params.DefaultsSectionName,
			Metadata:       md,
			DefaultBackend: "backend_not_found",
			Mode: func() string {
				if listener.Protocol == gatewayv1.HTTPProtocolType || listener.Protocol == gatewayv1.HTTPSProtocolType {
					return "http"
				}
				if listener.Protocol == gatewayv1.TCPProtocolType {
					return "tcp"
				}
				return ""
			}(),
		},
		ACLList:                  aclList,
		TCPRequestRuleList:       tcpRules,
		HTTPRequestRuleList:      httpRules,
		BackendSwitchingRuleList: backendSwitchingRules,
	}

	// Set other frontend properties based on the listener
	port := int64(listener.Port)
	if !b.params.DisableIPv4 {
		bind := models.Bind{
			Port: &port,
			Address: func() string {
				if b.params.IPv4BindAddress != "" {
					return b.params.IPv4BindAddress
				}
				return "0.0.0.0"
			}(),
			BindParams: b.bindParams(frontendName, "v4", treeGw, treeListener),
		}
		if fe.Binds == nil {
			fe.Binds = make(map[string]models.Bind)
		}
		fe.Binds[bind.Name] = bind
	}
	if !b.params.DisableIPv6 {
		bind := models.Bind{
			Port: &port,
			Address: func() string {
				if b.params.IPv6BindAddress != "" {
					return b.params.IPv6BindAddress
				}
				return "::"
			}(),
			BindParams: b.bindParams(frontendName, "v6", treeGw, treeListener),
		}
		if fe.Binds == nil {
			fe.Binds = make(map[string]models.Bind)
		}
		fe.Binds[bind.Name] = bind
	}
	return fe, nil
}

func (b *HaproxyConfMgrImpl) deleteFrontendForAllListeners(gwKey k8stypes.NamespacedName, gw *tree.Gateway) error {
	// K8s resource might be in:
	k8sGateway := gw.GetK8sResource()
	if k8sGateway == nil {
		return fmt.Errorf("no K8s resource found for gateway %s", gwKey)
	}

	for _, listener := range k8sGateway.Spec.Listeners {
		if err := b.deleteFrontendForListener(gwKey, listener); err != nil {
			continue
		}
	}
	return nil
}

func (b *HaproxyConfMgrImpl) deleteFrontendForListener(gwKey k8stypes.NamespacedName, listener gatewayv1.Listener) error {
	// Frontend for each listener
	feName, err := b.getFrontendName(gwKey, listener)
	if err != nil {
		b.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to get frontend name",
			slog.String("frontendNameTemplate", b.params.FrontendNameTemplate),
			logging.LogAttrKey(gwKey))
		return err
	}
	if err := b.deleteFrontend(gwKey, feName); err != nil {
		b.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to delete frontend",
			logging.LogAttrFrontendName(feName),
			logging.LogAttrError(err))
		return err
	}
	if b.firstSync.flag {
		delete(b.firstSync.frontends, feName)
	}
	return nil
}

func (b *HaproxyConfMgrImpl) deleteFrontend(gwKey client.ObjectKey, feName string) error {
	if err := b.configuration.deleteFrontend(b.logger, feName); err != nil {
		return err
	}
	b.frontendsOwnedbyGateway.RemoveFromUpdated(gwKey, feName)
	return nil
}

func (b *HaproxyConfMgrImpl) logGatewayUpdate(action string, gwKey k8stypes.NamespacedName) {
	b.logger.LogAttrs(context.Background(), slog.LevelDebug, "Processing Gateway ["+action+"]",
		logging.LogAttrKey(gwKey),
	)
}

func DeepCopyFrontend(original *models.Frontend) (*models.Frontend, error) {
	if original == nil {
		return nil, nil
	}
	var copied models.Frontend
	data, err := json.Marshal(original) // Serialize to JSON
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(data, &copied) // Deserialize to a new struct
	return &copied, nil
}

func NewFrontendsOwnedbyGateway() FrontendsOwnedbyGateway {
	return FrontendsOwnedbyGateway{
		current:           make(map[client.ObjectKey]map[string]struct{}),
		byUpdatedGateways: make(map[client.ObjectKey]map[string]struct{}),
	}
}

func (f *FrontendsOwnedbyGateway) AddToUpdated(gwKey client.ObjectKey, frontendName string) {
	if _, ok := f.byUpdatedGateways[gwKey]; !ok {
		f.byUpdatedGateways[gwKey] = make(map[string]struct{})
	}
	f.byUpdatedGateways[gwKey][frontendName] = struct{}{}
}

func (f *FrontendsOwnedbyGateway) RemoveFromUpdated(gwKey client.ObjectKey, frontendName string) {
	if _, ok := f.byUpdatedGateways[gwKey]; !ok {
		f.byUpdatedGateways[gwKey] = make(map[string]struct{})
	}
	if _, ok := f.byUpdatedGateways[gwKey]; ok {
		delete(f.byUpdatedGateways[gwKey], frontendName)
	}
}

func (b *HaproxyConfMgrImpl) bindParams(_, bindName string, treeGw *tree.Gateway, treeListener *tree.Listener) models.BindParams {
	params := models.BindParams{}
	params.Name = bindName

	// If no TLS
	if treeListener.K8sResource.TLS == nil {
		return params
	}

	// TLS terminate
	if utils.PointerDefaultValueIfNil((*treeListener.K8sResource.TLS).Mode) == gatewayv1.TLSModeTerminate {
		listenerKey := tree.ListenerKey(treeGw.K8sResource, treeListener.K8sResource)
		certFileDir := b.params.certificateStorage.CertListPath(listenerKey)
		params.CrtList = certFileDir.FullPath()
		params.Ssl = true
	}
	return params
}
