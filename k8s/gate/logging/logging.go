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
package logging

import (
	"context"
	"log/slog"
	"maps"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	v3 "github.com/haproxytech/haproxy-unified-gateway/api/gate/v3"
)

type ContextKey string

const (
	LevelNone               slog.Level = 100
	CallerAdditionalSkipKey ContextKey = "callerSkip"
)

var (
	DefaultLogLevelPerCategory = map[v3.Category]slog.Level{
		LogCategoryK8s:           slog.LevelWarn,
		LogCategoryGate:          slog.LevelInfo,
		LogCategoryApp:           slog.LevelInfo,
		LogCategoryHaproxyCfgMgr: slog.LevelInfo,
		LogCategoryBatch:         slog.LevelInfo,
		LogCategoryStatus:        slog.LevelInfo,
		LogCategoryHugService:    slog.LevelInfo,
		LogCategoryCertsStorage:  slog.LevelInfo,
		LogCategoryMapsStorage:   slog.LevelInfo,
	}
	DefaultLevel = slog.LevelInfo

	// logLevelPerCategory contains the seetings if a CR is deployed
	// if no CR is deployed, it's set to defaultLogLevelPerCategory
	logLevelPerCategory = make(map[v3.Category]slog.Level)
	// level is the level used when no category is set with this value if the conf CR
	level slog.Level
	mu    sync.RWMutex
)

// groupOrAttrs holds either a group name or a list of slog.Attrs.
type groupOrAttrs struct {
	group string      // group name if non-empty
	attrs []slog.Attr // attrs if non-empty
}

// CategoryFilterHandler filters log records by level and key-value category.
type CategoryFilterHandler struct {
	base        slog.Handler
	categoryKey string
	goas        []groupOrAttrs
}

type CategoryFilterHandlerParams struct {
	Base                  slog.Handler
	DefaultCategoryLevels map[v3.Category]slog.Level
	CategoryKey           string
	DefaultLevel          slog.Level
}

// NewCategoryFilterHandler wraps an existing handler and filters by level and category.
func NewCategoryFilterHandler(params CategoryFilterHandlerParams) *CategoryFilterHandler {
	mu.Lock()
	defer mu.Unlock()
	DefaultLogLevelPerCategory = copyCategoryLevels(params.DefaultCategoryLevels)
	DefaultLevel = params.DefaultLevel
	logLevelPerCategory = copyCategoryLevels(params.DefaultCategoryLevels)

	return &CategoryFilterHandler{
		base:        params.Base,
		categoryKey: params.CategoryKey,
	}
}

func (h *CategoryFilterHandler) withGroupOrAttrs(goa groupOrAttrs) *CategoryFilterHandler {
	h2 := *h
	h2.goas = make([]groupOrAttrs, len(h.goas)+1)
	copy(h2.goas, h.goas)
	h2.goas[len(h2.goas)-1] = goa
	return &h2
}

func copyCategoryLevels(m map[v3.Category]slog.Level) map[v3.Category]slog.Level {
	cp := make(map[v3.Category]slog.Level, len(m))
	maps.Copy(cp, m)
	return cp
}

func GetLogSettings() (slog.Level, map[v3.Category]slog.Level) {
	mu.RLock()
	defer mu.RUnlock()
	return level, logLevelPerCategory
}

func (*CategoryFilterHandler) Enabled(_ context.Context, _ slog.Level) bool {
	return true // decision deferred to Handle
}

func (h *CategoryFilterHandler) Handle(ctx context.Context, r slog.Record) error {
	mu.Lock()
	defer mu.Unlock()
	filename := ""

	skip := 3 // default skip
	customSkip := 0
	if v := ctx.Value(CallerAdditionalSkipKey); v != nil {
		if c, ok := v.(int); ok {
			customSkip = c
		}
	}
	_, file, no, _ := runtime.Caller(skip + customSkip)

	// Empty Category shoulAd happen only for k8s Logs
	category := LogCategoryK8s

	goas := h.goas
	if r.NumAttrs() == 0 {
		// If the record has no Attrs, remove groups at the end of the list; they are empty.
		for len(goas) > 0 && goas[len(goas)-1].group != "" {
			goas = goas[:len(goas)-1]
		}
	}
	for _, goa := range goas {
		for _, a := range goa.attrs {
			r.AddAttrs(a)
			cat := h.getCategory(a)
			if cat != "" {
				category = v3.Category(cat)
				break
			}
		}
	}

	r.Attrs(func(a slog.Attr) bool {
		cat := h.getCategory(a)
		if cat != "" {
			category = v3.Category(cat)
			return false
		}
		return true
	})

	if category == LogCategoryK8s {
		filename = file
		// If no category is set, we use the default level
		r.AddAttrs(slog.String(h.categoryKey, string(category)))
	} else {
		filename = shortFilePath(file)
	}
	r.AddAttrs(LogAttrFileSource(filename, no))

	catLevel, ok := logLevelPerCategory[category]
	if !ok {
		catLevel = level
	}

	if r.Level < catLevel || catLevel == LevelNone {
		return nil
	}

	return h.base.Handle(ctx, r)
}

func (h *CategoryFilterHandler) getCategory(a slog.Attr) string {
	if a.Value.Kind() == slog.KindString && (a.Key == h.categoryKey) {
		return a.Value.String()
	}
	return ""
}

func (h *CategoryFilterHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	return h.withGroupOrAttrs(groupOrAttrs{attrs: attrs})
}

func (h *CategoryFilterHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return h.withGroupOrAttrs(groupOrAttrs{group: name})
}

func (h *CategoryFilterHandler) ResetToDefaults() {
	_ = h.ReconcileLogSettings(DefaultLevel, DefaultLogLevelPerCategory)
}

func (*CategoryFilterHandler) ReconcileLogSettings(aLevel slog.Level, categories map[v3.Category]slog.Level) bool {
	mu.Lock()
	defer mu.Unlock()
	levelChanged := level != aLevel
	if levelChanged {
		level = aLevel
	}

	newSet := make(map[v3.Category]slog.Level, len(categories))
	maps.Copy(newSet, categories)
	changed := !mapsEqual(logLevelPerCategory, newSet)
	if changed {
		logLevelPerCategory = newSet
	}
	return levelChanged || changed
}

func LogLevelString2SlogLevel(level string) slog.Level {
	switch level { //revive:disable:identical-switch-branches
	case "Info":
		return slog.LevelInfo
	case "Warn":
		return slog.LevelWarn
	case "Error":
		return slog.LevelError
	case "Debug":
		return slog.LevelDebug
	case "None":
		return LevelNone
	default:
		return slog.LevelInfo // Default to Info if unknown level
	}
}

func mapsEqual(a, b map[v3.Category]slog.Level) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func shortFilePath(fullPath string) string {
	const markerK8s = "k8s/"
	const markerController = "controller/"
	idx := strings.Index(fullPath, markerK8s)
	if idx >= 0 {
		return fullPath[idx:] // e.g., "k8s/gate/references/references.go"
	}
	idx = strings.Index(fullPath, markerController)
	if idx >= 0 {
		return fullPath[idx:] // e.g., "k8s/gate/references/references.go"
	}
	return filepath.Base(fullPath) // fallback to just filename
}
