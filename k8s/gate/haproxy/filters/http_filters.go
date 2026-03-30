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
	"strconv"
	"strings"

	"github.com/haproxytech/client-native/v6/models"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Result holds the HAProxy rules derived from Gateway API HTTPRoute filters.
type Result struct {
	// RedirectRules holds the http-request redirect rule(s).  Usually one rule,
	// but a ReplacePrefixMatch on the root path "/" requires two conditional rules
	// to correctly handle both the exact root and paths with a suffix.
	RedirectRules     models.HTTPRequestRules
	HTTPRequestRules  models.HTTPRequestRules
	HTTPResponseRules models.HTTPResponseRules
	// IsRedirect is true when the filters contain a RequestRedirect filter.
	// When true, the backend acts as a redirect-only backend with no real servers.
	IsRedirect bool
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
		case gatewayv1.HTTPRouteFilterRequestRedirect:
			if filter.RequestRedirect != nil {
				result.IsRedirect = true
				result.RedirectRules = requestRedirectRules(filter.RequestRedirect, matchPrefix)
			}
		case gatewayv1.HTTPRouteFilterCORS:
			if filter.CORS != nil {
				reqRules, respRules := corsRules(filter.CORS)
				result.HTTPRequestRules = append(result.HTTPRequestRules, reqRules...)
				result.HTTPResponseRules = append(result.HTTPResponseRules, respRules...)
			}
			// RequestMirror and ExtensionRef are handled separately.
		}
	}
	return result
}

// HasSideEffects returns true when the filter slice contains filters that produce
// HAProxy rules (i.e. anything other than ExtensionRef which is handled elsewhere).
func HasSideEffects(httpFilters []gatewayv1.HTTPRouteFilter) bool {
	for _, f := range httpFilters {
		switch f.Type {
		case gatewayv1.HTTPRouteFilterRequestHeaderModifier,
			gatewayv1.HTTPRouteFilterResponseHeaderModifier,
			gatewayv1.HTTPRouteFilterURLRewrite,
			gatewayv1.HTTPRouteFilterRequestRedirect,
			gatewayv1.HTTPRouteFilterCORS:
			return true
		}
	}
	return false
}

// corsOriginConfig holds the computed origin-matching strategy for a CORS filter.
type corsOriginConfig struct {
	// headerValue is the Access-Control-Allow-Origin header value: "*" or
	// "%[req.hdr(Origin)]" (HAProxy fetch expression that echoes the request origin).
	headerValue string
	// condTest is the HAProxy ACL test for "origin matches".  Empty means
	// unconditional (used when headerValue is "*" and credentials are not required).
	condTest string
}

// buildCORSOriginConfig derives the origin matching strategy from the filter spec.
func buildCORSOriginConfig(origins []gatewayv1.CORSOrigin, allowCredentials bool) corsOriginConfig {
	isWildcardAll := len(origins) == 0
	for _, o := range origins {
		if string(o) == "*" {
			isWildcardAll = true
			break
		}
	}
	if isWildcardAll {
		if !allowCredentials {
			return corsOriginConfig{headerValue: "*", condTest: ""}
		}
		return corsOriginConfig{
			headerValue: "%[req.hdr(Origin)]",
			condTest:    "{ hdr_cnt(Origin) gt 0 }",
		}
	}
	patterns := make([]string, 0, len(origins))
	for _, o := range origins {
		patterns = append(patterns, originToPattern(string(o)))
	}
	return corsOriginConfig{
		headerValue: "%[req.hdr(Origin)]",
		condTest:    fmt.Sprintf("{ req.hdr(Origin) -m reg ^(%s)$ }", strings.Join(patterns, "|")),
	}
}

// originToPattern converts a CORSOrigin string to a POSIX ERE pattern fragment.
func originToPattern(origin string) string {
	var b strings.Builder
	for _, c := range origin {
		switch c {
		case '.':
			_, _ = b.WriteString(`\.`)
		case '*':
			_, _ = b.WriteString(`.*`)
		default:
			_, _ = b.WriteRune(c)
		}
	}
	return b.String()
}

// corsRules converts an HTTPCORSFilter into http-request and http-response rules.
// A set-var-fmt rule captures the validated Origin into txn.cors_origin so that
// response rules can reference var(txn.cors_origin) instead of req.hdr(Origin),
// which HAProxy's config checker disallows in http-response context.
func corsRules(f *gatewayv1.HTTPCORSFilter) (models.HTTPRequestRules, models.HTTPResponseRules) {
	allowCredentials := f.AllowCredentials != nil && *f.AllowCredentials
	originCfg := buildCORSOriginConfig(f.AllowOrigins, allowCredentials)

	var reqRules models.HTTPRequestRules

	responseOriginValue := originCfg.headerValue
	if originCfg.condTest != "" {
		reqRules = append(reqRules, &models.HTTPRequestRule{
			Type:      "set-var-fmt",
			VarScope:  "txn",
			VarName:   "cors_origin",
			VarFormat: "%[req.hdr(Origin)]",
			Cond:      "if",
			CondTest:  originCfg.condTest,
		})
		responseOriginValue = "%[var(txn.cors_origin)]"
	}

	preflightHdrs := corsPreflightHeaders(f, originCfg, allowCredentials)
	var preflightCondTest string
	if originCfg.condTest == "" {
		preflightCondTest = "{ method OPTIONS } { hdr_cnt(Origin) gt 0 }"
	} else {
		preflightCondTest = "{ method OPTIONS } " + originCfg.condTest
	}
	statusCode := int64(204)
	reqRules = append(reqRules, &models.HTTPRequestRule{
		Type:             "return",
		ReturnStatusCode: &statusCode,
		ReturnHeaders:    preflightHdrs,
		Cond:             "if",
		CondTest:         preflightCondTest,
	})

	responseRules := corsResponseRules(f, originCfg, allowCredentials, responseOriginValue)
	return reqRules, responseRules
}

// corsPreflightHeaders builds the ReturnHeader slice for the preflight http-request
// return rule.  Multi-value strings such as "GET, POST" must be double-quoted so that
// HAProxy's config-parser treats the comma-separated list as a single token.
func corsPreflightHeaders(f *gatewayv1.HTTPCORSFilter, originCfg corsOriginConfig, allowCredentials bool) []*models.ReturnHeader {
	strPtr := func(s string) *string { return &s }
	quoteFmt := func(s string) string {
		if strings.Contains(s, " ") {
			return `"` + s + `"`
		}
		return s
	}

	hdrs := []*models.ReturnHeader{
		{Name: strPtr("Access-Control-Allow-Origin"), Fmt: strPtr(originCfg.headerValue)},
	}
	if allowCredentials {
		hdrs = append(hdrs, &models.ReturnHeader{Name: strPtr("Access-Control-Allow-Credentials"), Fmt: strPtr("true")})
	}
	if len(f.AllowMethods) > 0 {
		methods := make([]string, len(f.AllowMethods))
		for i, m := range f.AllowMethods {
			methods[i] = string(m)
		}
		hdrs = append(hdrs, &models.ReturnHeader{Name: strPtr("Access-Control-Allow-Methods"), Fmt: strPtr(quoteFmt(strings.Join(methods, ", ")))})
	}
	if len(f.AllowHeaders) > 0 {
		headers := make([]string, len(f.AllowHeaders))
		for i, h := range f.AllowHeaders {
			headers[i] = string(h)
		}
		hdrs = append(hdrs, &models.ReturnHeader{Name: strPtr("Access-Control-Allow-Headers"), Fmt: strPtr(quoteFmt(strings.Join(headers, ", ")))})
	}
	maxAge := int32(5)
	if f.MaxAge != 0 {
		maxAge = f.MaxAge
	}
	hdrs = append(hdrs, &models.ReturnHeader{Name: strPtr("Access-Control-Max-Age"), Fmt: strPtr(strconv.Itoa(int(maxAge)))})
	return hdrs
}

// corsResponseRules builds the http-response add-header rules for regular (non-OPTIONS)
// cross-origin requests.  A !{ method OPTIONS } guard prevents duplicate headers.
// When origins are specific, the condition uses var(txn.cors_origin) instead of
// req.hdr(Origin) because HAProxy rejects req.* fetches in http-response context.
func corsResponseRules(f *gatewayv1.HTTPCORSFilter, originCfg corsOriginConfig, allowCredentials bool, originValue string) models.HTTPResponseRules {
	quoteFmt := func(s string) string {
		if strings.Contains(s, " ") {
			return `"` + s + `"`
		}
		return s
	}

	// Use var(txn.cors_origin) presence as condition when origins are specific:
	// the var is only set when the request Origin matched, so checking it avoids
	// using req.hdr which HAProxy forbids in http-response rules.
	var condTest string
	if originCfg.condTest == "" {
		condTest = "!{ method OPTIONS }"
	} else {
		condTest = "{ var(txn.cors_origin) -m found } !{ method OPTIONS }"
	}

	addRule := func(name, value string) *models.HTTPResponseRule {
		return &models.HTTPResponseRule{
			Type:      "add-header",
			HdrName:   name,
			HdrFormat: value,
			Cond:      "if",
			CondTest:  condTest,
		}
	}

	rules := models.HTTPResponseRules{addRule("Access-Control-Allow-Origin", originValue)}
	if allowCredentials {
		rules = append(rules, addRule("Access-Control-Allow-Credentials", "true"))
	}
	if len(f.ExposeHeaders) > 0 {
		headers := make([]string, len(f.ExposeHeaders))
		for i, h := range f.ExposeHeaders {
			headers[i] = string(h)
		}
		rules = append(rules, addRule("Access-Control-Expose-Headers", quoteFmt(strings.Join(headers, ", "))))
	}
	return rules
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

// requestRedirectRules converts an HTTPRequestRedirectFilter to one or two
// http-request redirect rules.  Two rules are needed when a ReplacePrefixMatch
// is applied to the root path "/": one for the exact root and one for paths
// that have a suffix (see the urlRewriteRules comment for the full rationale).
func requestRedirectRules(f *gatewayv1.HTTPRequestRedirectFilter, matchPrefix string) models.HTTPRequestRules {
	statusCode := int64(302)
	if f.StatusCode != nil {
		statusCode = int64(*f.StatusCode)
	}

	// Scheme-only redirect: HAProxy handles this natively and preserves the
	// original host, path, and query string automatically.
	if f.Scheme != nil && f.Hostname == nil && f.Port == nil && f.Path == nil {
		return models.HTTPRequestRules{{
			Type:       "redirect",
			RedirType:  "scheme",
			RedirValue: *f.Scheme,
			RedirCode:  &statusCode,
		}}
	}

	// Build the authority prefix (scheme://host:port) shared by all location rules.
	var authority strings.Builder
	if f.Scheme != nil {
		_, _ = authority.WriteString(*f.Scheme)
		_, _ = authority.WriteString("://")
	}
	if f.Hostname != nil {
		_, _ = authority.WriteString(string(*f.Hostname))
	} else if f.Scheme != nil || f.Port != nil {
		_, _ = authority.WriteString("%[req.hdr(host),host_only]")
	}
	if f.Port != nil {
		_, _ = authority.WriteString(fmt.Sprintf(":%d", *f.Port))
	}
	pfx := authority.String()

	redirect := func(location, cond, condTest string) *models.HTTPRequestRule {
		r := &models.HTTPRequestRule{
			Type:       "redirect",
			RedirType:  "location",
			RedirValue: location,
			RedirCode:  &statusCode,
		}
		if cond != "" {
			r.Cond = cond
			r.CondTest = condTest
		}
		return r
	}

	if f.Path == nil {
		return models.HTTPRequestRules{redirect(pfx+"%[path]", "", "")}
	}

	switch f.Path.Type {
	case gatewayv1.FullPathHTTPPathModifier:
		pathValue := "%[path]"
		if f.Path.ReplaceFullPath != nil {
			pathValue = *f.Path.ReplaceFullPath
		}
		return models.HTTPRequestRules{redirect(pfx+pathValue, "", "")}

	case gatewayv1.PrefixMatchHTTPPathModifier:
		if f.Path.ReplacePrefixMatch == nil || matchPrefix == "" {
			return models.HTTPRequestRules{redirect(pfx+"%[path]", "", "")}
		}
		replacement := strings.TrimRight(*f.Path.ReplacePrefixMatch, "/")
		if matchPrefix == "/" {
			return models.HTTPRequestRules{
				redirect(fmt.Sprintf("%s%s%%[path,regsub(^/$,)]", pfx, replacement), "", ""),
			}
		}
		escapedPrefix := regexEscapePath(matchPrefix)
		return models.HTTPRequestRules{
			redirect(fmt.Sprintf("%s%%[path,regsub(^%s(/.*)?$,%s\\1)]",
				pfx, escapedPrefix, replacement), "", ""),
		}
	}

	return models.HTTPRequestRules{redirect(pfx+"%[path]", "", "")}
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
