//
// Copyright 2025 HAProxy Technologies LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
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

package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/index"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"

	ctlr "sigs.k8s.io/controller-runtime"
	ctlr_builder "sigs.k8s.io/controller-runtime/pkg/builder"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntimehandler "sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	// addIndexFieldTimeout is the timeout used for adding an Index Field to a cache.
	addIndexFieldTimeout = 2 * time.Minute
)

type recConfig struct {
	k8sPredicate         predicate.Predicate
	namespacedNameFilter NamespacedNameFilterFunc
	fieldIndices         index.FieldIndices
	newReconciler        NewReconcilerFunc
	enqueueForList       []enqueueForParams
	onlyMetadata         bool
}

type NewReconcilerFunc func(cfg ReconcilerConfig) *Reconciler

// Option defines configuration options for registering a controller.
type Option func(*recConfig)

// WithNamespacedNameFilter enables filtering of objects by NamespacedName by the controller.
func WithNamespacedNameFilter(filter NamespacedNameFilterFunc) Option {
	return func(cfg *recConfig) {
		cfg.namespacedNameFilter = filter
	}
}

// WithK8sPredicate enables filtering of events before they are sent to the controller.
func WithK8sPredicate(p predicate.Predicate) Option {
	return func(cfg *recConfig) {
		cfg.k8sPredicate = p
	}
}

// WithFieldIndices adds indices to the FieldIndexer of the manager.
func WithFieldIndices(fieldIndices index.FieldIndices) Option {
	return func(cfg *recConfig) {
		cfg.fieldIndices = fieldIndices
	}
}

// WithNewReconciler allows us to mock reconciler creation in the unit tests.
func WithNewReconciler(newReconciler NewReconcilerFunc) Option {
	return func(cfg *recConfig) {
		cfg.newReconciler = newReconciler
	}
}

// WithOnlyMetadata tells the controller to only cache metadata, and to watch the API server in metadata-only form.
func WithOnlyMetadata() Option {
	return func(cfg *recConfig) {
		cfg.onlyMetadata = true
	}
}

// WithEnqueueFor tells the controller to also watch for other types
// WithEnqueueFor registers secondary-watch enqueue edges. It accumulates across
// calls, so a controller may declare its edges in one call with a list or across
// several WithEnqueueFor calls; every edge is kept (the previous behaviour
// replaced the list, silently dropping all but the last call's edges).
func WithEnqueueFor(l []enqueueForParams) Option {
	return func(cfg *recConfig) {
		cfg.enqueueForList = append(cfg.enqueueForList, l...)
	}
}

func defaultConfig() recConfig {
	return recConfig{
		newReconciler: NewReconciler,
	}
}

func (c recConfig) hasEnqueueFor() bool {
	return len(c.enqueueForList) != 0
}

type registerParams struct {
	ctx        context.Context
	objectType ctrlruntimeclient.Object
	mgr        manager.Manager
	logger     *slog.Logger
	eventCh    chan<- any
	extractGVK utilsk8s.ExtractGVK
	name       string
	options    []Option
}

type enqueueForParams struct {
	// Which extra object type we want to Watch
	// For example, a GatewayClass controller depends and want to watch for HugGate
	watchSource ctrlruntimeclient.Object
	enqueueFunc func(client ctrlruntimeclient.Client, extractGVK utilsk8s.ExtractGVK) ctrlruntimehandler.MapFunc
	predicate   predicate.Predicate
}

// Register registers a new controller for the object type in the manager and configure it with the provided options.
// If the options include WithFieldIndices, it will add the specified indices to FieldIndexer of the manager.
// The registered controller will send events to the provided channel.
func Register(params registerParams) error {
	cfg := defaultConfig()

	for _, opt := range params.options {
		opt(&cfg)
	}

	for field, indexerFunc := range cfg.fieldIndices {
		addIndexParams := addIndexParams{
			ctx:         params.ctx,
			indexer:     params.mgr.GetFieldIndexer(),
			objectType:  params.objectType,
			field:       field,
			indexerFunc: indexerFunc,
		}
		if err := addIndex(addIndexParams); err != nil {
			return err
		}
	}

	var forOpts []ctlr_builder.ForOption
	// .For
	// // This is the equivalent of calling
	// Watches(source.Kind(cache, &Type{}, &handler.EnqueueRequestForObject{})).
	// It would be possible to enqueue more dependent object reconcilitions.
	if cfg.onlyMetadata {
		if params.objectType.GetObjectKind().GroupVersionKind().Empty() {
			return errors.New("the object must have its GVK set")
		}
		forOpts = append(forOpts, ctlr_builder.OnlyMetadata)
	}

	// If we have some predicates, add them
	if cfg.k8sPredicate != nil {
		forOpts = append(forOpts, ctlr_builder.WithPredicates(cfg.k8sPredicate))
	}

	// 1. Watch for the objectType itself
	builder := ctlr.NewControllerManagedBy(params.mgr).
		Named(params.name).
		For(params.objectType, forOpts...)

	// 2. Watch for dependent objects
	if cfg.hasEnqueueFor() {
		for _, ef := range cfg.enqueueForList {
			var enqueueOpts []ctlr_builder.WatchesOption
			if ef.predicate != nil {
				enqueueOpts = append(enqueueOpts, ctlr_builder.WithPredicates(ef.predicate))
			}

			builder = builder.Watches(
				ef.watchSource,
				ctrlruntimehandler.EnqueueRequestsFromMapFunc(ef.enqueueFunc(params.mgr.GetClient(), params.extractGVK)),
				enqueueOpts...,
			)
		}
	}

	reconcileConfig := ReconcilerConfig{
		Getter:               params.mgr.GetClient(),
		ObjectType:           params.objectType,
		EventCh:              params.eventCh,
		NamespacedNameFilter: cfg.namespacedNameFilter,
		OnlyMetadata:         cfg.onlyMetadata,
		Logger:               params.logger,
	}

	if err := builder.Complete(cfg.newReconciler(reconcileConfig)); err != nil {
		return fmt.Errorf("cannot build a controller for %T: %w", params.objectType, err)
	}

	return nil
}

type addIndexParams struct {
	ctx         context.Context
	indexer     ctrlruntimeclient.FieldIndexer
	objectType  ctrlruntimeclient.Object
	indexerFunc ctrlruntimeclient.IndexerFunc
	field       string
}

func addIndex(params addIndexParams) error {
	c, cancel := context.WithTimeout(params.ctx, addIndexFieldTimeout)
	defer cancel()

	if err := params.indexer.IndexField(c, params.objectType, params.field, params.indexerFunc); err != nil {
		return fmt.Errorf("failed to add index for %T for field %s: %w", params.objectType, params.field, err)
	}

	return nil
}
