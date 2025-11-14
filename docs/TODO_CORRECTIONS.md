# Documentation - Simplification complète

## ✅ Phase 1: Corrections (terminée)

### Corrections appliquées (commits ae63aea → d902775)

- Correction des noms de champs JSON
- Retrait des mentions 99.79%
- Correction format --window (nécessite --begin/--end)
- Correction des tableaux de sortie (locks, tempfiles, checkpoints)
- Ajout des hosts dans Clients
- Fix maintenance formatting

## ✅ Phase 2: Simplification (terminée)

### Objectif
Retirer tout langage prescriptif (pros/cons, best-for, recommendations), rester factuel et descriptif.

### Fichiers simplifiés

**postgresql-setup.md** (403 → 57 lignes, -85%)
- ✅ Retiré: Quick Configuration, Production/Development Recommendations
- ✅ Gardé: exemple de configuration, descriptions factuelles des paramètres

**formats.md** (420 → 87 lignes, -79%)
- ✅ Retiré: sections détection verbose, tableaux performance, best practices
- ✅ Gardé: 3 formats logs, 3 formats compression, explication détection

**filtering-logs.md** (350 → 130 lignes, -63%)
- ✅ Retiré: tips/warnings, exemples scriptés, verification section
- ✅ Gardé: toutes les options exhaustivement documentées

**default-report.md**
- ✅ Corrigé: format Locks (3 tableaux), Tempfiles (table avec colonnes)
- ✅ Corrigé: Checkpoints histogram, Clients avec HOSTS, Maintenance formatting
- ✅ Ajouté: notes sur quand les query details apparaissent

**sql-reports.md** (464 → 137 lignes, -70%)
- ✅ Split output par section avec exemples complets
- ✅ Histogrammes 6 buckets avec requêtes rapides (<10s)
- ✅ Explications colonnes pour tous les tableaux multi-colonnes
- ✅ SQL keywords en majuscules dans Raw Query

**json-export.md** (415 → 236 lignes, -43%)
- ✅ Testé structure réelle avec A.log
- ✅ Sections: summary, events, sql_performance, temp_files, locks, maintenance, checkpoints, clients
- ✅ Exemples concrets de chaque section
- ✅ Exemples jq simples
- ✅ Retiré: intégrations verbose (Prometheus, Grafana, Slack, automation)

**markdown-export.md** (474 → 189 lignes, -60%)
- ✅ Testé structure réelle avec A.log
- ✅ Exemples de chaque section (SUMMARY, SQL, EVENTS, TEMP FILES, LOCKS, etc.)
- ✅ Retiré: use cases verbose, conversion pandoc, customization, version control, automation, compatibility list
