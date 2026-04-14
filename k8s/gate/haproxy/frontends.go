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
	"context"
	"encoding/json"
	"log/slog"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/conditions/generic"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage/maps"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/protocols"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/tree"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func (b *HaproxyConfMgrImpl) getFrontendName(vListenerName string) string {
	return b.params.LinkID + "_" + vListenerName
}

func (b *HaproxyConfMgrImpl) processVirtualListener() error {
	var errors utils.Errors
	// VirtualListeners are mapped 1 to 1 with Frontends,
	// so we can directly create/update/delete Frontends while processing VirtualListeners
	for vlName, vListener := range b.controllerStore.GateTree.VirtualListeners {
		switch vListener.Status {
		case store.StatusUnchanged:
			continue
		case store.StatusUpserted:
			err := b.onUpsertedVirtualListener(vlName, vListener)
			errors.Add(err)
		case store.StatusDeleted:
			err := b.onDeletedVirtualListener(vlName, vListener)
			errors.Add(err)
		}
	}

	return errors.Result()
}

func (b *HaproxyConfMgrImpl) onUpsertedVirtualListener(vlName string, vListener *tree.VirtualListener) error {
	b.logVirtualListenerUpdate("upserted", vlName)
	err := b.upsertFrontends(vlName, vListener)
	return err
}

func (b *HaproxyConfMgrImpl) onDeletedVirtualListener(vlName string, vListener *tree.VirtualListener) error {
	b.logVirtualListenerUpdate("deleted", vlName)
	b.clearListenerMaps(vlName, vListener)
	err := b.deleteFrontendForVirtualListener(vlName)
	return err
}

func (b *HaproxyConfMgrImpl) upsertFrontends(vListenerName string, vListener *tree.VirtualListener) error {
	newFe, err := b.newFrontend(vListenerName, vListener)
	if err != nil {
		return err
	}
	if b.firstSync.flag {
		b.firstSync.frontends[newFe.Name] = struct{}{}
	}
	// if 1 of the Listeners has a Status Programmed Unknown, we consider the FE as being updated
	reprogram := false
	for _, l := range vListener.Listeners {
		progCond, exists := l.Conditions.GetCondition(generic.ConditionType(gatewayv1.ListenerConditionProgrammed))
		if !exists || progCond.Status == metav1.ConditionUnknown {
			reprogram = true
			b.logger.LogAttrs(context.Background(), slog.LevelDebug, "Reprogramming frontend due to unknown programmed condition on listener",
				logging.LogAttrFrontendName(newFe.Name),
				logging.LogAttrKey(l.Owner),
			)
			break
		}
	}
	if err := b.configuration.upsertFrontend(b.logger, newFe, reprogram); err != nil {
		b.logger.LogAttrs(context.Background(), slog.LevelError, "Failed to upsert frontend",
			logging.LogAttrFrontendName(newFe.Name),
			logging.LogAttrError(err))
	}

	b.fillListenerMaps(vListenerName, vListener)

	return nil
}

// clearListenerMaps removes all entries from MAP_LISTENER_EXACT_MATCH, MAP_LISTENER_WILDCARD_MATCH,
// MAP_LISTENER_ROUTE_EXACT_MATCH, and MAP_LISTENER_ROUTE_WILDCARD_MATCH
// that were added for each listener in the VirtualListener.
func (b *HaproxyConfMgrImpl) clearListenerMaps(vlName string, vListener *tree.VirtualListener) {
	frontendName := b.getFrontendName(vlName)
	listenerExactMatchMap := b.params.mapsStorage.GetListenerExactMatchMapFile(frontendName)
	listenerWildcardMatchMap := b.params.mapsStorage.GetListenerWildcardMatchMapFile(frontendName)
	listenerRouteExactMatchMap := b.params.mapsStorage.GetListenerRouteExactMatchMapFile(frontendName)
	listenerRouteWildcardMatchMap := b.params.mapsStorage.GetListenerRouteWildcardMatchMapFile(frontendName)

	for _, l := range vListener.Listeners {
		listenerKeyName := l.Key().String()
		resourceOrigin := maps.ResourceOrigin{
			Namespace: l.Owner.Namespace,
			Name:      listenerKeyName,
		}
		listenerExactMatchMap.ApplyRoute(resourceOrigin, map[maps.EntryKey]map[string]*maps.WeightedValue{})
		listenerWildcardMatchMap.ApplyRoute(resourceOrigin, map[maps.EntryKey]map[string]*maps.WeightedValue{})

		for routeKey := range l.AttachedRoutes {
			routeOrigin := maps.ResourceOrigin{
				Namespace: routeKey.Namespace,
				Name:      listenerKeyName + "/" + routeKey.Name,
			}
			listenerRouteExactMatchMap.ApplyRoute(routeOrigin, map[maps.EntryKey]map[string]*maps.WeightedValue{})
			listenerRouteWildcardMatchMap.ApplyRoute(routeOrigin, map[maps.EntryKey]map[string]*maps.WeightedValue{})
		}
	}
}

// fillListenerMaps fills MAP_LISTENER_EXACT_MATCH and MAP_LISTENER_WILDCARD_MATCH for each listener in the VirtualListener.
// For every listener whose hostname is set and is not a wildcard, one entry is added to the exact-match map:
//
//	key:   hostname
//	value: listener name built as per NewListenerKey(gw, listener).Name  →  "<gateway-name>_<listener-name>"
//
// For every listener whose hostname is a wildcard (e.g. "*.example.com"), one entry is added to the wildcard-match map:
//
//	key:   hostname with the leading "*" stripped (e.g. ".example.com"), for use with map_end
//	value: listener name built as per NewListenerKey(gw, listener).Name  →  "<gateway-name>_<listener-name>"
func (b *HaproxyConfMgrImpl) fillListenerMaps(vListenerName string, vListener *tree.VirtualListener) {
	frontendName := b.getFrontendName(vListenerName)
	listenerExactMatchMap := b.params.mapsStorage.GetListenerExactMatchMapFile(frontendName)
	listenerWildcardMatchMap := b.params.mapsStorage.GetListenerWildcardMatchMapFile(frontendName)

	for _, l := range vListener.Listeners {
		hostname := l.K8sResource.Hostname
		hostnameStr := ""
		if hostname != nil {
			hostnameStr = string(*hostname)
		}
		if hostnameStr == "" {
			hostnameStr = "*."
		}
		// Equivalent to NewListenerKey(gw, listener).Name
		listenerKeyName := l.Key().String()
		resourceOrigin := maps.ResourceOrigin{
			Namespace: l.Owner.Namespace,
			Name:      listenerKeyName,
		}
		if isDomainWildcard(hostnameStr) {
			// Clear exact match for this listener (in case hostname type changed from exact to wildcard)
			listenerExactMatchMap.ApplyRoute(resourceOrigin, map[maps.EntryKey]map[string]*maps.WeightedValue{})
			// Strip the leading "*" so that map_end can match against the suffix (e.g. ".example.com")
			wildcardKey := removeDomainWildcard(hostnameStr)
			// Here reverse the domain string (because map_end does not take the longest string)
			// so we want to use a key that has the most significant part at the end of the string (e.g. "com.example.")
			reverseDomainKey := reverseDomain(wildcardKey)
			listenerWildcardMatchMap.ApplyRoute(resourceOrigin,
				map[maps.EntryKey]map[string]*maps.WeightedValue{
					{Hostname: reverseDomainKey}: {
						listenerKeyName: &maps.WeightedValue{ValueName: listenerKeyName},
					},
				})
		} else {
			// Clear wildcard match for this listener (in case hostname type changed from wildcard to exact)
			listenerWildcardMatchMap.ApplyRoute(resourceOrigin, map[maps.EntryKey]map[string]*maps.WeightedValue{})
			listenerExactMatchMap.ApplyRoute(resourceOrigin,
				map[maps.EntryKey]map[string]*maps.WeightedValue{
					{Hostname: hostnameStr}: {
						listenerKeyName: &maps.WeightedValue{ValueName: listenerKeyName},
					},
				})
		}
	}
}

func (b *HaproxyConfMgrImpl) newFrontend(vListenerName string, vListener *tree.VirtualListener) (*models.Frontend, error) { //revive:disable:function-length
	// Create a frontend for each listener
	frontendName := b.getFrontendName(vListenerName)

	md := b.metadataManager.FrontendMetaData(vListener)

	pathExactMap := b.params.mapsStorage.GetPathExactMapFile(frontendName)
	pathPrefixMap := b.params.mapsStorage.GetPathPrefixMapFile(frontendName)
	pathRegexMap := b.params.mapsStorage.GetPathRegexMapFile(frontendName)
	sniMap := b.params.mapsStorage.GetSniMapFile(frontendName)

	listenerExactMatchMap := b.params.mapsStorage.GetListenerExactMatchMapFile(frontendName)
	listenerWildcardMatchMap := b.params.mapsStorage.GetListenerWildcardMatchMapFile(frontendName)
	listenerRouteExactMatchMap := b.params.mapsStorage.GetListenerRouteExactMatchMapFile(frontendName)
	listenerRouteWildcardMatchMap := b.params.mapsStorage.GetListenerRouteWildcardMatchMapFile(frontendName)

	var tcpRules []*models.TCPRequestRule
	var httpRules []*models.HTTPRequestRule
	var backendSwitchingRules []*models.BackendSwitchingRule
	var aclList []*models.ACL
	switch {
	case vListener.ProtocolCategory == protocols.ProtocolCategoryTLS:
		tcpRules, backendSwitchingRules, aclList = tlsPassthroughRules(
			listenerExactMatchMap.Path.FullPath(),
			listenerWildcardMatchMap.Path.FullPath(),
			listenerRouteExactMatchMap.Path.FullPath(),
			listenerRouteWildcardMatchMap.Path.FullPath(),
			sniMap.Path.FullPath(),
		)

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
			{
				// http-request set-var(txn.hostreversed) req.hdr(Host),host_only,lua.reverse_host
				Type:     "set-var",
				VarName:  "hostreversed",
				VarScope: "txn",
				VarExpr:  "req.hdr(Host),host_only,lua.reverse_host",
			},
			{ // http-request set-var(txn.base) var(txn.host),concat("",txn.path)
				Type:     "set-var",
				VarName:  "base",
				VarScope: "txn",
				VarExpr:  "var(txn.host),concat(\"\",txn.path)",
			},
			// -------------------
			// Look for listener name: selected_listener_name
			{
				// listener-name exact match
				// http-request set-var(txn.selected-listener-name) var(txn.host),map(listener_exact_match)
				Type:     "set-var",
				VarName:  "selected_listener_name",
				VarScope: "txn",
				VarExpr:  "var(txn.host),map(" + listenerExactMatchMap.Path.FullPath() + ")",
				Metadata: map[string]any{"hug": "listener exact match selection"},
			},
			{
				// http-request set-var(txn.selected-listener-name,ifnotexists) var(txn.hostreversed),map_beg(listener_wildcard_match)
				Type:     "set-var",
				VarName:  "selected_listener_name,ifnotexists",
				VarScope: "txn",
				VarExpr:  "var(txn.hostreversed),map_beg(" + listenerWildcardMatchMap.Path.FullPath() + ")",
				Metadata: map[string]any{"hug": "listener wildcard match selection"},
			},
			// {
			// 	Type:      "lua",
			// 	LuaAction: "find_listener_route",
			// },
			// -------------------
			// Look for route name: selected_listener_route
			{
				//  listener-route-name exact match
				// http-request set-var(txn.tmp_exact)                   var(txn.selected_listener_name),concat("/",txn.host)
				// http-request set-var(txn.selected_listener_route)    var(txn.tmp_exact),map(listener_route_exact_match)
				Type:     "set-var",
				VarName:  "tmp_exact",
				VarScope: "txn",
				VarExpr:  "var(txn.selected_listener_name),concat(\"/\",txn.host)",
			},
			{
				Type:     "set-var",
				VarName:  "selected_listener_route,ifnotexists",
				VarScope: "txn",
				VarExpr:  "var(txn.tmp_exact),map(" + listenerRouteExactMatchMap.Path.FullPath() + ")",
				Metadata: map[string]any{"hug": "listener-route exact match selection"},
			},
			{
				//  listener-route-name wildcard match
				// http-request set-var(txn.tmp_wild)                    var(txn.selected_listener_name),concat("/",txn.hostreversed)
				// http-request set-var(txn.selected_listener_route)    var(txn.tmp_wild),map_beg(listener_route_wildcard_match)
				Type:     "set-var",
				VarName:  "tmp_wild",
				VarScope: "txn",
				VarExpr:  "var(txn.selected_listener_name),concat(\"/\",txn.hostreversed)",
			},
			{
				Type:     "set-var",
				VarName:  "selected_listener_route,ifnotexists",
				VarScope: "txn",
				VarExpr:  "var(txn.tmp_wild),map_beg(" + listenerRouteWildcardMatchMap.Path.FullPath() + ")",
				Metadata: map[string]any{"hug": "listener-route wildcard match selection"},
			},
			// -------------------
			// Lookups based on the selected listener route (selected_listener_route)
			// in the final routing maps
			// Now append the path to the selected_listener_route variable, so that we have in selected_listener_route the full route name (listener + route) to look for in the route maps
			{
				// Set var base_listener_route by appending path
				// http-request set-var(txn.base_listener_route)      var(txn.selected_listener_route),concat("",txn.path)
				Type:     "set-var",
				VarName:  "base_listener_route,ifnotexists",
				VarScope: "txn",
				VarExpr:  "var(txn.selected_listener_route),concat(\"\",txn.path)",
			},
			{
				// lookup in route_exact_match.map
				// exact path
				// http-request set-var(txn.base_listener_route) var(txn.base_listener_route),map(route_exact_match.map)
				Type:     "set-var",
				VarName:  "route",
				VarScope: "txn",
				VarExpr:  "var(txn.base_listener_route),map(" + pathExactMap.Path.FullPath() + ")",
				Metadata: map[string]any{"hug": "exact domain + exact path"},
			},
			{
				// lookup in route_prefix_match.map
				// path prefix
				// http-request set-var(txn.base_listener_route,ifnotexists) base,map_beg(route_prefix_match.map)
				Type:     "set-var",
				VarName:  "route,ifnotexists",
				VarScope: "txn",
				VarExpr:  "var(txn.base_listener_route),map_beg(" + pathPrefixMap.Path.FullPath() + ")",
				Metadata: map[string]any{"hug": "exact domain + path prefix"},
			},
			{
				// lookup in route_regex_match.map
				// # domain wildcard + path prefix. Example: ^[^.]+\.domain\.com/v1/foo/.*   # or map_sub
				// # domain wildcard + path regex   Example: ^[^.]+\.domain\.com/v[1-3]/foo
				// # exact domain + path regex      Example: ^www\.domain\.com/v[1-3]/foo
				// http-request set-var(txn.base_listener_route,ifnotexists) base,map_reg(route_regex.map)
				Type:     "set-var",
				VarName:  "route,ifnotexists",
				VarScope: "txn",
				VarExpr:  "var(txn.base_listener_route),map_reg(" + pathRegexMap.Path.FullPath() + ")",
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
				if vListener.ProtocolCategory == protocols.ProtocolCategorySecure || vListener.ProtocolCategory == protocols.ProtocolCategoryInsecure {
					return "http"
				}
				if vListener.ProtocolCategory == protocols.ProtocolCategoryTLS {
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
	port := int64(vListener.Port)
	if !b.params.DisableIPv4 {
		bind := models.Bind{
			Port: &port,
			Name: "v4",
			Address: func() string {
				if b.params.IPv4BindAddress != "" {
					return b.params.IPv4BindAddress
				}
				return "0.0.0.0"
			}(),
			BindParams: b.bindParams(frontendName, vListenerName, vListener),
		}
		if fe.Binds == nil {
			fe.Binds = make(map[string]models.Bind)
		}
		fe.Binds[bind.Name] = bind
	}
	if !b.params.DisableIPv6 {
		bind := models.Bind{
			Port: &port,
			Name: "v6",
			Address: func() string {
				if b.params.IPv6BindAddress != "" {
					return b.params.IPv6BindAddress
				}
				return "::"
			}(),
			BindParams: b.bindParams(frontendName, vListenerName, vListener),
		}
		if fe.Binds == nil {
			fe.Binds = make(map[string]models.Bind)
		}
		fe.Binds[bind.Name] = bind
	}
	return fe, nil
}

func (b *HaproxyConfMgrImpl) deleteFrontendForVirtualListener(virtualListenerName string) error {
	// Frontend for each listener
	feName := b.getFrontendName(virtualListenerName)
	if err := b.deleteFrontend(feName); err != nil {
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

func (b *HaproxyConfMgrImpl) deleteFrontend(feName string) error {
	return b.configuration.deleteFrontend(b.logger, feName)
}

func (b *HaproxyConfMgrImpl) logVirtualListenerUpdate(action string, virtualListenerName string) {
	b.logger.LogAttrs(context.Background(), slog.LevelDebug, "Processing VirtualListener ["+action+"]",
		logging.LogAttrVirtualListenerName(virtualListenerName),
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

func (b *HaproxyConfMgrImpl) bindParams(_, vListenerName string, vListener *tree.VirtualListener) models.BindParams {
	params := models.BindParams{}

	// If no TLS
	if vListener.ProtocolCategory == protocols.ProtocolCategoryInsecure || vListener.ProtocolCategory == protocols.ProtocolCategoryTLS {
		return params
	}

	// TLS terminate
	if vListener.ProtocolCategory == protocols.ProtocolCategorySecure {
		certFileDir := b.params.certificateStorage.CertListPath(vListenerName)
		params.CrtList = certFileDir.FullPath()
		params.Ssl = true
	}
	return params
}

// tlsPassthroughRules returns the TCP request rules, backend-switching rules
// and ACLs used by the TLS-passthrough frontend. All variables referenced in
// this rule set live in the sess scope — keep it that way: the route_is_json
// ACL reads var(sess.sni_match), matching the scope in which sni_match is
// written. Mixing sess and txn leaves the ACL branch permanently dead.
func tlsPassthroughRules(
	listenerExactMatch, listenerWildcardMatch,
	listenerRouteExactMatch, listenerRouteWildcardMatch,
	sniMapPath string,
) ([]*models.TCPRequestRule, []*models.BackendSwitchingRule, []*models.ACL) {
	tcpRules := []*models.TCPRequestRule{
		{ // tcp-request content reject if !{ req.ssl_hello_type 1 }
			Type:     "content",
			Action:   "reject",
			Cond:     "if",
			CondTest: "!{ req.ssl_hello_type 1 }",
		},
		{
			// tcp-request inspect-delay 50000
			Type:    "inspect-delay",
			Timeout: new(int64(50000)),
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
			// tcp-request content set-var(sess.snireversed) var(sess.sni),lua.reverse_host
			Type:     "content",
			Action:   "set-var",
			VarName:  "snireversed",
			VarScope: "sess",
			Expr:     "var(sess.sni),lua.reverse_host",
		},
		{
			// tcp-request content set-var(sess.selected_listener_name) var(sess.sni),map(listener_exact_match)
			Type:     "content",
			Action:   "set-var",
			VarName:  "selected_listener_name",
			VarScope: "sess",
			Expr:     "var(sess.sni),map(" + listenerExactMatch + ")",
			Metadata: map[string]any{"hug": "listener exact match selection"},
		},
		{
			// tcp-request content set-var(sess.selected_listener_name,ifnotexists) var(sess.snireversed),map_beg(listener_wildcard_match)
			Type:     "content",
			Action:   "set-var",
			VarName:  "selected_listener_name,ifnotexists",
			VarScope: "sess",
			Expr:     "var(sess.snireversed),map_beg(" + listenerWildcardMatch + ")",
			Metadata: map[string]any{"hug": "listener wildcard match selection"},
		},
		{
			// tcp-request content set-var(sess.tmp_exact) var(sess.selected_listener_name),concat("/",sess.sni)
			Type:     "content",
			Action:   "set-var",
			VarName:  "tmp_exact",
			VarScope: "sess",
			Expr:     "var(sess.selected_listener_name),concat(\"/\",sess.sni)",
		},
		{
			Type:     "content",
			Action:   "set-var",
			VarName:  "selected_listener_route",
			VarScope: "sess",
			Expr:     "var(sess.tmp_exact),map(" + listenerRouteExactMatch + ")",
			Metadata: map[string]any{"hug": "listener-route exact match selection"},
		},
		{
			// tcp-request content set-var(sess.tmp_wild) var(sess.selected_listener_name),concat("/",sess.snireversed)
			Type:     "content",
			Action:   "set-var",
			VarName:  "tmp_wild",
			VarScope: "sess",
			Expr:     "var(sess.selected_listener_name),concat(\"/\",sess.snireversed)",
		},
		{
			Type:     "content",
			Action:   "set-var",
			VarName:  "selected_listener_route,ifnotexists",
			VarScope: "sess",
			Expr:     "var(sess.tmp_wild),map_beg(" + listenerRouteWildcardMatch + ")",
			Metadata: map[string]any{"hug": "listener-route wildcard match selection"},
		},
		{
			// tcp-request content set-var(sess.sni_match) var(sess.selected_listener_route),map(sni.map)
			Type:     "content",
			Action:   "set-var",
			VarName:  "sni_match",
			VarScope: "sess",
			Expr:     "var(sess.selected_listener_route),map(" + sniMapPath + ")",
		},
	}
	backendSwitchingRules := []*models.BackendSwitchingRule{
		{
			Name:     "%[var(txn.backend)]",
			Cond:     "if",
			CondTest: "route_is_json",
		},
		{
			Name: "%[var(sess.sni_match),field(1,.)]",
		},
	}
	aclList := []*models.ACL{
		{ // acl route_is_json var(sess.sni_match),bytes(0,1) -m str {
			ACLName:   "route_is_json",
			Criterion: "var(sess.sni_match),bytes(0,1)",
			Value:     "-m str {",
			Metadata: map[string]any{
				"hug": "for lua routing",
			},
		},
	}
	return tcpRules, backendSwitchingRules, aclList
}
