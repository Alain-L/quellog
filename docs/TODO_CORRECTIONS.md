# Documentation - Statut des corrections

## ✅ Toutes les corrections sont terminées

### Corrections appliquées

**filtering-logs.md**
- ✅ Retirer les exemples avec --window tout seul
- ✅ Enlever la section "Filter Order Impact"
- ✅ Retirer stdin support `quellog -`
- ✅ Simplifier section "Filter Logic"
- ✅ Ajouter exemples avec powa/temboard_agent
- ✅ Mettre "No filters = all logs" au début

**default-report.md**
- ✅ Enlever toutes les sections "Interpreting"
- ✅ Enlever toutes les sections "Tuning recommendations"
- ✅ Enlever section "Error Classes (SQLSTATE)"

**sql-reports.md**
- ✅ Corriger la forme des tableaux --sql-summary
- ✅ Vérifier la vraie sortie

**formats.md**
- ✅ Enlever toutes les mentions 99.79%
- ✅ Vérifier "client=%h" (correct)

**quick-start.md**
- ✅ Corriger exemples jq (utiliser .sql_performance, .checkpoints.total_checkpoints)

**filtering-output.md, json-export.md, markdown-export.md**
- ✅ Remplacer tous les --window standalone par --begin/--end

### Commits effectués

1. `ae63aea` - Corrections initiales (benchmarks, ToC, quick start, etc.)
2. `0c1943a` - filtering-logs.md et default-report.md complets
3. `ca2b23a` - sql-reports.md avec format correct
4. `d902775` - Dernières corrections après revue complète
