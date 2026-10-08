# Arêtes d'enqueue et attachement des routes à un ListenerSet — condensé de la discussion

Contexte : étape 10 du plan (`feature-listenerset-etapes.md`), « Attachement des routes ».
Objet du document : conserver la trace du raisonnement sur **ce qu'il faut ré-enqueuer /
retraiter**, depuis l'arête manquante jusqu'à la solution retenue.

Marqueurs : **Assistant** = moi, **Utilisateur** = toi.

---

## 1. État initial de l'étape 10

**Assistant** — L'attachement est écrit (TLSRoute et HTTPRoute résolvent un `sectionName`
dans la map du parent nommé : `OwnListenerByName` / `ContributedListenerByName`, et sans
`sectionName` `OwnListeners()` / `ContributedListeners()`), mais il reste un trou
fonctionnel : **les contrôleurs de routes ne surveillent que `ObjectTypeGateway`**. Un
changement de ListenerSet est relayé à sa Gateway parente
(`reprocessGatewaysOfChangedListenerSets`), la Gateway est reconstruite
(`constructListenersOfUpsertedGateways` → `constructMergedListeners`) avec des
`AttachedRoutes` **neufs**, mais les routes attachées au LS ne sont pas dans
`Updates.HTTPRoutes`/`Updates.TLSRoutes` (`computeGateTreeUpdates` n'itère que celles-là)
→ compteurs et conditions périmés. C'est le point 1.

**Utilisateur** — D'accord pour traiter le point 1 en premier.

---

## 2. Première tentative : ajouter l'arête `ListenerSet`

**Assistant** — Câblage de `watchSource: ObjectTypeListenerSet` avec les enqueue existants,
conditionné à la CRD servie (`crdInstalled`). Build/vet/tests OK. Mais la vérification
montre que l'arête seule ne suffit pas : le matching est **nom/namespace seul**, sans le
kind, donc
- changement de LS → la Gateway parente est rebuildée → les routes à `parentRef` Gateway
  ne sont pas ré-enqueuées ;
- changement de Gateway → les routes à `parentRef` ListenerSet ne le sont pas ;
- homonymie → faux positifs.

**Utilisateur** — « Je ne comprends pas pourquoi `enqueueHTTPRouteForGateway` et l'ajouter
de nouveau à `httpRouteEnqueueFor` si nous servons les ListenerSet… Je ne vois d'ailleurs
pas dans la fonction mentionnée quoi que ce soit qui vérifie que la HTTPRoute référence
effectivement un ListenerSet. »

**Assistant** — Deux mises au point :
1. `WithEnqueueFor` se lit **objet surveillé → requêtes de reconcile du contrôleur**. Ici :
   un LS change → on réconcilie les HTTPRoutes que la fonction retourne. On n'enqueue
   jamais de Gateway. Le nom `enqueueHTTPRouteForGateway` est historique et trompeur.
2. Exact : la fonction ne regarde **pas le kind**. Elle filtre seulement les kinds non
   supportés puis matche nom/namespace. C'est le défaut à corriger.

---

## 3. Refactor demandé : séparer par kind et factoriser

**Utilisateur** — « Au minimum renommer `enqueueHTTPRouteForGateway` en
`enqueueHTTPRouteForListenerSet` et factoriser le code commun entre ces deux fonctions. Et
dans `enqueueHTTPRouteForListenerSet`, on ne traite bien que les cas d'attachement à un
listenerset. »

**Assistant** — Fait :
- matcher partagé `routeParentRefNamesObject(parentRefs, routeNamespace, o, extractGVK,
  kind)` : **le kind est comparé à celui de l'appelant**, jamais déduit de `o` ;
- cœurs `enqueueHTTPRoutesForParentKind` / `enqueueTLSRoutesForParentKind` (listing,
  filtre `dns`, boucle, requêtes en un endroit) ;
- quatre enveloppes nommées : `enqueueHTTPRouteForGateway` / `…ForListenerSet` et les deux
  équivalents TLSRoute ; l'arête LS pointe sur les variantes ListenerSet.

Effet : un événement Gateway n'atteint plus une route nommant un LS (et réciproquement).

---

## 4. Question : et quand la Gateway est modifiée ?

**Utilisateur** — « Lorsque la GW est modifiée, il n'y a pas de retraitement accompagnateur
des routes qui s'y réfèrent ? »

**Assistant** — Si : l'arête `ObjectTypeGateway` déclenche un `reconcile.Request` pour
chaque route dont le `parentRef` (kind Gateway) nomme la Gateway ; la route est
réconciliée, écrite dans `ClusterStore.Updates`, retraitée par le builder de routes. Mais
elle ne couvre **que le kind Gateway**. Or le rebuild de la Gateway recrée aussi les
entrées contribuées. Et le re-upsert par relais LS → Gateway n'est **pas** un événement
Kubernetes : il ne déclenche aucun watch. D'où deux moitiés manquantes.

**Utilisateur** — « Tu as l'air de dire que c'est une conséquence du refactoring mais avant
cela que faisait-il qui ne produisait pas cet écart ? »

**Assistant** — Le refactoring n'est pas la cause, ma formulation était fautive. À HEAD :
`IsParentRefGroupKindSupported` (v1) était **Gateway-only**, aucune source
`ObjectTypeListenerSet`, et les routes ne pouvaient pas référencer un LS. Donc
« rattaché à G ⇔ nomme G » tenait trivialement. Les entrées contribuées existaient déjà
(étape 8) et étaient déjà effacées à chaque rebuild, mais **rien ne pouvait s'y attacher**
→ compteur toujours 0 → effacement invisible. C'est l'étape 10 qui rend l'effacement
observable, en ouvrant la seconde façon de s'attacher aux listeners de G (nommer un LS).
Le gap appartient donc à l'étape 10, pas au refactor.

---

## 5. Piste « une route d'un LS est une route de la GW »

**Utilisateur** — « Est-ce qu'il ne faut pas considérer les HTTPRoutes des LS attachés à
une GW comme étant des routes de la GW au final et donc les refiler depuis
`enqueueHTTPRouteForGateway` ? »

**Assistant** — Conceptuellement oui, c'est la même règle : « routes de G » = nommant G
**ou** un LS de G. Mais l'implémenter dans l'enqueue exige :
1. **G → ses LS** : aucun index (`index/listenerset.go` n'a que les certRefs) et pas
   d'accès à `contributedListenersByGatewayKey` dans un map func → `List` filtré sur
   `spec.parentRef`, ou nouvel index de champ ;
2. **la moitié symétrique** : un changement de LS rebuild la Gateway, donc l'arête LS doit
   aussi délivrer les routes nommant la Gateway **et** les LS frères ;
3. **le defaulting du `parentRef` d'un LS** (group/kind/namespace par défaut) vit dans
   `tree.parentGatewayKey`, non exporté ;
4. réserve de fond : la couche enqueue **re-dériverait** la relation d'attachement que
   `checkParentRef` possède déjà — la duplication qu'on venait justement de supprimer.

---

## 6. Prise de recul : la vraie portée

**Utilisateur** — « Toutes ces enqueues sont bien compliqués au final. J'ai l'impression
qu'on a peut-être manqué ici ou en général la vraie portée de ce qui devait être
reenqueué. »

**Assistant** — L'état d'une route (attachement **et** conditions) dépend de : la route, le
parent nommé (Gateway ou LS), **la Gateway parente du LS**, **tous les LS de cette
Gateway** (liste fusionnée + conflits), plus GatewayClass / Namespaces / Secrets /
ReferenceGrants. Ce n'est pas une paire d'objets, c'est la **fermeture transitive d'un
graphe**. Chaque arête `WithEnqueueFor` est une approximation à la main d'un arc de ce
graphe, écrite dans une couche qui ne le connaît pas — d'où l'explosion.

Deux leviers :
- **(a) ne pas jeter l'état dérivé** : le compteur tombe à 0 parce que le rebuild recrée
  les listeners avec des maps neuves. S'il préservait `AttachedRoutes` par `listenerKey`,
  le rebuild cesserait d'être destructeur ;
- **(b) propager la saleté dans l'arbre** : garder les arêtes pour « faire entrer »
  l'objet changé, et laisser l'arbre marquer les dépendants (comme
  `reprocessGatewaysOfChangedListenerSets` le fait déjà LS → Gateway).

Proposition : vérifier la faisabilité de (a) avant de coder.

**Utilisateur** — oui.

---

## 7. Vérification de faisabilité de (a)

**Assistant** — Trois faits :
1. **Clé stable** : `listenerKey{GatewayKey, ListenerSetKey, ListenerName}` — aucune
   génération, aucun index → reporter les maps est possible.
2. **Sites destructeurs** : `constructOwnListeners` (`gateway.go`) et
   `listenersFromEntries` (`listenerset_contribution_builder.go`) créent
   `AttachedRoutes: make(...)` à chaque passage ; point d'entrée commun :
   `constructListeners`.
3. **Chemin de retrait manquant** : `addAttachedRoute` n'est appelé qu'à l'attache, et
   `deleteAttachedRoute` **uniquement** dans la branche `StatusDeleted`. `ResetChecks`
   vide `r.Listeners` mais **pas** les maps des listeners. Sur upsert, rien ne retire.

Conclusion : (a) faisable (report dans `constructListeners` + retrait à l'upsert), et il
dissout les trous cross-kind (un rebuild Gateway ne touche plus les entrées, et
réciproquement). Reste une arête vraiment due : un changement de Gateway qui touche
`allowedListeners` change l'existence des entrées d'un LS → routes nommant ce LS non
réveillées.

---

## 8. Solution retenue : forcer le retraitement au rebuild de la Gateway

**Utilisateur** — « Ne peut-on pas tout simplement forcer que le traitement d'une gateway
upsertée retravaille toutes routes attachées aux LS composants ? »

**Assistant** — Oui, et c'est le **motif déjà présent** dans le dépôt :
`store.Update.Indirect` (« not direct from a K8s object but from a linked K8s object
update »), précédent `GatewayClassBuilder.addIndirectClusterStoreUpdates`, et helper
générique `addIndirectFromReferenced` (commenté dans `tree.go`).

Portée à choisir : **« sert G »** (nomme G, ou nomme un LS dont G est la parente — y
compris un LS non autorisé), et non « attachée à G », sinon une route qui **devient**
attachable (listener ajouté, `allowedListeners` élargi) est ratée.

C'est **mieux que (a)** : une seule règle au lieu de deux changements, elle couvre les deux
kinds, les deux sens et `allowedListeners`, ne nécessite aucun nouvel index, et rend les
arêtes enqueue inutiles **pour l'attachement**.

**Utilisateur** — oui.

**Assistant** — Implémentation dans `tree/gateway_builder.go` :
- appel **après** `checkListenerConflicts()` (ce dernier re-upserte des Gateways et
  rappelle `constructMergedListeners` — marquer avant les raterait) ;
- `markRoutesOfRebuiltGateways()` : collecte les Gateways `StatusUpserted`, injecte un
  upsert indirect pour chaque route (HTTPRoute/TLSRoute, gérée ou non) qui en sert une ;
- `routeServesAnyRebuiltGateway()` : kind Gateway → direct ; kind ListenerSet →
  `parentGatewayKey` sur le LS miroité ;
- `injectIndirectUpdate[T]()` : idiome (`Indirect: true`, `OldObject = Object`), **skip si
  un update est déjà en file** (changement réel ou suppression non écrasés).
- Ordre du pipeline garanti : `gatewayBuilder` tourne avant `httpRouteBuilder` /
  `tlsRouteBuilder`, donc le retraitement a lieu dans le **même cycle**.
- Tests : `TestRebuiltGatewayRemarksEveryRouteServingIt`,
  `TestRebuiltGatewayLeavesAPendingRouteUpdateAlone`.

---

## 9. Retrait de l'arête LS devenue redondante

**Assistant** — Un événement LS → relais → Gateway `Upserted` → rebuild →
`markRoutesOfRebuiltGateways` force déjà toutes les routes qui servent cette Gateway, y
compris celles nommant le LS. La source `ObjectTypeListenerSet` et les enveloppes
`enqueueHTTPRouteForListenerSet` / `enqueueTLSRouteForListenerSet` ne servent plus à rien
(coût : un watch + un `List` de toutes les routes par événement LS).

**Utilisateur** — oui.

**Assistant** — Retirées. Conservés : le cœur kind-aware (`routeParentRefNamesObject`,
`enqueueHTTPRoutesForParentKind`, `enqueueTLSRoutesForParentKind`) et les enveloppes
Gateway — le kind-aware reste utile pour qu'un événement Gateway n'atteigne pas une route
nommant un LS homonyme. Sonde `listenerSetServed` supprimée.

---

## Solution adoptée (résumé)

| Élément | Décision |
|---|---|
| Où exprimer « ce qu'il faut retraiter » | Dans l'arbre, au rebuild de la Gateway — pas dans les arêtes |
| Règle | Toute route qui **sert** une Gateway `StatusUpserted` est marquée `Upserted` (update indirect) |
| Résolution de « sert » | `parentRef` kind Gateway (direct) ou kind ListenerSet → `parentGatewayKey` du LS miroité (LS non autorisé inclus) |
| Mécanisme | `store.Update.Indirect`, idiome `addIndirectClusterStoreUpdates` ; skip si update déjà en file |
| Arête `ObjectTypeListenerSet` | Retirée (redondante) |
| Enqueue kind-aware | Conservé (matcher + cœurs + enveloppes Gateway) |
| Non retenu | Index `ListenerSet` par parent Gateway ; enqueue relation-aware ; report d'`AttachedRoutes` (a) + retrait à l'upsert |

Artefacts : `k8s/gate/tree/gateway_builder.go` (`markRoutesOfRebuiltGateways`,
`routeServesAnyRebuiltGateway`, `injectIndirectUpdate`),
`k8s/gate/tree/gateway_builder_rebuilt_routes_test.go`, `k8s/gate/enqueuefor.go`,
`k8s/gate/controller.go`.

## Points ouverts (hors de cette ligne)

- `parentRef.Port` ignoré dans `checkParentRef` (HTTPRoute et TLSRoute).
- Vocabulaire de raison au niveau listener : HUG publie `UnsupportedProtocol` là où
  `tlsroute-listener-terminate-not-supported.go` attend `UnsupportedValue`.
- L'arête Gateway → ses LS (`allowedListeners` / GatewayClass) n'est pas un watch : le
  statut du LS est recalculé à chaque cycle, et les routes sont couvertes par la règle
  tree-level ; à confirmer par la conformance.
