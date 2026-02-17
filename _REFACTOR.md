# Plan de refactoring quellog

Audit du 2026-02-16. Chaque point = un commit sur la branche `refactor/code-quality`.

---

## ~~1. Fix race condition sur `cachedTimestampFormat`~~ DONE

**Fichier** : `parser/csv_parser.go:142`

**Probleme** : Variable globale `cachedTimestampFormat` lue et ecrite sans synchronisation. Quand plusieurs goroutines parsent des fichiers CSV en parallele (via `parseFilesAsync`), c'est un data race.

**Correction** : Supprimer la variable globale. Passer le format cache en parametre de `parseCSVTimestamp()` et stocker le cache dans `CsvParser` en tant que champ d'instance. Chaque goroutine a sa propre instance de parser, donc pas de conflit.

---

## ~~2. Remplacer bubble sort par `sort.Slice()`~~ DONE

**Fichier** : `parser/mmap_parser.go:432-441`

**Probleme** : Tri O(n^2) par boucles imbriquees sur `emissionOrder`. Sur un gros fichier syslog avec beaucoup de PIDs, la taille de ce slice peut etre significative.

**Correction** : Remplacer par `sort.Slice()` (O(n log n), stable si besoin avec `sort.SliceStable()`).

---

## ~~3. Factoriser la duplication dans `cmd/execute.go`~~ DONE

**Fichier** : `cmd/execute.go:230-324`

**Probleme** : Trois blocs quasi-identiques pour `--sql-detail`, `--sql-performance`, `--sql-overview` : meme validation des metriques, meme creation du writer de sortie, meme pattern d'appel.

**Correction** : Extraire deux helpers :
- `createOutputWriter(outputFlag) (io.Writer, func())` — cree le writer et retourne un closer
- `requireMetrics(filteredLogs, totalFileSize) *AggregatedMetrics` — aggregate et fatal si count == 0

Chaque bloc se reduit a : writer + metriques + appel specifique.

---

## ~~4. Supprimer les build tags legacy~~ DONE

**Fichier** : `parser/mmap_parser.go:1-3`

**Probleme** : Melange `//go:build` (Go 1.17+) et `// +build` (legacy). Go >= 1.17 ne necessite que `//go:build`.

**Correction** : Supprimer les lignes `// +build` (lignes 2-3). Garder uniquement `//go:build (linux || darwin) && !wasm`.

---

## ~~5. Supprimer le code mort~~ DONE

**Fichiers** :
- `output/formatter.go` : struct `AnalysisReport` (l.9-19), interface `Formatter` (l.43-46) — jamais utilisees
- `analysis/summary.go` : `mapKeysAsSlice()` (l.745-752) — jamais appelee

**Correction** : Supprimer ces declarations.

---

## ~~6. Traduire les commentaires francais en anglais~~ DONE

**Fichiers** :
- `output/histogram.go` : 7 commentaires (l.26, 31, 36, 45, 58, 79, 91)
- `output/formatter.go` : 3 commentaires (l.9, 21, 43 — ceux sur le code mort disparaitront avec le point 5, ne reste que `formatBytes` l.21)

**Correction** : Traduire chaque commentaire en anglais idiomatique.

---

## ~~7. Corriger `normalization_test.go`~~ DONE

**Fichier** : `analysis/normalization_test.go`

**Probleme** : Utilise `fmt.Printf("FAIL: ...")` au lieu de `t.Errorf()`. Le test ne remonte jamais d'echec — il imprime "FAIL" sur stdout mais `go test` dit PASS.

**Correction** : Remplacer `fmt.Printf("FAIL: ...")` par `t.Errorf(...)` et `fmt.Printf("PASS: ...")` par `t.Logf(...)` (ou supprimer les PASS qui polluent la sortie).

---

## ~~8. Remplacer `Readdir` deprecie par `ReadDir`~~ DONE

**Fichier** : `cmd/files.go:60-70`

**Probleme** : `f.Readdir(-1)` est deprecie depuis Go 1.16. `os.ReadDir()` est plus efficace (retourne `[]fs.DirEntry` au lieu de `[]os.FileInfo`, evite les `Stat` sur chaque entree).

**Correction** : Remplacer par `os.ReadDir(dir)`. Adapter la boucle (`entry.IsDir()` reste identique, `entry.Name()` aussi).

---

## ~~9. Renommer `SqlMetrics` en `SQLMetrics`~~ DONE

**Fichier** : `analysis/sql.go`

**Probleme** : Convention Go : les acronymes sont en majuscules (`SQL`, `HTTP`, `ID`). `SqlMetrics` devrait etre `SQLMetrics`.

**Identifiants concernes** :
- `type SqlMetrics struct` (l.238)
- `func (a *SQLAnalyzer) Finalize() SqlMetrics` (l.652)
- `func CollectQueriesWithoutDuration(sql *SqlMetrics, ...)` (l.872)
- Toutes les references dans `output/` et `cmd/`

**Correction** : Renommer `SqlMetrics` → `SQLMetrics` partout (rename refactor).

---

## 10. Ajouter un garde contre l'accumulation de listeners JS

**Fichier** : `web/js/filters.js:608-627`

**Probleme** : `setupFilterEventListeners()` ajoute des event listeners sur `document` a chaque appel, sans verifier si c'est deja fait. Si la fonction est appelee plusieurs fois, les handlers se dupliquent.

**Correction** : Ajouter un flag module-level `let listenersRegistered = false` et sortir immediatement si deja enregistre. Alternative : utiliser `{ once: false }` avec des references nommees et `removeEventListener` avant `addEventListener`.

---

## 11. Ajouter le nettoyage des charts JS

**Fichier** : `web/js/state.js:26-32`

**Probleme** : Les collections `charts`, `modalCharts`, `modalChartsData`, `chartIntervalMap` grandissent mais ne sont jamais videes. Sur une session longue avec plusieurs chargements de fichiers, la memoire s'accumule.

**Correction** : Ajouter une fonction `clearAllCharts()` qui `.clear()` les Maps, vide le tableau, et deconnecte les ResizeObservers. L'appeler au debut de chaque nouveau chargement de fichier (dans `file-handler.js`).

---

## 12. Valider les bornes dans `extractZip` JS

**Fichier** : `web/js/compression.js:85-137`

**Probleme** : `compSize` lu depuis le header ZIP sans validation. Un ZIP malformed peut faire pointer `offset` ou `dataStart + compSize` hors du buffer.

**Correction** : Ajouter des checks :
```js
if (dataStart + compSize > data.length) {
    console.warn(`[quellog] Truncated zip entry: ${name}`);
    break;
}
```
Idem pour `nameLen` et `extraLen` (verifier que `offset + 30 + nameLen + extraLen <= data.length`).

---

## 13. Decouper les fonctions longues (bonus, progressif)

A traiter en sous-commits si le temps le permet. Par ordre de valeur :

### 13a. `analysis/locks.go:Process()` (282 lignes)
Extraire `handleLockStatement()`, `handleLockDeadlock()`, `handleLockEvent()`.

### 13b. `analysis/temp_files.go:Process()` (234 lignes)
Extraire `handleQueryLine()`, `handleTempFile()`.

### 13c. `parser/mmap_parser.go:parseMmapDataSyslog()` (169 lignes)
Extraire le tri dans `sortEmittedEntries()` (lie au point 2).

### 13d. `parser/prefix.go:scoreRemainingValues()` (170 lignes)
Extraire `scoreSingleValue()`, `scoreTwoValues()`, `scoreThreeValues()`.

---

## Ordre d'implementation suggere

| # | Point | Type | Risque |
|---|-------|------|--------|
| 1 | Race condition CSV | Bug fix | Faible |
| 2 | Bubble sort → sort.Slice | Perf fix | Faible |
| 3 | Build tags legacy | Cleanup | Nul |
| 4 | Code mort | Cleanup | Nul |
| 5 | Commentaires FR → EN | Cleanup | Nul |
| 6 | normalization_test.go | Bug fix | Nul |
| 7 | Readdir deprecie | Cleanup | Nul |
| 8 | SqlMetrics → SQLMetrics | Rename | Faible (grep-verify) |
| 9 | Duplication execute.go | Refactor | Moyen |
| 10 | Listeners JS | Bug fix | Faible |
| 11 | Charts cleanup JS | Bug fix | Faible |
| 12 | ZIP bounds JS | Security fix | Faible |
| 13a-d | Fonctions longues | Refactor | Moyen |
