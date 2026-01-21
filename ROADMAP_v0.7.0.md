# Roadmap v0.7.0

## 1. Parsing détaillé des events

**Statut :** A faire
**Effort :** 1 session
**Difficulté :** Facile

### Objectif
Enrichir la catégorisation des ERROR/FATAL/PANIC/WARNING avec des sous-catégories exploitables.

### Tâches
- [ ] Ajouter sous-catégories d'erreurs :
  - Syntax error
  - Permission denied
  - Connection refused / Connection reset
  - Deadlock detected
  - Out of memory
  - Disk full
  - Lock timeout
  - Canceling statement due to conflict
  - etc.
- [ ] Parser les lignes DETAIL/HINT/CONTEXT pour enrichir le message
- [ ] Affichage groupé par catégorie dans le rapport text/JSON/HTML
- [ ] Tests sur logs réels avec variété d'erreurs

### Fichiers concernés
- `analysis/errclass.go`
- `analysis/events.go`
- `output/text.go`
- `output/json.go`
- `web/app.js`

---

## 2. Accessibilité HTML report (WCAG 2.1 AA)

**Statut :** A faire
**Effort :** 3-4 sessions
**Difficulté :** Moyen à Élevé

### Checklist par difficulté croissante

#### Niveau 1 : Structure & Sémantique (Facile)
- [ ] **Landmarks & Titres** : Remplacer `div.section-header` par `<h2>` ou `<h3>`.
- [ ] **Langue** : Vérifier l'attribut `lang="en"` (ou fr selon la locale).
- [ ] **Skip Link** : Ajouter un lien invisible "Aller au contenu principal" en haut de page.
- [ ] **ARIA Labels** : Ajouter `aria-label` sur les boutons sans texte (icônes close, expand).
- [ ] **Champs de formulaire** : Ajouter des `<label>` associés aux inputs (slider temps, sélecteur fichiers).

#### Niveau 2 : Composants Interactifs (Moyen)
- [ ] **Navigation Clavier** : Assurer que tous les éléments interactifs sont focusables (tabindex).
- [ ] **Tabs Pattern** :
  - Conteneur : `role="tablist"`
  - Boutons : `role="tab"`, `aria-selected`, `aria-controls`
  - Contenu : `role="tabpanel"`, `aria-labelledby`
- [ ] **Dropdowns Filtres** :
  - Trigger : `aria-haspopup="listbox"`, `aria-expanded`
  - Menu : `role="listbox"`
  - Items : `role="option"`, `aria-selected`
- [ ] **Focus Visible** : Renforcer l'outline CSS au focus clavier.

#### Niveau 3 : Patterns Complexes (Difficile)
- [ ] **Modales (Focus Trap)** :
  - `role="dialog"`, `aria-modal="true"`
  - Empêcher le focus de sortir de la modale.
  - Retour du focus sur l'élément déclencheur à la fermeture.
- [ ] **Alternatives Graphiques (uPlot)** :
  - Ajouter un bouton "Voir les données" pour chaque chart.
  - Générer un tableau HTML sémantique (`<table>`, `<th>`) masqué par défaut contenant les données brutes.
- [ ] **Navigation Graphique** : Permettre l'exploration des points de données (tooltips) via les flèches clavier (Bonus).

### Fichiers concernés
- `web/index.html` (Structure statique)
- `web/app.js` (Génération dynamique du DOM & Gestion focus)
- `web/styles.css` (Focus visible, Contrastes)
- `output/report_template.html` (Template embarqué)

---

## 3. Mode --follow (minimal)

**Statut :** A faire
**Effort :** 1-2 sessions
**Difficulté :** Moyen

### Objectif
Surveillance continue des logs pour intégration monitoring.

### Usage cible
```bash
# Surveillance console (refresh périodique)
quellog --follow --interval 30s --last 1h /var/log/postgresql/*.log

# Export JSON pour Grafana
quellog --follow --interval 30s --json --output /tmp/quellog.json /var/log/postgresql/*.log

# Export HTML auto-refresh
quellog --follow --interval 1m --html --output /var/www/html/logs.html /var/log/postgresql/*.log
```

### Approche
**Simple, sans état :**
1. Re-parse complet du fichier à chaque intervalle
2. `--last` limite la fenêtre temporelle (évite explosion mémoire/CPU)
3. `--output` écrase le fichier à chaque itération
4. Pas d'optimisation incrémentale (KISS)

### Tâches
- [ ] Nouveau flag `--follow` (bool)
- [ ] Nouveau flag `--interval` (duration, défaut 30s)
- [ ] Nouveau flag `--output` (path, optionnel)
- [ ] Boucle principale avec signal handling (SIGINT/SIGTERM)
- [ ] Message de statut périodique en mode console
- [ ] Documentation dans `docs/`
- [ ] Tests manuels sur logs actifs

### Fichiers concernés
- `cmd/root.go` (flags)
- `cmd/execute.go` (boucle follow)
- Nouveau `cmd/follow.go` ? (ou intégré dans execute)

### Pour plus tard (v0.8+)
- Parsing incrémental (seek to last position)
- Gestion rotation logs (detect truncate)
- Mode serveur HTTP (`--serve :9090`)
- Endpoint Prometheus (`/metrics`)
- State persistant pour métriques cumulatives

---

## 4. Exemple Grafana

**Statut :** A faire
**Effort :** 1 session
**Difficulté :** Facile

### Objectif
Montrer comment intégrer quellog dans un workflow Grafana.

### Livrables
- [ ] Dashboard JSON prêt à importer (`examples/grafana/quellog-dashboard.json`)
- [ ] Configuration datasource Infinity
- [ ] Documentation `docs/grafana.md` :
  - Prérequis (Grafana, plugin Infinity)
  - Setup quellog --follow
  - Import dashboard
  - Screenshots
- [ ] Screenshots pour la doc

### Architecture
```
quellog --follow --json --output /tmp/quellog.json ...
    ↓
Grafana + Infinity datasource → lit /tmp/quellog.json
    ↓
Dashboard avec panels équivalents au rapport HTML
```

---

## 5. Durcissement & Qualité (Post-Audit)

**Statut :** A faire
**Effort :** 1-2 sessions
**Difficulté :** Moyen

### Objectif
Sécuriser l'outil contre les entrées malveillantes et garantir une maintenance aisée du code.

### Tâches
- [ ] **Sécurité des archives :**
  - Implémenter une limite de taille décompressée (`max_decompressed_size`) pour parer aux "Zip Bombs".
  - Ajouter une validation des chemins dans les archives (anti-path traversal).
- [ ] **Automatisation de la qualité :**
  - Intégrer `gofmt -l` et `go vet` dans la CI (échec du build si non conforme).
  - Ajouter `golangci-lint` pour une analyse statique plus fine.
- [ ] **Refactoring structurel :**
  - Découper `parser/stderr_parser.go` (>1300 lignes) en sous-modules : `syslog.go`, `cloud_providers.go`, `stderr_core.go`.
  - Harmoniser le formatage des 16 fichiers actuellement non conformes.

### Fichiers concernés
- `parser/tar_parser.go` (sécurité)
- `parser/stderr_parser.go` (refactoring)
- `.github/workflows/ci.yml` (automatisation)
- Tout le projet (formatage)

---

## Ordre de priorité

1. **Parsing events** — Quick win, fondation pour affichage enrichi
2. **Durcissement & Qualité** — Assure la pérennité et la sécurité (fondations)
3. **Mode --follow** — Débloque l'intégration Grafana
4. **Exemple Grafana** — Capitalise sur --follow, valeur visible
5. **Accessibilité HTML** — Peut être fait en parallèle, travail indépendant

---

## Estimation totale

| Feature | Sessions | Difficulté |
|---------|----------|------------|
| Parsing events | 1 | Facile |
| Accessibilité HTML | 2-3 | Moyen |
| Mode --follow | 1-2 | Moyen |
| Exemple Grafana | 1 | Facile |
| Durcissement & Qualité | 1-2 | Moyen |
| **Total** | **6-9** | |

---

## Nice to have

### Build standalone en Go

**Statut :** Optionnel
**Effort :** 1 session
**Difficulté :** Facile

Réécrire `scripts/build_standalone.py` en Go pour éliminer la dépendance Python.

**Avantages :**
- Zéro dépendance externe
- Intégration `go generate`
- Cohérent avec le reste du projet

**Implémentation :**
- `os.ReadFile` pour lire les fichiers
- Regex pour minification CSS/JS (comme Python)
- `github.com/klauspost/compress/zstd` (déjà en dépendance)
- `compress/gzip` (stdlib)
- `encoding/base64` (stdlib)
- `//go:embed` pour fzstd.min.js et uplot.min.js

---

## Notes de session

_Espace pour noter l'avancement au fil des sessions_

### Session 1 - [DATE]
- ...

### Session 2 - [DATE]
- ...
