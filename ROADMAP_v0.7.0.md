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

## 2. Accessibilité HTML report

**Statut :** A faire
**Effort :** 2-3 sessions
**Difficulté :** Moyen

### Objectif
Atteindre WCAG 2.1 AA compliance pour le rapport HTML.

### Tâches
- [ ] ARIA labels sur tous les éléments interactifs (boutons, dropdowns, modals)
- [ ] Navigation clavier complète (Tab, Shift+Tab, Enter, Escape)
- [ ] Focus visible sur tous les éléments focusables
- [ ] Skip links ("Skip to main content")
- [ ] Vérifier contraste couleurs (dark/light themes)
- [ ] Alternatives textuelles pour les charts uPlot :
  - Option A : tableau masqué avec données brutes
  - Option B : aria-label descriptif
  - Option C : bouton "View as table"
- [ ] Rôles sémantiques (`role="table"`, `role="dialog"`, `role="tablist"`, etc.)
- [ ] Labels pour les inputs de filtres
- [ ] Test avec screen reader (VoiceOver / NVDA)
- [ ] Test navigation clavier uniquement

### Fichiers concernés
- `web/app.js`
- `web/styles.css`
- `web/index.html`
- `output/report_template.html`
- `scripts/build_standalone.py` (rebuild après modifs)

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
