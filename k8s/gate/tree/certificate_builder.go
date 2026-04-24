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
package tree

import (
	"context"
	"log/slog"

	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/certificate"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/haproxy/storage"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/logging"
	objtypes "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/object-types"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/store"
	"github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils"
	utilsk8s "github.com/haproxytech/haproxy-unified-gateway/k8s/gate/utils-k8s"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ Builder = &CertificateBuilderImpl{}

type CertificateBuilderImpl struct {
	certStorage storage.CertificateStorage
	*ControllerStore
	// storeCertificatesOnDisk is a flag that indicates to the gate library to store certificates on disk
	storeCertificateOnDisk bool
	// try to perform runtime update of haproxy using runtime socket
	runtimeUpdateHaproxy bool
}

func NewCertificateBuilder(controllerStore *ControllerStore, storeCertOnDisk, runtimeUpdate bool, certStorage storage.CertificateStorage) Builder {
	return &CertificateBuilderImpl{
		ControllerStore:        controllerStore,
		storeCertificateOnDisk: storeCertOnDisk,
		certStorage:            certStorage,
		runtimeUpdateHaproxy:   runtimeUpdate,
	}
}

// --------------------
// GateTree Updates
// --------------------

// --------------------

func (b *CertificateBuilderImpl) ComputeTreeUpdates() {
	b.computeCertificateDiffs()
	if b.storeCertificateOnDisk {
		b.ensureCertificatesStorage()
		err := b.certStorage.DeleteEmptyCertsDir()
		if err != nil {
			b.Logger.LogAttrs(context.Background(), slog.LevelError, "error deleting empty cert dirs",
				logging.LogAttrError(err))
		}
	}
	if b.runtimeUpdateHaproxy {
		b.runtimeUpdates()
	}
}

func (*CertificateBuilderImpl) CleanTreeUpdates() {
}

// -----------------------------------------------

func (b *CertificateBuilderImpl) computeCertificateDiffs() {
	// Current secrets referenced by a Listener
	currentSecretsReferenced := b.ControllerStore.ReferencedObjects.ReferencedSecrets.AllReferenced(b.ControllerStore.ExtractGVK(objtypes.ObjectTypeGateway))
	// Previous secrets referenced by a Listener
	previousSecretsReferenced := b.ControllerStore.ReferencedObjects.PreviousReferencedSecrets.AllReferenced(b.ControllerStore.ExtractGVK(objtypes.ObjectTypeGateway))

	// Cert
	// Handle the newly added referenced Secrets
	// b.handleNewReferencedSecretsStorage(previousSecretsReferenced, currentSecretsReferenced)
	b.handleNewReferencedSecretsStorage(previousSecretsReferenced, currentSecretsReferenced)
	// Handle the Secrets that are de-referenced
	b.handleDeReferencedSecretsStorage(previousSecretsReferenced, currentSecretsReferenced)
	// Handle the Secrets that are still referenced, but migth have added : UPSERTED or DELETED
	b.handleUpdatedSecretsStorage(previousSecretsReferenced, currentSecretsReferenced)

	// crt-list
	b.handleCrtList(currentSecretsReferenced)
}

func (b *CertificateBuilderImpl) ensureCertificatesStorage() {
	// ---------------
	// Write them on disk
	certUpdates := b.ControllerStore.CertUpdates
	crtListUpdates := b.ControllerStore.CrtListUpdates
	// ------------
	// cert
	for _, certData := range certUpdates.Created {
		_ = b.certStorage.WriteOnDisk(certData)
	}
	for _, certData := range certUpdates.Updated {
		_ = b.certStorage.WriteOnDisk(certData)
	}
	for _, certData := range certUpdates.Deleted {
		_ = b.certStorage.DeleteFromDisk(certData)
	}

	// -------------
	// crt-list
	for _, crtListData := range crtListUpdates.Created {
		_ = b.certStorage.WriteCrtListOnDisk(crtListData)
	}
	for _, crtListData := range crtListUpdates.Updated {
		_ = b.certStorage.WriteCrtListOnDisk(crtListData)
	}
	for _, crtListData := range crtListUpdates.Deleted {
		_ = b.certStorage.DeleteCrtListFromDisk(crtListData)
	}
}

func (*CertificateBuilderImpl) runtimeUpdates() {
}

// -----------
// cert

func (b *CertificateBuilderImpl) handleNewReferencedSecretsStorage(previousRefSecrets, newRefSecrets map[client.ObjectKey]map[client.ObjectKey]struct{}) {
	newlyReferenced := utils.SetDifference(newRefSecrets, previousRefSecrets)
	// -----------
	// Compute new CertificateData
	for nsName := range newlyReferenced {
		secret, ok := b.GateTree.Secrets[nsName]
		if !ok {
			b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "Cert [none-newref not found]", logging.LogAttrKey(nsName))
			continue
		}
		// In the same bach handling, the Gateway is updated with a new Secret, but the Secret is deleted
		// Do not create it
		if secret.TreeStatus.Status == store.StatusDeleted {
			b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "Cert [none-newref deleted]", logging.LogAttrKey(nsName))
			continue
		}

		certData, err := b.certStorage.NewCertificateData(secret.K8sResource)
		if err != nil {
			continue
		}
		b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "Cert [create-newref]", logging.LogAttrKey(nsName))
		b.ControllerStore.addCreatedCertificate(certData)
	}
}

func (b *CertificateBuilderImpl) handleDeReferencedSecretsStorage(previousRefSecrets, newRefSecrets map[client.ObjectKey]map[client.ObjectKey]struct{}) {
	deReferenced := utils.SetDifference(previousRefSecrets, newRefSecrets)
	for nsName := range deReferenced {
		certPath := b.certStorage.CertPath(nsName)
		certData := certificate.NewCertificateData(certPath, nil)
		b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "Cert [delete-unref]", logging.LogAttrKey(nsName))
		b.ControllerStore.addDeletedCertificate(certData)
	}
}

func (b *CertificateBuilderImpl) handleUpdatedSecretsStorage(previousRefSecrets, newRefSecrets map[client.ObjectKey]map[client.ObjectKey]struct{}) {
	secretsIntersection := utils.SetIntersection(previousRefSecrets, newRefSecrets)
	for secretKey := range secretsIntersection {
		// Is secret updated ?
		secretUpdate, ok := b.ClusterStore.Updates.Secrets[secretKey]
		if ok {
			switch secretUpdate.Status {
			case store.StatusUpserted:
				certData, err := b.certStorage.NewCertificateData(secretUpdate.NewObject)
				if err != nil {
					continue
				}
				// Add the impacted Gateways
				// Adding the impacted Frontends will be done in haproxycfg mgr
				if secretUpdate.OldObject == nil {
					// Secret CREATED
					b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "Cert [create-sameref]", logging.LogAttrKey(secretKey))
					b.ControllerStore.addCreatedCertificate(certData)
					continue
				}
				// Secret UPDATED
				b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "Cert [update-sameref]", logging.LogAttrKey(secretKey))
				b.ControllerStore.addUpdatedCertificate(certData)

			case store.StatusDeleted:
				b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "Cert [delete-sameref]", logging.LogAttrKey(secretKey))
				certPath := b.certStorage.CertPath(secretKey)
				certData := certificate.NewCertificateData(certPath, nil)
				b.ControllerStore.addDeletedCertificate(certData)
			default:
				b.Logger.LogAttrs(context.Background(), slog.LevelError, "handling secret [default]", logging.LogAttrKey(secretKey))
			}
		}
	}
}

// -----------
// crt-list
// previousRefSecrets and newRefSecrets are the list of secrets referenced by a Gateway listener,
// They are a map[SecretKey]map[GatewayListenerKey]struct{}
// For example:
//
//		newRefSecrets =
//	     map[client.ObjectKey{
//		  Namespace: "example", Name: "offload"}]   ===> SecretKey
//		      map[client.ObjectKey{Namespace: "example", Name: hug-gateway_https"}]struct{}{}  ====> Set of GatewayListenerKey referencing this Secret
//		}
func (b *CertificateBuilderImpl) handleCrtList(newRefSecrets map[client.ObjectKey]map[client.ObjectKey]struct{}) {
	previousSecretsByGatewayListener := b.previousSecretsPerVirtualListener()
	newSecretsByGatewayListener := b.secretsPerVirtualListener(newRefSecrets)

	b.handleNewReferencedCrtList(previousSecretsByGatewayListener, newSecretsByGatewayListener)
	b.handleDeReferencedCrtList(previousSecretsByGatewayListener, newSecretsByGatewayListener)
	b.handleUpdatedCrtList(previousSecretsByGatewayListener, newSecretsByGatewayListener)
}

// secretsPerVirtualListener returns for each virtualListener Name the list of secret Keys
// Input: mapSecret2Listeners
//
//	is a map[SecretKey]map[GatewayListenerKey]struct{}
//
// For example:
//
//		newRefSecrets =
//	     map[client.ObjectKey{
//		  Namespace: "example", Name: "offload"}]   ===> SecretKey
//		      map[client.ObjectKey{Namespace: "example", Name: hug-gateway_https"}]struct{}{}  ====> Set of GatewayListenerKey referencing this Secret
//		}
//
// It returns a map[VirtualListenerName]map[SecretKey]struct{}
func (b *CertificateBuilderImpl) secretsPerVirtualListener(mapSecret2Listeners map[client.ObjectKey]map[client.ObjectKey]struct{}) map[string]map[client.ObjectKey]struct{} {
	secretsPerVirtualListener := make(map[string]map[client.ObjectKey]struct{})
	for secretKey, listenerKeys := range mapSecret2Listeners {
		for listenerKey := range listenerKeys {
			listener := b.GetListenerForKey(listenerKey)
			if listener == nil {
				continue
			}
			virtualListenerName := listener.VirtualListenerName
			if _, ok := secretsPerVirtualListener[virtualListenerName]; !ok {
				secretsPerVirtualListener[virtualListenerName] = make(map[client.ObjectKey]struct{})
			}
			secretsPerVirtualListener[virtualListenerName][secretKey] = struct{}{}
		}
	}
	return secretsPerVirtualListener
}

// previousSecretsPerVirtualListener rebuilds the previous virtual-listener→secret mapping
// directly from GateTree.PreviousVirtualListeners instead of re-deriving it from the
// previousReferencedSecrets index via GetListenerForKey.
//
// The indirect path breaks when a gateway is deleted: gateway.reset() clears g.Listeners
// before the certificate builder runs, so GetListenerForKey returns nil and the deleted
// gateway's secrets are silently dropped from the previous mapping. That causes
// handleUpdatedCrtList to see no removed secrets and skip the crt-list update, leaving a
// stale entry in the .list file that points to the already-deleted PEM.
//
// PreviousVirtualListeners holds *Listener values from the previous cycle. Those Listener
// objects are referenced by the slice in each VirtualListener and survive gateway.reset()
// because reset only replaces g.Listeners (the map on the Gateway), not the Listener
// objects themselves.
func (b *CertificateBuilderImpl) previousSecretsPerVirtualListener() map[string]map[client.ObjectKey]struct{} {
	result := make(map[string]map[client.ObjectKey]struct{})
	for vlName, vl := range b.GateTree.PreviousVirtualListeners {
		for _, listener := range vl.Listeners {
			if listener.K8sResource.TLS == nil {
				continue
			}
			for _, certRef := range listener.K8sResource.TLS.CertificateRefs {
				if !utilsk8s.IsSecretGroupKindSupported(certRef) {
					continue
				}
				ns := listener.Owner.Namespace
				if certRef.Namespace != nil {
					ns = string(*certRef.Namespace)
				}
				secretKey := client.ObjectKey{Namespace: ns, Name: string(certRef.Name)}
				if _, ok := result[vlName]; !ok {
					result[vlName] = make(map[client.ObjectKey]struct{})
				}
				result[vlName][secretKey] = struct{}{}
			}
		}
	}
	return result
}

// handleNewReferencedCrtList creates crt-list entries for newly referenced virtual listeners.
// It compares the previous and new sets of secrets per virtual listener to identify which
// virtual listeners have been newly referenced (present in new but not in previous).
// For each newly referenced virtual listener, it filters the associated secrets to keep only
// existing and non-deleted ones, then creates a new crt-list data entry and marks it as created
// in the ControllerStore.
//
// Parameters:
//   - previousSecretsPerVirtualListener: Map of virtual listener names to their referenced secrets in the previous state
//   - newSecretsPerVirtualListener: Map of virtual listener names to their referenced secrets in the current state
func (b *CertificateBuilderImpl) handleNewReferencedCrtList(previousSecretsPerVirtualListener, newSecretsPerVirtualListener map[string]map[client.ObjectKey]struct{}) {
	newReferencedListeners := utils.SetDifference(newSecretsPerVirtualListener, previousSecretsPerVirtualListener)

	for virtualListenerName := range newReferencedListeners {
		// Secret Keys for this Gateway
		secretKeys := newSecretsPerVirtualListener[virtualListenerName]

		// Keep only existing secrets
		filteredSecretKeys := b.keepOnlyExistingSecrets(virtualListenerName, secretKeys)

		// Write the crt-list
		crtListData := b.certStorage.NewCrtListData(virtualListenerName, filteredSecretKeys)
		b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "crt-list [create-newref]", logging.LogAttrVirtualListenerName(virtualListenerName))
		b.ControllerStore.addCreatedCrtList(crtListData)
	}
}

// handleDeReferencedCrtList deletes crt-list entries for virtual listeners that are no longer referenced.
// It compares the previous and new sets of secrets per virtual listener to identify which
// virtual listeners have been de-referenced (present in previous but not in new).
// For each de-referenced virtual listener, it creates a crt-list data entry with nil secrets
// and marks it as deleted in the ControllerStore, signaling that the crt-list should be removed
// from HAProxy configuration.
//
// Parameters:
//   - previousSecretsByVirtualListener: Map of virtual listener names to their referenced secrets in the previous state
//   - newSecretsByVirtualListener: Map of virtual listener names to their referenced secrets in the current state
func (b *CertificateBuilderImpl) handleDeReferencedCrtList(previousSecretsByVirtualListener, newSecretsByVirtualListener map[string]map[client.ObjectKey]struct{}) {
	deReferencedListeners := utils.SetDifference(previousSecretsByVirtualListener, newSecretsByVirtualListener)
	for virtualListenerName := range deReferencedListeners {
		crtListData := b.certStorage.NewCrtListData(virtualListenerName, nil)
		b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "crt-list [delete-unref]", logging.LogAttrVirtualListenerName(virtualListenerName))
		b.addDeletedCrtList(crtListData)
	}
}

func (b *CertificateBuilderImpl) keepOnlyExistingSecrets(virtualListenerName string, secretKeys map[client.ObjectKey]struct{}) map[client.ObjectKey]struct{} {
	res := make(map[client.ObjectKey]struct{})

	// Keep only existing secrets
	for secretKey := range secretKeys {
		if _, ok := b.GateTree.Secrets[secretKey]; !ok {
			b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "crt-list [discard][non-existing]", logging.LogAttrVirtualListenerName(virtualListenerName),
				slog.String("secretKey", secretKey.String()))
			delete(secretKeys, secretKey)
			continue
		}
		if b.GateTree.Secrets[secretKey].TreeStatus.Status == store.StatusDeleted {
			b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "crt-list [discard][deleted]", logging.LogAttrVirtualListenerName(virtualListenerName),
				slog.String("secretKey", secretKey.String()))
			delete(secretKeys, secretKey)
			continue
		}
		res[secretKey] = struct{}{}
		// b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "crt-list [content][keep]", logging.LogAttrKey(listenerKey),
		// 	slog.String("secretKey", secretKey.String()))
	}
	return res
}

// handleUpdatedCrtList updates crt-list entries for virtual listeners whose secret references have changed.
// It processes virtual listeners that were referencing secrets before and still are, but where the set
// of referenced secrets or their statuses have changed. The function determines whether a crt-list needs
// to be updated by tracking:
//   - Newly added secret references (filtered to keep only existing secrets)
//   - Secret references that were removed
//   - Unchanged secret references that have been created, updated, or deleted
//
// For each virtual listener with changes, it rebuilds the crt-list by:
//  1. Adding new secret references (if the secrets exist)
//  2. Adding unchanged secret references, including newly created or updated secrets
//  3. Excluding deleted secrets from unchanged references
//  4. Detecting removed secret references
//
// If the crt-list was updated and still has secrets, it's marked as updated in the ControllerStore.
// If the crt-list was updated but has no remaining secrets, it's marked as deleted.
//
// Note: Changes to secret content (certificate data) don't trigger crt-list updates, as that
// information is stored in Cert objects, not crt-lists. Only the list of secrets itself matters.
//
// Parameters:
//   - previousSecretsByVirtualListener: Map of virtual listener names to their referenced secrets in the previous state
//   - newSecretsByVirtualListener: Map of virtual listener names to their referenced secrets in the current state
func (b *CertificateBuilderImpl) handleUpdatedCrtList(previousSecretsByVirtualListener, newSecretsByVirtualListener map[string]map[client.ObjectKey]struct{}) {
	// Gateway listeners that were referencing secrets and still are...
	// But Secrets might have been created or deleted
	// Secret content change is ok, it's stored in Cert, not in crt-list
	listenersIntersection := utils.SetIntersection(previousSecretsByVirtualListener, newSecretsByVirtualListener)

	// Computing which one have an updated crt-list content (= list of secrets modified)
	for virtualListenerName := range listenersIntersection {
		previousSecretRefKeys := previousSecretsByVirtualListener[virtualListenerName]
		newSecretRefKeys := newSecretsByVirtualListener[virtualListenerName]

		// Added/Removed/Unchanged
		addedSecretRefsForListener := utils.SetDifference(newSecretRefKeys, previousSecretRefKeys)
		unchangedSecretRefsForListener := utils.SetIntersection(newSecretRefKeys, previousSecretRefKeys)
		removedSecretRefsForListener := utils.SetDifference(previousSecretRefKeys, newSecretRefKeys)

		// Build the list of crt-list
		crtlistUpdated := false

		// 1- Add the new cert if exisiting....
		secretKeys := make(map[client.ObjectKey]struct{})
		for secretKey := range addedSecretRefsForListener {
			b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "crt-list [content][add-newref]", logging.LogAttrVirtualListenerName(virtualListenerName),
				slog.String("secretKey", secretKey.String()))
			secretKeys[secretKey] = struct{}{}
		}
		secretKeys = b.keepOnlyExistingSecrets(virtualListenerName, secretKeys)
		if len(secretKeys) != 0 {
			b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "crt-list [updated][new entries]", logging.LogAttrVirtualListenerName(virtualListenerName))
			crtlistUpdated = true
		}

		// Also check if there are any newly created Secrets
		for secretKey := range unchangedSecretRefsForListener {
			treeSecret, ok := b.GateTree.Secrets[secretKey]
			if !ok {
				b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "crt-list [content][skip-notfound]", logging.LogAttrVirtualListenerName(virtualListenerName),
					slog.String("secretKey", secretKey.String()))
				continue
			}
			if treeSecret.TreeStatus.Status == store.StatusUpserted {
				if treeSecret.TreeStatus.OldTreeResource == nil {
					b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "crt-list [content][add-new]", logging.LogAttrVirtualListenerName(virtualListenerName),
						slog.String("secretKey", secretKey.String()))
					secretKeys[secretKey] = struct{}{}
					crtlistUpdated = true
					continue
				}
				b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "crt-list [content][add-updated]", logging.LogAttrVirtualListenerName(virtualListenerName),
					slog.String("secretKey", secretKey.String()))
				crtlistUpdated = true
				secretKeys[secretKey] = struct{}{}
			}
			if treeSecret.TreeStatus.Status == store.StatusDeleted {
				b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "crt-list [content][discard-deleted]", logging.LogAttrVirtualListenerName(virtualListenerName),
					slog.String("secretKey", secretKey.String()))
				crtlistUpdated = true
				continue
			}
			b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "crt-list [content][add-unchanged]", logging.LogAttrVirtualListenerName(virtualListenerName),
				slog.String("secretKey", secretKey.String()))
			secretKeys[secretKey] = struct{}{}
		}

		// Now check removed ones
		if len(removedSecretRefsForListener) != 0 {
			crtlistUpdated = true
		}

		if crtlistUpdated {
			if len(secretKeys) != 0 {
				crtListData := b.certStorage.NewCrtListData(virtualListenerName, secretKeys)
				b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "crt-list [update-content]", logging.LogAttrVirtualListenerName(virtualListenerName))
				b.ControllerStore.addUpdatedCrtList(crtListData)
			} else {
				crtListData := b.certStorage.NewCrtListData(virtualListenerName, nil)
				b.Logger.LogAttrs(context.Background(), slog.LevelDebug, "crt-list [delete-empty]", logging.LogAttrVirtualListenerName(virtualListenerName))
				b.ControllerStore.addDeletedCrtList(crtListData)
			}
		}
	}
}
