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
package filters

import (
	"fmt"
	"strings"

	"github.com/haproxytech/client-native/v6/models"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Result holds the HAProxy rules derived from Gateway API HTTPRoute filters.
type Result struct {
	HTTPRequestRules  models.HTTPRequestRules
	HTTPResponseRules models.HTTPResponseRules
}

// ToHAProxyRules converts a slice of HTTPRouteFilters into HAProxy HTTP rules.
// matchPrefix is the path prefix matched by the route rule and is required for
// URLRewrite and RequestRedirect filters that use PrefixMatchHTTPPathModifier.
func ToHAProxyRules(httpFilters []gatewayv1.HTTPRouteFilter, matchPrefix string) Result {
	var result Result
	for _, filter := range httpFilters {
		switch filter.Type {
		case gatewayv1.HTTPRouteFilterRequestHeaderModifier:
			if filter.RequestHeaderModifier != nil {
				rules := requestHeaderModifierRules(filter.RequestHeaderModifier)
				result.HTTPRequestRules = append(result.HTTPRequestRules, rules...)
			}
		case gatewayv1.HTTPRouteFilterResponseHeaderModifier:
			if filter.ResponseHeaderModifier != nil {
				rules := responseHeaderModifierRules(filter.ResponseHeaderModifier)
				result.HTTPResponseRules = append(result.HTTPResponseRules, rules...)
			}
		case gatewayv1.HTTPRouteFilterURLRewrite:
			if filter.URLRewrite != nil {
				rules := urlRewriteRules(filter.URLRewrite, matchPrefix)
				result.HTTPRequestRules = append(result.HTTPRequestRules, rules...)
			}
		// RequestMirror and ExtensionRef are handled separately.
		}
	}
	_ = matchPrefix
	return result
}

// HasSideEffects returns true when the filter slice contains filters that produce
// HAProxy rules (i.e. anything other than ExtensionRef which is handled elsewhere).
func HasSideEffects(httpFilters []gatewayv1.HTTPRouteFilter) bool {
	for _, f := range httpFilters {
		switch f.Type {
		case gatewayv1.HTTPRouteFilterRequestHeaderModifier,
			gatewayv1.HTTPRouteFilterResponseHeaderModifier,
			gatewayv1.HTTPRouteFilterURLRewrite:
			return true
		}
	}
	return false
}

// urlRewriteRules converts an HTTPURLRewriteFilter to http-request rules.
func urlRewriteRules(f *gatewayv1.HTTPURLRewriteFilter, matchPrefix string) models.HTTPRequestRules {
	var rules models.HTTPRequestRules

	if f.Hostname != nil {
		rules = append(rules, &models.HTTPRequestRule{
			Type:      "set-header",
			HdrName:   "Host",
			HdrFormat: string(*f.Hostname),
		})
	}

	if f.Path != nil {
		switch f.Path.Type {
		case gatewayv1.FullPathHTTPPathModifier:
			if f.Path.ReplaceFullPath != nil {
				rules = append(rules, &models.HTTPRequestRule{
					Type:    "set-path",
					PathFmt: *f.Path.ReplaceFullPath,
				})
			}
		case gatewayv1.PrefixMatchHTTPPathModifier:
			if f.Path.ReplacePrefixMatch != nil && matchPrefix != "" {
				replacement := strings.TrimRight(*f.Path.ReplacePrefixMatch, "/")
				if matchPrefix == "/" {
					rules = append(rules, &models.HTTPRequestRule{
						Type:    "set-path",
						PathFmt: fmt.Sprintf("%s%%[path,regsub(^/$,)]", replacement),
					})
				} else {
					escapedPrefix := regexEscapePath(matchPrefix)
					rules = append(rules, &models.HTTPRequestRule{
						Type:      "replace-path",
						PathMatch: fmt.Sprintf("^%s(/.*)?$", escapedPrefix),
						PathFmt:   fmt.Sprintf("%s\\1", replacement),
					})
				}
			}
		}
	}
	return rules
}

// regexEscapePath escapes path characters that are special in POSIX extended regex.
func regexEscapePath(s string) string {
	const specialChars = `\.+*?()|[]{}^$`
	var b strings.Builder
	for _, c := range s {
		if strings.ContainsRune(specialChars, c) {
			_, _ = b.WriteRune('\\')
		}
		_, _ = b.WriteRune(c)
	}
	return b.String()
}

// requestHeaderModifierRules converts an HTTPHeaderFilter to http-request rules.
func requestHeaderModifierRules(f *gatewayv1.HTTPHeaderFilter) models.HTTPRequestRules {
	var rules models.HTTPRequestRules
	for _, h := range f.Set {
		rules = append(rules, &models.HTTPRequestRule{
			Type:      "set-header",
			HdrName:   string(h.Name),
			HdrFormat: h.Value,
		})
	}
	for _, h := range f.Add {
		rules = append(rules, &models.HTTPRequestRule{
			Type:      "add-header",
			HdrName:   string(h.Name),
			HdrFormat: h.Value,
		})
	}
	for _, name := range f.Remove {
		rules = append(rules, &models.HTTPRequestRule{
			Type:    "del-header",
			HdrName: name,
		})
	}
	return rules
}

// responseHeaderModifierRules converts an HTTPHeaderFilter to http-response rules.
func responseHeaderModifierRules(f *gatewayv1.HTTPHeaderFilter) models.HTTPResponseRules {
	var rules models.HTTPResponseRules
	for _, h := range f.Set {
		rules = append(rules, &models.HTTPResponseRule{
			Type:      "set-header",
			HdrName:   string(h.Name),
			HdrFormat: h.Value,
		})
	}
	for _, h := range f.Add {
		rules = append(rules, &models.HTTPResponseRule{
			Type:      "add-header",
			HdrName:   string(h.Name),
			HdrFormat: h.Value,
		})
	}
	for _, name := range f.Remove {
		rules = append(rules, &models.HTTPResponseRule{
			Type:    "del-header",
			HdrName: name,
		})
	}
	return rules
}
