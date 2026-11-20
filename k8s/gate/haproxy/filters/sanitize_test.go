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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func headerFilter(f gatewayv1.HTTPHeaderFilter) []gatewayv1.HTTPRouteFilter {
	return []gatewayv1.HTTPRouteFilter{{Type: gatewayv1.HTTPRouteFilterRequestHeaderModifier, RequestHeaderModifier: &f}}
}

func responseHeaderFilter(f gatewayv1.HTTPHeaderFilter) []gatewayv1.HTTPRouteFilter {
	return []gatewayv1.HTTPRouteFilter{{Type: gatewayv1.HTTPRouteFilterResponseHeaderModifier, ResponseHeaderModifier: &f}}
}

func rewriteFilter(path gatewayv1.HTTPPathModifier) []gatewayv1.HTTPRouteFilter {
	return []gatewayv1.HTTPRouteFilter{{
		Type:       gatewayv1.HTTPRouteFilterURLRewrite,
		URLRewrite: &gatewayv1.HTTPURLRewriteFilter{Path: &path},
	}}
}

func redirectFilter(f gatewayv1.HTTPRequestRedirectFilter) []gatewayv1.HTTPRouteFilter {
	return []gatewayv1.HTTPRouteFilter{{Type: gatewayv1.HTTPRouteFilterRequestRedirect, RequestRedirect: &f}}
}

func corsFilter(f gatewayv1.HTTPCORSFilter) []gatewayv1.HTTPRouteFilter {
	return []gatewayv1.HTTPRouteFilter{{Type: gatewayv1.HTTPRouteFilterCORS, CORS: &f}}
}

func fullPath(p string) gatewayv1.HTTPPathModifier {
	return gatewayv1.HTTPPathModifier{Type: gatewayv1.FullPathHTTPPathModifier, ReplaceFullPath: &p}
}

func prefixPath(p string) gatewayv1.HTTPPathModifier {
	return gatewayv1.HTTPPathModifier{Type: gatewayv1.PrefixMatchHTTPPathModifier, ReplacePrefixMatch: &p}
}

func TestLiteralFmt(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"plain word", "bar", `'bar'`},
		{"spaces stay one argument", "a b", `'a b'`},
		{"sample expression is not evaluated", "%[env(HOME)]", `'%%[env(HOME)]'`},
		{"variable is not expanded", "${HOME}", `'${HOME}'`},
		{"single quote", "it's", `'it'\''s'`},
		{"backslash, double quote and comment sign", `a\n "q" #x`, `'a\n "q" #x'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, literalFmt(tt.in))
		})
	}
}

func TestValidateFiltersAccepts(t *testing.T) {
	host := gatewayv1.PreciseHostname("example.org")
	tests := []struct {
		name     string
		filters  []gatewayv1.HTTPRouteFilter
		prefixes []string
	}{
		{"header modifier", headerFilter(gatewayv1.HTTPHeaderFilter{
			Set:    []gatewayv1.HTTPHeader{{Name: "X-Foo", Value: "bar baz\twith tab"}},
			Add:    []gatewayv1.HTTPHeader{{Name: "x_a.b-c!&*+^`|~", Value: `%[env(HOME)] ${HOME} it's "q" #x \n`}},
			Remove: []string{"X-Remove"},
		}), nil},
		{"rewrite full path", rewriteFilter(fullPath("/a%20b/c;d=e:f@g!$&*+~._-")), nil},
		{"rewrite prefix", rewriteFilter(prefixPath("/new")), []string{"/old.v1"}},
		{"redirect", redirectFilter(gatewayv1.HTTPRequestRedirectFilter{
			Scheme: new("https"), Hostname: &host, Path: new(prefixPath("/new")),
		}), []string{"/old"}},
		{"cors", corsFilter(gatewayv1.HTTPCORSFilter{
			AllowOrigins:  []gatewayv1.CORSOrigin{"https://*.example.org:8443", "*"},
			AllowMethods:  []gatewayv1.HTTPMethodWithWildcard{"GET", "*"},
			AllowHeaders:  []gatewayv1.HTTPHeaderName{"X-A", "*"},
			ExposeHeaders: []gatewayv1.HTTPHeaderName{"X-B"},
		}), nil},
		{"unsafe matched prefix without a prefix replacement", rewriteFilter(fullPath("/x")), []string{"/a,b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NoError(t, ValidateFilters(tt.filters, tt.prefixes...))
		})
	}
}

func TestValidateFiltersRejects(t *testing.T) {
	badHost := gatewayv1.PreciseHostname("example.org evil")
	tests := []struct {
		name     string
		filters  []gatewayv1.HTTPRouteFilter
		prefixes []string
	}{
		{"remove entry opening a new config line", headerFilter(gatewayv1.HTTPHeaderFilter{
			Remove: []string{"x\nfrontend injected\n  bind 127.0.0.1:28999"},
		}), nil},
		{"remove entry with a space", headerFilter(gatewayv1.HTTPHeaderFilter{Remove: []string{"x if TRUE"}}), nil},
		{"empty remove entry", headerFilter(gatewayv1.HTTPHeaderFilter{Remove: []string{""}}), nil},
		{"response remove entry with a newline", responseHeaderFilter(gatewayv1.HTTPHeaderFilter{Remove: []string{"x\ny"}}), nil},
		{"name with a comment sign", headerFilter(gatewayv1.HTTPHeaderFilter{Set: []gatewayv1.HTTPHeader{{Name: "a#b", Value: "v"}}}), nil},
		{"name with a quote", headerFilter(gatewayv1.HTTPHeaderFilter{Set: []gatewayv1.HTTPHeader{{Name: "a'b", Value: "v"}}}), nil},
		{"name with a dollar", headerFilter(gatewayv1.HTTPHeaderFilter{Add: []gatewayv1.HTTPHeader{{Name: "a$b", Value: "v"}}}), nil},
		{"name with a percent", headerFilter(gatewayv1.HTTPHeaderFilter{Add: []gatewayv1.HTTPHeader{{Name: "a%b", Value: "v"}}}), nil},
		{"name closing the sample expression", headerFilter(gatewayv1.HTTPHeaderFilter{Add: []gatewayv1.HTTPHeader{{Name: "a)]b", Value: "v"}}}), nil},
		{"value with a newline", headerFilter(gatewayv1.HTTPHeaderFilter{Set: []gatewayv1.HTTPHeader{{Name: "X", Value: "a\nb"}}}), nil},
		{"value with a carriage return", responseHeaderFilter(gatewayv1.HTTPHeaderFilter{Add: []gatewayv1.HTTPHeader{{Name: "X", Value: "a\rb"}}}), nil},
		{"rewrite path with a space", rewriteFilter(fullPath("/a b")), nil},
		{"rewrite path with a sample expression", rewriteFilter(fullPath("/%[env(HOME)]")), nil},
		{"rewrite prefix with a backslash", rewriteFilter(prefixPath(`/a\1`)), []string{"/old"}},
		{"redirect path with a newline", redirectFilter(gatewayv1.HTTPRequestRedirectFilter{Path: new(fullPath("/a\nb"))}), nil},
		{"redirect prefix closing the converter", redirectFilter(gatewayv1.HTTPRequestRedirectFilter{Path: new(prefixPath("/a),lower"))}), []string{"/old"}},
		{"matched prefix closing the converter", redirectFilter(gatewayv1.HTTPRequestRedirectFilter{Path: new(prefixPath("/new"))}), []string{"/a,b)"}},
		{"redirect hostname with a space", redirectFilter(gatewayv1.HTTPRequestRedirectFilter{Hostname: &badHost}), nil},
		{"redirect scheme with a space", redirectFilter(gatewayv1.HTTPRequestRedirectFilter{Scheme: new("https evil")}), nil},
		{"rewrite hostname with a space", []gatewayv1.HTTPRouteFilter{{
			Type: gatewayv1.HTTPRouteFilterURLRewrite, URLRewrite: &gatewayv1.HTTPURLRewriteFilter{Hostname: &badHost},
		}}, nil},
		{"cors origin closing the condition", corsFilter(gatewayv1.HTTPCORSFilter{AllowOrigins: []gatewayv1.CORSOrigin{"https://a } { always_true"}}), nil},
		{"cors header with a space", corsFilter(gatewayv1.HTTPCORSFilter{AllowHeaders: []gatewayv1.HTTPHeaderName{"a b"}}), nil},
		{"cors exposed header with a dollar", corsFilter(gatewayv1.HTTPCORSFilter{ExposeHeaders: []gatewayv1.HTTPHeaderName{"${HOME}"}}), nil},
		{"cors method with a space", corsFilter(gatewayv1.HTTPCORSFilter{AllowMethods: []gatewayv1.HTTPMethodWithWildcard{"GET evil"}}), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Error(t, ValidateFilters(tt.filters, tt.prefixes...))
		})
	}
}

func TestToHAProxyRulesRendersValuesLiterally(t *testing.T) {
	result := ToHAProxyRules(headerFilter(gatewayv1.HTTPHeaderFilter{
		Set: []gatewayv1.HTTPHeader{{Name: "X-Set", Value: "%[env(HOME)] it's"}},
		Add: []gatewayv1.HTTPHeader{{Name: "X-Add", Value: "a b"}},
	}), "")
	require.Len(t, result.HTTPRequestRules, 3)
	assert.Equal(t, `'%%[env(HOME)] it'\''s'`, result.HTTPRequestRules[0].HdrFormat)
	assert.Equal(t, `%[req.fhdr(X-Add)],'a b'`, result.HTTPRequestRules[1].HdrFormat)
	assert.Equal(t, `'a b'`, result.HTTPRequestRules[2].HdrFormat)

	response := ToHAProxyRules(responseHeaderFilter(gatewayv1.HTTPHeaderFilter{
		Set: []gatewayv1.HTTPHeader{{Name: "X-Set", Value: "100%"}},
		Add: []gatewayv1.HTTPHeader{{Name: "X-Add", Value: "a b"}},
	}), "")
	require.Len(t, response.HTTPResponseRules, 3)
	assert.Equal(t, `'100%%'`, response.HTTPResponseRules[0].HdrFormat)
	assert.Equal(t, `%[res.fhdr(X-Add)],'a b'`, response.HTTPResponseRules[1].HdrFormat)
	assert.Equal(t, `'a b'`, response.HTTPResponseRules[2].HdrFormat)
}

func TestToHAProxyRulesKeepsPercentInPathsLiteral(t *testing.T) {
	rewrite := ToHAProxyRules(rewriteFilter(fullPath("/a%20b")), "")
	require.Len(t, rewrite.HTTPRequestRules, 1)
	assert.Equal(t, "/a%%20b", rewrite.HTTPRequestRules[0].PathFmt)

	prefix := ToHAProxyRules(rewriteFilter(prefixPath("/n%20w")), "/old")
	require.Len(t, prefix.HTTPRequestRules, 1)
	assert.Equal(t, `/n%%20w\1`, prefix.HTTPRequestRules[0].PathFmt)

	root := ToHAProxyRules(rewriteFilter(prefixPath("/n%20w")), "/")
	require.Len(t, root.HTTPRequestRules, 1)
	assert.Equal(t, "/n%%20w%[path,regsub(^/$,)]", root.HTTPRequestRules[0].PathFmt)

	redirect := ToHAProxyRules(redirectFilter(gatewayv1.HTTPRequestRedirectFilter{Path: new(fullPath("/a%20b"))}), "")
	require.Len(t, redirect.RedirectRules, 1)
	assert.Equal(t, "/a%%20b", redirect.RedirectRules[0].RedirValue)

	// Converter arguments are not log-format: the percent stays single.
	redirectPrefix := ToHAProxyRules(redirectFilter(gatewayv1.HTTPRequestRedirectFilter{Path: new(prefixPath("/n%20w"))}), "/old")
	require.Len(t, redirectPrefix.RedirectRules, 1)
	assert.Equal(t, "%[path,regsub(^/old,/n%20w)]", redirectPrefix.RedirectRules[0].RedirValue)
}

func TestToHAProxyRulesRefusesUnsafeFilters(t *testing.T) {
	result := ToHAProxyRules(headerFilter(gatewayv1.HTTPHeaderFilter{
		Set:    []gatewayv1.HTTPHeader{{Name: "X-Ok", Value: "v"}},
		Remove: []string{"x\nfrontend injected"},
	}), "")
	assert.Empty(t, result.HTTPRequestRules)

	prefix := ToHAProxyRules(redirectFilter(gatewayv1.HTTPRequestRedirectFilter{Path: new(prefixPath("/new"))}), "/a,b)")
	assert.Empty(t, prefix.RedirectRules)
}
